package null

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func testWorld(t *testing.T) World {
	t.Helper()
	return World{Options: Options{DataRoot: t.TempDir()}}
}

func twoRobotSession() contract.Session {
	return contract.Session{
		World: "nullworld",
		Robots: []contract.Robot{
			{Index: 1, Name: "robot1", Pose: contract.Pose{X: 1, Z: 0.05}},
			{Index: 2, Name: "robot2", Pose: contract.Pose{X: 4, Z: 0.05}},
		},
	}
}

func TestLifecycle(t *testing.T) {
	world := testWorld(t)
	ctx := context.Background()

	// 未 start：Status 缺席、Pose 报错、Stop 幂等。
	status, err := world.Status(ctx)
	if err != nil || status.Present {
		t.Fatalf("expected absent status, got %+v (%v)", status, err)
	}
	if _, err := world.Pose(ctx, 1); err == nil {
		t.Fatal("pose before start must fail")
	}
	if err := world.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	attachments, err := world.Start(ctx, twoRobotSession(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 || attachments[0].Kind() != Kind {
		t.Fatalf("attachments=%v", attachments)
	}

	ready, err := world.Ready(ctx, twoRobotSession())
	if err != nil {
		t.Fatal(err)
	}
	if ready.World != "nullworld" || len(ready.Robots) != 2 || ready.Robots[0] != "robot1" {
		t.Fatalf("ready=%+v", ready)
	}

	status, err = world.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Present || !status.Running || len(status.Ready.Robots) != 2 {
		t.Fatalf("status=%+v", status)
	}

	pose, err := world.Pose(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if pose.Entity != "robot2" || pose.Pose.X != 4 {
		t.Fatalf("pose=%+v", pose)
	}

	moved, err := world.Reset(ctx, 2, contract.Pose{X: 9, Y: 8, Yaw: 1.5})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Pose.X != 9 || moved.Pose.Yaw != 1.5 {
		t.Fatalf("reset=%+v", moved)
	}
	// Reset 要持久化：另一个"进程"（新的 World 实例）读到的必须是新位姿。
	again, err := testWorldAt(world, t).Pose(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pose.X != 9 {
		t.Fatalf("reset did not persist: %+v", again.Pose)
	}

	if _, err := world.Pose(ctx, 9); err == nil {
		t.Fatal("unknown robot must fail")
	}

	if err := world.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = world.Status(ctx)
	if err != nil || status.Present {
		t.Fatalf("expected absent after stop, got %+v (%v)", status, err)
	}
}

// testWorldAt 造一个指向同一状态目录的新实例，模拟另一次 CLI 调用。
func testWorldAt(w World, t *testing.T) World {
	t.Helper()
	return World{Options: w.Options}
}

func TestStartParsesSectionAndRecordsNote(t *testing.T) {
	world := testWorld(t)
	ctx := context.Background()
	if _, err := world.Start(ctx, twoRobotSession(), []byte(`{"note": "second implementation"}`)); err != nil {
		t.Fatal(err)
	}
	status, err := world.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Detail["note"] != "second implementation" {
		t.Fatalf("section did not reach the world port: %v", status.Detail)
	}
	if _, err := world.Start(ctx, twoRobotSession(), []byte(`{"bogus": 1}`)); err == nil {
		t.Fatal("unknown section field must fail")
	}
}

func TestDefaultDataRootUsesCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	world := World{}
	path, err := world.statePath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("simenv", "null", "world.json")) {
		t.Fatalf("statePath=%q", path)
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Fatal("state dir must not be created before Start")
	}
}
