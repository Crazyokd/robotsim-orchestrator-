package world

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// 出生位姿（含 yaw）必须在 create 请求里给全：`create` 的 pose 会整体覆盖 SDF 里的
// model pose，所以请求里少了朝向就等于位姿里少了朝向——走廊里那 2.2 m 的滑移就是这么来的。
func TestCreateEntityCarriesTheSpawnPose(t *testing.T) {
	var request string
	gz := GZ{
		Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			for i, arg := range args {
				if arg == "--req" && i+1 < len(args) {
					request = args[i+1]
				}
			}
			return []byte("data: true"), nil
		},
	}

	if err := gz.CreateEntity(context.Background(), "map_world", "robot2", "model.sdf",
		contract.Pose{X: 7.5, Y: 0.3, Z: 0.05, Yaw: math.Pi}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"x: 7.5", "y: 0.3", "z: 0.05",
		"orientation: {x: 0, y: 0, z: 1, w: 6.12323399573675"} {
		if !strings.Contains(request, want) {
			t.Fatalf("request %q misses %q", request, want)
		}
	}

	// yaw=0 时四元数是单位：{z: 0, w: 1}。
	if err := gz.CreateEntity(context.Background(), "map_world", "robot1", "model.sdf",
		contract.Pose{X: -7.5, Z: 0.05}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request, "orientation: {x: 0, y: 0, z: 0, w: 1}") {
		t.Fatalf("request %q misses the identity orientation", request)
	}
}

func TestWaitClockEnforcesItsOwnTimeout(t *testing.T) {
	gz := GZ{
		Run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	err := gz.waitClockWithTimeout(context.Background(), 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}
