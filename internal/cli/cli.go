package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

type Runner interface {
	Start(ctx context.Context, cfg contract.StartConfig) (orchestrator.Status, error)
	Stop(ctx context.Context) error
	Status(ctx context.Context) (orchestrator.Status, error)
}

func Run(args []string, stdout, stderr io.Writer, runner Runner) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: simenv start|status|stop")
	}
	switch args[0] {
	case "start":
		cfg, err := parseStart(args[1:])
		if err != nil {
			return err
		}
		status, err := runner.Start(context.Background(), cfg)
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
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseStart(args []string) (contract.StartConfig, error) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	robots := fs.Int("robots", 1, "")
	worldName := fs.String("world", contract.DefaultWorld, "")
	appImage := fs.String("app-image", "", "")
	envImage := fs.String("env-image", contract.DefaultEnvImage, "")
	partition := fs.String("partition", contract.DefaultPartition, "")
	dataRoot := fs.String("data-root", "", "")
	posesText := fs.String("poses", "", "")
	if err := fs.Parse(args); err != nil {
		return contract.StartConfig{}, err
	}
	if strings.TrimSpace(*appImage) == "" {
		return contract.StartConfig{}, fmt.Errorf("app-image must not be empty")
	}
	poses, err := parsePoses(*posesText)
	if err != nil {
		return contract.StartConfig{}, err
	}
	return contract.StartConfig{
		Robots:    *robots,
		World:     *worldName,
		AppImage:  *appImage,
		EnvImage:  *envImage,
		Partition: *partition,
		DataRoot:  *dataRoot,
		Poses:     poses,
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

func printStatus(w io.Writer, status orchestrator.Status) {
	fmt.Fprintf(w, "world running=%t image=%s clock=%t robots=%s\n",
		status.World.Running, status.World.Image, status.Ready.Clock, strings.Join(status.Ready.Robots, ","))
	for _, app := range status.Apps {
		fmt.Fprintf(w, "app %s running=%t api=:%d image=%s\n", app.Name, app.Running, app.APIPort, app.Image)
	}
	fmt.Fprintf(w, "connected=%t\n", status.Connected)
}
