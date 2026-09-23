package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("simenv-world", flag.ContinueOnError)
	sessionText := fs.String("session", "", "")
	camerasText := fs.String("cameras", "", "")
	assets := fs.String("assets", "/opt/simenv", "")
	model := fs.String("model", "/opt/simenv/models/easygo_bj_bkn_description/models/easygo_bj_bkn_model.sdf", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sessionText == "" {
		return fmt.Errorf("session must not be empty")
	}
	var session contract.Session
	if err := json.Unmarshal([]byte(*sessionText), &session); err != nil {
		return err
	}
	if err := session.Validate(); err != nil {
		return err
	}
	cameraRobots, err := parseCameraRobots(*camerasText, session)
	if err != nil {
		return err
	}
	bodies := make([]world.Body, 0, len(session.Robots))
	for _, robot := range session.Robots {
		bodies = append(bodies, world.Body{
			Name:        robot.Name,
			TopicPrefix: robot.TopicPrefix,
			Pose:        robot.Pose,
			KeepCameras: cameraRobots[robot.Index],
		})
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	proc := world.Process{
		AssetsRoot: *assets + "/worlds",
		ModelPath:  *model,
		ReadyPath:  contract.WorldReadyPath,
		Transport:  world.GZ{},
		WorldName:  session.World,
		Bodies:     bodies,
	}
	return proc.Run(ctx)
}

func parseCameraRobots(text string, session contract.Session) (map[int]bool, error) {
	known := make(map[int]bool, len(session.Robots))
	for _, robot := range session.Robots {
		known[robot.Index] = true
	}
	selected := map[int]bool{}
	for _, part := range strings.Split(strings.TrimSpace(text), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		index, err := strconv.Atoi(part)
		if err != nil || index < 1 {
			return nil, fmt.Errorf("cameras robot %q is not a positive index", part)
		}
		if !known[index] {
			return nil, fmt.Errorf("cameras robot %d not in session", index)
		}
		selected[index] = true
	}
	return selected, nil
}
