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
	Name      string
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
	// Kind 是世界种类：bundle 按它取 worlds.<kind> 节，会话标记按它路由。
	Kind() string
	// Start 拉起世界，并返回每台车的挂接信息（与 session.Robots 等长同序）。
	// section 是 bundle 里该世界种类的原始节（裸世界为 nil），认不出就报错。
	Start(ctx context.Context, session contract.Session, section assets.Section) ([]Attachment, error)
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

// NewWorldFunc 按种类构造世界 Port；构造失败（例如它依赖的运行时不可用）
// 返回错误。NewVehicleFactoryFunc 同理。
type NewWorldFunc func(cfg contract.StartConfig) (WorldPort, error)
type NewVehicleFactoryFunc func(cfg contract.StartConfig) (VehiclePortFactory, error)

// Orchestrator 是编排框架：它只认识接口。世界与车端的具体实现由组合根
// 按种类注册进 Worlds / Vehicles；start 时种类由 bundle 节的唯一键决定
// （裸世界由 --world-kind / --vehicle-kind 显式给），start 之后的动词按
// 会话标记路由到当时用的种类。测试用 World / VehicleFactory 直接给假实现，
// 注入即绕过注册表与标记。
type Orchestrator struct {
	World          WorldPort
	VehicleFactory VehiclePortFactory
	Worlds         map[string]NewWorldFunc
	Vehicles       map[string]NewVehicleFactoryFunc
	assetsDir      string
}

