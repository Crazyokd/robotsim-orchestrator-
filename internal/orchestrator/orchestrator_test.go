package orchestrator

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

type fakeEngine struct {
	imageFiles map[string]string
	started    []ContainerSpec
	stopped    []string
	infos      map[string]ContainerInfo
	ready      string
	execErr    error
}

type blockingReadyEngine struct {
	fakeEngine
}

// fakeRegistry / fakeSyncRegistry mirror the shape of the registry files the app
// image ships: main.vehicle_serial_no has no default_value, sync.membership_port
// has one that collides across vehicles on a shared host network.
const fakeRegistry = `items:
  - key: main.vehicle_type
    name: 车型
    value_type: string

  - key: main.vehicle_serial_no
    name: 车辆序列号
    value_type: string
    is_coverable: 0

  - key: main.nickname
    name: 自定义名称
    default_value: "EasyGo"
    value_type: string
`

const fakeSyncRegistry = `items:
  - key: sync.discovery_backend
    default_value: cluster-gossip
    value_type: enum

  - key: sync.membership_port
    name: 同步 membership 端口
    default_value: 25354
    value_type: int32
`

// ReadImageFile stands in for reading the image's registry files.
func (f *fakeEngine) ReadImageFile(_ context.Context, _, path string) ([]byte, error) {
	if f.imageFiles != nil {
		if content, ok := f.imageFiles[path]; ok {
			return []byte(content), nil
		}
	}
	switch filepath.Base(path) {
	case filepath.Base(vehicleSerialRegistry):
		return []byte(fakeRegistry), nil
	case filepath.Base(syncRegistry):
		return []byte(fakeSyncRegistry), nil
	}
	return nil, fmt.Errorf("fake image has no %s", path)
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	return "sha256:" + ref, nil
}

func (f *fakeEngine) Start(_ context.Context, spec ContainerSpec) error {
	f.started = append(f.started, spec)
	f.infos[spec.Name] = ContainerInfo{
		Name: spec.Name, Running: true, Image: spec.Image, ID: spec.Name + "-id", Labels: spec.Labels,
	}
	return nil
}

func (f *fakeEngine) Stop(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	info := f.infos[name]
	info.Running = false
	f.infos[name] = info
	return nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (ContainerInfo, error) {
	info, ok := f.infos[name]
	if !ok {
		return ContainerInfo{}, ErrNotFound
	}
	return info, nil
}

func (f *fakeEngine) Exec(_ context.Context, name string, cmd []string) (string, error) {
	if name == contract.WorldContainer && strings.Contains(strings.Join(cmd, " "), contract.WorldReadyPath) {
		if f.execErr != nil {
			return "", f.execErr
		}
		return f.ready, nil
	}
	return "", nil
}

func (f *fakeEngine) List(_ context.Context) ([]ContainerInfo, error) {
	var out []ContainerInfo
	for _, info := range f.infos {
		out = append(out, info)
	}
	return out, nil
}

func (f *blockingReadyEngine) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type fakeWorldPort struct {
	started []contract.Session
	stopped bool
}

func (f *fakeWorldPort) Start(_ context.Context, session contract.Session) error {
	f.started = append(f.started, session)
	return nil
}

func (f *fakeWorldPort) Ready(context.Context, contract.Session) (world.ReadyReport, error) {
	return world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}}, nil
}

func (f *fakeWorldPort) Status(context.Context) (ContainerInfo, world.ReadyReport, error) {
	return ContainerInfo{Name: contract.WorldContainer, Running: true, Image: "env"}, world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}}, nil
}

func (f *fakeWorldPort) Stop(context.Context) error {
	f.stopped = true
	return nil
}

func (f *fakeWorldPort) Pose(context.Context, int) (EntityPose, error) {
	return EntityPose{}, fmt.Errorf("fake world has no pose")
}

