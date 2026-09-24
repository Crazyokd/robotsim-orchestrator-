package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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
}

type VehiclePort interface {
	Start(ctx context.Context) error
	Status(ctx context.Context) (AppStatus, error)
	Stop(ctx context.Context) error
	SeedMap(ctx context.Context, archive []byte) error
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
	assetsDir      string
}

const defaultAssetsDir = "deploy/assets"

func (o Orchestrator) assetsPath() string {
	if o.assetsDir != "" {
		return o.assetsDir
	}
	return defaultAssetsDir
}

func (o Orchestrator) worldMapExportPath(world string) string {
	return filepath.Join(o.assetsPath(), "worlds", world+".map_export")
}

func (o Orchestrator) Start(ctx context.Context, cfg contract.StartConfig) (Status, error) {
	cfg = withDefaults(cfg)
	if err := cfg.Validate(); err != nil {
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
	}
	if err := o.seedWorldMap(ctx, session.World, vehiclePorts); err != nil {
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
	return EasyGoVehicleFactory{Engine: o.Engine, Config: cfg}
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
	Engine Engine
	Config contract.StartConfig
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
}

func (v EasyGoVehicle) Start(ctx context.Context) error {
	if err := v.Session.Validate(); err != nil {
		return err
	}
	cfg := withDefaults(v.Config)
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
	runtime := easyGoRuntimeFor(v.Robot.Index)
	useRviz := cfg.WantsRviz(v.Robot.Index)
	useGzGui := cfg.WantsGzGui(v.Robot.Index)
	useCameras := cfg.WantsCameras(v.Robot.Index)
	cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, easyGoLaunchArgs(v.Robot, runtime, useRviz, useGzGui, useCameras)...)
	binds := []string{hostSros + ":/sros"}
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

	// Wait for the app to be ready.
	if err := waitAppReady(ctx, importURL); err != nil {
		return fmt.Errorf("seed map %s: %w", v.Robot.Name, err)
	}

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

// seedWorldMap reads the world's optional map export file and distributes it to
// every vehicle.
func (o Orchestrator) seedWorldMap(ctx context.Context, world string, vehicles []VehiclePort) error {
	path := o.worldMapExportPath(world)
	archive, err := os.ReadFile(path)
	if err != nil {
		// No map for this world — not an error.
		return nil
	}
	for _, vp := range vehicles {
		if err := vp.SeedMap(ctx, archive); err != nil {
			return err
		}
	}
	return nil
}

func waitAppReady(ctx context.Context, importURL string) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("app not ready after 3m")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, importURL[:len(importURL)-len("/map/import")]+"/map", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
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