// usingRegistry 区分两条路：组合根的注册表（生产），或直接注入的假实现（测试）。
func (o Orchestrator) usingRegistry() bool {
	return o.World == nil && o.VehicleFactory == nil
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
	if o.usingRegistry() {
		records, err := loadSessions()
		if err != nil {
			return Status{}, err
		}
		if cfg.SessionName == "" {
			cfg.SessionName, err = randomSessionName(records)
			if err != nil {
				return Status{}, err
			}
		}
		// 同名会话会被这次启动替换，槽位按替换之后的名单分配，好让它沿用自己的槽。
		cfg.Slot = assignSlot(exceptSession(records, cfg.SessionName))
	} else if cfg.SessionName == "" {
		cfg.SessionName, err = randomSessionName(nil)
		if err != nil {
			return Status{}, err
		}
	}
	// 种类决定：bundle 节的唯一键是单一来源，显式 kind 只在裸世界时必需、
	// 给了就必须与 bundle 一致。选完才构造对应 Port——未选中的实现连构造
	// 都不发生（它的运行时不可用不该拖垮别的种类的会话）。
	worldPort, err := o.worldPortForStart(cfg, bundle)
	if err != nil {
		return Status{}, err
	}
	vehicleFactory, err := o.vehicleFactoryForStart(cfg, bundle)
	if err != nil {
		return Status{}, err
	}
	// 两个种类的节在碰任何运行态之前取好：缺节即失败，且不先杀掉旧会话。
	var worldSection, vehicleSection assets.Section
	if bundle != nil {
		worldSection, err = bundle.WorldSection(worldPort.Kind())
		if err != nil {
			return Status{}, err
		}
		vehicleSection, err = bundle.VehicleSection(vehicleFactory.Kind())
		if err != nil {
			return Status{}, err
		}
	}
	// 同名会话先停掉再开，其它名字留着。注入路径（测试）停掉工厂发现的全部车。
	if o.usingRegistry() {
		records, err := loadSessions()
		if err != nil {
			return Status{}, err
		}
		if _, ok := findSession(records, cfg.SessionName); ok {
			if err := o.stopSession(ctx, cfg.SessionName); err != nil {
				return Status{}, err
			}
		}
	} else {
		existingVehicles, err := vehicleFactory.Discover(ctx)
		if err != nil {
			return Status{}, err
		}
		if err := stopVehiclePorts(ctx, existingVehicles); err != nil {
			return Status{}, err
		}
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
	attachments, err := worldPort.Start(ctx, session, worldSection)
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
	for i, robot := range session.Robots {
		vehiclePort, err := vehicleFactory.New(ctx, session, robot, attachments[i], vehicleSection)
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
	if o.usingRegistry() {
		if err := upsertSession(sessionRecord{
			Name:        cfg.SessionName,
			WorldKind:   worldPort.Kind(),
			VehicleKind: vehicleFactory.Kind(),
			Slot:        cfg.Slot,
			StartedUnix: time.Now().UnixNano(),
		}); err != nil {
			return Status{}, errors.Join(err, rollback())
		}
	}
	status.Name = cfg.SessionName
	return status, nil
}

func (o Orchestrator) Stop(ctx context.Context) error {
	return o.StopSession(ctx, "")
}

// StopSession 停掉指名的会话。名字空着时停最近启动的那一个；没有会话则直接成功。
func (o Orchestrator) StopSession(ctx context.Context, name string) error {
	if !o.usingRegistry() {
		return o.stopInjected(ctx)
	}
	records, err := loadSessions()
	if err != nil {
		return err
	}
	if name == "" {
		rec, ok := latestSession(records)
		if !ok {
			return nil
		}
		name = rec.Name
	}
	if _, ok := findSession(records, name); !ok {
		return fmt.Errorf("no session %q", name)
	}
	return o.stopSession(ctx, name)
}

func (o Orchestrator) stopInjected(ctx context.Context) error {
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
	if o.usingRegistry() {
		return o.markedStatus(ctx)
	}
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

// SessionSummary 是 status 不点名时的一行：名字、种类、是否接通。
type SessionSummary struct {
	Name        string
	WorldKind   string
	VehicleKind string
	Connected   bool
}

// Sessions 列出全部进行中的会话。注入路径没有名单，按当前这一次端口报告一行。
func (o Orchestrator) Sessions(ctx context.Context) ([]SessionSummary, error) {
	if !o.usingRegistry() {
		status, err := o.Status(ctx)
		if err != nil {
			return nil, err
		}
		if !status.World.Present && len(status.Vehicles) == 0 {
			return nil, nil
		}
		return []SessionSummary{{
			Name:      status.Name,
			Connected: status.Connected,
		}}, nil
	}
	records, err := loadSessions()
	if err != nil {
		return nil, err
	}
	summaries := make([]SessionSummary, 0, len(records))
	for _, rec := range records {
		status, err := o.statusRecord(ctx, rec)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, SessionSummary{
			Name:        rec.Name,
			WorldKind:   rec.WorldKind,
			VehicleKind: rec.VehicleKind,
			Connected:   status.Connected,
		})
	}
	return summaries, nil
}

// StatusSession 报告指名会话的详情。名字空着时用最近启动的那一个；
// 没有会话则是空状态，不构造任何实现。
func (o Orchestrator) StatusSession(ctx context.Context, name string) (Status, error) {
	if !o.usingRegistry() {
		return o.Status(ctx)
	}
	records, err := loadSessions()
	if err != nil {
		return Status{}, err
	}
	rec, ok, err := pickSession(records, name)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{}, nil
	}
	status, err := o.statusRecord(ctx, rec)
	if err != nil {
		return Status{}, err
	}
	status.Name = rec.Name
	return status, nil
}

func (o Orchestrator) statusRecord(ctx context.Context, rec sessionRecord) (Status, error) {
	cfg := portConfig(rec)
	worldPort, err := o.newWorld(rec.WorldKind, cfg)
	if err != nil {
		return Status{}, err
	}
	vehicleFactory, err := o.newVehicleFactory(rec.VehicleKind, cfg)
	if err != nil {
		return Status{}, err
	}
	vehiclePorts, err := vehicleFactory.Discover(ctx)
	if err != nil {
		return Status{}, err
	}
	return statusFromPorts(ctx, worldPort, vehiclePorts)
}

// markedStatus 是注册表路径的 status：最近一次会话。没有名单就是没有进行中的会话。
func (o Orchestrator) markedStatus(ctx context.Context) (Status, error) {
	return o.StatusSession(ctx, "")
}

// stopSession 停掉指名会话。全部停干净才从名单摘掉；停不干净记录留着，好让下一次重试。
func (o Orchestrator) stopSession(ctx context.Context, name string) error {
	records, err := loadSessions()
	if err != nil {
		return err
	}
	rec, ok := findSession(records, name)
	if !ok {
		return fmt.Errorf("no session %q", name)
	}
	cfg := portConfig(rec)
	var stopErr error
	if vehicleFactory, ferr := o.newVehicleFactory(rec.VehicleKind, cfg); ferr != nil {
		stopErr = errors.Join(stopErr, ferr)
	} else if vehiclePorts, derr := vehicleFactory.Discover(ctx); derr != nil {
		stopErr = errors.Join(stopErr, derr)
	} else {
		stopErr = errors.Join(stopErr, stopVehiclePorts(ctx, vehiclePorts))
	}
	if worldPort, werr := o.newWorld(rec.WorldKind, cfg); werr != nil {
		stopErr = errors.Join(stopErr, werr)
	} else {
		stopErr = errors.Join(stopErr, worldPort.Stop(ctx))
	}
	if stopErr != nil {
		return stopErr
	}
	return deleteSession(name)
}

func portConfig(rec sessionRecord) contract.StartConfig {
	return contract.StartConfig{SessionName: rec.Name, Slot: rec.Slot}
}

// pickSession 按名字取一条。名字空着时取最近启动的；名单空则 ok=false，不算错误。
// 点了名却没有这条，是错误。
func pickSession(records []sessionRecord, name string) (sessionRecord, bool, error) {
	if name == "" {
		rec, ok := latestSession(records)
		return rec, ok, nil
	}
	rec, ok := findSession(records, name)
	if !ok {
		return sessionRecord{}, false, fmt.Errorf("no session %q", name)
	}
	return rec, true, nil
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
	return nil, fmt.Errorf("world port is not configured")
}

func (o Orchestrator) vehicleFactory(cfg contract.StartConfig) (VehiclePortFactory, error) {
	if o.VehicleFactory != nil {
		return o.VehicleFactory, nil
	}
	return nil, fmt.Errorf("vehicle factory is not configured")
}

// worldPortForStart 决定 start 用哪个世界实现并构造它：注入优先（测试）；
// 否则种类由 bundle worlds 节唯一键决定，显式 kind 与其一致性校验后生效。
func (o Orchestrator) worldPortForStart(cfg contract.StartConfig, bundle *assets.Bundle) (WorldPort, error) {
	if o.World != nil {
		return o.World, nil
	}
	bundleKinds := []string(nil)
	if bundle != nil {
		for kind := range bundle.Worlds {
			bundleKinds = append(bundleKinds, kind)
		}
	}
	kind, err := resolveKind("world", "--world-kind", bundleKinds, cfg.WorldKind)
	if err != nil {
		return nil, err
	}
	return o.newWorld(kind, cfg)
}

func (o Orchestrator) vehicleFactoryForStart(cfg contract.StartConfig, bundle *assets.Bundle) (VehiclePortFactory, error) {
	if o.VehicleFactory != nil {
		return o.VehicleFactory, nil
	}
	bundleKinds := []string(nil)
	if bundle != nil {
		for kind := range bundle.Vehicles {
			bundleKinds = append(bundleKinds, kind)
		}
	}
	kind, err := resolveKind("vehicle", "--vehicle-kind", bundleKinds, cfg.VehicleKind)
	if err != nil {
		return nil, err
	}
	return o.newVehicleFactory(kind, cfg)
}

// resolveKind 是种类决定的单一规则：bundle 有节时节必须恰好一个、它是单一
// 来源；裸世界（无 bundle）必须显式给 kind；两个来源都有却不一致直接失败。
func resolveKind(what, flagName string, bundleKinds []string, explicit string) (string, error) {
	switch len(bundleKinds) {
	case 0:
		if explicit == "" {
			return "", fmt.Errorf("world ships no bundle: %s is required to pick a %s implementation", flagName, what)
		}
		return explicit, nil
	case 1:
		if explicit != "" && explicit != bundleKinds[0] {
			return "", fmt.Errorf("%s %q does not match the bundle's %s kind %q", flagName, explicit, what, bundleKinds[0])
		}
		return bundleKinds[0], nil
	default:
		return "", fmt.Errorf("bundle declares multiple %s kinds %v: exactly one section is allowed", what, bundleKinds)
	}
}

func (o Orchestrator) newWorld(kind string, cfg contract.StartConfig) (WorldPort, error) {
	newWorld, ok := o.Worlds[kind]
	if !ok {
		return nil, fmt.Errorf("no world port registered for kind %q", kind)
	}
	return newWorld(cfg)
}

func (o Orchestrator) newVehicleFactory(kind string, cfg contract.StartConfig) (VehiclePortFactory, error) {
	newVehicles, ok := o.Vehicles[kind]
	if !ok {
		return nil, fmt.Errorf("no vehicle port registered for kind %q", kind)
	}
	return newVehicles(cfg)
}

func stopVehiclePorts(ctx context.Context, vehiclePorts []VehiclePort) error {
	var stopErr error
	for i := len(vehiclePorts) - 1; i >= 0; i-- {
		stopErr = errors.Join(stopErr, vehiclePorts[i].Stop(ctx))
	}
	return stopErr
}
