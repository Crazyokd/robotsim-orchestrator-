package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
)

// testAttachment 是假世界发给每台车的挂接：框架只按车转交，不解释内容。
type testAttachment struct{ prefix string }

func (testAttachment) Kind() string { return "test" }

type fakeWorldPort struct {
	started     []contract.Session
	stopped     bool
	startErr    error
	stopErr     error
	attachments []Attachment
	status      WorldStatus
	sections    []assets.Section
}

func (f *fakeWorldPort) Kind() string { return "fake-world" }

func (f *fakeWorldPort) Start(_ context.Context, session contract.Session, section assets.Section) ([]Attachment, error) {
	f.started = append(f.started, session)
	f.sections = append(f.sections, section)
	if f.startErr != nil {
		return nil, f.startErr
	}
	if f.attachments != nil {
		return f.attachments, nil
	}
	attachments := make([]Attachment, 0, len(session.Robots))
	for _, robot := range session.Robots {
		attachments = append(attachments, testAttachment{prefix: robot.Name})
	}
	return attachments, nil
}

func (f *fakeWorldPort) Ready(_ context.Context, session contract.Session) (WorldReady, error) {
	names := make([]string, 0, len(session.Robots))
	for _, robot := range session.Robots {
		names = append(names, robot.Name)
	}
	return WorldReady{World: session.World, Robots: names}, nil
}

func (f *fakeWorldPort) Status(context.Context) (WorldStatus, error) {
	if f.status.Present {
		return f.status, nil
	}
	ready := WorldReady{World: "map_world"}
	if len(f.started) > 0 {
		for _, robot := range f.started[0].Robots {
			ready.Robots = append(ready.Robots, robot.Name)
		}
	}
	return WorldStatus{
		Present: true,
		Running: true,
		Ready:   ready,
		Detail:  map[string]string{"image": "env"},
	}, nil
}

func (f *fakeWorldPort) Stop(context.Context) error {
	f.stopped = true
	return f.stopErr
}

func (f *fakeWorldPort) Pose(context.Context, int) (EntityPose, error) {
	return EntityPose{}, fmt.Errorf("fake world has no pose")
}

func (f *fakeWorldPort) Reset(context.Context, int, contract.Pose) (EntityPose, error) {
	return EntityPose{}, fmt.Errorf("fake world has no reset")
}

type fakeVehiclePort struct {
	session      contract.Session
	robot        contract.Robot
	attachment   Attachment
	status       VehicleStatus
	started      bool
	stopped      bool
	startErr     error
	readyErr     error
	provisionErr error
	provisioned  []Provisioning
	forward      func(ctx context.Context, request []byte) ([]byte, error)
	stopOrder    *[]string
}

func (f *fakeVehiclePort) Start(context.Context) error {
	f.started = true
	return f.startErr
}

func (f *fakeVehiclePort) Status(context.Context) (VehicleStatus, error) {
	return f.status, nil
}

func (f *fakeVehiclePort) Stop(context.Context) error {
	f.stopped = true
	if f.stopOrder != nil {
		*f.stopOrder = append(*f.stopOrder, f.robot.Name)
	}
	return nil
}

func (f *fakeVehiclePort) WaitReady(context.Context) error { return f.readyErr }

func (f *fakeVehiclePort) Provision(_ context.Context, provisioning Provisioning) error {
	if f.provisionErr != nil {
		return f.provisionErr
	}
	f.provisioned = append(f.provisioned, provisioning)
	return nil
}

func (f *fakeVehiclePort) Forward(ctx context.Context, request []byte) ([]byte, error) {
	if f.forward != nil {
		return f.forward(ctx, request)
	}
	return nil, fmt.Errorf("fake vehicle has no forward")
}

type fakeVehicleFactory struct {
	ports         []*fakeVehiclePort
	discovered    []*fakeVehiclePort // 非空时 Discover 返回它们：预置的残留会话
	failRobot     string
	notReadyRobot string
	provisionFail string
	stopOrder     []string
	discoverErr   error
	sections      []assets.Section
}

func (f *fakeVehicleFactory) Kind() string { return "fake" }

func (f *fakeVehicleFactory) New(_ context.Context, session contract.Session, robot contract.Robot, attachment Attachment, section assets.Section) (VehiclePort, error) {
	port := &fakeVehiclePort{
		session:    session,
		robot:      robot,
		attachment: attachment,
		status: VehicleStatus{
			Name:    robot.Name,
			Running: true,
		},
		stopOrder: &f.stopOrder,
	}
	f.sections = append(f.sections, section)
	if robot.Name == f.failRobot {
		port.startErr = errors.New("vehicle start failed")
	}
	if robot.Name == f.notReadyRobot {
		port.readyErr = errors.New("app API never answered")
	}
	if robot.Name == f.provisionFail {
		port.provisionErr = errors.New("provision refused")
	}
	f.ports = append(f.ports, port)
	return port, nil
}

