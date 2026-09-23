package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
)

type ContainerSpec struct {
	Name    string
	Image   string
	Command []string
	Env     map[string]string
	Binds   []string
	Network string
	Ipc     string
	Labels  map[string]string
}

type ContainerInfo struct {
	Name    string
	ID      string
	Image   string
	Running bool
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
	Name    string
	Running bool
	APIPort int
	Image   string
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
	Ready(ctx context.Context) (world.ReadyReport, error)
	Status(ctx context.Context) (ContainerInfo, world.ReadyReport, error)
	Stop(ctx context.Context) error
}

type VehiclePort interface {
	Start(ctx context.Context, session contract.Session, robot contract.Robot) error
	Status(ctx context.Context) ([]AppStatus, error)
	Stop(ctx context.Context) error
}

type Orchestrator struct {
	Engine  Engine
	World   WorldPort
	Vehicle VehiclePort
	Now     func() time.Time
	Sleep   func(context.Context, time.Duration) error
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
	vehiclePort := o.vehiclePort(cfg)
	if err := worldPort.Start(ctx, session); err != nil {
		return Status{}, err
	}
	if _, err := worldPort.Ready(ctx); err != nil {
		return Status{}, err
	}
	for _, robot := range session.Robots {
		if err := vehiclePort.Start(ctx, session, robot); err != nil {
			return Status{}, err
		}
	}
	return statusFromPorts(ctx, worldPort, vehiclePort)
}

func (o Orchestrator) Stop(ctx context.Context) error {
	var first error
	if err := o.vehiclePort(contract.StartConfig{}).Stop(ctx); err != nil {
		first = err
	}
	if err := o.worldPort(contract.StartConfig{}).Stop(ctx); err != nil && first == nil {
		first = err
	}
	return first
}

func (o Orchestrator) Status(ctx context.Context) (Status, error) {
	return statusFromPorts(ctx, o.worldPort(contract.StartConfig{}), o.vehiclePort(contract.StartConfig{}))
}

func statusFromPorts(ctx context.Context, worldPort WorldPort, vehiclePort VehiclePort) (Status, error) {
	worldInfo, ready, err := worldPort.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	apps, err := vehiclePort.Status(ctx)
	if err != nil {
		return Status{}, err
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
		Connected: worldInfo.Running && ready.Clock && len(ready.Robots) > 0,
	}, nil
}

func (o Orchestrator) worldPort(cfg contract.StartConfig) WorldPort {
	if o.World != nil {
		return o.World
	}
	return DockerWorld{Engine: o.Engine, Config: cfg, Sleep: o.Sleep}
}

func (o Orchestrator) vehiclePort(cfg contract.StartConfig) VehiclePort {
	if o.Vehicle != nil {
		return o.Vehicle
	}
	return EasyGoVehicle{Engine: o.Engine, Config: cfg}
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
		Labels:  map[string]string{contract.LabelManaged: "true", contract.LabelRole: contract.RoleWorld},
	})
}

func (w DockerWorld) Ready(ctx context.Context) (world.ReadyReport, error) {
	return waitReady(ctx, w.Engine, w.Sleep)
}

func (w DockerWorld) Status(ctx context.Context) (ContainerInfo, world.ReadyReport, error) {
	worldInfo, err := w.Engine.Inspect(ctx, contract.WorldContainer)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ContainerInfo{}, world.ReadyReport{}, err
	}
	ready, _ := readReady(ctx, w.Engine)
	return worldInfo, ready, nil
}

func (w DockerWorld) Stop(ctx context.Context) error {
	return w.Engine.Stop(ctx, contract.WorldContainer)
}

type EasyGoVehicle struct {
	Engine Engine
	Config contract.StartConfig
}

func (v EasyGoVehicle) Start(ctx context.Context, session contract.Session, robot contract.Robot) error {
	if err := session.Validate(); err != nil {
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
	hostSros := filepath.Join(dataRoot, robot.Name)
	if err := os.MkdirAll(hostSros, 0o755); err != nil {
		return err
	}
	runtime := easyGoRuntimeFor(robot.Index)
	cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, easyGoLaunchArgs(robot, runtime, cfg.GUI)...)
	if err := v.Engine.Start(ctx, ContainerSpec{
		Name:    contract.AppContainer(robot.Name),
		Image:   cfg.AppImage,
		Command: cmd,
		Env:     appEnv(robot, session, runtime),
		Binds:   []string{hostSros + ":/sros"},
		Network: "host",
		Ipc:     "host",
		Labels:  map[string]string{contract.LabelManaged: "true", contract.LabelRole: contract.RoleApp},
	}); err != nil {
		return err
	}
	return nil
}

func (v EasyGoVehicle) Status(ctx context.Context) ([]AppStatus, error) {
	infos, err := v.Engine.List(ctx)
	if err != nil {
		return nil, err
	}
	var apps []AppStatus
	for _, info := range infos {
		if info.Name == contract.WorldContainer {
			continue
		}
		index := 0
		fmt.Sscanf(info.Name, "simenv-robot%d", &index)
		api := 0
		if index > 0 {
			api = easyGoRuntimeFor(index).apiPort
		}
		apps = append(apps, AppStatus{Name: info.Name, Running: info.Running, APIPort: api, Image: info.Image})
	}
	return apps, nil
}

func (v EasyGoVehicle) Stop(ctx context.Context) error {
	infos, err := v.Engine.List(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, info := range infos {
		if info.Name == contract.WorldContainer {
			continue
		}
		if err := v.Engine.Stop(ctx, info.Name); err != nil && first == nil {
			first = err
		}
	}
	return first
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

func waitReady(ctx context.Context, engine Engine, sleep func(context.Context, time.Duration) error) (world.ReadyReport, error) {
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
	deadline := time.Now().Add(3 * time.Minute)
	for {
		report, err := readReady(ctx, engine)
		if err == nil && report.Clock && len(report.Robots) > 0 {
			return report, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return world.ReadyReport{}, fmt.Errorf("world not ready: %w", err)
			}
			return world.ReadyReport{}, fmt.Errorf("world not ready")
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return world.ReadyReport{}, err
		}
	}
}

func readReady(ctx context.Context, engine Engine) (world.ReadyReport, error) {
	out, err := engine.Exec(ctx, contract.WorldContainer, []string{"cat", contract.WorldReadyPath})
	if err != nil {
		return world.ReadyReport{}, err
	}
	var report world.ReadyReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		return world.ReadyReport{}, err
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

func easyGoLaunchArgs(robot contract.Robot, runtime easyGoRuntime, gui bool) []string {
	return []string{
		"start_gazebo:=false",
		fmt.Sprintf("use_gz_gui:=%t", gui),
		fmt.Sprintf("use_rviz:=%t", gui),
		"enable_person_follow:=false",
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
	}
}

func cycloneDDSURI() string {
	return `<CycloneDDS><Domain><General><Interfaces><NetworkInterface name="lo" multicast="false" /></Interfaces><AllowMulticast>false</AllowMulticast><DontRoute>true</DontRoute></General><Discovery><Peers><Peer Address="127.0.0.1" /></Peers><ParticipantIndex>auto</ParticipantIndex><MaxAutoParticipantIndex>255</MaxAutoParticipantIndex></Discovery></Domain></CycloneDDS>`
}
