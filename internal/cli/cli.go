package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

// startTimeout 给一次 start 的总预算：世界拉起、车辆就绪、world init 任一
// 环节挂住都必须以失败收场，而不是无限等。
const startTimeout = 10 * time.Minute

type Runner interface {
	Start(ctx context.Context, cfg contract.StartConfig) (orchestrator.Status, error)
	Stop(ctx context.Context) error
	Status(ctx context.Context) (orchestrator.Status, error)
	Pose(ctx context.Context, robot int) (orchestrator.EntityPose, error)
	Reset(ctx context.Context, robot int, pose contract.Pose) (orchestrator.EntityPose, error)
	Call(ctx context.Context, robot int, request []byte) ([]byte, error)
}

// Run 跑 CLI。registerStartFlags 是各 Port 的 RegisterFlags（组合根传入）：
// 它们把实现参数挂到 start 的 FlagSet 上——CLI 自己只认识世界与机器人。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, runner Runner, registerStartFlags ...func(*flag.FlagSet)) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: simenv start|status|stop|pose|reset|call")
	}
	switch args[0] {
	case "start":
		cfg, err := parseStart(args[1:], registerStartFlags)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		defer cancel()
		status, err := runner.Start(ctx, cfg)
		if err != nil {
			return err
		}
		printStatus(stdout, status)
		return nil
	case "status":
		status, err := runner.Status(context.Background())
		if err != nil {
			return err
		}
		printStatus(stdout, status)
		return nil
	case "stop":
		return runner.Stop(context.Background())
	case "pose":
		robot, err := parseRobotOnly(args[1:])
		if err != nil {
			return err
		}
		pose, err := runner.Pose(context.Background(), robot)
		if err != nil {
			return err
		}
		printEntityPose(stdout, pose)
		return nil
	case "reset":
		robot, pose, err := parseReset(args[1:])
		if err != nil {
			return err
		}
		got, err := runner.Reset(context.Background(), robot, pose)
		if err != nil {
			return err
		}
		printEntityPose(stdout, got)
		return nil
	case "call":
		robot, err := parseCall(args[1:])
		if err != nil {
			return err
		}
		request, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(request)) == 0 {
			return fmt.Errorf("request is empty")
		}
		response, err := runner.Call(context.Background(), robot, request)
		if err != nil {
			return err
		}
		_, err = stdout.Write(response)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// parseStart 解析 start 的参数：框架只有 --world / --robots / --poses，
// 其余 flag 由各 Port 的 RegisterFlags 挂上来、parse 时直接填进它们的 Options。
func parseStart(args []string, registerFlags []func(*flag.FlagSet)) (contract.StartConfig, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robots := fs.Int("robots", 1, "")
	worldName := fs.String("world", "", "")
	posesText := fs.String("poses", "", "")
	for _, register := range registerFlags {
		register(fs)
	}
	if err := fs.Parse(args); err != nil {
		return contract.StartConfig{}, err
	}
	poses, err := parsePoses(*posesText)
	if err != nil {
		return contract.StartConfig{}, err
	}
	return contract.StartConfig{
		Robots: *robots,
		World:  *worldName,
		Poses:  poses,
	}, nil
}

func parsePoses(text string) ([]contract.Pose, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	var poses []contract.Pose
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ",")
		if len(fields) < 3 {
			return nil, fmt.Errorf("pose %q must be x,y,yaw[,z]", part)
		}
		var pose contract.Pose
		if _, err := fmt.Sscanf(strings.Join(fields, " "), "%f %f %f", &pose.X, &pose.Y, &pose.Yaw); err != nil {
			return nil, fmt.Errorf("pose %q: %w", part, err)
		}
		if len(fields) >= 4 {
			if _, err := fmt.Sscanf(fields[3], "%f", &pose.Z); err != nil {
				return nil, err
			}
		} else {
			pose.Z = contract.DefaultSpawnZ
		}
		poses = append(poses, pose)
	}
	return poses, nil
}

func parseRobotOnly(args []string) (int, error) {
	fs := flag.NewFlagSet("pose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *robot < 1 {
		return 0, fmt.Errorf("robot must be >= 1")
	}
	return *robot, nil
}

func parseReset(args []string) (int, contract.Pose, error) {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	xSet, ySet, yawSet := false, false, false
	var x, y, yaw, z float64
	z = contract.DefaultSpawnZ
	fs.Func("x", "", func(value string) error {
		xSet = true
		_, err := fmt.Sscanf(value, "%f", &x)
		return err
	})
	fs.Func("y", "", func(value string) error {
		ySet = true
		_, err := fmt.Sscanf(value, "%f", &y)
		return err
	})
	fs.Func("yaw", "", func(value string) error {
		yawSet = true
		_, err := fmt.Sscanf(value, "%f", &yaw)
		return err
	})
	fs.Func("z", "", func(value string) error {
		_, err := fmt.Sscanf(value, "%f", &z)
		return err
	})
	if err := fs.Parse(args); err != nil {
		return 0, contract.Pose{}, err
	}
	if *robot < 1 {
		return 0, contract.Pose{}, fmt.Errorf("robot must be >= 1")
	}
	if !xSet || !ySet || !yawSet {
		return 0, contract.Pose{}, fmt.Errorf("reset requires --x --y --yaw")
	}
	return *robot, contract.Pose{X: x, Y: y, Z: z, Yaw: yaw}, nil
}

func printStatus(w io.Writer, status orchestrator.Status) {
	fmt.Fprintf(w, "world running=%t robots=%s%s\n",
		status.World.Running, strings.Join(status.World.Ready.Robots, ","), formatDetail(status.World.Detail))
	for _, vehicle := range status.Vehicles {
		fmt.Fprintf(w, "vehicle %s running=%t endpoint=%s%s\n",
			vehicle.Name, vehicle.Running, vehicle.Endpoint, formatDetail(vehicle.Detail))
	}
	fmt.Fprintf(w, "connected=%t\n", status.Connected)
}

// formatDetail 原样打印实现专属的 Detail（键排序，输出稳定）。
func formatDetail(detail map[string]string) string {
	if len(detail) == 0 {
		return ""
	}
	keys := make([]string, 0, len(detail))
	for key := range detail {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, " %s=%s", key, detail[key])
	}
	return b.String()
}

func printEntityPose(w io.Writer, pose orchestrator.EntityPose) {
	fmt.Fprintf(w, "robot=%d entity=%s x=%g y=%g z=%g yaw=%g\n",
		pose.Robot, pose.Entity, pose.Pose.X, pose.Pose.Y, pose.Pose.Z, pose.Pose.Yaw)
}

func parseCall(args []string) (int, error) {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *robot < 1 {
		return 0, fmt.Errorf("robot must be >= 1")
	}
	if fs.NArg() != 0 {
		return 0, fmt.Errorf("call only accepts --robot")
	}
	return *robot, nil
}
