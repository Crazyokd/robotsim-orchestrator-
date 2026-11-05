package main

import (
	"flag"
	"fmt"
	"os"

	"git.standard-robots.com/yicao/sim/internal/cli"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/vehicle/easygo"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
)

// 组合根：这里唯一允许认识所有 Port 实现的地方。新增一个世界/车端 Port
// 就是 import 它的包并在构造器里加一行。
func main() {
	engine, err := docker.NewDockerEngine()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// 各 Port 的 Options 由它们的 RegisterFlags 在 start 解析时填充；
	// status / stop / call 不经 start FlagSet，Options 保持 nil——
	// 这些路径只发现与停容器，不需要启动参数。
	var worldOptions *gazebo.Options
	var vehicleOptions *easygo.Options
	runner := orchestrator.Orchestrator{
		NewWorld: func(cfg contract.StartConfig) orchestrator.WorldPort {
			return gazebo.DockerWorld{Engine: engine, Options: deref(worldOptions)}
		},
		NewVehicles: func(cfg contract.StartConfig) orchestrator.VehiclePortFactory {
			return easygo.Factory{Engine: engine, Options: deref(vehicleOptions)}
		},
	}
	registerFlags := func(fs *flag.FlagSet) {
		worldOptions = gazebo.RegisterFlags(fs)
		vehicleOptions = easygo.RegisterFlags(fs)
	}
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, runner, registerFlags); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