func (f *fakeVehicleFactory) Discover(context.Context) ([]VehiclePort, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	source := f.ports
	if f.discovered != nil {
		source = f.discovered
	}
	ports := make([]VehiclePort, 0, len(source))
	for _, port := range source {
		ports = append(ports, port)
	}
	return ports, nil
}

func TestStartDelegatesThroughWorldAndVehiclePorts(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}
	status, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(worldPort.started) != 1 {
		t.Fatalf("world starts=%d", len(worldPort.started))
	}
	if got := worldPort.started[0].Robots; len(got) != 2 || got[0].Name != "robot1" || got[1].Name != "robot2" {
		t.Fatalf("session=%+v", worldPort.started[0])
	}
	if len(vehicleFactory.ports) != 2 {
		t.Fatalf("vehicle ports=%d", len(vehicleFactory.ports))
	}
	if !vehicleFactory.ports[0].started || !vehicleFactory.ports[1].started {
		t.Fatalf("vehicle ports were not started: %+v", vehicleFactory.ports)
	}
	if vehicleFactory.ports[0].robot.Name != "robot1" || vehicleFactory.ports[1].robot.Name != "robot2" {
		t.Fatalf("vehicle robots=%+v", vehicleFactory.ports)
	}
	if vehicleFactory.ports[0].session.Robots[1].Name != "robot2" {
		t.Fatalf("vehicle session=%+v", vehicleFactory.ports[0].session)
	}
	// 世界给的挂接按车原样转交，框架不解释。
	for i, port := range vehicleFactory.ports {
		if port.attachment != (testAttachment{prefix: port.robot.Name}) {
			t.Fatalf("attachment %d=%+v", i, port.attachment)
		}
	}
	if !status.Connected || len(status.Vehicles) != 2 {
		t.Fatalf("status=%+v", status)
	}
}

func TestStartFailsWhenTheWorldReturnsTheWrongAttachmentCount(t *testing.T) {
	worldPort := &fakeWorldPort{attachments: []Attachment{}}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), "0 attachments for 2 robots") {
		t.Fatalf("error=%v", err)
	}
	if !worldPort.stopped {
		t.Fatal("world was not rolled back")
	}
	if len(vehicleFactory.ports) != 0 {
		t.Fatalf("vehicles were created despite the attachment mismatch: %+v", vehicleFactory.ports)
	}
}

func TestStartRollsBackWorldAndStartedVehicles(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{failRobot: "robot2"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}

	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple",
	})
	if err == nil {
		t.Fatal("expected vehicle start error")
	}
	if !worldPort.stopped {
		t.Fatal("world was not rolled back")
	}
	if len(vehicleFactory.ports) != 2 {
		t.Fatalf("ports=%d", len(vehicleFactory.ports))
	}
	if !vehicleFactory.ports[0].stopped || !vehicleFactory.ports[1].stopped {
		t.Fatalf("vehicles were not rolled back: %+v", vehicleFactory.ports)
	}
	if got, want := strings.Join(vehicleFactory.stopOrder, ","), "robot2,robot1"; got != want {
		t.Fatalf("rollback order=%q, want %q", got, want)
	}
}

func TestStartStopsPreviouslyDiscoveredVehicles(t *testing.T) {
	stale := &fakeVehiclePort{
		robot:  contract.Robot{Index: 2, Name: "robot2"},
		status: VehicleStatus{Name: "robot2"},
	}
	vehicleFactory := &fakeVehicleFactory{ports: []*fakeVehiclePort{stale}}
	orch := Orchestrator{World: &fakeWorldPort{}, VehicleFactory: vehicleFactory}

	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	}); err != nil {
		t.Fatal(err)
	}
	if !stale.stopped {
		t.Fatal("stale vehicle was not stopped")
	}
}

func TestStartRejectsEmptyWorld(t *testing.T) {
	orch := Orchestrator{World: &fakeWorldPort{}, VehicleFactory: &fakeVehicleFactory{}}
	_, err := orch.Start(context.Background(), contract.StartConfig{Robots: 1})
	if err == nil {
		t.Fatal("expected error: world is required, no default")
	}
}

func TestStatusAggregatesIndependentVehiclePorts(t *testing.T) {
	worldPort := &fakeWorldPort{status: WorldStatus{
		Present: true,
		Running: true,
		Ready:   WorldReady{World: "map_world", Robots: []string{"robot1", "robot2"}},
		Detail:  map[string]string{"image": "env"},
	}}
	vehicleFactory := &fakeVehicleFactory{ports: []*fakeVehiclePort{
		{status: VehicleStatus{Name: "robot1", Running: true, Endpoint: "http://127.0.0.1:18189", Detail: map[string]string{"image": "runtime-1"}}},
		{status: VehicleStatus{Name: "robot2", Running: false, Endpoint: "http://127.0.0.1:18289", Detail: map[string]string{"image": "runtime-2"}}},
	}}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}

	status, err := orch.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Vehicles) != 2 {
		t.Fatalf("apps=%+v", status.Vehicles)
	}
	if status.Vehicles[0].Name == status.Vehicles[1].Name {
		t.Fatalf("apps=%+v", status.Vehicles)
	}
	if status.Connected {
		t.Fatalf("stopped vehicle must disconnect the session: %+v", status)
	}
}

