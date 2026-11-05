package easygo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
)

// Forward writes request to this vehicle's API and returns the response bytes.
// 字节原样进出：框架不解析，HTTP 细节是本 Port 的事。
func (v Vehicle) Forward(ctx context.Context, request []byte) ([]byte, error) {
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

func (v Vehicle) dialAPI(ctx context.Context) (net.Conn, error) {
	address := fmt.Sprintf("127.0.0.1:%d", runtimeFor(v.Robot.Index).apiPort)
	if v.Dial != nil {
		return v.Dial(ctx, "tcp", address)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}
