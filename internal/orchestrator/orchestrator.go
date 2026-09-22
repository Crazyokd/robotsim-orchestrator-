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

type WorldPort interface {
	Start(ctx context.Context, session contract.Session) error
	Ready(ctx context.Context) (world.ReadyReport, error)
	Status(ctx context.Context) (ContainerInfo, world.ReadyReport, error)
	Stop(ctx context.Context) error
}

type VehiclePort interface {
	Start(ctx context.Context, session contract.Session, attachment contract.Attachment) error
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
	for _, attachment := range session.Attachments {
		if err := vehiclePort.Start(ctx, session, attachment); err != nil {
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
	cfg := withDefaults(w.Config)
	worldCmd := []string{"/usr/local/bin/simenv-world", "--world", cfg.World, "--robots", strconv.Itoa(len(session.Attachments))}
	return w.Engine.Start(ctx, ContainerSpec{
		Name:    contract.WorldContainer,
		Image:   cfg.EnvImage,
		Command: worldCmd,
		Env: map[string]string{
			"GZ_IP":         contract.GZIP,
			"GZ_PARTITION":  cfg.Partition,
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

func (v EasyGoVehicle) Start(ctx context.Context, _ contract.Session, attachment contract.Attachment) error {
	cfg := withDefaults(v.Config)
	robot, err := cfg.RobotFor(attachment)
	if err != nil {
		return err
	}
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
	cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, robot.LaunchArgs()...)
	return v.Engine.Start(ctx, ContainerSpec{
		Name:    contract.AppContainer(robot.Name),
		Image:   cfg.AppImage,
		Command: cmd,
		Env:     appEnv(robot, cfg),
		Binds:   []string{hostSros + ":/sros"},
		Network: "host",
		Ipc:     "host",
		Labels:  map[string]string{contract.LabelManaged: "true", contract.LabelRole: contract.RoleApp},
	})
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
			if robot, err := contract.NewRobot(index, contract.Pose{X: 1}); err == nil {
				api = robot.APIPort
			}
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

func appEnv(robot contract.Robot, cfg contract.StartConfig) map[string]string {
	return map[string]string{
		"ROS_DOMAIN_ID":                 strconv.Itoa(robot.ROSDomainID),
		"ROS_AUTOMATIC_DISCOVERY_RANGE": "LOCALHOST",
		"RMW_IMPLEMENTATION":            "rmw_cyclonedds_cpp",
		"CYCLONEDDS_URI":                contract.CycloneDDSURI(),
		"GZ_IP":                         contract.GZIP,
		"GZ_PARTITION":                  cfg.Partition,
		"SERIAL_NUMBER":                 robot.Name,
		"MQTT_BROKER_PORT":              strconv.Itoa(robot.MQTTPort),
		"BACKEND_PORT":                  strconv.Itoa(robot.VoicePort),
		"PARAM_DB_PATH":                 robot.RuntimeRoot + "/db/cfg.db3",
		"DATABASE_URL":                  "sqlite://" + robot.RuntimeRoot + "/db/easygo.db?mode=rwc",
		"EASYGO_SIM_WORLD":              cfg.World,
		"EASYGO_SIM_APP_ROOT":           robot.RuntimeRoot + "/easygo-app",
		"EASYGO_WEBAPP_DIR":             robot.RuntimeRoot + "/webapp",
	}
}