func TestStatusRequiresTheReadyRobotSet(t *testing.T) {
	worldPort := &fakeWorldPort{status: WorldStatus{
		Present: true,
		Running: true,
		Ready:   WorldReady{World: "map_world", Robots: []string{"robot1", "robot2"}},
	}}
	vehicleFactory := &fakeVehicleFactory{ports: []*fakeVehiclePort{
		{status: VehicleStatus{Name: "robot1", Running: true}},
		{status: VehicleStatus{Name: "robot3", Running: true}},
	}}

	status, err := (Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connected {
		t.Fatalf("unexpected connected status: %+v", status)
	}
}

func TestStopStopsWorldAndApps(t *testing.T) {
	worldPort := &fakeWorldPort{}
	app := &fakeVehiclePort{
		robot:  contract.Robot{Index: 1, Name: "robot1"},
		status: VehicleStatus{Name: "robot1", Running: true},
	}
	orch := Orchestrator{World: worldPort, VehicleFactory: &fakeVehicleFactory{ports: []*fakeVehiclePort{app}}}
	if err := orch.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !app.stopped {
		t.Fatal("app was not stopped")
	}
	if !worldPort.stopped {
		t.Fatal("world was not stopped")
	}
}

func TestStopStopsWorldWhenVehicleDiscoveryFails(t *testing.T) {
	worldPort := &fakeWorldPort{}
	discoverErr := errors.New("vehicle discovery failed")
	orch := Orchestrator{
		World:          worldPort,
		VehicleFactory: &fakeVehicleFactory{discoverErr: discoverErr},
	}

	err := orch.Stop(context.Background())
	if !errors.Is(err, discoverErr) {
		t.Fatalf("error=%v, want %v", err, discoverErr)
	}
	if !worldPort.stopped {
		t.Fatal("world was not stopped")
	}
}

func TestStartFailsAndRollsBackWhenVehicleAppNeverServes(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{notReadyRobot: "robot1"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	}); err == nil {
		t.Fatal("expected start to fail when the vehicle app never serves")
	}
	if !worldPort.stopped {
		t.Fatal("world was not rolled back")
	}
	if len(vehicleFactory.ports) != 1 || !vehicleFactory.ports[0].stopped {
		t.Fatalf("vehicle was not rolled back: %+v", vehicleFactory.ports)
	}
}

func TestStartRollsBackWhenProvisionFails(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{provisionFail: "robot1"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithBundle(t)}
	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	})
	if err == nil || !strings.Contains(err.Error(), "provision refused") {
		t.Fatalf("expected provision error, got %v", err)
	}
	if !worldPort.stopped || !vehicleFactory.ports[0].stopped {
		t.Fatal("start did not roll back after the provision failure")
	}
}

func TestStartSkipsProvisioningWhenTheWorldShipsNoMap(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: t.TempDir()}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple",
	}); err != nil {
		t.Fatal(err)
	}
	if len(vehicleFactory.ports[0].provisioned) != 0 {
		t.Fatal("vehicle provisioned without a world bundle")
	}
}

// assetsWithBundle lays down the "simple" world bundle the tests assert on.
// 站点是世界系 m / rad；地图怎么灌是车端 Port 的事，框架测试不放地图档案。
func assetsWithBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", arenaBundleJSON())
	return dir
}

// arenaBundleJSON is a bundle manifest whose stations the tests assert on.
// initial_poses 留空 -> 出生位姿沿用 session 的 spawn 位姿。
// 两节的键是假实现的注册名：世界节会经 Start 交给世界 Port，车端节经 New 给车端。
func arenaBundleJSON() string {
	return `{
  "world": "simple",
  "stations": [
    {"id": 4, "x": 8.038, "y": 10.0385, "yaw": 0},
    {"id": 7, "x": 14.478, "y": 9.8185, "yaw": 0}
  ],
  "worlds": {"fake-world": {}},
  "vehicles": {"fake": {}}
}`
}

// writeWorldBundle 按目录布局写世界的 manifest：worlds/<world>/bundle.json。
func writeWorldBundle(t *testing.T, dir, world, manifest string) {
	t.Helper()
	writeWorldFile(t, dir, world, "bundle.json", []byte(manifest))
}

// writeWorldFile 在世界 bundle 目录里写任意文件（测试"目录有资产无 manifest"用）。
func writeWorldFile(t *testing.T, dir, world, name string, content []byte) {
	t.Helper()
	worldDir := filepath.Join(dir, "worlds", world)
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worldDir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}
