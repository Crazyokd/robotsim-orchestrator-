package easygo

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestForwardWritesRequestBytesUnchanged(t *testing.T) {
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
	vehicle := Vehicle{
		Robot: contract.Robot{Index: 1, Name: "robot1"},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "127.0.0.1:18189" {
				t.Errorf("dialed %s", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
		},
	}
	out, err := vehicle.Forward(context.Background(), request)
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

func TestForwardDialsTheRobotsOwnAPIPort(t *testing.T) {
	vehicle := Vehicle{
		Robot: contract.Robot{Index: 2, Name: "robot2"},
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			if address != "127.0.0.1:18289" {
				t.Fatalf("dialed %s, want robot2's API", address)
			}
			return nil, context.Canceled
		},
	}
	if _, err := vehicle.Forward(context.Background(), []byte("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Fatal("expected the dial to fail")
	}
}