func (f *fakeWorldPort) Reset(context.Context, int, contract.Pose) (EntityPose, error) {
	return EntityPose{}, fmt.Errorf("fake world has no reset")
}

type fakeVehiclePort struct {
	session        contract.Session
	robot          contract.Robot
	status         AppStatus
	started        bool
	stopped        bool
	startErr       error
	readyErr       error
	localizeErr    error
	localized      string
	localizedPose  contract.Pose
	stationsErr    error
	seededStations []contract.Station
	seededMapName  string
	stopOrder      *[]string
}

func (f *fakeVehiclePort) Start(context.Context) error {
	f.started = true
	return f.startErr
}

func (f *fakeVehiclePort) Status(context.Context) (AppStatus, error) {
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

func (f *fakeVehiclePort) Localize(_ context.Context, mapName string, pose contract.Pose) error {
	f.localized = mapName
	f.localizedPose = pose
	return f.localizeErr
}

func (f *fakeVehiclePort) Forward(context.Context, []byte) ([]byte, error) {
	return nil, fmt.Errorf("fake vehicle has no forward")
}

func (f *fakeVehiclePort) SeedMap(context.Context, []byte) error { return nil }

func (f *fakeVehiclePort) SeedStations(_ context.Context, mapName string, stations []contract.Station) error {
	if f.stationsErr != nil {
		return f.stationsErr
	}
	f.seededMapName = mapName
	f.seededStations = stations
	return nil
}

type fakeVehicleFactory struct {
	ports         []*fakeVehiclePort
	failRobot     string
	notReadyRobot string
	localizeFails string
	stationsFail  string
	stopOrder     []string
	discoverErr   error
}

func (f *fakeVehicleFactory) New(session contract.Session, robot contract.Robot) VehiclePort {
	port := &fakeVehiclePort{
		session: session,
		robot:   robot,
		status: AppStatus{
			Name:      contract.AppContainer(robot.Name),
			RobotName: robot.Name,
			Running:   true,
			APIPort:   easyGoRuntimeFor(robot.Index).apiPort,
			Image:     "runtime",
		},
		stopOrder: &f.stopOrder,
	}
	if robot.Name == f.failRobot {
		port.startErr = errors.New("vehicle start failed")
	}
	if robot.Name == f.notReadyRobot {
		port.readyErr = errors.New("app API never answered")
	}
	if robot.Name == f.localizeFails {
		port.localizeErr = errors.New("localization refused")
	}
	if robot.Name == f.stationsFail {
		port.stationsErr = errors.New("stations refused")
	}
	f.ports = append(f.ports, port)
	return port
}

func (f *fakeVehicleFactory) Discover(context.Context) ([]VehiclePort, error) {
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	ports := make([]VehiclePort, 0, len(f.ports))
	for _, port := range f.ports {
		ports = append(ports, port)
	}
	return ports, nil
}

func TestStartDelegatesThroughWorldAndVehiclePorts(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}
	status, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime", EnvImage: "env",
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
	if !status.Connected || len(status.Apps) != 2 {
		t.Fatalf("status=%+v", status)
	}
}

