package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func (o Orchestrator) Call(ctx context.Context, robot int, request []byte) ([]byte, error) {
	if robot < 1 {
		return nil, fmt.Errorf("robot index must be >= 1")
	}
	if len(bytes.TrimSpace(request)) == 0 {
		return nil, fmt.Errorf("request is empty")
	}
	ports, err := o.vehicleFactory(contract.StartConfig{}).Discover(ctx)
	if err != nil {
		return nil, err
	}
	want := contract.RobotName(robot)
	for _, port := range ports {
		status, err := port.Status(ctx)
		if err != nil || status.RobotName != want {
			continue
		}
		return port.Forward(ctx, request)
	}
	return nil, fmt.Errorf("robot %d is not in the running session", robot)
}

func (v EasyGoVehicle) Forward(ctx context.Context, request []byte) ([]byte, error) {
	if len(bytes.TrimSpace(request)) == 0 {
		return nil, fmt.Errorf("request is empty")
	}
	conn, err := v.dialAPI(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	if closer, ok := conn.(interface{ CloseWrite() error }); ok {
		if err := closer.CloseWrite(); err != nil {
			return nil, err
		}
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		return nil, err
	}
	if len(response) == 0 {
		return nil, fmt.Errorf("vehicle returned an empty response")
	}
	return response, nil
}

func (v EasyGoVehicle) dialAPI(ctx context.Context) (net.Conn, error) {
	address := fmt.Sprintf("127.0.0.1:%d", easyGoRuntimeFor(v.Robot.Index).apiPort)
	if v.Dial != nil {
		return v.Dial(ctx, "tcp", address)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}
