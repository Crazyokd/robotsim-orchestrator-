package orchestrator

import (
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
)

// world init 把 bundle 落成运行态：每台车一次 Provision，框架不解释内容。
// 交给车端的站点是世界系 m / rad，换算成车端坐标系是车端 Port 的事。
func TestStartProvisionsEveryVehicleFromTheBundle(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithBundle(t)}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	}); err != nil {
		t.Fatal(err)
	}
	for i, port := range vehicleFactory.ports {
		if len(port.provisioned) != 1 {
			t.Fatalf("%s provisioned %d times", port.robot.Name, len(port.provisioned))
		}
		provisioning := port.provisioned[0]
		if len(provisioning.Stations) != 2 {
			t.Fatalf("%s got %d stations", port.robot.Name, len(provisioning.Stations))
		}
		if provisioning.Stations[0] != (assets.Station{ID: 4, X: 8.038, Y: 10.0385, Yaw: 0}) {
			t.Fatalf("%s station 4 = %+v", port.robot.Name, provisioning.Stations[0])
		}
		if provisioning.Pose != worldPort.started[0].Robots[i].Pose {
			t.Fatalf("%s pose %+v != session pose %+v", port.robot.Name, provisioning.Pose, worldPort.started[0].Robots[i].Pose)
		}
	}
}

// bundle 按车端 kind 分发节：缺节即失败——世界换了而车端节没跟上，宁可启动失败。
func TestStartFailsWhenTheBundleHasNoSectionForTheVehicleKind(t *testing.T) {
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", `{
  "world": "simple",
  "stations": [],
  "worlds": {},
  "vehicles": {"other-vehicle": {}}
}`)
	orch := Orchestrator{World: &fakeWorldPort{}, VehicleFactory: &fakeVehicleFactory{}, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), "has no vehicles.fake section") {
		t.Fatalf("expected a missing-section error, got %v", err)
	}
}

func TestStartUsesBundleInitialPosesWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", `{
  "world": "simple",
  "stations": [],
  "initial_poses": [
    {"index": 1, "x": 1.5, "y": 2.5, "z": 0.1, "yaw": 1.57},
    {"index": 2, "x": -1.5, "y": -2.5, "z": 0.1, "yaw": 0}
  ],
  "worlds": {},
  "vehicles": {"fake": {}}
}`)
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	}); err != nil {
		t.Fatal(err)
	}
	// bundle 的 initial_poses 既喂世界 spawn（session），也喂车端出生位姿——同一份。
	if got := worldPort.started[0].Robots[0].Pose; got != (contract.Pose{X: 1.5, Y: 2.5, Z: 0.1, Yaw: 1.57}) {
		t.Fatalf("robot1 spawned at %+v, want the bundle's initial pose", got)
	}
	if got := vehicleFactory.ports[0].provisioned[0].Pose; got != (contract.Pose{X: 1.5, Y: 2.5, Z: 0.1, Yaw: 1.57}) {
		t.Fatalf("robot1 provisioned at %+v, want the bundle's initial pose", got)
	}
	if got := vehicleFactory.ports[1].provisioned[0].Pose; got != (contract.Pose{X: -1.5, Y: -2.5, Z: 0.1}) {
		t.Fatalf("robot2 provisioned at %+v, want the bundle's initial pose", got)
	}
}

// 目录里躺着资产却没有 manifest 是错误：bundle 已不完整，宁可失败也不猜。
func TestStartFailsWhenTheWorldShipsAssetsButNoBundle(t *testing.T) {
	dir := t.TempDir()
	writeWorldFile(t, dir, "simple", "stray.map_export", []byte("archive"))
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), "ships assets but no bundle.json manifest") {
		t.Fatalf("expected missing-bundle error, got %v", err)
	}
	if len(vehicleFactory.ports) != 0 {
		t.Fatal("containers were created although the bundle is incomplete")
	}
}

func TestStartFailsWhenTheBundleIsForAnotherWorld(t *testing.T) {
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", strings.Replace(arenaBundleJSON(), `"world": "simple"`, `"world": "corridor"`, 1))
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), `requested "simple"`) {
		t.Fatalf("expected world mismatch, got %v", err)
	}
}

// bundle 只给部分车声明初始位姿时直接失败：混用"声明的"和"默认 stride"是静默陷阱。
func TestStartRejectsPartialBundleInitialPoses(t *testing.T) {
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", `{
  "world": "simple",
  "stations": [],
  "initial_poses": [{"index": 1, "x": 1, "y": 1, "z": 0.05, "yaw": 0}],
  "worlds": {},
  "vehicles": {"fake": {}}
}`)
	orch := Orchestrator{World: &fakeWorldPort{}, VehicleFactory: &fakeVehicleFactory{}, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), "declares no initial pose for robot 2") {
		t.Fatalf("expected a partial-initial-poses error, got %v", err)
	}
}
