package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
)

// VehicleStatus 是一台车的状态：框架只认名字与运行态，端点是给人看的
// 地址，其余实现专属信息进 Detail，CLI 原样打印。
type VehicleStatus struct {
	Name     string
	Running  bool
	Endpoint string
	Detail   map[string]string
}

type Status struct {
	World     WorldStatus
	Vehicles  []VehicleStatus
	Connected bool
}

// Attachment 是世界 Port 给每台车的挂接信息：框架只按车转交，不解释内容。
// 车端 Port 认不出自己那种 Attachment 时在 Factory.New 直接失败。
type Attachment interface{ Kind() string }

// WorldReady 是世界 Port 报告的就绪态：世界已可用、实体已就位。
// 世界内部等什么（时钟、实体）是它自己的事，报告只讲结果。
type WorldReady struct {
	World  string
	Robots []string
}

// WorldStatus 是世界 Port 报告的完整状态；实现专属信息进 Detail，框架不解释。
type WorldStatus struct {
	Present bool
	Running bool
	Ready   WorldReady
	Detail  map[string]string
}

type WorldPort interface {
	// Start 拉起世界，并返回每台车的挂接信息（与 session.Robots 等长同序）。
	Start(ctx context.Context, session contract.Session) ([]Attachment, error)
	Ready(ctx context.Context, session contract.Session) (WorldReady, error)
	Status(ctx context.Context) (WorldStatus, error)
	Stop(ctx context.Context) error
	Pose(ctx context.Context, robot int) (EntityPose, error)
	Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error)
}

// Provisioning 是 world init 交给一台车的全部输入：站点与出生位姿，都是世界系。
// 换算成车端自己的坐标系是车端 Port 的事——换算错了是它自己报错。
type Provisioning struct {
	Stations []assets.Station
	Pose     contract.Pose
}

type VehiclePort interface {
	Start(ctx context.Context) error
	// WaitReady reports when the vehicle is actually serving: its app answered
	// on the API. Start returning nil only means the container exists, so the
	// orchestrator treats a vehicle as started once both have succeeded.
	WaitReady(ctx context.Context) error
	// Provision 把 world init 的输入一次性交给车端：地图、站点、出生位姿。
	Provision(ctx context.Context, provisioning Provisioning) error
	Status(ctx context.Context) (VehicleStatus, error)
	Stop(ctx context.Context) error
	// Forward writes request to this vehicle and returns the response bytes.
	Forward(ctx context.Context, request []byte) ([]byte, error)
}

type VehiclePortFactory interface {
	// Kind 是车端种类：bundle 按它取 vehicles.<kind> 节交给 New；缺节即失败。
	Kind() string
	// New 造一台车的 Port；attachment 是世界给的挂接信息，section 是 bundle 里
	// 该车端种类的原始节（裸世界为 nil），认不出任一者就报错。
	New(ctx context.Context, session contract.Session, robot contract.Robot, attachment Attachment, section assets.Section) (VehiclePort, error)
	Discover(ctx context.Context) ([]VehiclePort, error)
}

// Orchestrator 是编排框架：它只认识接口。世界与车端的具体实现由组合根
// 经 NewWorld / NewVehicles 注入；测试用 World / VehicleFactory 直接给假实现。
type Orchestrator struct {
	World          WorldPort
	NewWorld       func(cfg contract.StartConfig) WorldPort
	VehicleFactory VehiclePortFactory
	NewVehicles    func(cfg contract.StartConfig) VehiclePortFactory
	assetsDir      string
}

func (o Orchestrator) assetsPath() string {
	return assets.Path(o.assetsDir)
}

