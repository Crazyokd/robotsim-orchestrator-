package easygo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
)

type noExecEngine struct {
	docker.Engine
}

func (noExecEngine) Exec(context.Context, string, []string) (string, error) {
	return "", errors.New("localization must use the vehicle API without an external probe")
}

type localizeTransport func(*http.Request) (*http.Response, error)

func (f localizeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLocalizationUsesTheVehicleResultWithoutAnExternalProbe(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			previous := httpClient
			t.Cleanup(func() { httpClient = previous })
			requests, observations := 0, 0
			httpClient = &http.Client{Transport: localizeTransport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodPost || request.URL.Path != "/api/v6/vehicle/localization/initial-pose" {
					t.Fatalf("unexpected vehicle request: %s %s", request.Method, request.URL.Path)
				}
				requests++
				body := `{"error_code":0,"error_msg":"","data":null}`
				if status == http.StatusConflict {
					body = `{"error_code":900009,"error_msg":"Localization failed"}`
				}
				return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			vehicle := Vehicle{
				Robot: contract.Robot{Index: 1, Name: "robot1"}, Engine: noExecEngine{},
				Section: Section{MapName: "map"},
				DoJSON: func(context.Context, string, string, []byte) ([]byte, int, error) {
					observations++
					return []byte(`{"data":{"localization":{"pose":{"map_id":"map","x":0,"y":0,"theta":0}}}}`), http.StatusOK, nil
				},
			}
			err := vehicle.localize(context.Background(), "map", contract.Pose{})
			if requests != 1 {
				t.Fatalf("localization did not go directly to the vehicle: requests=%d err=%v", requests, err)
			}
			if status == http.StatusConflict {
				if err == nil || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "Localization failed") || observations != 0 {
					t.Fatalf("vehicle rejection was not propagated: err=%v observations=%d", err, observations)
				}
			} else if err != nil || observations != 1 {
				t.Fatalf("accepted localization did not verify convergence: err=%v observations=%d", err, observations)
			}
		})
	}
}

func TestInitialPoseRequestConvertsToMillimetresAndMilliradians(t *testing.T) {
	body, err := initialPoseRequest("arena_world", contract.Pose{X: 3, Y: -1.5, Yaw: 1.25})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["map_id"] != "arena_world" {
		t.Fatalf("map_id=%v", got["map_id"])
	}
	if got["x"] != float64(3000) || got["y"] != float64(-1500) || got["theta"] != float64(1250) {
		t.Fatalf("pose=%v", got)
	}
	if got["loctype"] != float64(2) {
		t.Fatalf("loctype=%v", got["loctype"])
	}
}

func TestAwaitPoseUsesTheSameToleranceForEveryMap(t *testing.T) {
	for _, mapName := range []string{"simple", "corridor", "0814caterpiller"} {
		for _, tc := range []struct {
			name, observedMap string
			x, y, theta       float64
			accepted          bool
		}{
			{"within tolerance", mapName, 140, 140, 0, true},
			{"boundary", mapName, 150, -150, 300, true},
			{"outside x", mapName, 151, 0, 0, false},
			{"outside y", mapName, 0, -151, 0, false},
			{"observed CAD offset", mapName, -284.5, 166.6, -2, false},
			{"wrong map", "other-map", 0, 0, 0, false},
			{"missing map", "", 0, 0, 0, false},
			{"wrong yaw", mapName, 0, 0, 301, false},
		} {
			t.Run(mapName+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				vehicle := Vehicle{Section: Section{MapName: mapName}, Robot: contract.Robot{Name: "robot1"}, DoJSON: func(context.Context, string, string, []byte) ([]byte, int, error) {
					cancel()
					return []byte(fmt.Sprintf(`{"data":{"localization":{"pose":{"map_id":%q,"x":%v,"y":%v,"theta":%v,"confidence":99}}}}`, tc.observedMap, tc.x, tc.y, tc.theta)), http.StatusOK, nil
				}}
				err := vehicle.awaitPose(ctx, contract.Pose{})
				if tc.accepted && err != nil {
					t.Fatalf("pose rejected: %v", err)
				}
				if !tc.accepted && !errors.Is(err, context.Canceled) {
					t.Fatalf("expected an unconverged pose, got %v", err)
				}
			})
		}
	}
}

// arena 的 bundle 偏移：世界坐标 = 地图坐标 + (1978.0, -3181.5) mm。
var arenaOffset = OriginOffset{X: 1978.0, Y: -3181.5}

func TestMapPoseConvertsWorldToMapFrame(t *testing.T) {
	got := mapPose(contract.Pose{X: 0, Y: 0, Z: 0.05, Yaw: 0}, arenaOffset)
	if got != (contract.Pose{X: -1.978, Y: 3.1815, Z: 0.05, Yaw: 0}) {
		t.Fatalf("mapPose = %+v", got)
	}
	// 与实测对齐：robot1 在世界 (0,0) 时，车端上报 x≈-1969mm y≈3166mm。
	if abs(got.X*1000-(-1969)) > 300 || abs(got.Y*1000-3166) > 300 {
		t.Fatalf("converted pose %+v is not within tolerance of the observed report", got)
	}
}

