package main

import (
	"fmt"
	"os"

	"git.standard-robots.com/yicao/sim/internal/cli"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

func main() {
	engine, err := orchestrator.NewDockerEngine()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, orchestrator.Orchestrator{Engine: engine}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
