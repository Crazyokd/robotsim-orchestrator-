package process

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

func TestParseEntityPoseReadsNamedModel(t *testing.T) {
	raw := `{"pose":[{"name":"robot2","position":{"x":1.5,"y":-2,"z":0.05},"orientation":{"x":0,"y":0,"z":0,"w":1}}]}`
	pose, err := ParseEntityPose(raw, "robot2")
	if err != nil {
		t.Fatal(err)
	}
	if pose.X != 1.5 || pose.Y != -2 || pose.Z != 0.05 || pose.Yaw != 0 {
		t.Fatalf("pose=%+v", pose)
	}
}

func TestParseEntityPoseFailsWhenNameIsMissing(t *testing.T) {
	if _, err := ParseEntityPose(`{"pose":[{"name":"robot1"}]}`, "robot2"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseEntityPoseReadsLatestMatchingSample(t *testing.T) {
	raw := `{"pose":[{"name":"robot1","position":{"x":1},"orientation":{"w":1}}]}
{"pose":[{"name":"robot1","position":{"x":2},"orientation":{"w":1}}]}
{"pose":[{"name":"robot2","position":{"x":3},"orientation":{"w":1}}]}`
	pose, err := ParseEntityPose(raw, "robot1")
	if err != nil {
		t.Fatal(err)
	}
	if pose.X != 2 {
		t.Fatalf("pose=%+v, want latest robot1 sample", pose)
	}
}

func TestParseEntityPoseRejectsMalformedStream(t *testing.T) {
	valid := `{"pose":[{"name":"robot1","orientation":{"w":1}}]}`
	for _, raw := range []string{"", "{", valid + "\n{", valid + "\nnoise", valid + "\n[]", valid + "\nnull"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseEntityPose(raw, "robot1"); err == nil {
				t.Fatal("expected malformed stream to fail")
			}
		})
	}
}

func TestSetPoseCommandCarriesYaw(t *testing.T) {
	cmd := SetPoseCommand("simple", "robot1", contract.Pose{X: 3, Y: 1, Z: 0.05, Yaw: math.Pi})
	req := cmd[len(cmd)-1]
	for _, want := range []string{`name: "robot1"`, "x: 3", "y: 1", "z: 0.05", "z: 1", "w: 6.12323399573675"} {
		if !strings.Contains(req, want) {
			t.Fatalf("req %q misses %q", req, want)
		}
	}
}

func TestAssertSetPoseOK(t *testing.T) {
	if err := AssertSetPoseOK("data: true"); err != nil {
		t.Fatal(err)
	}
	if err := AssertSetPoseOK("data: false"); err == nil {
		t.Fatal("expected error")
	}
}
