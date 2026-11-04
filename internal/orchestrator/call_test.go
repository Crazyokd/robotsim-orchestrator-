package orchestrator

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestCallForwardsRequestBytesUnchanged(t *testing.T) {
	request := []byte("POST /api/v6/mapping/start HTTP/1.1\r\nHost: vehicle\r\nContent-Length: 11\r\nConnection: close\r\n\r\n{\"name\":\"a\"}")
	response := []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	got := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf, _ := io.ReadAll(conn)
		got <- buf
		_, _ = conn.Write(response)
	}()
	factory := &callFactory{vehicles: []VehiclePort{
		EasyGoVehicle{
			Engine: &controlEngine{},
			Robot:  mustRobot(1),
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "127.0.0.1:18189" {
					t.Errorf("dialed %s", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
			},
		},
	}}
	out, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 1, request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(<-got, request) {
		t.Fatalf("wire request changed")
	}
	if !bytes.Equal(out, response) {
		t.Fatalf("response=%q", out)
	}
}

func TestCallFindsTheRequestedRobot(t *testing.T) {
	request := []byte("GET /api/v6/map HTTP/1.1\r\nHost: vehicle\r\nConnection: close\r\n\r\n")
	response := []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.ReadAll(conn)
		_, _ = conn.Write(response)
	}()
	factory := &callFactory{vehicles: []VehiclePort{
		EasyGoVehicle{
			Engine: &controlEngine{},
			Robot:  mustRobot(2),
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
			},
		},
	}}
	out, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 2, request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, response) {
		t.Fatalf("response=%q", out)
	}
	if _, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 1, request); err == nil {
		t.Fatal("expected missing robot")
	}
}

func TestCallRejectsAnEmptyRequestWithoutDialing(t *testing.T) {
	factory := &callFactory{vehicles: []VehiclePort{
		EasyGoVehicle{
			Engine: &controlEngine{},
			Robot:  mustRobot(1),
			Dial: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("should not dial")
				return nil, nil
			},
		},
	}}
	if _, err := (Orchestrator{VehicleFactory: factory}).Call(context.Background(), 1, []byte(" \n")); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err=%v", err)
	}
}

type callFactory struct{ vehicles []VehiclePort }

func (f *callFactory) New(contract.Session, contract.Robot) VehiclePort { return nil }
func (f *callFactory) Discover(context.Context) ([]VehiclePort, error)  { return f.vehicles, nil }

func mustRobot(index int) contract.Robot {
	robot, err := contract.NewRobot(index, contract.Pose{})
	if err != nil {
		panic(err)
	}
	return robot
}
