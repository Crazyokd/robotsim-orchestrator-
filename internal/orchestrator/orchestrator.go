package orchestrator

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

var ErrNotFound = errors.New("container not found")

const (
	easyGoHTTPBaseFrontend = 18088
	easyGoHTTPBaseAPI      = 18089
	easyGoHTTPBaseVoice    = 18091
	easyGoHTTPStride       = 100
	easyGoMQTTBase         = 1883
	easyGoObserveBase      = 8090
	easyGoWebGZWebBase     = 6080
	easyGoWebGZRFBBase     = 5901
	easyGoRVizWebBase      = 6081
	easyGoRVizRFBBase      = 5902
	easyGoROSDomainBase    = 11
	easyGoRuntimeRoot      = "/sros"
	hostGPUDevice          = "/dev/dri"
	vehicleRobotLabel      = "simenv.robot"
	vehicleIndexLabel      = "simenv.robot.index"
	// appGZResourcePath lets the vehicle-side Gazebo GUI resolve model://
	// URIs. The robot description lives in the app image under /opt/sros/share,
	// world models (pallets, cages) in the bringup package. Empty here means
	// every robot link mesh fails to load and the vehicle renders as nothing.
	appGZResourcePath = "/opt/sros/share:/opt/sros/share/easygo_sim_bringup/models"

	// appRegistryDir is where the app image keeps its parameter registry. The
	// registry ships with the easygo-param-manager deb, not easygo-app; the
	// runtime launch reads this same path unless EASYGO_SIM_APP_CONFIG_DIR /
	// PARAM_CFG_DIR overrides it, so a bind here reaches the live defaults.
	// simenv overrides individual files inside it instead of replacing the
	// directory, so every other default the image ships stays authoritative.
	appRegistryDir = "/opt/sros/share/easygo-param-manager/cfg"
	// appConfigHostDir holds, per robot, the registry files simenv overrides.
	appConfigHostDir = "app-config"

	// vehicleSerialRegistry carries the vehicle identity.
	vehicleSerialRegistry = "param-registry/function.basic_setting.yaml"
	// syncRegistry carries the sync service's cluster-gossip membership port.
	syncRegistry = "param-registry/function.sync.yaml"
	// vehicleSerialKey / syncMembershipPortKey are the registry entries simenv
	// pins per robot.
	vehicleSerialKey      = "main.vehicle_serial_no"
	syncMembershipPortKey = "sync.membership_port"

	// appReadyTimeout bounds how long a starting vehicle may take to serve its
	// API before the whole start is rolled back.
	appReadyTimeout  = 5 * time.Minute
	appReadyInterval = 2 * time.Second

	// multiAMRPeerPort is the UDP peer-link port standing in for BLE.
	multiAMRPeerPort = 16889
	// Per-robot offsets for the sync service's discovery sockets. Several
	// vehicles sharing one host network would otherwise bind the same port and
	// easygo-app would abort with "Address already in use".
	mdnsPortBase           = 25353
	syncMembershipPortBase = 25354
	syncPortStride         = 100
)

type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
	Env     map[string]string
	Binds   []string
	Network string
	Ipc     string
	Devices []string
	Labels  map[string]string
}

type ContainerInfo struct {
	Name    string
	ID      string
	Image   string
	Running bool
	Labels  map[string]string
}

type Engine interface {
	ImageID(ctx context.Context, ref string) (string, error)
	// ReadImageFile reads one file out of an image without running anything.
	ReadImageFile(ctx context.Context, image, path string) ([]byte, error)
	Start(ctx context.Context, spec ContainerSpec) error
	Stop(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (ContainerInfo, error)
	Exec(ctx context.Context, name string, cmd []string) (string, error)
	List(ctx context.Context) ([]ContainerInfo, error)
}

type AppStatus struct {
	Name      string
	RobotName string
	Running   bool
	APIPort   int
	Image     string
}

type Status struct {
	World     ContainerInfo
	Apps      []AppStatus
	Ready     world.ReadyReport
	EnvImage  string
	AppImage  string
	Connected bool
}

type easyGoRuntime struct {
	frontendPort int
	apiPort      int
	voicePort    int
	mqttPort     int
	observePort  int
	webGZWebPort int
	webGZRFBPort int
	rvizWebPort  int
	rvizRFBPort  int
	rosDomainID  int
	runtimeRoot  string
}

type WorldPort interface {
	Start(ctx context.Context, session contract.Session) error
	Ready(ctx context.Context, session contract.Session) (world.ReadyReport, error)
	Status(ctx context.Context) (ContainerInfo, world.ReadyReport, error)
	Stop(ctx context.Context) error
	Pose(ctx context.Context, robot int) (EntityPose, error)
	Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error)
}

