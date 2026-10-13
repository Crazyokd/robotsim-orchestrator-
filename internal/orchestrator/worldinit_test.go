package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

// world init 把 bundle 落成运行态：地图 → 站点 → 定位，缺一不可。
func TestStartSeedsBundleStationsAndLocalizes(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithWorldMap(t, archive)}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err != nil {
		t.Fatal(err)
	}
	for _, port := range vehicleFactory.ports {
		if port.seededMapName != "arena_world" {
			t.Fatalf("%s seeded stations on map %q", port.robot.Name, port.seededMapName)
		}
		if len(port.seededStations) != 2 {
			t.Fatalf("%s seeded %d stations", port.robot.Name, len(port.seededStations))
		}
		if port.seededStations[0] != (contract.Station{ID: 4, X: 6060, Y: 13220, Yaw: 0}) {
			t.Fatalf("%s station 4 = %+v", port.robot.Name, port.seededStations[0])
		}
		if port.localized != "arena_world" {
			t.Fatalf("%s localized on %q", port.robot.Name, port.localized)
		}
	}
}

func TestStartUsesBundleInitialPosesWhenDeclared(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	writeWorldAsset(t, dir, "simple.world.json", []byte(`{
  "world": "simple",
  "map_name": "arena_world",
  "origin_offset_mm": {"x": 0, "y": 0},
  "stations": [],
  "initial_poses": [
    {"index": 1, "x": 1.5, "y": 2.5, "z": 0.1, "yaw": 1.57},
    {"index": 2, "x": -1.5, "y": -2.5, "z": 0.1, "yaw": 0}
  ],
  "scenarios": ["s1.sh"]
}`))
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err != nil {
		t.Fatal(err)
	}
	if got := vehicleFactory.ports[0].localizedPose; got != (contract.Pose{X: 1.5, Y: 2.5, Z: 0.1, Yaw: 1.57}) {
		t.Fatalf("robot1 localized at %+v, want the bundle's initial pose", got)
	}
	if got := vehicleFactory.ports[1].localizedPose; got != (contract.Pose{X: -1.5, Y: -2.5, Z: 0.1}) {
		t.Fatalf("robot2 localized at %+v, want the bundle's initial pose", got)
	}
}

func TestStartFailsWhenTheWorldShipsAMapWithoutABundle(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	})
	if err == nil || !strings.Contains(err.Error(), "ships a map but no simple.world.json bundle") {
		t.Fatalf("expected missing-bundle error, got %v", err)
	}
	if len(vehicleFactory.ports) != 0 {
		t.Fatal("containers were created although the bundle is incomplete")
	}
}

func TestStartFailsWhenTheBundleDeclaresAnotherMap(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	writeWorldAsset(t, dir, "simple.world.json", []byte(arenaBundleJSON("other_world")))
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	})
	if err == nil || !strings.Contains(err.Error(), `holds "arena_world" but bundle declares "other_world"`) {
		t.Fatalf("expected map-name mismatch, got %v", err)
	}
	if vehicleFactory.ports[0].localized != "" {
		t.Fatal("vehicle localized despite the bundle mismatch")
	}
}

func TestStartFailsWhenTheBundleIsForAnotherWorld(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	writeWorldAsset(t, dir, "simple.world.json", []byte(strings.Replace(arenaBundleJSON("arena_world"), `"world": "simple"`, `"world": "corridor"`, 1)))
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	})
	if err == nil || !strings.Contains(err.Error(), `requested "simple"`) {
		t.Fatalf("expected world mismatch, got %v", err)
	}
}

func TestStartRollsBackWhenSeedingStationsFails(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	vehicleFactory := &fakeVehicleFactory{stationsFail: "robot1"}
	worldPort := &fakeWorldPort{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithWorldMap(t, archive)}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	})
	if err == nil || !strings.Contains(err.Error(), "stations refused") {
		t.Fatalf("expected station seeding error, got %v", err)
	}
	if !worldPort.stopped || !vehicleFactory.ports[0].stopped {
		t.Fatal("start did not roll back after the station seeding failure")
	}
}

// 过渡态：世界由环境提供，而车端 launch 按自己镜像的 worlds 目录校验世界名
// （sim_nav2.launch.py 的 _world_path）。把环境这份 SDF 只读挂进车端容器，
// 世界就只有一份文件。easygo 侧接受显式世界路径后（合同 §5 第 1 条）一起删。
func TestStartHandsTheWorldFileToTheAppContainer(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "corridor.sdf", []byte("<sdf/>"))
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady, assetsDir: dir}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "corridor", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		DataRoot: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "worlds", "corridor.sdf") + ":" + appWorldsDir + "/corridor.sdf:ro"
	for _, spec := range engine.started {
		if spec.Labels[contract.LabelRole] != contract.RoleApp {
			continue
		}
		if !slices.Contains(spec.Binds, want) {
			t.Fatalf("%s binds=%v missing the world handover", spec.Name, spec.Binds)
		}
		return
	}
	t.Fatal("no app container was started")
}

func TestWorldHandoverBindSkipsWorldsTheEnvironmentDoesNotShip(t *testing.T) {
	dir := t.TempDir()
	writeWorldAsset(t, dir, "corridor.sdf", []byte("<sdf/>"))
	if bind := worldHandoverBind(dir, "simple"); bind != "" {
		t.Fatalf("unexpected handover for a world the environment does not ship: %s", bind)
	}
	if bind := worldHandoverBind(dir, "corridor"); !strings.HasSuffix(bind, "/corridor.sdf:ro") {
		t.Fatalf("handover bind=%q", bind)
	}
}

func TestWorldHandoverBindUsesAnAbsoluteHostPath(t *testing.T) {
	dir := t.TempDir()
	writeWorldAsset(t, dir, "corridor.sdf", []byte("<sdf/>"))
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	// 相对的资产目录：读文件没问题，但挂载源必须被解析成绝对路径，否则 Docker 拒绝创建容器。
	bind := worldHandoverBind("worlds-assets", "corridor")
	if bind != "" {
		t.Fatalf("expected no handover when the world file is not under the assets dir: %s", bind)
	}
	if err := os.MkdirAll(filepath.Join(dir, "worlds-assets", "worlds"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "worlds", "corridor.sdf"), filepath.Join(dir, "worlds-assets", "worlds", "corridor.sdf")); err != nil {
		t.Fatal(err)
	}
	bind = worldHandoverBind("worlds-assets", "corridor")
	host, _, ok := strings.Cut(bind, ":")
	if !ok {
		t.Fatalf("bind=%q", bind)
	}
	if !filepath.IsAbs(host) {
		t.Fatalf("host side of the bind is relative: %q", bind)
	}
}

// bundle 只给部分车声明初始位姿时直接失败：混用"声明的"和"默认 stride"是静默陷阱。
func TestStartRejectsPartialBundleInitialPoses(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	writeWorldAsset(t, dir, "simple.world.json", []byte(`{
  "world": "simple",
  "map_name": "arena_world",
  "origin_offset_mm": {"x": 0, "y": 0},
  "stations": [],
  "initial_poses": [{"index": 1, "x": 1, "y": 1, "z": 0.05, "yaw": 0}],
  "scenarios": ["s1.sh"]
}`))
	orch := Orchestrator{Engine: &fakeEngine{infos: map[string]ContainerInfo{}}, AppReady: alwaysReady, assetsDir: dir}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		DataRoot: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "declares no initial pose for robot 2") {
		t.Fatalf("expected a partial-initial-poses error, got %v", err)
	}
}
