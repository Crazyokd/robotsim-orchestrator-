package orchestrator

import (
	"bytes"
	"context"
	"fmt"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// Call 只路由：把请求字节交给目标车的 Forward，怎么 dial、什么协议是车端 Port 的事。
func (o Orchestrator) Call(ctx context.Context, robot int, request []byte) ([]byte, error) {
	if robot < 1 {
		return nil, fmt.Errorf("robot index must be >= 1")
	}
	if len(bytes.TrimSpace(request)) == 0 {
		return nil, fmt.Errorf("request is empty")
	}
	vehicleFactory, err := o.vehicleFactory(contract.StartConfig{})
	if err != nil {
		return nil, err
	}
	ports, err := vehicleFactory.Discover(ctx)
	if err != nil {
		return nil, err
	}
	want := contract.RobotName(robot)
	for _, port := range ports {
		status, err := port.Status(ctx)
		if err != nil || status.Name != want {
			continue
		}
		return port.Forward(ctx, request)
	}
	return nil, fmt.Errorf("robot %d is not in the running session", robot)
}