type VehiclePort interface {
	Start(ctx context.Context) error
	// WaitReady reports when the vehicle is actually serving: its app answered
	// on the API. Start returning nil only means the container exists, so the
	// orchestrator treats a vehicle as started once both have succeeded.
	WaitReady(ctx context.Context) error
	Status(ctx context.Context) (AppStatus, error)
	Stop(ctx context.Context) error
	SeedMap(ctx context.Context, archive []byte) error
	// SeedStations makes the world bundle's stations exist on the vehicle's map.
	// 站点的坐标是 bundle 的一部分：世界换了而站点没跟上，这里必须失败。
	SeedStations(ctx context.Context, mapName string, stations []contract.Station) error
	// Localize puts the vehicle on the map at the given world pose (bundle 的
	// initial_poses 优先于 session 的 spawn 位姿).
	Localize(ctx context.Context, mapName string, pose contract.Pose) error
	// Forward writes request to this vehicle and returns the response bytes.
	Forward(ctx context.Context, request []byte) ([]byte, error)
}

type VehiclePortFactory interface {
	New(session contract.Session, robot contract.Robot) VehiclePort
	Discover(ctx context.Context) ([]VehiclePort, error)
}

type Orchestrator struct {
	Engine         Engine
	World          WorldPort
	VehicleFactory VehiclePortFactory
	Sleep          func(context.Context, time.Duration) error
	// AppReady probes a vehicle's API until it answers. Nil means the real HTTP
	// probe; tests substitute it so they do not need a listening app.
	AppReady  func(ctx context.Context, apiPort int) error
	assetsDir string
}

const defaultAssetsDir = "deploy/assets"

func (o Orchestrator) assetsPath() string {
	return assetsPath(o.assetsDir)
}

// assetsPath 解析资产目录。默认值 `deploy/assets` 是**相对可执行文件所在目录**解析的，
// 不是相对 cwd：`simenv` 是仓库内的开发工具，从任意目录调用都该找到同一份资产。
//
// 这里踩过一次：原来直接返回相对路径，于是从别的目录调用时
// `deploy/assets/worlds/<world>.sdf` 找不到 → `worldHandoverBind` **静默返回空**
// → 车端容器拿不到环境这份世界文件 → 车端 launch 按自己的 worlds 目录校验世界名时
// 只认镜像内已有的世界。表现是：arena（镜像里有 simple.sdf）正常，
// 而 corridor 起栈时 app 起不来（`app API on port 18189 silent`）——而同样的命令
// 在仓库根目录下手动跑就成功。
func assetsPath(dir string) string {
	if dir != "" {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), defaultAssetsDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return defaultAssetsDir
}

// appWorldsDir 是车端镜像里 easygo_sim_bringup 的世界目录：车端 launch 按它校验世界名
// （easygo_sim_bringup/launch/sim_nav2.launch.py 的 `_world_path`），而世界本身由环境提供。
// 这是**过渡态**：easygo 侧接受显式世界路径后（合同 §5 第 1 条），本常量与 worldHandoverBind 一起删。
const appWorldsDir = "/opt/sros/share/easygo_sim_bringup/worlds"

// worldHandoverBind 把环境这份世界 SDF 只读挂进车端容器，让"同一份文件既喂 gz server
// 又喂车端校验"——否则世界在两边各存一份，内容会悄悄漂移。世界文件不在环境里时不挂
// （裸世界只看场景，车端用它自己那份）。
func worldHandoverBind(assetsDir, world string) string {
	path := filepath.Join(assetsPath(assetsDir), "worlds", world+".sdf")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	// 挂载源必须是绝对路径：默认资产目录是相对的（`deploy/assets`），Docker 会把它
	// 当成 volume 名而拒绝创建容器。
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return absolute + ":" + filepath.Join(appWorldsDir, world+".sdf") + ":ro"
}

func (o Orchestrator) worldMapExportPath(world string) string {
	return filepath.Join(o.assetsPath(), "worlds", world+".map_export")
}