func TestPoseConverged(t *testing.T) {
	want := contract.Pose{X: -1.978, Y: 3.1815, Yaw: 0}
	status := func(x, y, theta float64, confidence int) []byte {
		return []byte(fmt.Sprintf(
			`{"data":{"localization":{"pose":{"map_id":"arena_world","x":%v,"y":%v,"theta":%v,"confidence":%d}}}}`,
			x, y, theta, confidence,
		))
	}
	cases := []struct {
		name string
		body []byte
		ok   bool
	}{
		{"converged within centimetres", status(-1969, 3166, 1.2, 99), true},
		{"exactly on target", status(-1978, 3181.5, 0, 99), true},
		{"world frame instead of map frame (the frame bug)", status(0, 0, 0, 99), false},
		{"low confidence is recorded, not gated", status(-1969, 3166, 1.2, 40), true},
		{"missing confidence is fine", []byte(`{"data":{"localization":{"pose":{"map_id":"arena_world","x":-1969,"y":3166,"theta":1.2}}}}`), true},
		{"pose missing", []byte(`{"data":{"localization":{"pose":{"map_id":"arena_world"}}}}`), false},
		{"heading off by a radian", status(-1969, 3166, 1000, 99), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, observed, err := poseConverged(tc.body, want, "arena_world")
			if err != nil {
				t.Fatal(err)
			}
			if ok != tc.ok {
				t.Fatalf("converged=%v (observed %s), want %v", ok, observed, tc.ok)
			}
			if observed == "" {
				t.Fatal("observed pose description is empty")
			}
		})
	}
}

// 朝向比较必须按角度折：车端对 π 的表示是 -π（上报毫弧度 -3141），与下发的 +π 是同一个朝向。
// 实测 robot2 位置只差 2 mm、conf 80，却因为直接相减得到 6.28 rad 被判"没收敛"。
func TestPoseConvergedWrapsHeadingAcrossPi(t *testing.T) {
	want := contract.Pose{X: 7.192, Y: 0.458, Yaw: math.Pi}
	cases := []struct {
		name  string
		theta float64
		ok    bool
	}{
		{"reported as -pi (milliradians)", -3141, true},
		{"reported as +pi", 3141.6, true},
		{"reported as pi plus noise", 3170, true},
		{"a quarter turn away", 2356, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(
				`{"data":{"localization":{"pose":{"map_id":"arena_world","x":7194,"y":459,"theta":%v,"confidence":80}}}}`, tc.theta))
			ok, observed, err := poseConverged(body, want, "arena_world")
			if err != nil {
				t.Fatal(err)
			}
			if ok != tc.ok {
				t.Fatalf("converged=%v (observed %s), want %v", ok, observed, tc.ok)
			}
		})
	}
}

func TestInitializationState(t *testing.T) {
	result, step, err := initializationState([]byte(`{"data":{"system":{"initialization":{"result":"pending","step":"nav2_startup"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if result != "pending" || step != "nav2_startup" {
		t.Fatalf("result=%q step=%q", result, step)
	}
	if _, _, err := initializationState([]byte(`{"data":{"system":{}}}`)); err == nil ||
		!strings.Contains(err.Error(), "no system.initialization.result") {
		t.Fatalf("expected missing-initialization error, got %v", err)
	}
}

// 车端 init 没完成前不能放车：等它报 succeeded，failed 直接失败，超窗也失败。
func TestWaitForVehicleInitPollsUntilSucceeded(t *testing.T) {
	defer fastPolling(t)()
	served := 0
	server := statusServer(t, func(w http.ResponseWriter, r *http.Request) {
		served++
		if served < 3 {
			fmt.Fprint(w, `{"data":{"system":{"initialization":{"result":"pending","step":"nav2_startup"}}}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"system":{"initialization":{"result":"succeeded"}}}}`)
	})
	if err := waitForVehicleInit(context.Background(), server); err != nil {
		t.Fatalf("expected init to succeed after retries: %v", err)
	}
	if served < 3 {
		t.Fatalf("expected polling, served=%d", served)
	}
}

func TestWaitForVehicleInitRetriesTransientErrors(t *testing.T) {
	defer fastPolling(t)()
	served := 0
	server := statusServer(t, func(w http.ResponseWriter, r *http.Request) {
		served++
		if served < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"data":{"system":{"initialization":{"result":"succeeded"}}}}`)
	})
	if err := waitForVehicleInit(context.Background(), server); err != nil {
		t.Fatalf("expected transient errors to be retried: %v", err)
	}
	if served < 3 {
		t.Fatalf("expected retries, served=%d", served)
	}
}

func TestWaitForVehicleInitFailsOnFailed(t *testing.T) {
	server := statusServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"system":{"initialization":{"result":"failed","step":"nav2_startup"}}}}`)
	})
	err := waitForVehicleInit(context.Background(), server)
	if err == nil || !strings.Contains(err.Error(), "reports failed") {
		t.Fatalf("expected failed-init error, got %v", err)
	}
}

func TestWaitForVehicleInitStopsWithContext(t *testing.T) {
	server := statusServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"system":{"initialization":{"result":"pending"}}}}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := waitForVehicleInit(ctx, server); err == nil {
		t.Fatal("expected the init wait to stop with its context")
	}
}

func fastPolling(t *testing.T) func() {
	t.Helper()
	previous := localizePollInterval
	localizePollInterval = time.Millisecond
	return func() { localizePollInterval = previous }
}

// statusServer 起一个只回状态体的假车端，返回其端口。
func statusServer(t *testing.T, handler http.HandlerFunc) int {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	addr, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address %v", server.Listener.Addr())
	}
	return addr.Port
}
