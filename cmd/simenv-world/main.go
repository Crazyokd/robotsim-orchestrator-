package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
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
	bodies := make([]world.Body, 0, len(session.Robots))
	for _, robot := range session.Robots {
		bodies = append(bodies, world.Body{Name: robot.Name, TopicPrefix: robot.TopicPrefix, Pose: robot.Pose})
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