func TestStartWorldThenApp(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	cfg := contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: t.TempDir(),
		Poses:        []contract.Pose{{X: 7, Y: 8, Z: contract.DefaultSpawnZ, Yaw: 1.5}},
		CameraRobots: []int{1},
	}
	status, err := orch.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if status.World.Image != "simenv-world:local" || len(status.Apps) != 1 {
		t.Fatalf("status=%+v", status)
	}
	if status.Apps[0].APIPort != 18189 {
		t.Fatalf("api=%d", status.Apps[0].APIPort)
	}
	if len(engine.started) != 2 {
		t.Fatalf("started=%d", len(engine.started))
	}
	worldSpec := engine.started[0]
	if worldSpec.Name != contract.WorldContainer || worldSpec.Network != "host" {
		t.Fatalf("world spec=%+v", worldSpec)
	}
	if containsAny(worldSpec.Command, "easygo-app", "slam", "nav2") {
		t.Fatal("world command must not start the vehicle stack")
	}
	worldSession := sessionFromCommand(t, worldSpec.Command)
	if worldSession.World != "simple" || worldSession.Partition != contract.DefaultPartition {
		t.Fatalf("world session=%+v", worldSession)
	}
	if len(worldSession.Robots) != 1 || worldSession.Robots[0].Pose.X != 7 || worldSession.Robots[0].Name != "robot1" {
		t.Fatalf("world robots=%+v", worldSession.Robots)
	}
	if got, ok := commandValue(worldSpec.Command, "--cameras"); !ok || got != "1" {
		t.Fatalf("world camera selection=%q, present=%t", got, ok)
	}
	appSpec := engine.started[1]
	if appSpec.Name != "simenv-robot1" {
		t.Fatalf("app name=%s", appSpec.Name)
	}
	joined := strings.Join(appSpec.Command, " ")
	for _, arg := range []string{"start_gazebo:=false", "use_gz_gui:=false", "robot_name:=robot1", "api_port:=18189", "runtime_root:=/sros"} {
		if !strings.Contains(joined, arg) {
			t.Fatalf("app command missing %s: %v", arg, appSpec.Command)
		}
	}
	if !strings.Contains(joined, "bridge_rgbd:=true") {
		t.Fatalf("app command missing camera bridge: %v", appSpec.Command)
	}
	if appSpec.Labels[vehicleRobotLabel] != "robot1" || appSpec.Labels[vehicleIndexLabel] != "1" {
		t.Fatalf("app labels=%v", appSpec.Labels)
	}
	if containsAny(appSpec.Command, "start_gazebo:=true") {
		t.Fatalf("app command=%v", appSpec.Command)
	}
	if appSpec.Env["GZ_PARTITION"] != "easygo_sim" || appSpec.Env["ROS_DOMAIN_ID"] != "11" || appSpec.Env["BACKEND_PORT"] != "18191" {
		t.Fatalf("app env=%v", appSpec.Env)
	}
}

func TestWaitReadyRequiresAllSessionRobots(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{
		World: "map_world", Clock: true, Robots: []string{"robot1"},
	})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	session := contract.Session{
		World:     "simple",
		Partition: contract.DefaultPartition,
		Robots: []contract.Robot{
			{Index: 1, Name: "robot1", TopicPrefix: "robot1"},
			{Index: 2, Name: "robot2", TopicPrefix: "robot2"},
		},
	}
	_, err := waitReady(context.Background(), engine, func(context.Context, time.Duration) error {
		return errors.New("retry")
	}, session)
	if err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("error=%v", err)
	}
}

func TestWaitReadyEnforcesItsOwnTimeout(t *testing.T) {
	session := contract.Session{
		World:     "simple",
		Partition: contract.DefaultPartition,
		Robots:    []contract.Robot{{Index: 1, Name: "robot1", TopicPrefix: "robot1"}},
	}
	engine := &blockingReadyEngine{}

	_, err := waitReadyWithTimeout(context.Background(), engine, nil, session, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}

func TestStatusAggregatesIndependentVehiclePorts(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true, Image: "env"},
		"simenv-robot1": {
			Name: "simenv-robot1", Running: true, Image: "runtime-1",
			Labels: appLabels("robot1", 1),
		},
		"simenv-robot2": {
			Name: "simenv-robot2", Running: false, Image: "runtime-2",
			Labels: appLabels("robot2", 2),
		},
	}}
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}})
	engine.ready = string(report)
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}

	status, err := orch.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Apps) != 2 {
		t.Fatalf("apps=%+v", status.Apps)
	}
	if status.Apps[0].Name == status.Apps[1].Name {
		t.Fatalf("apps=%+v", status.Apps)
	}
	if status.Connected {
		t.Fatalf("stopped vehicle must disconnect the session: %+v", status)
	}
}

