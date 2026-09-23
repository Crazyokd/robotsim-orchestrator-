package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

type fakeEngine struct {
	started []ContainerSpec
	stopped []string
	infos   map[string]ContainerInfo
	ready   string
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	return "sha256:" + ref, nil
}

func (f *fakeEngine) Start(_ context.Context, spec ContainerSpec) error {
	f.started = append(f.started, spec)
	f.infos[spec.Name] = ContainerInfo{Name: spec.Name, Running: true, Image: spec.Image, ID: spec.Name + "-id"}
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

type fakeWorldPort struct {
	started []contract.Session
	stopped bool
}

func (f *fakeWorldPort) Start(_ context.Context, session contract.Session) error {
	f.started = append(f.started, session)
	return nil
}

func (f *fakeWorldPort) Ready(context.Context) (world.ReadyReport, error) {
	return world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}}, nil
}

func (f *fakeWorldPort) Status(context.Context) (ContainerInfo, world.ReadyReport, error) {
	return ContainerInfo{Name: contract.WorldContainer, Running: true, Image: "env"}, world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}}, nil
}

func (f *fakeWorldPort) Stop(context.Context) error {
	f.stopped = true
	return nil
}

type fakeVehiclePort struct {
	session contract.Session
	robot   contract.Robot
	status  AppStatus
	started bool
	stopped bool
}

func (f *fakeVehiclePort) Start(context.Context) error {
	f.started = true
	return nil
}

func (f *fakeVehiclePort) Status(context.Context) (AppStatus, error) {
	return f.status, nil
}

func (f *fakeVehiclePort) Stop(context.Context) error {
	f.stopped = true
	return nil
}

type fakeVehicleFactory struct {
	ports []*fakeVehiclePort
}

func (f *fakeVehicleFactory) New(session contract.Session, robot contract.Robot) VehiclePort {
	port := &fakeVehiclePort{
		session: session,
		robot:   robot,
		status: AppStatus{
			Name:    contract.AppContainer(robot.Name),
			Running: true,
			APIPort: easyGoRuntimeFor(robot.Index).apiPort,
			Image:   "runtime",
		},
	}
	f.ports = append(f.ports, port)
	return port
}

func (f *fakeVehicleFactory) Discover(context.Context) ([]VehiclePort, error) {
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
	orch := Orchestrator{Engine: engine}
	cfg := contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: t.TempDir(),
		Poses: []contract.Pose{{X: 7, Y: 8, Z: contract.DefaultSpawnZ, Yaw: 1.5}},
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
	if containsAny(appSpec.Command, "start_gazebo:=true") {
		t.Fatalf("app command=%v", appSpec.Command)
	}
	if appSpec.Env["GZ_PARTITION"] != "easygo_sim" || appSpec.Env["ROS_DOMAIN_ID"] != "11" || appSpec.Env["BACKEND_PORT"] != "18191" {
		t.Fatalf("app env=%v", appSpec.Env)
	}
}

func TestStatusAggregatesIndependentVehiclePorts(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true, Image: "env"},
		"simenv-robot1":         {Name: "simenv-robot1", Running: true, Image: "runtime-1"},
		"simenv-robot2":         {Name: "simenv-robot2", Running: false, Image: "runtime-2"},
	}}
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1", "robot2"}})
	engine.ready = string(report)
	orch := Orchestrator{Engine: engine}

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
}

func TestStartRejectsEmptyAppImage(t *testing.T) {
	orch := Orchestrator{Engine: &fakeEngine{infos: map[string]ContainerInfo{}}}
	_, err := orch.Start(context.Background(), contract.StartConfig{Robots: 1, World: "simple", EnvImage: "env"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEasyGoLaunchUsesTopicPrefixForBridgeName(t *testing.T) {
	args := easyGoLaunchArgs(contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "fleet/robot1"}, easyGoRuntimeFor(1), false)
	if !containsAny(args, "robot_name:=fleet/robot1") {
		t.Fatalf("args=%v", args)
	}
	if !containsAny(args, "serial_number:=robot1") {
		t.Fatalf("args=%v", args)
	}
}

func TestEasyGoLaunchGuiFlagTogglesViewers(t *testing.T) {
	headless := easyGoLaunchArgs(contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "robot1"}, easyGoRuntimeFor(1), false)
	if !containsAny(headless, "use_gz_gui:=false") || !containsAny(headless, "use_rviz:=false") {
		t.Fatalf("headless args=%v", headless)
	}
	gui := easyGoLaunchArgs(contract.Robot{Index: 1, Name: "robot1", TopicPrefix: "robot1"}, easyGoRuntimeFor(1), true)
	if !containsAny(gui, "use_gz_gui:=true") || !containsAny(gui, "use_rviz:=true") {
		t.Fatalf("gui args=%v", gui)
	}
}

func TestStopStopsWorldAndApps(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
		"simenv-robot1":         {Name: "simenv-robot1", Running: true},
	}}
	orch := Orchestrator{Engine: engine}
	if err := orch.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 2 {
		t.Fatalf("stopped=%v", engine.stopped)
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
		"simenv-robot1":         {Name: "simenv-robot1", Running: true},
		"simenv-robot02":        {Name: "simenv-robot02", Running: true},
		"simenv-robot2-debug":   {Name: "simenv-robot2-debug", Running: true},
	}}
	factory := EasyGoVehicleFactory{Engine: engine}

	ports, err := factory.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 {
		t.Fatalf("ports=%d", len(ports))
	}
	vehicle, ok := ports[0].(EasyGoVehicle)
	if !ok {
		t.Fatalf("port=%T", ports[0])
	}
	if vehicle.Robot.Name != "robot1" {
		t.Fatalf("robot=%+v", vehicle.Robot)
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
