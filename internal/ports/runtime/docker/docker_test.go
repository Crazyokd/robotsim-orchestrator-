package docker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func dockerResponse(status int) *http.Response {
	return dockerResponseBody(status, "")
}

func dockerResponseBody(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestStripDockerStreamPlain(t *testing.T) {
	if got := stripDockerStream([]byte(`{"clock":true}`)); got != `{"clock":true}` {
		t.Fatalf("got %q", got)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(ErrNotFound) {
		t.Fatal("plain ErrNotFound")
	}
}

func TestDockerStopDeletesAlreadyStoppedContainer(t *testing.T) {
	var requests []string
	engine := DockerEngine{
		HTTP: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.RequestURI())
				switch len(requests) {
				case 1:
					return dockerResponse(http.StatusNotModified), nil
				case 2:
					return dockerResponse(http.StatusNoContent), nil
				default:
					t.Fatalf("unexpected request %v", req)
					return nil, nil
				}
			}),
		},
	}

	if err := engine.Stop(context.Background(), "robot1"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /containers/robot1/stop?t=20",
		"DELETE /containers/robot1?force=true",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests=%v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests=%v, want %v", requests, want)
		}
	}
}

func TestDockerStartReturnsInspectError(t *testing.T) {
	var requests []string
	engine := DockerEngine{
		HTTP: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.RequestURI())
				return dockerResponseBody(http.StatusInternalServerError, "inspect failed"), nil
			}),
		},
	}

	err := engine.Start(context.Background(), ContainerSpec{Name: "robot1"})
	if err == nil || !strings.Contains(err.Error(), "inspect failed") {
		t.Fatalf("error=%v", err)
	}
	if len(requests) != 1 || requests[0] != "GET /containers/robot1/json" {
		t.Fatalf("requests=%v", requests)
	}
}

func TestDockerStartReturnsStopError(t *testing.T) {
	var requests []string
	engine := DockerEngine{
		HTTP: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.RequestURI())
				switch len(requests) {
				case 1:
					return dockerResponseBody(http.StatusOK, `{"Id":"id","Name":"/robot1","State":{"Running":true},"Config":{"Image":"img"}}`), nil
				case 2:
					return dockerResponseBody(http.StatusInternalServerError, "stop failed"), nil
				default:
					t.Fatalf("unexpected request %v", req)
					return nil, nil
				}
			}),
		},
	}

	err := engine.Start(context.Background(), ContainerSpec{Name: "robot1"})
	if err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("error=%v", err)
	}
	want := []string{
		"GET /containers/robot1/json",
		"POST /containers/robot1/stop?t=20",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests=%v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests=%v, want %v", requests, want)
		}
	}
}

func TestDockerStartRemovesContainerWhenStartFails(t *testing.T) {
	var requests []string
	engine := DockerEngine{
		HTTP: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.RequestURI())
				switch len(requests) {
				case 1:
					return dockerResponseBody(http.StatusNotFound, "missing"), nil
				case 2:
					return dockerResponseBody(http.StatusCreated, `{"Id":"created-id"}`), nil
				case 3:
					return dockerResponseBody(http.StatusInternalServerError, "start failed"), nil
				case 4:
					return dockerResponse(http.StatusNoContent), nil
				default:
					t.Fatalf("unexpected request %v", req)
					return nil, nil
				}
			}),
		},
	}

	err := engine.Start(context.Background(), ContainerSpec{Name: "robot1"})
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("error=%v", err)
	}
	want := []string{
		"GET /containers/robot1/json",
		"POST /containers/create?name=robot1",
		"POST /containers/created-id/start",
		"DELETE /containers/created-id?force=true",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests=%v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests=%v, want %v", requests, want)
		}
	}
}

func TestDeviceMappings(t *testing.T) {
	got := deviceMappings([]string{"/dev/dri", "/dev/foo:/dev/bar"})
	if len(got) != 2 {
		t.Fatalf("mappings=%v", got)
	}
	if got[0]["PathOnHost"] != "/dev/dri" || got[0]["PathInContainer"] != "/dev/dri" {
		t.Fatalf("bare path mapping=%v", got[0])
	}
	if got[0]["CgroupPermissions"] != "rwm" {
		t.Fatalf("permissions=%v", got[0])
	}
	if got[1]["PathOnHost"] != "/dev/foo" || got[1]["PathInContainer"] != "/dev/bar" {
		t.Fatalf("explicit mapping=%v", got[1])
	}
	if len(deviceMappings(nil)) != 0 {
		t.Fatal("nil devices must map to none")
	}
}
