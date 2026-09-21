package main

import (
	"context"
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
	worldName := fs.String("world", contract.DefaultWorld, "")
	robotsN := fs.Int("robots", 1, "")
	assets := fs.String("assets", "/opt/simenv", "")
	model := fs.String("model", "/opt/simenv/models/easygo_bj_bkn_description/models/easygo_bj_bkn_model.sdf", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := contract.StartConfig{Robots: *robotsN, World: *worldName, AppImage: "unused", EnvImage: "unused"}
	robots, err := cfg.RobotsSpec()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	proc := world.Process{
		AssetsRoot: *assets + "/worlds",
		ModelPath:  *model,
		ReadyPath:  contract.WorldReadyPath,
		Transport:  world.GZ{},
		WorldName:  *worldName,
		Robots:     robots,
		Hold: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		},
	}
	return proc.Run(ctx)
}