func (o Orchestrator) Start(ctx context.Context, cfg contract.StartConfig) (Status, error) {
	if err := cfg.Validate(); err != nil {
		return Status{}, err
	}
	// 世界 bundle 决定初始位姿：生成（世界进程 spawn）与定位必须用同一份，
	// 否则会出现"车在东端待避区、却被告知你在西端"这种自相矛盾的启动。
	bundle, err := o.loadBundle(cfg.World)
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
	worldPort, err := o.worldPort(cfg)
	if err != nil {
		return Status{}, err
	}
	vehicleFactory, err := o.vehicleFactory(cfg)
	if err != nil {
		return Status{}, err
	}
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
	attachments, err := worldPort.Start(ctx, session)
	if err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	if len(attachments) != len(session.Robots) {
		return Status{}, errors.Join(
			fmt.Errorf("world returned %d attachments for %d robots", len(attachments), len(session.Robots)),
			rollback(),
		)
	}
	if _, err := worldPort.Ready(ctx, session); err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	var section assets.Section
	if bundle != nil {
		section, err = bundle.VehicleSection(vehicleFactory.Kind())
		if err != nil {
			return Status{}, errors.Join(err, rollback())
		}
	}
	for i, robot := range session.Robots {
		vehiclePort, err := vehicleFactory.New(ctx, session, robot, attachments[i], section)
		if err != nil {
			return Status{}, errors.Join(err, rollback())
		}
		vehiclePorts = append(vehiclePorts, vehiclePort)
		if err := vehiclePort.Start(ctx); err != nil {
			return Status{}, errors.Join(err, rollback())
		}
		if err := vehiclePort.WaitReady(ctx); err != nil {
			return Status{}, errors.Join(err, rollback())
		}
	}
	// world init：bundle → 每台车一次 Provision。任一步不满足即失败退出。
	if err := o.worldInit(ctx, session, vehiclePorts, bundle); err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	status, err := statusFromPorts(ctx, worldPort, vehiclePorts)
	if err != nil {
		return Status{}, errors.Join(err, rollback())
	}
	return status, nil
}

func (o Orchestrator) Stop(ctx context.Context) error {
	worldPort, worldErr := o.worldPort(contract.StartConfig{})
	vehicleFactory, factoryErr := o.vehicleFactory(contract.StartConfig{})
	var vehiclePorts []VehiclePort
	var discoverErr error
	if factoryErr == nil {
		vehiclePorts, discoverErr = vehicleFactory.Discover(ctx)
	}
	stopErr := stopVehiclePorts(ctx, vehiclePorts)
	if worldErr != nil {
		return errors.Join(worldErr, factoryErr, discoverErr, stopErr)
	}
	return errors.Join(
		factoryErr,
		discoverErr,
		stopErr,
		worldPort.Stop(ctx),
	)
}

func (o Orchestrator) Status(ctx context.Context) (Status, error) {
	worldPort, err := o.worldPort(contract.StartConfig{})
	if err != nil {
		return Status{}, err
	}
	vehicleFactory, err := o.vehicleFactory(contract.StartConfig{})
	if err != nil {
		return Status{}, err
	}
	vehiclePorts, err := vehicleFactory.Discover(ctx)
	if err != nil {
		return Status{}, err
	}
	return statusFromPorts(ctx, worldPort, vehiclePorts)
}

func statusFromPorts(ctx context.Context, worldPort WorldPort, vehiclePorts []VehiclePort) (Status, error) {
	worldStatus, err := worldPort.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	vehicles := make([]VehicleStatus, 0, len(vehiclePorts))
	for _, vehiclePort := range vehiclePorts {
		vehicle, err := vehiclePort.Status(ctx)
		if err != nil {
			return Status{}, err
		}
		vehicles = append(vehicles, vehicle)
	}
	return Status{
		World:     worldStatus,
		Vehicles:  vehicles,
		Connected: sessionConnected(worldStatus, vehicles),
	}, nil
}

func sessionConnected(world WorldStatus, vehicles []VehicleStatus) bool {
	if !world.Running || len(world.Ready.Robots) == 0 || len(vehicles) != len(world.Ready.Robots) {
		return false
	}
	expected := make(map[string]struct{}, len(world.Ready.Robots))
	for _, name := range world.Ready.Robots {
		expected[name] = struct{}{}
	}
	if len(expected) != len(world.Ready.Robots) {
		return false
	}
	for _, vehicle := range vehicles {
		if !vehicle.Running {
			return false
		}
		if _, ok := expected[vehicle.Name]; !ok {
			return false
		}
		delete(expected, vehicle.Name)
	}
	return len(expected) == 0
}

func (o Orchestrator) worldPort(cfg contract.StartConfig) (WorldPort, error) {
	if o.World != nil {
		return o.World, nil
	}
	if o.NewWorld != nil {
		return o.NewWorld(cfg), nil
	}
	return nil, fmt.Errorf("world port is not configured")
}

func (o Orchestrator) vehicleFactory(cfg contract.StartConfig) (VehiclePortFactory, error) {
	if o.VehicleFactory != nil {
		return o.VehicleFactory, nil
	}
	if o.NewVehicles != nil {
		return o.NewVehicles(cfg), nil
	}
	return nil, fmt.Errorf("vehicle factory is not configured")
}

func stopVehiclePorts(ctx context.Context, vehiclePorts []VehiclePort) error {
	var stopErr error
	for i := len(vehiclePorts) - 1; i >= 0; i-- {
		stopErr = errors.Join(stopErr, vehiclePorts[i].Stop(ctx))
	}
	return stopErr
}
