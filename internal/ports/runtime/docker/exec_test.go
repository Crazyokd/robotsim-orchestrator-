package docker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDockerExecRequiresACompletedSuccessfulCommand(t *testing.T) {
	for _, tc := range []struct {
		name, inspect, wantError string
		inspectStatus            int
	}{
		{"success", `{"Running":false,"ExitCode":0}`, "", http.StatusOK},
		{"timeout", `{"Running":false,"ExitCode":124}`, "exit code 124", http.StatusOK},
		{"missing command", `{"Running":false,"ExitCode":127}`, "exit code 127", http.StatusOK},
		{"still running", `{"Running":true,"ExitCode":0}`, "still running", http.StatusOK},
		{"inspect failure", `inspect failed`, "inspect failed", http.StatusInternalServerError},
		{"malformed inspect", `{`, "unexpected end", http.StatusOK},
		{"missing running", `{"ExitCode":0}`, "missing exec state", http.StatusOK},
		{"missing exit code", `{"Running":false}`, "missing exec state", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 3)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				requests <- request.Method + " " + request.URL.Path
				switch request.Method + " " + request.URL.Path {
				case "POST /containers/robot1/exec":
					fmt.Fprint(w, `{"Id":"scan-probe"}`)
				case "POST /exec/scan-probe/start":
					fmt.Fprint(w, "scan output\n")
				case "GET /exec/scan-probe/json":
					w.WriteHeader(tc.inspectStatus)
					fmt.Fprint(w, tc.inspect)
				default:
					t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
					http.Error(w, "unexpected request", http.StatusNotFound)
				}
			}))
			defer server.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, server.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			engine := DockerEngine{HTTP: &http.Client{Transport: transport}}

			output, err := engine.Exec(context.Background(), "robot1", []string{"timeout", "180", "scan-probe"})
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error=%v, want %q", err, tc.wantError)
			}
			if tc.name == "timeout" || tc.name == "missing command" {
				var commandError *ExecError
				if !errors.As(err, &commandError) || commandError.Container != "robot1" || commandError.Output != output {
					t.Fatalf("command failure metadata was lost: error=%v output=%q", err, output)
				}
			}
			if output != "scan output" {
				t.Fatalf("output=%q", output)
			}
			if len(requests) != 3 {
				t.Fatalf("command completion was not inspected: requests=%d", len(requests))
			}
			for _, want := range []string{"POST /containers/robot1/exec", "POST /exec/scan-probe/start", "GET /exec/scan-probe/json"} {
				if got := <-requests; got != want {
					t.Fatalf("request=%q, want %q", got, want)
				}
			}
		})
	}
}