func TestDockerWorldStatusReportsNotReadyWithoutError(t *testing.T) {
	for _, output := range []string{
		"",
		"cat: /tmp/simenv/ready.json: No such file or directory",
	} {
		t.Run(output, func(t *testing.T) {
			engine := &fakeEngine{
				infos: map[string]ContainerInfo{
					contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
				},
				ready: output,
			}

			_, ready, err := (DockerWorld{Engine: engine}).Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if ready.Clock || len(ready.Robots) != 0 {
				t.Fatalf("ready=%+v", ready)
			}
		})
	}
}

func TestStatusRequiresTheReadyRobotSet(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true, Image: "env"},
		"simenv-robot1": {
			Name: "simenv-robot1", Running: true, Image: "runtime-1",
			Labels: appLabels("robot1", 1),
		},
		"simenv-robot3": {
			Name: "simenv-robot3", Running: true, Image: "runtime-3",
			Labels: appLabels("robot3", 3),
		},
	}}
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}})
	engine.ready = string(report)

	status, err := (Orchestrator{Engine: engine}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Connected {
		t.Fatalf("unexpected connected status: %+v", status)
	}
}

func TestDockerWorldStatusReturnsReadyErrorForRunningWorld(t *testing.T) {
	readyErr := errors.New("ready read failed")
	engine := &fakeEngine{
		infos: map[string]ContainerInfo{
			contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
		},
		execErr: readyErr,
	}

	_, _, err := (DockerWorld{Engine: engine}).Status(context.Background())
	if !errors.Is(err, readyErr) {
		t.Fatalf("error=%v, want %v", err, readyErr)
	}
}

func TestDockerWorldStatusIgnoresStaleReadyWhenWorldIsStopped(t *testing.T) {
	engine := &fakeEngine{
		infos: map[string]ContainerInfo{
			contract.WorldContainer: {Name: contract.WorldContainer, Running: false},
		},
		execErr: errors.New("ready file should not be read"),
	}

	_, ready, err := (DockerWorld{Engine: engine}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ready.Clock || len(ready.Robots) != 0 {
		t.Fatalf("ready=%+v", ready)
	}
}

func TestStartRollsBackWorldAndStartedVehicles(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{failRobot: "robot2"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}

	_, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime", EnvImage: "env",
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
		robot:  contract.Robot{Index: 2, Name: "robot2", TopicPrefix: "robot2"},
		status: AppStatus{Name: "simenv-robot2"},
	}
	vehicleFactory := &fakeVehicleFactory{ports: []*fakeVehiclePort{stale}}
	orch := Orchestrator{World: &fakeWorldPort{}, VehicleFactory: vehicleFactory}

	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err != nil {
		t.Fatal(err)
	}
	if !stale.stopped {
		t.Fatal("stale vehicle was not stopped")
	}
}