func (o Orchestrator) Start(ctx context.Context, cfg contract.StartConfig) (Status, error) {
	cfg = withDefaults(cfg)
	if err := cfg.Validate(); err != nil {
		return Status{}, err
	}
	// 世界 bundle 决定初始位姿：生成（世界进程 spawn）与定位必须用同一份，
	// 否则会出现"车在东端待避区、却被告知你在西端"这种自相矛盾的启动。
	bundle, err := o.loadWorldBundle(cfg.World)
	if err != nil {
		return Status{}, err
	}
	if err := applyBundlePoses(&cfg, bundle); err != nil {
		return Status{}, err
	}
	session, err := cfg.Session()
	if err != nil {
		return Status{}, err
	}
	worldPort := o.worldPort(cfg)
	vehicleFactory := o.vehicleFactory(cfg)
	existingVehicles, err := vehicleFactory.Discover(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := stopVehiclePorts(ctx, existingVehicles); err != nil {
		return Status{}, err
	}
	vehiclePorts := make([]VehiclePort, 0, len(session.Robots))
	rollback := func() error {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var cleanupErr error
		cleanupErr = errors.Join(cleanupErr, stopVehiclePorts(cleanupCtx, vehiclePorts))
		cleanupErr = errors.Join(cleanupErr, worldPort.Stop(cleanupCtx))
		return cleanupErr
	}
	if err := worldPort.Start(ctx, session); err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	if _, err := worldPort.Ready(ctx, session); err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	for _, robot := range session.Robots {
		vehiclePort := vehicleFactory.New(session, robot)
		vehiclePorts = append(vehiclePorts, vehiclePort)
		if err := vehiclePort.Start(ctx); err != nil {
			return Status{}, errors.Join(err, rollback())
		}
		if err := vehiclePort.WaitReady(ctx); err != nil {
			return Status{}, errors.Join(err, rollback())
		}
	}
	// world init：bundle → 地图 → 站点 → 放车定位。任一步不满足即失败退出。
	if _, err := o.worldInit(ctx, session, vehiclePorts, bundle); err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	status, err := statusFromPorts(ctx, worldPort, vehiclePorts)
	if err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	return status, nil
}

func (o Orchestrator) Stop(ctx context.Context) error {
	worldPort := o.worldPort(contract.StartConfig{})
	vehiclePorts, discoverErr := o.vehicleFactory(contract.StartConfig{}).Discover(ctx)
	return errors.Join(
		discoverErr,
		stopVehiclePorts(ctx, vehiclePorts),
		worldPort.Stop(ctx),
	)
}

func (o Orchestrator) Status(ctx context.Context) (Status, error) {
	vehiclePorts, err := o.vehicleFactory(contract.StartConfig{}).Discover(ctx)
	if err != nil {
		return Status{}, err
	}
	return statusFromPorts(ctx, o.worldPort(contract.StartConfig{}), vehiclePorts)
}

func statusFromPorts(ctx context.Context, worldPort WorldPort, vehiclePorts []VehiclePort) (Status, error) {
	worldInfo, ready, err := worldPort.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	apps := make([]AppStatus, 0, len(vehiclePorts))
	for _, vehiclePort := range vehiclePorts {
		app, err := vehiclePort.Status(ctx)
		if err != nil {
			return Status{}, err
		}
		apps = append(apps, app)
	}
	appImage := ""
	if len(apps) > 0 {
		appImage = apps[0].Image
	}
	return Status{
		World:     worldInfo,
		Apps:      apps,
		Ready:     ready,
		EnvImage:  worldInfo.Image,
		AppImage:  appImage,
		Connected: sessionConnected(worldInfo, ready, apps),
	}, nil
}

func sessionConnected(worldInfo ContainerInfo, ready world.ReadyReport, apps []AppStatus) bool {
	if !worldInfo.Running || !ready.Clock || len(ready.Robots) == 0 || len(apps) != len(ready.Robots) {
		return false
	}
	expected := make(map[string]struct{}, len(ready.Robots))
	for _, name := range ready.Robots {
		expected[name] = struct{}{}
	}
	if len(expected) != len(ready.Robots) {
		return false
	}
	for _, app := range apps {
		if !app.Running {
			return false
		}
		if _, ok := expected[app.RobotName]; !ok {
			return false
		}
		delete(expected, app.RobotName)
	}
	return len(expected) == 0
}

func (o Orchestrator) worldPort(cfg contract.StartConfig) WorldPort {
	if o.World != nil {
		return o.World
	}
	return DockerWorld{Engine: o.Engine, Config: cfg, Sleep: o.Sleep}
}

func (o Orchestrator) vehicleFactory(cfg contract.StartConfig) VehiclePortFactory {
	if o.VehicleFactory != nil {
		return o.VehicleFactory
	}
	return EasyGoVehicleFactory{Engine: o.Engine, Config: cfg, AppReady: o.AppReady, AssetsDir: o.assetsDir}
}

func stopVehiclePorts(ctx context.Context, vehiclePorts []VehiclePort) error {
	var stopErr error
	for i := len(vehiclePorts) - 1; i >= 0; i-- {
		stopErr = errors.Join(stopErr, vehiclePorts[i].Stop(ctx))
	}
	return stopErr
}

type DockerWorld struct {
	Engine Engine
	Config contract.StartConfig
	Sleep  func(context.Context, time.Duration) error
}

func (w DockerWorld) Start(ctx context.Context, session contract.Session) error {
	if err := session.Validate(); err != nil {
		return err
	}
	cfg := withDefaults(w.Config)
	rawSession, err := json.Marshal(session)
	if err != nil {
		return err
	}
	worldCmd := []string{"--session", string(rawSession)}
	if len(cfg.CameraRobots) > 0 {
		cameraRobots := make([]string, 0, len(cfg.CameraRobots))
		for _, index := range cfg.CameraRobots {
			cameraRobots = append(cameraRobots, strconv.Itoa(index))
		}
		worldCmd = append(worldCmd, "--cameras", strings.Join(cameraRobots, ","))
	}
	return w.Engine.Start(ctx, ContainerSpec{
		Name:    contract.WorldContainer,
		Image:   cfg.EnvImage,
		Command: worldCmd,
		Env: map[string]string{
			"GZ_IP":         contract.GZIP,
			"GZ_PARTITION":  session.Partition,
			"ROS_DOMAIN_ID": strconv.Itoa(contract.EnvROSDomainID),
		},
		Network: "host",
		Ipc:     "host",
		Devices: hostDevices(),
		Labels:  map[string]string{contract.LabelManaged: "true", contract.LabelRole: contract.RoleWorld},
	})
}

func (w DockerWorld) Ready(ctx context.Context, session contract.Session) (world.ReadyReport, error) {
	if err := session.Validate(); err != nil {
		return world.ReadyReport{}, err
	}
	return waitReady(ctx, w.Engine, w.Sleep, session)
}

func (w DockerWorld) Status(ctx context.Context) (ContainerInfo, world.ReadyReport, error) {
	worldInfo, err := w.Engine.Inspect(ctx, contract.WorldContainer)
	if errors.Is(err, ErrNotFound) {
		return ContainerInfo{}, world.ReadyReport{}, nil
	}
	if err != nil {
		return ContainerInfo{}, world.ReadyReport{}, err
	}
	if !worldInfo.Running {
		return worldInfo, world.ReadyReport{}, nil
	}
	ready, err := readReady(ctx, w.Engine)
	if errors.Is(err, errWorldNotReady) {
		return worldInfo, world.ReadyReport{}, nil
	}
	if err != nil {
		return worldInfo, world.ReadyReport{}, fmt.Errorf("read world readiness: %w", err)
	}
	return worldInfo, ready, nil
}

func (w DockerWorld) Stop(ctx context.Context) error {
	return w.Engine.Stop(ctx, contract.WorldContainer)
}

type EasyGoVehicleFactory struct {
	Engine    Engine
	Config    contract.StartConfig
	AppReady  func(ctx context.Context, apiPort int) error
	AssetsDir string
}

func (f EasyGoVehicleFactory) New(session contract.Session, robot contract.Robot) VehiclePort {
	return f.newVehicle(session, robot, contract.AppContainer(robot.Name))
}

func (f EasyGoVehicleFactory) newVehicle(session contract.Session, robot contract.Robot, containerName string) VehiclePort {
	return EasyGoVehicle{
		Engine:        f.Engine,
		Config:        f.Config,
		Session:       session,
		Robot:         robot,
		ContainerName: containerName,
		AppReady:      f.AppReady,
		AssetsDir:     f.AssetsDir,
	}
}

func (f EasyGoVehicleFactory) Discover(ctx context.Context) ([]VehiclePort, error) {
	infos, err := f.Engine.List(ctx)
	if err != nil {
		return nil, err
	}
	ports := make([]VehiclePort, 0, len(infos))
	for _, info := range infos {
		if info.Labels[contract.LabelManaged] != "true" || info.Labels[contract.LabelRole] != contract.RoleApp {
			continue
		}
		robot, ok := robotFromLabels(info.Labels)
		if !ok {
			continue
		}
		ports = append(ports, f.newVehicle(contract.Session{}, robot, info.Name))
	}
	return ports, nil
}

func robotFromLabels(labels map[string]string) (contract.Robot, bool) {
	name := strings.TrimSpace(labels[vehicleRobotLabel])
	indexText := strings.TrimSpace(labels[vehicleIndexLabel])
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 1 || name != contract.RobotName(index) {
		return contract.Robot{}, false
	}
	robot, err := contract.NewRobot(index, contract.Pose{})
	if err != nil {
		return contract.Robot{}, false
	}
	return robot, true
}

type EasyGoVehicle struct {
	Engine        Engine
	Config        contract.StartConfig
	Session       contract.Session
	Robot         contract.Robot
	ContainerName string
	AppReady      func(ctx context.Context, apiPort int) error
	AssetsDir     string
	DoJSON        func(ctx context.Context, method, path string, payload []byte) ([]byte, int, error)
	Dial          func(ctx context.Context, network, address string) (net.Conn, error)
}

func (v EasyGoVehicle) Start(ctx context.Context) error {
	if err := v.Session.Validate(); err != nil {
		return err
	}
	cfg := withDefaults(v.Config)
	runtime := easyGoRuntimeFor(v.Robot.Index)
	useRviz := cfg.WantsRviz(v.Robot.Index)
	useGzGui := cfg.WantsGzGui(v.Robot.Index)
	useCameras := cfg.WantsCameras(v.Robot.Index)
	cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, easyGoLaunchArgs(v.Robot, runtime, useRviz, useGzGui, useCameras)...)
	var binds []string
	if prefix := strings.TrimSpace(cfg.DataVolumePrefix); prefix != "" {
		// Volume 形态：daemon 自管数据卷，首次挂载继承镜像 /sros（0777，
		// 车端 std 可写）。registry 补丁与世界 SDF handover 都依赖宿主机
		// 路径绑定，此形态下一律不挂——车辆身份由 launch 的 SERIAL_NUMBER
		// 播种（单车无 sync 端口冲突），世界 SDF 用车端镜像自带那份
		// （与环境 pinned 资产同源）。
		binds = append(binds, prefix+"-"+v.Robot.Name+":/sros")
	} else {
		dataRoot := cfg.DataRoot
		if dataRoot == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				return err
			}
			dataRoot = filepath.Join(cache, "simenv")
		}
		hostSros := filepath.Join(dataRoot, v.Robot.Name)
		if err := os.MkdirAll(hostSros, 0o755); err != nil {
			return err
		}
		// The vehicle process runs as the image's std user (uid 1000) while
		// simenv may run as another uid (CI runs as root). The bind target
		// must stay writable for the vehicle, otherwise its launch refuses
		// to start.
		if err := os.Chmod(hostSros, 0o777); err != nil {
			return fmt.Errorf("make %s writable for the vehicle user: %w", hostSros, err)
		}
		appConfigBinds, err := v.initializeAppConfig(ctx, cfg, hostSros)
		if err != nil {
			return err
		}
		binds = append([]string{hostSros + ":/sros"}, appConfigBinds...)
		if handover := worldHandoverBind(v.AssetsDir, v.Session.World); handover != "" {
			binds = append(binds, handover)
		}
	}
	if useGzGui {
		if bind := gzGuiAssetBind(); bind != "" {
			binds = append(binds, bind)
		}
	}
	if err := v.Engine.Start(ctx, ContainerSpec{
		Name:    contract.AppContainer(v.Robot.Name),
		Image:   cfg.AppImage,
		Command: cmd,
		Env:     appEnv(v.Robot, v.Session, runtime),
		Binds:   binds,
		Network: "host",
		Ipc:     "host",
		Devices: guiDevices(useRviz || useGzGui),
		Labels: map[string]string{
			contract.LabelManaged: "true",
			contract.LabelRole:    contract.RoleApp,
			vehicleRobotLabel:     v.Robot.Name,
			vehicleIndexLabel:     strconv.Itoa(v.Robot.Index),
		},
	}); err != nil {
		return err
	}
	return nil
}

