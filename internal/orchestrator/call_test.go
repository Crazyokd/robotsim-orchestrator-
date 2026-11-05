package orchestrator

import (
	"bytes"
	"context"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// call 只路由：按车找到 Port，字节原样进出；怎么 dial 是车端 Port 的事
// （dial 与协议断言在车端 Port 包里）。
func TestCallRoutesToTheRequestedRobot(t *testing.T) {
	request := []byte("GET /api/v6/map HTTP/1.1\r\nHost: vehicle\r\nConnection: close\r\n\r\n")
	response := []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}")
	var got []byte
	robot2 := &fakeVehiclePort{
		robot:  contract.Robot{Index: 2, Name: "robot2"},
		status: VehicleStatus{Name: "robot2", Running: true},
		forward: func(_ context.Context, req []byte) ([]byte, error) {
			got = append([]byte(nil), req...)
			return response, nil
		},
	}
	robot1 := &fakeVehiclePort{
		robot:  contract.Robot{Index: 1, Name: "robot1"},
		status: VehicleStatus{Name: "robot1", Running: true},
		forward: func(context.Context, []byte) ([]byte, error) {
			t.Fatal("call for robot 2 must not reach robot 1")
			return nil, nil
		},
	}
	factory := &fakeVehicleFactory{ports: []*fakeVehiclePort{robot1, robot2}}

	out, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 2, request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, request) {
		t.Fatalf("wire request changed: %q", got)
	}
	if !bytes.Equal(out, response) {
		t.Fatalf("response=%q", out)
	}
	if _, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 3, request); err == nil {
		t.Fatal("expected missing robot")
	}
}

func TestCallRejectsAnEmptyRequestWithoutForwarding(t *testing.T) {
	port := &fakeVehiclePort{
		robot:  contract.Robot{Index: 1, Name: "robot1"},
		status: VehicleStatus{Name: "robot1", Running: true},
		forward: func(context.Context, []byte) ([]byte, error) {
			t.Fatal("should not forward")
			return nil, nil
		},
	}
	factory := &fakeVehicleFactory{ports: []*fakeVehiclePort{port}}
	if _, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 1, []byte(" \n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestCallFailsWithoutAVehicleFactory(t *testing.T) {
	if _, err := (Orchestrator{}).Call(context.Background(), 1, []byte("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Fatal("expected error")
	}
}