func TestStartRejectsEmptyAppImage(t *testing.T) {
	orch := Orchestrator{Engine: &fakeEngine{infos: map[string]ContainerInfo{}}, AppReady: alwaysReady}
	_, err := orch.Start(context.Background(), contract.StartConfig{Robots: 1, World: "simple", EnvImage: "env"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEasyGoLaunchUsesTopicPrefixForBridgeName(t *testing.T) {
	args := easyGoLaunchArgs(contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "fleet/robot1"}, easyGoRuntimeFor(1), false, false, false)
	if !containsAny(args, "robot_name:=fleet/robot1") {
		t.Fatalf("args=%v", args)
	}
	if !containsAny(args, "serial_number:=robot1") {
		t.Fatalf("args=%v", args)
	}
}

func TestEasyGoLaunchTogglesViewersIndependently(t *testing.T) {
	robot := contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "robot1"}
	headless := easyGoLaunchArgs(robot, easyGoRuntimeFor(1), false, false, false)
	if !containsAny(headless, "use_gz_gui:=false") || !containsAny(headless, "use_rviz:=false") {
		t.Fatalf("headless args=%v", headless)
	}
	rvizOnly := easyGoLaunchArgs(robot, easyGoRuntimeFor(1), true, false, false)
	if !containsAny(rvizOnly, "use_rviz:=true") || !containsAny(rvizOnly, "use_gz_gui:=false") {
		t.Fatalf("rviz-only args=%v", rvizOnly)
	}
	gzGuiOnly := easyGoLaunchArgs(robot, easyGoRuntimeFor(1), false, true, false)
	if !containsAny(gzGuiOnly, "use_gz_gui:=true") || !containsAny(gzGuiOnly, "use_rviz:=false") {
		t.Fatalf("gz-gui-only args=%v", gzGuiOnly)
	}
	both := easyGoLaunchArgs(robot, easyGoRuntimeFor(1), true, true, false)
	if !containsAny(both, "use_gz_gui:=true") || !containsAny(both, "use_rviz:=true") {
		t.Fatalf("both args=%v", both)
	}
	cameras := easyGoLaunchArgs(robot, easyGoRuntimeFor(1), false, false, true)
	if !containsAny(cameras, "bridge_rgbd:=true") {
		t.Fatalf("camera args=%v", cameras)
	}
}

func TestStopStopsWorldAndApps(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
		"simenv-robot1": {
			Name: "simenv-robot1", Running: true,
			Labels: appLabels("robot1", 1),
		},
	}}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	if err := orch.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 2 {
		t.Fatalf("stopped=%v", engine.stopped)
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

func TestEasyGoVehicleOnlyManagesItsRobot(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		"simenv-robot1": {Name: "simenv-robot1", Running: true, Image: "runtime-1"},
		"simenv-robot2": {Name: "simenv-robot2", Running: true, Image: "runtime-2"},
	}}
	vehicle := EasyGoVehicle{
		Engine: engine,
		Robot:  contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "robot1"},
	}

	status, err := vehicle.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Name != "simenv-robot1" || status.Image != "runtime-1" {
		t.Fatalf("status=%+v", status)
	}

	if err := vehicle.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 1 || engine.stopped[0] != "simenv-robot1" {
		t.Fatalf("stopped=%v", engine.stopped)
	}
}

func TestEasyGoVehicleFactoryDiscoversOnlyRobotContainers(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
		"legacy-app-name": {
			Name:    "legacy-app-name",
			Running: true,
			Labels:  appLabels("robot1", 1),
		},
		"simenv-robot02": {
			Name:    "simenv-robot02",
			Running: true,
			Labels: map[string]string{
				"simenv.managed":     "true",
				"simenv.role":        "app",
				"simenv.robot":       "robot2",
				"simenv.robot.index": "2",
			},
		},
		"simenv-robot2-debug": {
			Name:    "simenv-robot2-debug",
			Running: true,
			Labels: map[string]string{
				"simenv.managed": "true",
				"simenv.role":    "app",
			},
		},
	}}
	factory := EasyGoVehicleFactory{Engine: engine}

	ports, err := factory.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 {
		t.Fatalf("ports=%d", len(ports))
	}
	names := map[string]bool{}
	for _, port := range ports {
		vehicle, ok := port.(EasyGoVehicle)
		if !ok {
			t.Fatalf("port=%T", port)
		}
		names[vehicle.Robot.Name] = true
	}
	if !names["robot1"] || !names["robot2"] {
		t.Fatalf("robots=%v", names)
	}
}

func TestDiscoveredVehicleUsesActualContainerName(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		"legacy-app-name": {
			Name: "legacy-app-name", Running: true, Image: "runtime",
			Labels: appLabels("robot1", 1),
		},
	}}
	factory := EasyGoVehicleFactory{Engine: engine}

	ports, err := factory.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 {
		t.Fatalf("ports=%d", len(ports))
	}
	status, err := ports[0].Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Name != "legacy-app-name" {
		t.Fatalf("status=%+v", status)
	}
	if err := ports[0].Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 1 || engine.stopped[0] != "legacy-app-name" {
		t.Fatalf("stopped=%v", engine.stopped)
	}
}