// initializeAppConfig pins this robot's own parameter values and returns the
// mounts that put them in place. It runs before the vehicle container starts:
// easygo-app reads these parameters during startup, so they cannot be applied
// afterwards.
//
// The image already ships every default, so initialization only overwrites the
// parameters that must differ per robot. Each one is read out of the image,
// patched in place in Go, and mounted back over the image's own file; nothing
// else about the registry changes.
func (v EasyGoVehicle) initializeAppConfig(ctx context.Context, cfg contract.StartConfig, hostSros string) ([]string, error) {
	overrides := v.initialRegistryDefaults()
	hostDir := filepath.Join(hostSros, appConfigHostDir)
	if err := os.RemoveAll(hostDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return nil, err
	}
	staged := make(map[string]string, len(overrides))
	binds := make([]string, 0, len(overrides))
	for _, override := range overrides {
		name := filepath.Base(override.Registry)
		if other, clash := staged[name]; clash {
			return nil, fmt.Errorf("registry overrides %s and %s stage the same file name", other, override.Registry)
		}
		staged[name] = override.Registry

		registry, err := v.Engine.ReadImageFile(ctx, cfg.AppImage, appRegistryDir+"/"+override.Registry)
		if err != nil {
			return nil, fmt.Errorf("read %s from %s: %w", override.Registry, cfg.AppImage, err)
		}
		patched, err := patchRegistryDefault(registry, override.Key, override.Value)
		if err != nil {
			return nil, fmt.Errorf("initialize app config for %s: %w", v.Robot.Name, err)
		}
		hostPath := filepath.Join(hostDir, name)
		if err := os.WriteFile(hostPath, patched, 0o644); err != nil {
			return nil, fmt.Errorf("initialize app config for %s: %w", v.Robot.Name, err)
		}
		binds = append(binds, hostPath+":"+appRegistryDir+"/"+override.Registry)
	}
	return binds, nil
}

