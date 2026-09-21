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

type Orchestrator struct {
	Engine Engine
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
}

func (o Orchestrator) Start(ctx context.Context, cfg contract.StartConfig) (Status, error) {
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
	if err := cfg.Validate(); err != nil {
		return Status{}, err
	}
	robots, err := cfg.RobotsSpec()
	if err != nil {
		return Status{}, err
	}
	dataRoot := cfg.DataRoot
	if dataRoot == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return Status{}, err
		}
		dataRoot = filepath.Join(cache, "simenv")
	}
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return Status{}, err
	}
	worldCmd := []string{"/usr/local/bin/simenv-world", "--world", cfg.World, "--robots", strconv.Itoa(cfg.Robots)}
	if err := o.Engine.Start(ctx, ContainerSpec{
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
	}); err != nil {
		return Status{}, err
	}
	if _, err := o.waitReady(ctx); err != nil {
		return Status{}, err
	}
	for _, robot := range robots {
		hostSros := filepath.Join(dataRoot, robot.Name)
		if err := os.MkdirAll(hostSros, 0o755); err != nil {
			return Status{}, err
		}
		cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, robot.LaunchArgs()...)
		if err := o.Engine.Start(ctx, ContainerSpec{
			Name:    contract.AppContainer(robot.Name),
			Image:   cfg.AppImage,
			Command: cmd,
			Env:     appEnv(robot, cfg),
			Binds:   []string{hostSros + ":/sros"},
			Network: "host",
			Ipc:     "host",
			Labels:  map[string]string{contract.LabelManaged: "true", contract.LabelRole: contract.RoleApp},
		}); err != nil {
			return Status{}, err
		}
	}
	return o.Status(ctx)
}

func (o Orchestrator) Stop(ctx context.Context) error {
	infos, err := o.Engine.List(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, info := range infos {
		if err := o.Engine.Stop(ctx, info.Name); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (o Orchestrator) Status(ctx context.Context) (Status, error) {
	worldInfo, err := o.Engine.Inspect(ctx, contract.WorldContainer)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Status{}, err
	}
	ready, _ := o.readReady(ctx)
	infos, err := o.Engine.List(ctx)
	if err != nil {
		return Status{}, err
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
	connected := worldInfo.Running && ready.Clock && len(ready.Robots) > 0
	return Status{
		World:     worldInfo,
		Apps:      apps,
		Ready:     ready,
		EnvImage:  worldInfo.Image,
		Connected: connected,
	}, nil
}

func (o Orchestrator) waitReady(ctx context.Context) (world.ReadyReport, error) {
	sleep := o.Sleep
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
		report, err := o.readReady(ctx)
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

func (o Orchestrator) readReady(ctx context.Context) (world.ReadyReport, error) {
	out, err := o.Engine.Exec(ctx, contract.WorldContainer, []string{"cat", contract.WorldReadyPath})
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