func appLabels(name string, index int) map[string]string {
	return map[string]string{
		contract.LabelManaged: "true",
		contract.LabelRole:    contract.RoleApp,
		vehicleRobotLabel:     name,
		vehicleIndexLabel:     strconv.Itoa(index),
	}
}

func containsAny(items []string, needles ...string) bool {
	blob := strings.Join(items, " ")
	for _, n := range needles {
		if strings.Contains(blob, n) {
			return true
		}
	}
	return false
}

func commandValue(command []string, flag string) (string, bool) {
	for i, item := range command {
		if item == flag && i+1 < len(command) {
			return command[i+1], true
		}
	}
	return "", false
}

func TestViewersArePerRobot(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2", "robot3"}})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	cfg := contract.StartConfig{
		Robots: 3, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: t.TempDir(),
		RvizRobots:  []int{1},
		GzGuiRobots: []int{2},
	}
	if _, err := orch.Start(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// started[0] is the world; the rest follow session order.
	if len(engine.started) != 4 {
		t.Fatalf("started=%d", len(engine.started))
	}
	cases := []struct {
		name        string
		rviz, gzGui bool
	}{
		{"simenv-robot1", true, false},
		{"simenv-robot2", false, true},
		{"simenv-robot3", false, false},
	}
	for i, want := range cases {
		spec := engine.started[i+1]
		if spec.Name != want.name {
			t.Fatalf("spec[%d] name=%s", i+1, spec.Name)
		}
		joined := strings.Join(spec.Command, " ")
		if got := strings.Contains(joined, "use_rviz:=true"); got != want.rviz {
			t.Fatalf("%s use_rviz: want %t in %v", want.name, want.rviz, spec.Command)
		}
		if got := strings.Contains(joined, "use_gz_gui:=true"); got != want.gzGui {
			t.Fatalf("%s use_gz_gui: want %t in %v", want.name, want.gzGui, spec.Command)
		}
		// Only the robots with a viewer ask for the host GPU. Compare against
		// hostDevices() so the expectation follows the host: a machine without
		// /dev/dri gets none for anyone.
		wantDevices := 0
		if want.rviz || want.gzGui {
			wantDevices = len(hostDevices())
		}
		if len(spec.Devices) != wantDevices {
			t.Fatalf("%s devices=%v (want %d)", want.name, spec.Devices, wantDevices)
		}
	}
}