// initialRegistryDefaults lists the parameter defaults simenv pins for one
// robot. Add an entry here to make another parameter differ per vehicle.
func (v EasyGoVehicle) initialRegistryDefaults() []registryOverride {
	return []registryOverride{
		// Vehicle identity. It is the multi-AMR peer id: with it empty every
		// vehicle advertises the same one, peers discard each other as their own
		// frames, and a multi-vehicle world never converges.
		{Registry: vehicleSerialRegistry, Key: vehicleSerialKey, Value: strconv.Quote(v.Robot.Name)},
		// A UDP bind for the sync service's cluster-gossip discovery. Vehicles
		// sharing one host network would otherwise collide and easygo-app would
		// abort at startup.
		{
			Registry: syncRegistry,
			Key:      syncMembershipPortKey,
			Value:    strconv.Itoa(syncMembershipPortBase + (v.Robot.Index-1)*syncPortStride),
		},
	}
}

type registryOverride struct {
	// Registry is the registry file relative to appRegistryDir.
	Registry string
	Key      string
	// Value is YAML scalar text, already quoted if the scalar needs quoting.
	Value string
}

// patchRegistryDefault rewrites default_value on one registry entry, preserving
// the document's formatting.
func patchRegistryDefault(registry []byte, key, value string) ([]byte, error) {
	keyLine := "- key: " + key
	lines := strings.Split(string(registry), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == keyLine {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("registry has no %s entry", key)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "- key: ") {
			end = i
			break
		}
	}
	entryIndent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
	assignment := "default_value: " + value
	for i := start + 1; i < end; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "default_value:") {
			lineIndent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
			lines[i] = lineIndent + assignment
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	patched := make([]string, 0, len(lines)+1)
	patched = append(patched, lines[:start+1]...)
	patched = append(patched, entryIndent+"  "+assignment)
	patched = append(patched, lines[start+1:]...)
	return []byte(strings.Join(patched, "\n")), nil
}

