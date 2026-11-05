package main

import (
	"flag"
	"fmt"
	"os"
	"sync"

	"git.standard-robots.com/yicao/sim/internal/cli"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/vehicle/easygo"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
	nullworld "git.standard-robots.com/yicao/sim/internal/ports/world/null"
)

// 组合根：这里唯一允许认识所有 Port 实现的地方。新增一个世界/车端 Port
// 就是 import 它的包、按种类注册进 Worlds / Vehicles、并在 registerFlags
// 里挂它的 RegisterFlags。
func main() {
	// 容器运行时是惰性构造的：只有选中依赖它的种类时才建立连接——
	// 不依赖它的会话（例如纯进程内的种类）不该被要求有容器运行时可用。
	var (
		engineOnce sync.Once
		engine     docker.Engine
		engineErr  error
	)
	sharedEngine := func() (docker.Engine, error) {
		engineOnce.Do(func() {
			engine, engineErr = docker.NewDockerEngine()
		})
		return engine, engineErr
	}
	// 各 Port 的 Options 由它们的 RegisterFlags 在 start 解析时填充；
	// status / stop / call 不经 start FlagSet，Options 保持 nil——
	// 这些路径只发现与停容器，不需要启动参数。
	var worldOptions *gazebo.Options
	var nullWorldOptions *nullworld.Options
	var vehicleOptions *easygo.Options
	runner := orchestrator.Orchestrator{
		Worlds: map[string]orchestrator.NewWorldFunc{
			gazebo.Kind: func(contract.StartConfig) (orchestrator.WorldPort, error) {
				engine, err := sharedEngine()
				if err != nil {
					return nil, err
				}
				return gazebo.DockerWorld{Engine: engine, Options: deref(worldOptions)}, nil
			},
			nullworld.Kind: func(contract.StartConfig) (orchestrator.WorldPort, error) {
				return nullworld.World{Options: deref(nullWorldOptions)}, nil
			},
		},
		Vehicles: map[string]orchestrator.NewVehicleFactoryFunc{
			easygo.Kind: func(contract.StartConfig) (orchestrator.VehiclePortFactory, error) {
				engine, err := sharedEngine()
				if err != nil {
					return nil, err
				}
				return easygo.Factory{Engine: engine, Options: deref(vehicleOptions)}, nil
			},
		},
	}
	registerFlags := func(fs *flag.FlagSet) {
		worldOptions = gazebo.RegisterFlags(fs)
		nullWorldOptions = nullworld.RegisterFlags(fs)
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
