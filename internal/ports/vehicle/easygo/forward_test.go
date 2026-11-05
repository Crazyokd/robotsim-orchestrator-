package easygo

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// 车端 HTTP 服务对 half-close 连接直接丢包（CI 实测），Forward 必须走
// net/http 标准栈而不是手写 CloseWrite + io.ReadAll。这些测试用真实
// HTTP 服务器（httptest）验证语义契约：方法/路径/Host/请求体原样到达，
// 响应以 HTTP/1.1 文本返回（status line + headers + content-length + body）。
func TestForwardDeliversRequestToVehicleAPI(t *testing.T) {
	request := []byte("POST /api/v6/mapping/start HTTP/1.1\r\nHost: vehicle\r\nContent-Type: application/json\r\nContent-Length: 12\r\nConnection: close\r\n\r\n{\"name\":\"a\"}")
	var gotMethod, gotPath, gotHost, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHost = r.Host
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	vehicle := Vehicle{
		Robot: contract.Robot{Index: 1, Name: "robot1"},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "127.0.0.1:18189" {
				t.Errorf("dialed %s", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	out, err := vehicle.Forward(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/api/v6/mapping/start" || gotBody != `{"name":"a"}` {
		t.Fatalf("server received %s %s body=%q", gotMethod, gotPath, gotBody)
	}
	if gotHost != "vehicle" {
		t.Fatalf("host header lost: %q", gotHost)
	}
	text := string(out)
	if !strings.HasPrefix(text, "HTTP/1.1 200") {
		t.Fatalf("status line: %q", text)
	}
	if !strings.Contains(text, "\r\n\r\n") || !strings.HasSuffix(text, `{"ok":true}`) {
		t.Fatalf("response framing: %q", text)
	}
	if !strings.Contains(strings.ToLower(text), "content-length:") {
		t.Fatalf("missing content-length: %q", text)
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
	if _, err := vehicle.Forward(context.Background(), []byte("GET / HTTP/1.1\r\nHost: vehicle\r\n\r\n")); err == nil {
		t.Fatal("expected the dial to fail")
	}
}

func TestForwardRejectsMalformedRequestText(t *testing.T) {
	vehicle := Vehicle{
		Robot: contract.Robot{Index: 1, Name: "robot1"},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("should not dial")
			return nil, nil
		},
	}
	if _, err := vehicle.Forward(context.Background(), []byte("not http")); err == nil || !strings.Contains(err.Error(), "parse vehicle request") {
		t.Fatalf("err=%v", err)
	}
}