func TestStartFailsAndRollsBackWhenVehicleAppNeverServes(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{notReadyRobot: "robot1"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
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

// alwaysReady stands in for a vehicle whose app is already serving.
func alwaysReady(context.Context, int) error { return nil }

func sessionFromCommand(t *testing.T, command []string) contract.Session {
	t.Helper()
	for i, item := range command {
		if item == "--session" && i+1 < len(command) {
			var session contract.Session
			if err := json.Unmarshal([]byte(command[i+1]), &session); err != nil {
				t.Fatal(err)
			}
			return session
		}
	}
	t.Fatalf("missing --session in %v", command)
	return contract.Session{}
}

func TestStartInitializesEachVehicleRegistry(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}})
	dataRoot := t.TempDir()
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: dataRoot,
	}); err != nil {
		t.Fatal(err)
	}

	apps := map[string]ContainerSpec{}
	for _, spec := range engine.started {
		if spec.Labels[contract.LabelRole] == contract.RoleApp {
			apps[spec.Labels[vehicleRobotLabel]] = spec
		}
	}
	for _, want := range []struct {
		name       string
		membership int
	}{
		{name: "robot1", membership: syncMembershipPortBase},
		{name: "robot2", membership: syncMembershipPortBase + syncPortStride},
	} {
		spec, ok := apps[want.name]
		if !ok {
			t.Fatalf("no app container for %s", want.name)
		}
		// The container command stays the plain launch; initialization happened
		// before it, on the host, and reaches the container as mounts.
		if len(spec.Command) < 3 || spec.Command[0] != "ros2" || spec.Command[1] != "launch" {
			t.Fatalf("%s command=%v", want.name, spec.Command)
		}
		hostDir := filepath.Join(dataRoot, want.name, appConfigHostDir)
		wantBinds := []string{
			filepath.Join(hostDir, "function.basic_setting.yaml") + ":" + appRegistryDir + "/" + vehicleSerialRegistry,
			filepath.Join(hostDir, "function.sync.yaml") + ":" + appRegistryDir + "/" + syncRegistry,
		}
		for _, wantBind := range wantBinds {
			if !slices.Contains(spec.Binds, wantBind) {
				t.Fatalf("%s binds=%v missing %s", want.name, spec.Binds, wantBind)
			}
		}
		if len(spec.Binds) != 1+len(wantBinds) {
			t.Fatalf("%s binds=%v", want.name, spec.Binds)
		}
		registry, err := os.ReadFile(filepath.Join(hostDir, "function.basic_setting.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(registry), "default_value: \""+want.name+"\"") {
			t.Fatalf("%s serial not pinned:\n%s", want.name, registry)
		}
		if !strings.Contains(string(registry), `default_value: "EasyGo"`) {
			t.Fatalf("%s unrelated entries disturbed:\n%s", want.name, registry)
		}
		sync, err := os.ReadFile(filepath.Join(hostDir, "function.sync.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(sync), "default_value: "+strconv.Itoa(want.membership)) {
			t.Fatalf("%s membership port not pinned to %d:\n%s", want.name, want.membership, sync)
		}
		if want.membership != syncMembershipPortBase && strings.Contains(string(sync), "default_value: "+strconv.Itoa(syncMembershipPortBase)) {
			t.Fatalf("%s stale membership port left:\n%s", want.name, sync)
		}
	}
	robot2 := apps["robot2"]
	if got := robot2.Env["MDNS_PORT"]; got != strconv.Itoa(mdnsPortBase+syncPortStride) {
		t.Fatalf("robot2 MDNS_PORT=%q", got)
	}
	if _, ok := robot2.Env["PORT"]; ok {
		t.Fatal("PORT must be left to the launch file")
	}
}

func TestStartEnablesMultiAMRPeerLink(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	var app ContainerSpec
	for _, spec := range engine.started {
		if spec.Labels[contract.LabelRole] == contract.RoleApp {
			app = spec
		}
	}
	if app.Env["EASYGO_BLUETOOTH_TRANSPORT"] != "udp" {
		t.Fatalf("peer link transport=%q", app.Env["EASYGO_BLUETOOTH_TRANSPORT"])
	}
	if app.Env["ENABLE_MULTI_AMR_AVOIDANCE"] != "true" {
		t.Fatalf("multi-AMR avoidance=%q", app.Env["ENABLE_MULTI_AMR_AVOIDANCE"])
	}
	if app.Env["EASYGO_BLUETOOTH_UDP_PORT"] != strconv.Itoa(multiAMRPeerPort) {
		t.Fatalf("peer link port=%q", app.Env["EASYGO_BLUETOOTH_UDP_PORT"])
	}
}

func mapExportArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWorldMapNameResolvesPrimaryLayer(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{
		"sros/map/arena_world0.pgm":  "pgm",
		"sros/map/arena_world1.pgm":  "pgm",
		"sros/map/zone_2_0.pgm":      "sublayer",
		"sros/map/arena_world.json":  "{}",
		"sros/map/arena_world_1.png": "png",
	})
	name, err := worldMapName(archive)
	if err != nil {
		t.Fatal(err)
	}
	if name != "arena_world" {
		t.Fatalf("name=%q", name)
	}
}

