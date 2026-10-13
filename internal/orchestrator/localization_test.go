package orchestrator

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// arena 的 bundle 偏移：世界坐标 = 地图坐标 + (1978.0, -3181.5) mm。
var arenaOffset = contract.OriginOffset{X: 1978.0, Y: -3181.5}

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
			`{"data":{"localization":{"pose":{"x":%v,"y":%v,"theta":%v,"confidence":%d}}}}`,
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
		{"missing confidence is fine", []byte(`{"data":{"localization":{"pose":{"x":-1969,"y":3166,"theta":1.2}}}}`), true},
		{"pose missing", []byte(`{"data":{"localization":{"pose":{}}}}`), false},
		{"heading off by a radian", status(-1969, 3166, 1000, 99), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, observed, err := poseConverged(tc.body, want)
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
				`{"data":{"localization":{"pose":{"x":7194,"y":459,"theta":%v,"confidence":80}}}}`, tc.theta))
			ok, observed, err := poseConverged(body, want)
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
