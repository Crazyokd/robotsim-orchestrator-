package easygo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
)

type fakeEngine struct {
	imageErr   error
	imageFiles map[string]string
	readPaths  []string
	started    []docker.ContainerSpec
	stopped    []string
	infos      map[string]docker.ContainerInfo
}

// fakeSyncRegistry mirrors the registry file the runtime image ships: its default
// membership port collides across vehicles on a shared host network.
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
	f.readPaths = append(f.readPaths, path)
	if f.imageFiles != nil {
		if content, ok := f.imageFiles[path]; ok {
			return []byte(content), nil
		}
	}
	switch filepath.Base(path) {
	case filepath.Base(syncRegistry):
		return []byte(fakeSyncRegistry), nil
	}
	return nil, fmt.Errorf("fake image has no %s", path)
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	if f.imageErr != nil {
		return "", f.imageErr
	}
	return "sha256:" + ref, nil
}

func (f *fakeEngine) Start(_ context.Context, spec docker.ContainerSpec) error {
	f.started = append(f.started, spec)
	f.infos[spec.Name] = docker.ContainerInfo{
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

func (f *fakeEngine) Inspect(_ context.Context, name string) (docker.ContainerInfo, error) {
	info, ok := f.infos[name]
	if !ok {
		return docker.ContainerInfo{}, docker.ErrNotFound
	}
	return info, nil
}

func (f *fakeEngine) Exec(context.Context, string, []string) (string, error) {
	return "", nil
}

func (f *fakeEngine) List(_ context.Context) ([]docker.ContainerInfo, error) {
	var out []docker.ContainerInfo
	for _, info := range f.infos {
		out = append(out, info)
	}
	return out, nil
}

func oneRobotSession(t *testing.T) contract.Session {
	t.Helper()
	session := contract.Session{
		World:  "simple",
		Robots: []contract.Robot{{Index: 1, Name: "robot1", Pose: contract.Pose{X: 7, Y: 8, Z: contract.DefaultSpawnZ, Yaw: 1.5}}},
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}

func testLink(robot contract.Robot) gazebo.Attachment {
	return gazebo.Attachment{IP: gazebo.GZIP, Partition: gazebo.Partition, TopicPrefix: robot.Name}
}

func TestNamedSessionShiftsContainerAndPorts(t *testing.T) {
	vehicle := Vehicle{Robot: contract.Robot{Index: 1, Name: "robot1"}, SessionName: "alpha", Slot: 1}
	if vehicle.containerName() != "simenv-vehicle-alpha-robot1" {
		t.Fatalf("container=%s", vehicle.containerName())
	}
	if vehicle.runtime().apiPort == runtimeFor(1, 0).apiPort || vehicle.runtime().rosDomainID == runtimeFor(1, 0).rosDomainID {
		t.Fatalf("slot 1 reused the default ports: %+v", vehicle.runtime())
	}
	if (Vehicle{Robot: contract.Robot{Index: 1, Name: "robot1"}}).containerName() != "simenv-vehicle-robot1" {
		t.Fatal("the default session keeps simenv-vehicle-robotN")
	}
}

func TestNamedSessionsKeepSeparateDataAcrossSlotReuse(t *testing.T) {
	for _, mode := range []string{"explicit root", "cache root"} {
		t.Run(mode, func(t *testing.T) {
			dataRoot := t.TempDir()
			options := Options{AppImage: "runtime-sim:tag", DataRoot: dataRoot}
			if mode == "cache root" {
				t.Setenv("XDG_CACHE_HOME", dataRoot)
				dataRoot = filepath.Join(dataRoot, "simenv")
				options.DataRoot = ""
			}
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
			session := oneRobotSession(t)
			for i, tc := range []struct {
				name string
				slot int
				port string
			}{
				{name: "alpha", slot: 0, port: "25354"},
				{name: "beta", slot: 1, port: "35354"},
				{name: "alpha", slot: 1, port: "35354"},
			} {
				factory := Factory{Engine: engine, Options: options, SessionName: tc.name, Slot: tc.slot}
				vehicle, err := factory.New(context.Background(), session, session.Robots[0], testLink(session.Robots[0]), nil)
				if err != nil {
					t.Fatal(err)
				}
				if err := vehicle.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				spec := engine.started[len(engine.started)-1]
				root := filepath.Join(dataRoot, tc.name, "robot1")
				if got, want := spec.Binds[0], root+":/sros"; got != want {
					t.Fatalf("%s data bind=%q, want %q", tc.name, got, want)
				}
				registryPath := filepath.Join(root, "app-config", "function.sync.yaml")
				if !slices.Contains(spec.Binds, registryPath+":"+appRegistryDir+"/"+syncRegistry) {
					t.Fatalf("%s registry bind missing: %v", tc.name, spec.Binds)
				}
				registry, err := os.ReadFile(registryPath)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(registry), "default_value: "+tc.port) {
					t.Fatalf("%s registry has wrong port: %s", tc.name, registry)
				}
				marker := filepath.Join(root, "database-marker")
				if i == 2 {
					previous, err := os.ReadFile(marker)
					if err != nil || string(previous) != tc.name {
						t.Fatalf("%s lost data after slot change: marker=%q, error=%v", tc.name, previous, err)
					}
				}
				if previous, err := os.ReadFile(marker); err == nil {
					if string(previous) != tc.name {
						t.Fatalf("%s reused another session's data: %s", tc.name, previous)
					}
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte(tc.name), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"alpha", "beta"} {
				marker, err := os.ReadFile(filepath.Join(dataRoot, name, "robot1", "database-marker"))
				if err != nil || string(marker) != name {
					t.Fatalf("%s data lost: marker=%q, error=%v", name, marker, err)
				}
			}
		})
	}
}

func TestNamedVolumeFollowsSessionNameAcrossSlotReuse(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	session := oneRobotSession(t)
	for _, name := range []string{"alpha", "beta", "alpha"} {
		factory := Factory{
			Engine: engine, SessionName: name,
			Options: Options{AppImage: "runtime-sim:tag", DataVolumePrefix: "ci-1"},
		}
		vehicle, err := factory.New(context.Background(), session, session.Robots[0], testLink(session.Robots[0]), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := vehicle.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		spec := engine.started[len(engine.started)-1]
		if want := []string{"ci-1-" + name + "-robot1:/sros"}; !slices.Equal(spec.Binds, want) {
			t.Fatalf("%s binds=%v, want %v", name, spec.Binds, want)
		}
		if err := vehicle.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSlotShiftsHostResourcesTheVehicleOwns(t *testing.T) {
	base := runtimeFor(10, 0)
	shifted := runtimeFor(1, 1)
	if shifted.bluetoothUDPPort == base.bluetoothUDPPort || shifted.webgzDisplay == base.webgzDisplay || shifted.rvizDisplay == base.rvizDisplay || shifted.rosDomainID == base.rosDomainID || runtimeFor(1, 0).slamWebPort == runtimeFor(9, 0).slamWebPort {
		t.Fatalf("slot 1 reused slot 0 resources: %+v", shifted)
	}
}

func TestStartRejectsSlotResourceOverflow(t *testing.T) {
	cases := []struct {
		name   string
		robots []contract.Robot
		slot   int
	}{
		{name: "robot index", robots: []contract.Robot{{Index: 11, Name: "robot11"}}},
		{name: "session slot", robots: []contract.Robot{{Index: 1, Name: "robot1"}}, slot: 3},
		{name: "negative slot", robots: []contract.Robot{{Index: 1, Name: "robot1"}}, slot: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
			session := contract.Session{World: "simple", Robots: tc.robots}
			factory := Factory{Engine: engine, Options: Options{AppImage: "runtime-sim:tag", DataRoot: t.TempDir()}, Slot: tc.slot}
			vehicle, err := factory.New(context.Background(), session, tc.robots[0], testLink(tc.robots[0]), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := vehicle.Start(context.Background()); err == nil {
				t.Fatal("resource overflow must fail")
			}
			if len(engine.started) != 0 {
				t.Fatalf("resource guard started containers: %v", engine.started)
			}
		})
	}
}

func TestFactoryNewReadsTheGazeboAttachment(t *testing.T) {
	factory := Factory{Engine: &fakeEngine{infos: map[string]docker.ContainerInfo{}}}
	port, err := factory.New(context.Background(), oneRobotSession(t), contract.Robot{Index: 1, Name: "robot1"}, testLink(contract.Robot{Index: 1, Name: "robot1"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	vehicle, ok := port.(Vehicle)
	if !ok {
		t.Fatalf("port=%T", port)
	}
	if vehicle.Link.TopicPrefix != "robot1" || vehicle.Link.Partition != gazebo.Partition || vehicle.Link.IP != gazebo.GZIP {
		t.Fatalf("link=%+v", vehicle.Link)
	}
}

type unknownAttachment struct{}

func (unknownAttachment) Kind() string { return "unknown" }

func TestFactoryNewRejectsAnAttachmentItCannotRead(t *testing.T) {
	factory := Factory{}
	_, err := factory.New(context.Background(), contract.Session{}, contract.Robot{Index: 1, Name: "robot1"}, unknownAttachment{}, nil)
	if err == nil || !strings.Contains(err.Error(), `cannot attach to world kind "unknown"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestVehicleStartConfiguresTheContainer(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	session := oneRobotSession(t)
	factory := Factory{Engine: engine, Options: Options{AppImage: "runtime-sim:tag", DataRoot: t.TempDir()}}
	// 相机配对来自世界的挂接信息（gazebo.Attachment.Cameras），不是车端自己的配置。
	link := testLink(session.Robots[0])
	link.Cameras = true
	vehicle, err := factory.New(context.Background(), session, session.Robots[0], link, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := vehicle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(engine.started) != 1 {
		t.Fatalf("started=%d", len(engine.started))
	}
	appSpec := engine.started[0]
	if appSpec.Name != "simenv-vehicle-robot1" || appSpec.Image != "runtime-sim:tag" {
		t.Fatalf("app spec=%+v", appSpec)
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
	if containsAny(appSpec.Command, "start_gazebo:=true") {
		t.Fatalf("app command=%v", appSpec.Command)
	}
	if appSpec.Labels[vehicleRobotLabel] != "robot1" || appSpec.Labels[vehicleIndexLabel] != "1" {
		t.Fatalf("app labels=%v", appSpec.Labels)
	}
	if appSpec.Env["GZ_IP"] != gazebo.GZIP || appSpec.Env["GZ_PARTITION"] != gazebo.Partition || appSpec.Env["ROS_DOMAIN_ID"] != "11" || appSpec.Env["BACKEND_PORT"] != "18191" {
		t.Fatalf("app env=%v", appSpec.Env)
	}
	if appSpec.Env["PARAM_CFG_DIR"] != appRegistryDir {
		t.Fatalf("parameter config dir=%q, want %q", appSpec.Env["PARAM_CFG_DIR"], appRegistryDir)
	}
}

func TestLaunchUsesTopicPrefixForBridgeName(t *testing.T) {
	args := launchArgs(contract.Robot{Index: 1, Name: "robot1"}, "fleet/robot1", runtimeFor(1, 0), false, false, false)
	if !containsAny(args, "robot_name:=fleet/robot1") {
		t.Fatalf("args=%v", args)
	}
	if !containsAny(args, "serial_number:=robot1") {
		t.Fatalf("args=%v", args)
	}
}

func TestLaunchTogglesViewersIndependently(t *testing.T) {
	robot := contract.Robot{Index: 1, Name: "robot1"}
	headless := launchArgs(robot, "robot1", runtimeFor(1, 0), false, false, false)
	if !containsAny(headless, "use_gz_gui:=false") || !containsAny(headless, "use_rviz:=false") {
		t.Fatalf("headless args=%v", headless)
	}
	rvizOnly := launchArgs(robot, "robot1", runtimeFor(1, 0), true, false, false)
	if !containsAny(rvizOnly, "use_rviz:=true") || !containsAny(rvizOnly, "use_gz_gui:=false") {
		t.Fatalf("rviz-only args=%v", rvizOnly)
	}
	gzGuiOnly := launchArgs(robot, "robot1", runtimeFor(1, 0), false, true, false)
	if !containsAny(gzGuiOnly, "use_gz_gui:=true") || !containsAny(gzGuiOnly, "use_rviz:=false") {
		t.Fatalf("gz-gui-only args=%v", gzGuiOnly)
	}
	both := launchArgs(robot, "robot1", runtimeFor(1, 0), true, true, false)
	if !containsAny(both, "use_gz_gui:=true") || !containsAny(both, "use_rviz:=true") {
		t.Fatalf("both args=%v", both)
	}
	cameras := launchArgs(robot, "robot1", runtimeFor(1, 0), false, false, true)
	if !containsAny(cameras, "bridge_rgbd:=true") {
		t.Fatalf("camera args=%v", cameras)
	}
}

func TestViewersArePerRobot(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	factory := Factory{
		Engine:    engine,
		Options:   Options{AppImage: "runtime-sim:tag", DataRoot: t.TempDir(), RvizRobots: []int{1}, GzGuiRobots: []int{2}},
		AssetsDir: assetsWithModels(t),
	}
	session := contract.Session{
		World: "simple",
		Robots: []contract.Robot{
			{Index: 1, Name: "robot1"},
			{Index: 2, Name: "robot2"},
			{Index: 3, Name: "robot3"},
		},
	}
	for _, robot := range session.Robots {
		vehicle, err := factory.New(context.Background(), session, robot, testLink(robot), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := vehicle.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(engine.started) != 3 {
		t.Fatalf("started=%d", len(engine.started))
	}
	cases := []struct {
		name        string
		rviz, gzGui bool
	}{
		{"simenv-vehicle-robot1", true, false},
		{"simenv-vehicle-robot2", false, true},
		{"simenv-vehicle-robot3", false, false},
	}
	for i, want := range cases {
		spec := engine.started[i]
		if spec.Name != want.name {
			t.Fatalf("spec[%d] name=%s", i, spec.Name)
		}
		joined := strings.Join(spec.Command, " ")
		if got := strings.Contains(joined, "use_rviz:=true"); got != want.rviz {
			t.Fatalf("%s use_rviz: want %t in %v", want.name, want.rviz, spec.Command)
		}
		if got := strings.Contains(joined, "use_gz_gui:=true"); got != want.gzGui {
			t.Fatalf("%s use_gz_gui: want %t in %v", want.name, want.gzGui, spec.Command)
		}
		// Only the robots with a viewer ask for the host GPU. Compare against
		// HostGPUDevices() so the expectation follows the host: a machine without
		// /dev/dri gets none for anyone.
		wantDevices := 0
		if want.rviz || want.gzGui {
			wantDevices = len(docker.HostGPUDevices())
		}
		if len(spec.Devices) != wantDevices {
			t.Fatalf("%s devices=%v (want %d)", want.name, spec.Devices, wantDevices)
		}
	}
}

func TestStartInitializesEachVehicleSyncRegistry(t *testing.T) {
	dataRoot := t.TempDir()
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	factory := Factory{Engine: engine, Options: Options{AppImage: "runtime-sim:tag", DataRoot: dataRoot}}
	session := contract.Session{
		World:  "simple",
		Robots: []contract.Robot{{Index: 1, Name: "robot1"}, {Index: 2, Name: "robot2"}},
	}
	for _, robot := range session.Robots {
		vehicle, err := factory.New(context.Background(), session, robot, testLink(robot), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := vehicle.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wantReadPaths := []string{
		"/opt/sros/share/easygo-param-manager/cfg/" + syncRegistry,
		"/opt/sros/share/easygo-param-manager/cfg/" + syncRegistry,
	}
	if !slices.Equal(engine.readPaths, wantReadPaths) {
		t.Fatalf("registry reads=%v, want %v", engine.readPaths, wantReadPaths)
	}

	apps := map[string]docker.ContainerSpec{}
	for _, spec := range engine.started {
		apps[spec.Labels[vehicleRobotLabel]] = spec
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
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	factory := Factory{Engine: engine, Options: Options{AppImage: "runtime-sim:tag", DataRoot: t.TempDir()}}
	session := oneRobotSession(t)
	vehicle, err := factory.New(context.Background(), session, session.Robots[0], testLink(session.Robots[0]), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := vehicle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	app := engine.started[0]
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

// 过渡态：世界由环境提供，而车端 launch 按自己镜像的 worlds 目录校验世界名
// （sim_nav2.launch.py 的 _world_path）。把环境这份 SDF 只读挂进车端容器，
// 世界就只有一份文件。easygo 侧接受显式世界路径后（合同 §5 第 1 条）一起删。
func TestStartHandsTheWorldFileToTheAppContainer(t *testing.T) {
	dir := t.TempDir()
	writeWorldSDF(t, dir, "corridor")
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	factory := Factory{Engine: engine, Options: Options{AppImage: "runtime-sim:tag", DataRoot: t.TempDir()}, AssetsDir: dir}
	session := contract.Session{World: "corridor", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}}
	vehicle, err := factory.New(context.Background(), session, session.Robots[0], testLink(session.Robots[0]), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := vehicle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "worlds", "corridor", "gazebo", "world.sdf") + ":" + appWorldsDir + "/corridor.sdf:ro"
	if !slices.Contains(engine.started[0].Binds, want) {
		t.Fatalf("binds=%v missing the world handover", engine.started[0].Binds)
	}
}

// daemon 隔离环境（CI docker executor：simenv 跑在 runner 容器，bind 源由
// daemon 在宿主机解析）必须走 named volume：数据卷 daemon 自管，registry 与
// SDF 的宿主文件 bind 一律不挂。
func TestStartWithDataVolumePrefixUsesNamedVolumeOnly(t *testing.T) {
	dir := t.TempDir()
	writeWorldSDF(t, dir, "simple")
	dataRoot := t.TempDir()
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	factory := Factory{
		Engine:    engine,
		Options:   Options{AppImage: "runtime-sim:tag", DataRoot: dataRoot, DataVolumePrefix: "ci-1"},
		AssetsDir: dir,
	}
	session := contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}}
	vehicle, err := factory.New(context.Background(), session, session.Robots[0], testLink(session.Robots[0]), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := vehicle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := engine.started[0].Binds, []string{"ci-1-robot1:/sros"}; !slices.Equal(got, want) {
		t.Fatalf("binds=%v, want exactly %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "robot1")); !os.IsNotExist(err) {
		t.Fatalf("volume form must not stage host dirs under %s: %v", dataRoot, err)
	}
}

func TestStartRejectsParallelNamedVolumeResources(t *testing.T) {
	cases := []struct {
		name   string
		robots []contract.Robot
		slot   int
	}{
		{
			name:   "multiple robots",
			robots: []contract.Robot{{Index: 1, Name: "robot1"}, {Index: 2, Name: "robot2"}},
		},
		{
			name:   "parallel session slot",
			robots: []contract.Robot{{Index: 1, Name: "robot1"}},
			slot:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
			session := contract.Session{World: "simple", Robots: tc.robots}
			factory := Factory{
				Engine:      engine,
				Options:     Options{AppImage: "runtime-sim:tag", DataVolumePrefix: "ci-1"},
				SessionName: "session",
				Slot:        tc.slot,
			}
			vehicle, err := factory.New(context.Background(), session, tc.robots[0], testLink(tc.robots[0]), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := vehicle.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "data-volume-prefix requires exactly one robot in slot 0") {
				t.Fatalf("start error=%v", err)
			}
			if len(engine.started) != 0 {
				t.Fatalf("named-volume guard started containers: %v", engine.started)
			}
		})
	}
}

func TestWorldHandoverBindSkipsWorldsTheEnvironmentDoesNotShip(t *testing.T) {
	dir := t.TempDir()
	writeWorldSDF(t, dir, "corridor")
	if bind := worldHandoverBind(dir, "simple"); bind != "" {
		t.Fatalf("unexpected handover for a world the environment does not ship: %s", bind)
	}
	bind := worldHandoverBind(dir, "corridor")
	host, _, ok := strings.Cut(bind, ":")
	if !ok || !strings.HasSuffix(host, "/gazebo/world.sdf") {
		t.Fatalf("handover bind=%q", bind)
	}
}

func TestWorldHandoverBindUsesAnAbsoluteHostPath(t *testing.T) {
	dir := t.TempDir()
	writeWorldSDF(t, dir, "corridor")
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
	target := filepath.Join(dir, "worlds-assets", "worlds", "corridor", "gazebo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "worlds", "corridor", "gazebo", "world.sdf"), filepath.Join(target, "world.sdf")); err != nil {
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

func TestVehicleOnlyManagesItsRobot(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{
		"simenv-vehicle-robot1": {Name: "simenv-vehicle-robot1", Running: true, Image: "runtime-1"},
		"simenv-vehicle-robot2": {Name: "simenv-vehicle-robot2", Running: true, Image: "runtime-2"},
	}}
	vehicle := Vehicle{
		Engine: engine,
		Robot:  contract.Robot{Index: 1, Name: "robot1"},
	}

	status, err := vehicle.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var _ orchestrator.VehicleStatus = status // 返回类型由框架接口定义
	if status.Name != "robot1" || status.Detail["container"] != "simenv-vehicle-robot1" || status.Detail["image"] != "runtime-1" {
		t.Fatalf("status=%+v", status)
	}
	if status.Endpoint != "http://127.0.0.1:18189" {
		t.Fatalf("endpoint=%q", status.Endpoint)
	}

	if err := vehicle.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 1 || engine.stopped[0] != "simenv-vehicle-robot1" {
		t.Fatalf("stopped=%v", engine.stopped)
	}
}

func TestFactoryDiscoversOnlyRobotContainers(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{
		"simenv-world": {Name: "simenv-world", Running: true},
		"legacy-app-name": {
			Name:    "legacy-app-name",
			Running: true,
			Labels:  appLabels("robot1", 1),
		},
		"simenv-vehicle-robot02": {
			Name:    "simenv-vehicle-robot02",
			Running: true,
			Labels: map[string]string{
				"simenv.managed":     "true",
				"simenv.role":        "app",
				"simenv.robot":       "robot2",
				"simenv.robot.index": "2",
			},
		},
		"simenv-vehicle-robot2-debug": {
			Name:    "simenv-vehicle-robot2-debug",
			Running: true,
			Labels: map[string]string{
				"simenv.managed": "true",
				"simenv.role":    "app",
			},
		},
	}}
	factory := Factory{Engine: engine}

	ports, err := factory.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 {
		t.Fatalf("ports=%d", len(ports))
	}
	names := map[string]bool{}
	for _, port := range ports {
		vehicle, ok := port.(Vehicle)
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
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{
		"legacy-app-name": {
			Name: "legacy-app-name", Running: true, Image: "runtime",
			Labels: appLabels("robot1", 1),
		},
	}}
	factory := Factory{Engine: engine}

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
	if status.Name != "robot1" || status.Detail["container"] != "legacy-app-name" {
		t.Fatalf("status=%+v", status)
	}
	if err := ports[0].Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 1 || engine.stopped[0] != "legacy-app-name" {
		t.Fatalf("stopped=%v", engine.stopped)
	}
}

func TestRuntimePortArithmetic(t *testing.T) {
	runtime := runtimeFor(1, 0)
	if runtime.apiPort != 18189 || runtime.frontendPort != 18188 || runtime.voicePort != 18191 {
		t.Fatalf("robot1 runtime=%+v", runtime)
	}
	if runtime.rosDomainID != 11 {
		t.Fatalf("robot1 domain=%d", runtime.rosDomainID)
	}
	runtime = runtimeFor(2, 0)
	if runtime.apiPort != 18289 || runtime.rosDomainID != 12 {
		t.Fatalf("robot2 runtime=%+v", runtime)
	}
}

func appLabels(name string, index int) map[string]string {
	return map[string]string{
		docker.LabelManaged: "true",
		docker.LabelRole:    docker.RoleApp,
		vehicleRobotLabel:   name,
		vehicleIndexLabel:   strconv.Itoa(index),
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

// writeWorldSDF 按 bundle 目录布局写一份假的世界 SDF：worlds/<world>/gazebo/world.sdf。
func writeWorldSDF(t *testing.T, dir, world string) {
	t.Helper()
	gazeboDir := filepath.Join(dir, "worlds", world, "gazebo")
	if err := os.MkdirAll(gazeboDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gazeboDir, "world.sdf"), []byte("<sdf/>"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assetsWithModels 造一个带 models/ 的假资产目录：gz-gui 的道具挂载缺它即失败。
func assetsWithModels(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}