func TestWorldMapNameRejectsArchiveWithoutPrimaryLayer(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/thing.json": "{}"})
	if _, err := worldMapName(archive); err == nil {
		t.Fatal("expected an error for an archive without a primary layer")
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

func TestStartPlacesEveryVehicleOnTheWorldMap(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithWorldMap(t, archive)}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 2, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err != nil {
		t.Fatal(err)
	}
	if len(vehicleFactory.ports) != 2 {
		t.Fatalf("vehicles=%d", len(vehicleFactory.ports))
	}
	for _, port := range vehicleFactory.ports {
		if port.localized != "arena_world" {
			t.Fatalf("%s localized on %q", port.robot.Name, port.localized)
		}
	}
}

func TestStartRollsBackWhenLocalizationFails(t *testing.T) {
	archive := mapExportArchive(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"})
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{localizeFails: "robot1"}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: assetsWithWorldMap(t, archive)}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err == nil {
		t.Fatal("expected start to fail when the vehicle cannot be placed on the map")
	}
	if !worldPort.stopped || !vehicleFactory.ports[0].stopped {
		t.Fatalf("start did not roll back: %+v", vehicleFactory.ports)
	}
}

func TestStartSkipsLocalizationWhenTheWorldShipsNoMap(t *testing.T) {
	worldPort := &fakeWorldPort{}
	vehicleFactory := &fakeVehicleFactory{}
	orch := Orchestrator{World: worldPort, VehicleFactory: vehicleFactory, assetsDir: t.TempDir()}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime", EnvImage: "env",
	}); err != nil {
		t.Fatal(err)
	}
	if vehicleFactory.ports[0].localized != "" {
		t.Fatal("vehicle localized without a world map")
	}
}

// assetsWithWorldMap lays down the world map export for "simple", together with
// the bundle manifest a map is required to come with.
func assetsWithWorldMap(t *testing.T, archive []byte) string {
	t.Helper()
	dir := t.TempDir()
	writeWorldAsset(t, dir, "simple.map_export", archive)
	writeWorldAsset(t, dir, "simple.world.json", []byte(arenaBundleJSON("arena_world")))
	return dir
}

// arenaBundleJSON is a bundle manifest whose map name and stations the tests
// assert on. initial_poses 留空 -> 定位用 session 的 spawn 位姿。
func arenaBundleJSON(mapName string) string {
	return `{
  "world": "simple",
  "map_name": "` + mapName + `",
  "origin_offset_mm": {"x": 1978.0, "y": -3181.5},
  "stations": [
    {"id": 4, "x": 6060, "y": 13220, "yaw": 0},
    {"id": 7, "x": 12500, "y": 13000, "yaw": 0}
  ],
  "scenarios": ["s9_wide_lane_no_unlock.sh"]
}`
}

func writeWorldAsset(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	worlds := filepath.Join(dir, "worlds")
	if err := os.MkdirAll(worlds, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worlds, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStartWithDataVolumePrefixBindsOnlyTheVolume(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine, AppReady: alwaysReady}
	if _, err := orch.Start(context.Background(), contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataVolumePrefix: "ci-7",
	}); err != nil {
		t.Fatal(err)
	}
	var app *ContainerSpec
	for i, spec := range engine.started {
		if spec.Labels[contract.LabelRole] == contract.RoleApp {
			app = &engine.started[i]
		}
	}
	if app == nil {
		t.Fatal("no app container started")
	}
	want := []string{"ci-7-robot1:/sros"}
	if !slices.Equal(app.Binds, want) {
		t.Fatalf("binds=%v, want exactly %v", app.Binds, want)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), appConfigHostDir)); err == nil {
		t.Fatal("volume mode must not stage app-config on the host filesystem")
	}
}
