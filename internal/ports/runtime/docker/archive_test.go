package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStartCopiesAssetsBeforeLaunchingAndCleansUpOnCopyFailure(t *testing.T) {
	for _, failCopy := range []bool{false, true} {
		t.Run(map[bool]string{false: "copied before start", true: "failed copy"}[failCopy], func(t *testing.T) {
			var calls []string
			payload := []byte("the exact tar payload")
			engine := DockerEngine{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls = append(calls, req.Method+" "+req.URL.Path)
				switch req.Method + " " + req.URL.Path {
				case "GET /containers/asset-test/json":
					return dockerResponse(http.StatusNotFound), nil
				case "POST /containers/create":
					return dockerResponseBody(http.StatusCreated, `{"Id":"created"}`), nil
				case "PUT /containers/created/archive":
					body, err := io.ReadAll(req.Body)
					if err != nil || !bytes.Equal(body, payload) || req.URL.Query().Get("path") != "/opt" || req.Header.Get("Content-Type") != "application/x-tar" {
						t.Errorf("invalid archive request: %q %s", body, req.URL)
					}
					if failCopy {
						return dockerResponseBody(http.StatusInternalServerError, "copy failed"), nil
					}
					return dockerResponse(http.StatusOK), nil
				case "POST /containers/created/start":
					if len(calls) != 4 {
						t.Error("process started before asset copy")
					}
					return dockerResponse(http.StatusNoContent), nil
				case "DELETE /containers/created":
					return dockerResponse(http.StatusNoContent), nil
				default:
					return nil, errors.New("unexpected request")
				}
			})}}
			err := engine.Start(context.Background(), ContainerSpec{Name: "asset-test", Image: "image", Archives: map[string][]byte{"/opt": payload}})
			if failCopy {
				if err == nil || !strings.Contains(err.Error(), "copy failed") {
					t.Fatalf("copy error=%v", err)
				}
				if calls[len(calls)-1] != "DELETE /containers/created" {
					t.Fatalf("failed container leaked: %v", calls)
				}
			} else if err != nil || len(calls) != 4 {
				t.Fatalf("asset copy not executed: calls=%v, error=%v", calls, err)
			}
		})
	}
}

func TestReadingImageArchiveReturnsTemporaryContainerCleanupErrors(t *testing.T) {
	engine := DockerEngine{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPost:
			return dockerResponseBody(http.StatusCreated, `{"Id":"temporary"}`), nil
		case http.MethodGet:
			return dockerResponseBody(http.StatusOK, "archive bytes"), nil
		case http.MethodDelete:
			return dockerResponseBody(http.StatusInternalServerError, "cleanup denied"), nil
		default:
			return nil, errors.New("unexpected request")
		}
	})}}
	if _, err := engine.ReadImageArchive(context.Background(), "world:tag", "/opt/simenv"); err == nil || !strings.Contains(err.Error(), "cleanup denied") {
		t.Fatalf("temporary image container cleanup failure lost: %v", err)
	}
}
