package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
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
	StopSession(ctx context.Context, session string) error
	StatusSession(ctx context.Context, session string) (orchestrator.Status, error)
	Sessions(ctx context.Context) ([]orchestrator.SessionSummary, error)
	PoseSession(ctx context.Context, session string, robot int) (orchestrator.EntityPose, error)
	ResetSession(ctx context.Context, session string, robot int, pose contract.Pose) (orchestrator.EntityPose, error)
	CallSession(ctx context.Context, session string, robot int, request []byte) ([]byte, error)
}

// Run 跑 CLI。registerStartFlags 是各 Port 的 RegisterFlags（组合根传入）：
// 它们把实现参数挂到 start 的 FlagSet 上——CLI 自己只认识世界与机器人。
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, runner Runner, registerStartFlags ...func(*flag.FlagSet)) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: simenv start|status [name]|stop [name]|pose|reset|call")
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
		printSessionDetail(stdout, status.Name, status)
		return nil
	case "status":
		session, err := parseOptionalName("status", args[1:])
		if err != nil {
			return err
		}
		if session == "" {
			sessions, err := runner.Sessions(context.Background())
			printSessions(stdout, sessions)
			return err
		}
		status, err := runner.StatusSession(context.Background(), session)
		if err != nil {
			return err
		}
		printSessionDetail(stdout, session, status)
		return nil
	case "stop":
		session, err := parseOptionalName("stop", args[1:])
		if err != nil {
			return err
		}
		return runner.StopSession(context.Background(), session)
	case "pose":
		session, robot, err := parseRobotOnly(args[1:])
		if err != nil {
			return err
		}
		pose, err := runner.PoseSession(context.Background(), session, robot)
		if err != nil {
			return err
		}
		printEntityPose(stdout, pose)
		return nil
	case "reset":
		session, robot, pose, err := parseReset(args[1:])
		if err != nil {
			return err
		}
		got, err := runner.ResetSession(context.Background(), session, robot, pose)
		if err != nil {
			return err
		}
		printEntityPose(stdout, got)
		return nil
	case "call":
		session, robot, err := parseCall(args[1:])
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
		response, err := runner.CallSession(context.Background(), session, robot, request)
		if err != nil {
			return err
		}
		_, err = stdout.Write(response)
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// parseStart 解析 start 的参数：框架只有 --world / --robots / --poses /
// --world-kind / --vehicle-kind，其余 flag 由各 Port 的 RegisterFlags 挂上来、
// parse 时直接填进它们的 Options。
func parseStart(args []string, registerFlags []func(*flag.FlagSet)) (contract.StartConfig, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robots := fs.Int("robots", 1, "")
	worldName := fs.String("world", "", "")
	posesText := fs.String("poses", "", "")
	worldKind := fs.String("world-kind", "", "")
	vehicleKind := fs.String("vehicle-kind", "", "")
	sessionName := fs.String("session", "", "")
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
		Robots:      *robots,
		World:       *worldName,
		Poses:       poses,
		WorldKind:   *worldKind,
		VehicleKind: *vehicleKind,
		SessionName: *sessionName,
	}, nil
}

func parseOptionalName(command string, args []string) (string, error) {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	switch fs.NArg() {
	case 0:
		return "", nil
	case 1:
		return fs.Arg(0), nil
	default:
		return "", fmt.Errorf("%s accepts at most one session name", command)
	}
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
		if len(fields) < 3 || len(fields) > 4 {
			return nil, fmt.Errorf("pose %q must be x,y,yaw[,z]", part)
		}
		values := make([]float64, len(fields))
		for i, field := range fields {
			value, err := strconv.ParseFloat(strings.TrimSpace(field), 64)
			if err != nil {
				return nil, fmt.Errorf("pose %q: %w", part, err)
			}
			values[i] = value
		}
		pose := contract.Pose{X: values[0], Y: values[1], Yaw: values[2], Z: contract.DefaultSpawnZ}
		if len(values) == 4 {
			pose.Z = values[3]
		}
		if err := pose.Validate(); err != nil {
			return nil, err
		}
		poses = append(poses, pose)
	}
	return poses, nil
}

func parseRobotOnly(args []string) (string, int, error) {
	fs := flag.NewFlagSet("pose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	sessionName, err := parseControlFlags(fs, args)
	if err != nil {
		return "", 0, err
	}
	if *robot < 1 {
		return "", 0, fmt.Errorf("robot must be >= 1")
	}
	return sessionName, *robot, nil
}

func parseReset(args []string) (string, int, contract.Pose, error) {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	xSet, ySet, yawSet := false, false, false
	var x, y, yaw, z float64
	z = contract.DefaultSpawnZ
	fs.Func("x", "", func(value string) error {
		xSet = true
		var err error
		x, err = strconv.ParseFloat(value, 64)
		return err
	})
	fs.Func("y", "", func(value string) error {
		ySet = true
		var err error
		y, err = strconv.ParseFloat(value, 64)
		return err
	})
	fs.Func("yaw", "", func(value string) error {
		yawSet = true
		var err error
		yaw, err = strconv.ParseFloat(value, 64)
		return err
	})
	fs.Func("z", "", func(value string) error {
		var err error
		z, err = strconv.ParseFloat(value, 64)
		return err
	})
	sessionName, err := parseControlFlags(fs, args)
	if err != nil {
		return "", 0, contract.Pose{}, err
	}
	if *robot < 1 {
		return "", 0, contract.Pose{}, fmt.Errorf("robot must be >= 1")
	}
	if !xSet || !ySet || !yawSet {
		return "", 0, contract.Pose{}, fmt.Errorf("reset requires --x --y --yaw")
	}
	pose := contract.Pose{X: x, Y: y, Z: z, Yaw: yaw}
	if err := pose.Validate(); err != nil {
		return "", 0, contract.Pose{}, err
	}
	return sessionName, *robot, pose, nil
}

func printSessions(w io.Writer, sessions []orchestrator.SessionSummary) {
	for _, session := range sessions {
		fmt.Fprintf(w, "session %s world=%s vehicle=%s connected=%t",
			session.Name, session.WorldKind, session.VehicleKind, session.Connected)
		if session.Error != "" {
			fmt.Fprintf(w, " error=%q", session.Error)
		}
		fmt.Fprintln(w)
	}
}

func printSessionDetail(w io.Writer, name string, status orchestrator.Status) {
	fmt.Fprintf(w, "session %s\n", name)
	printStatus(w, status)
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

func parseCall(args []string) (string, int, error) {
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robot := fs.Int("robot", 0, "")
	sessionName, err := parseControlFlags(fs, args)
	if err != nil {
		return "", 0, err
	}
	if *robot < 1 {
		return "", 0, fmt.Errorf("robot must be >= 1")
	}
	return sessionName, *robot, nil
}

func parseControlFlags(fs *flag.FlagSet, args []string) (string, error) {
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
		if err := contract.ValidateName("session", name); err != nil {
			return "", err
		}
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 0 {
		return "", fmt.Errorf("%s requires the session name before flags", fs.Name())
	}
	return name, nil
}