func (v EasyGoVehicle) Status(ctx context.Context) (AppStatus, error) {
	info, err := v.Engine.Inspect(ctx, v.containerName())
	if err != nil {
		return AppStatus{}, err
	}
	return AppStatus{
		Name:      info.Name,
		RobotName: v.Robot.Name,
		Running:   info.Running,
		APIPort:   easyGoRuntimeFor(v.Robot.Index).apiPort,
		Image:     info.Image,
	}, nil
}

func (v EasyGoVehicle) Stop(ctx context.Context) error {
	return v.Engine.Stop(ctx, v.containerName())
}

func (v EasyGoVehicle) containerName() string {
	if v.ContainerName != "" {
		return v.ContainerName
	}
	return contract.AppContainer(v.Robot.Name)
}

// SeedMap imports a world map archive into the vehicle's easygo-app.  It retries
// until the app HTTP API becomes reachable.
func (v EasyGoVehicle) SeedMap(ctx context.Context, archive []byte) error {
	runtime := easyGoRuntimeFor(v.Robot.Index)
	importURL := fmt.Sprintf("http://127.0.0.1:%d/api/v6/map/import", runtime.apiPort)

	body := new(bytes.Buffer)
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("file", "world.map_export")
	if err != nil {
		return fmt.Errorf("seed map %s: build multipart: %w", v.Robot.Name, err)
	}
	if _, err := part.Write(archive); err != nil {
		return fmt.Errorf("seed map %s: write body: %w", v.Robot.Name, err)
	}
	mp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, importURL, body)
	if err != nil {
		return fmt.Errorf("seed map %s: %w", v.Robot.Name, err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("seed map %s: %w", v.Robot.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && !mapAlreadyExists(respBody) {
		return fmt.Errorf("seed map %s: %s (%s)", v.Robot.Name, resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// worldMapName reports the primary layer name inside a .map_export archive,
// resolved the same way easygo-app does: among the `{name}0.pgm` entries, the
// first name that is not a numbered sublayer.
func worldMapName(archive []byte) (string, error) {
	reader, err := mapArchiveReader(archive)
	if err != nil {
		return "", err
	}
	names := map[string]struct{}{}
	entries := tar.NewReader(reader)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read map export: %w", err)
		}
		base := path.Base(header.Name)
		stem, ok := strings.CutSuffix(base, "0.pgm")
		if !ok || stem == "" {
			continue
		}
		names[stem] = struct{}{}
	}
	if len(names) == 0 {
		return "", errors.New("map export has no primary layer")
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		if isSublayerName(name) {
			continue
		}
		ordered = append(ordered, name)
	}
	if len(ordered) == 0 {
		return "", errors.New("map export has no primary layer")
	}
	sort.Strings(ordered)
	return ordered[0], nil
}

// isSublayerName reports whether a layer name is a `_<digits>` sublayer.
func isSublayerName(name string) bool {
	_, suffix, ok := strings.Cut(name, "_")
	if !ok {
		return false
	}
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func mapArchiveReader(archive []byte) (io.Reader, error) {
	gzip, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return bytes.NewReader(archive), nil
	}
	return gzip, nil
}

// Localize puts this vehicle on the map at the given **map** pose (the world
// bundle's initial_poses, converted by mapPose; 缺省是 session 的 spawn 位姿换算后的结果).
// easygo-app takes millimetres and milliradians and drives the AGV through the
// VDA5050 initPosition action; 这里在下发之后再断言一次收敛——下发成功不等于定位成功，
// 坐标系搞错时车端只会静默地定到别处。
func (v EasyGoVehicle) Localize(ctx context.Context, mapName string, pose contract.Pose) error {
	body, err := initialPoseRequest(mapName, pose)
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v6/vehicle/localization/initial-pose", easyGoRuntimeFor(v.Robot.Index).apiPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("localize %s: %s (%s)", v.Robot.Name, resp.Status, strings.TrimSpace(string(respBody)))
	}
	return v.awaitPose(ctx, pose)
}

// initialPoseRequest builds the app's initial-pose body. The world spawns the
// robot in metres and radians; the app takes millimetres and milliradians.
func initialPoseRequest(mapName string, pose contract.Pose) ([]byte, error) {
	return json.Marshal(map[string]any{
		"map_id":  mapName,
		"x":       pose.X * 1000,
		"y":       pose.Y * 1000,
		"theta":   pose.Yaw * 1000,
		"loctype": 2,
	})
}

// WaitReady blocks until the vehicle's app answers on its API, so that a
// successfully started vehicle is one that is actually serving.
func (v EasyGoVehicle) WaitReady(ctx context.Context) error {
	probe := v.AppReady
	if probe == nil {
		probe = waitForAppAPI
	}
	if err := probe(ctx, easyGoRuntimeFor(v.Robot.Index).apiPort); err != nil {
		return fmt.Errorf("wait for %s app: %w", v.Robot.Name, err)
	}
	return nil
}

// waitForAppAPI 等车端真的可用：API 答话，并且 init 报 succeeded。
// API 答话不等于就绪——init 没完成前车端不接受初始位姿。
func waitForAppAPI(ctx context.Context, apiPort int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v6/map", apiPort)
	deadline := time.Now().Add(appReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return waitForVehicleInit(ctx, apiPort)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("app API on port %d silent for %s", apiPort, appReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(appReadyInterval):
		}
	}
}

// mapAlreadyExists reports whether body is a "map already exists" error
// (error_code 912004) from the easygo-app map import endpoint.
func mapAlreadyExists(body []byte) bool {
	return strings.Contains(string(body), `error_code":912004`)
}

func withDefaults(cfg contract.StartConfig) contract.StartConfig {
	if cfg.EnvImage == "" {
		cfg.EnvImage = contract.DefaultEnvImage
	}
	if cfg.Partition == "" {
		cfg.Partition = contract.DefaultPartition
	}
	if cfg.World == "" {
		cfg.World = contract.DefaultWorld
	}
	if cfg.Robots == 0 {
		cfg.Robots = 1
	}
	return cfg
}

func waitReady(ctx context.Context, engine Engine, sleep func(context.Context, time.Duration) error, session contract.Session) (world.ReadyReport, error) {
	return waitReadyWithTimeout(ctx, engine, sleep, session, 3*time.Minute)
}

func waitReadyWithTimeout(ctx context.Context, engine Engine, sleep func(context.Context, time.Duration) error, session contract.Session, timeout time.Duration) (world.ReadyReport, error) {
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	for {
		report, err := readReady(readyCtx, engine)
		if err == nil && readyReportMatches(report, session) {
			return report, nil
		}
		if readyErr := readyCtx.Err(); readyErr != nil {
			return world.ReadyReport{}, fmt.Errorf("world not ready: %w", readyErr)
		}
		if err := sleep(readyCtx, 200*time.Millisecond); err != nil {
			return world.ReadyReport{}, err
		}
	}
}

var errWorldNotReady = errors.New("world readiness not available")

func readyReportMatches(report world.ReadyReport, session contract.Session) bool {
	if !report.Clock || len(report.Robots) != len(session.Robots) {
		return false
	}
	expected := make(map[string]struct{}, len(session.Robots))
	for _, robot := range session.Robots {
		expected[robot.Name] = struct{}{}
	}
	if len(expected) != len(session.Robots) {
		return false
	}
	for _, name := range report.Robots {
		if _, ok := expected[name]; !ok {
			return false
		}
		delete(expected, name)
	}
	return len(expected) == 0
}

func readReady(ctx context.Context, engine Engine) (world.ReadyReport, error) {
	out, err := engine.Exec(ctx, contract.WorldContainer, []string{"cat", contract.WorldReadyPath})
	if err != nil {
		return world.ReadyReport{}, err
	}
	raw := strings.TrimSpace(out)
	lower := strings.ToLower(raw)
	if raw == "" || strings.Contains(lower, "no such file or directory") {
		return world.ReadyReport{}, errWorldNotReady
	}
	var report world.ReadyReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return world.ReadyReport{}, fmt.Errorf("decode world readiness: %w", err)
	}
	return report, nil
}

func easyGoRuntimeFor(index int) easyGoRuntime {
	return easyGoRuntime{
		frontendPort: easyGoHTTPBaseFrontend + index*easyGoHTTPStride,
		apiPort:      easyGoHTTPBaseAPI + index*easyGoHTTPStride,
		voicePort:    easyGoHTTPBaseVoice + index*easyGoHTTPStride,
		mqttPort:     easyGoMQTTBase + index,
		observePort:  easyGoObserveBase + index*easyGoHTTPStride,
		webGZWebPort: easyGoWebGZWebBase + index*easyGoHTTPStride,
		webGZRFBPort: easyGoWebGZRFBBase + index*easyGoHTTPStride,
		rvizWebPort:  easyGoRVizWebBase + index*easyGoHTTPStride,
		rvizRFBPort:  easyGoRVizRFBBase + index*easyGoHTTPStride,
		rosDomainID:  easyGoROSDomainBase + (index - 1),
		runtimeRoot:  easyGoRuntimeRoot,
	}
}

func easyGoLaunchArgs(robot contract.Robot, runtime easyGoRuntime, useRviz, useGzGui, useCameras bool) []string {
	return []string{
		"start_gazebo:=false",
		fmt.Sprintf("use_gz_gui:=%t", useGzGui),
		fmt.Sprintf("use_rviz:=%t", useRviz),
		"enable_person_follow:=false",
		fmt.Sprintf("bridge_rgbd:=%t", useCameras),
		"enable_multi_robot_wifi:=true",
		"robot_name:=" + robot.TopicPrefix,
		"runtime_root:=" + runtime.runtimeRoot,
		"map_path:=" + runtime.runtimeRoot + "/map/",
		fmt.Sprintf("frontend_port:=%d", runtime.frontendPort),
		fmt.Sprintf("api_port:=%d", runtime.apiPort),
		fmt.Sprintf("voice_port:=%d", runtime.voicePort),
		fmt.Sprintf("mqtt_port:=%d", runtime.mqttPort),
		fmt.Sprintf("slam_web_port:=%d", easyGoSlamWebPort(robot.Index)),
		fmt.Sprintf("observe_port:=%d", runtime.observePort),
		fmt.Sprintf("webgz_web_port:=%d", runtime.webGZWebPort),
		fmt.Sprintf("webgz_rfb_port:=%d", runtime.webGZRFBPort),
		fmt.Sprintf("webgz_display:=:%d", 10+robot.Index*10),
		"webgz_log_dir:=/tmp/webgz-" + robot.Name,
		fmt.Sprintf("rviz_web_port:=%d", runtime.rvizWebPort),
		fmt.Sprintf("rviz_rfb_port:=%d", runtime.rvizRFBPort),
		fmt.Sprintf("rviz_display:=:%d", 11+robot.Index*10),
		"rviz_log_dir:=/tmp/rviz-" + robot.Name,
		"serial_number:=" + robot.Name,
		"easygo_app_param_db_path:=" + runtime.runtimeRoot + "/db/cfg.db3",
		"database_url:=sqlite://" + runtime.runtimeRoot + "/db/easygo.db?mode=rwc",
	}
}

func easyGoSlamWebPort(index int) int {
	if index == 1 {
		return 8088
	}
	return 8018 + (index-2)*10
}

func appEnv(robot contract.Robot, session contract.Session, runtime easyGoRuntime) map[string]string {
	return map[string]string{
		"ROS_DOMAIN_ID":                 strconv.Itoa(runtime.rosDomainID),
		"ROS_AUTOMATIC_DISCOVERY_RANGE": "LOCALHOST",
		"RMW_IMPLEMENTATION":            "rmw_cyclonedds_cpp",
		"CYCLONEDDS_URI":                cycloneDDSURI(),
		"GZ_IP":                         contract.GZIP,
		"GZ_PARTITION":                  session.Partition,
		"SERIAL_NUMBER":                 robot.Name,
		"MQTT_BROKER_PORT":              strconv.Itoa(runtime.mqttPort),
		"BACKEND_PORT":                  strconv.Itoa(runtime.voicePort),
		"PARAM_DB_PATH":                 runtime.runtimeRoot + "/db/cfg.db3",
		"DATABASE_URL":                  "sqlite://" + runtime.runtimeRoot + "/db/easygo.db?mode=rwc",
		"EASYGO_SIM_WORLD":              session.World + ".sdf",
		"EASYGO_SIM_APP_ROOT":           runtime.runtimeRoot + "/easygo-app",
		"EASYGO_WEBAPP_DIR":             runtime.runtimeRoot + "/webapp",
		"GZ_SIM_RESOURCE_PATH":          appGZResourcePath,
		// Multi-AMR peer discovery: a UDP broadcast stands in for the BLE peer
		// link a real vehicle uses, so two robots in one host can see each other.
		"EASYGO_BLUETOOTH_TRANSPORT": "udp",
		"EASYGO_BLUETOOTH_UDP_PORT":  strconv.Itoa(multiAMRPeerPort),
		"ENABLE_MULTI_AMR_AVOIDANCE": "true",
		// The sync service advertises this port for mDNS discovery. The launch
		// owns PORT (the app's HTTP port); only mDNS needs a per-robot offset.
		"MDNS_PORT": strconv.Itoa(mdnsPortBase + (robot.Index-1)*syncPortStride),
	}
}

// hostDevices returns the GPU device nodes to map into a container when the
// host exposes them. Without them ogre2 ray casting falls back to llvmpipe,
// which cannot hold real time beyond one robot.
func hostDevices() []string {
	if _, err := os.Stat(hostGPUDevice); err != nil {
		return nil
	}
	return []string{hostGPUDevice}
}

func guiDevices(gui bool) []string {
	if !gui {
		return nil
	}
	return hostDevices()
}

// gzGuiAssetBind mounts this checkout's deploy/assets at /opt/simenv so the
// vehicle-side Gazebo GUI can read the world props. The world SDF includes
// model://black_pallet and model://cage1, which the world server resolves to
// /opt/simenv/models/... at load time -- absolute paths it then sends in the
// scene message. Those exist only inside the environment container, so a GUI
// without this mount renders the world without its props.
func gzGuiAssetBind() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	assets := filepath.Join(filepath.Dir(exe), "deploy", "assets")
	if _, err := os.Stat(filepath.Join(assets, "models")); err != nil {
		return ""
	}
	return assets + ":/opt/simenv"
}

func cycloneDDSURI() string {
	return `<CycloneDDS><Domain><General><Interfaces><NetworkInterface name="lo" multicast="false" /></Interfaces><AllowMulticast>false</AllowMulticast><DontRoute>true</DontRoute></General><Discovery><Peers><Peer Address="127.0.0.1" /></Peers><ParticipantIndex>auto</ParticipantIndex><MaxAutoParticipantIndex>255</MaxAutoParticipantIndex></Discovery></Domain></CycloneDDS>`
}
