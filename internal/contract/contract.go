package contract

import (
	"fmt"
	"math"
	"strings"
)

const (
	SpawnXStride  = 3.0
	DefaultSpawnZ = 0.05
)

// Pose 是世界系位姿：m / rad。
type Pose struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Z   float64 `json:"z"`
	Yaw float64 `json:"yaw"`
}

func (p Pose) Validate() error {
	for name, value := range map[string]float64{"x": p.X, "y": p.Y, "z": p.Z, "yaw": p.Yaw} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("pose %s must be finite", name)
		}
	}
	return nil
}

// Session 是编排框架与世界进程之间的线上契约：只有世界名与机器人清单。
// 仿真器的接线约定（partition、topic 前缀）不进 Session——世界 Port 启动后
// 经 Attachment 把它们交给车端。
type Session struct {
	World  string  `json:"world"`
	Robots []Robot `json:"robots"`
}

type Robot struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Pose  Pose   `json:"pose"`
}

// StartConfig 是一次会话的全部框架输入：世界名、车数、可选的逐车位姿，
// 以及可选的实现种类选择。镜像、数据目录、viewer 等实现参数是各 Port 的
// Options，不进契约。
type StartConfig struct {
	World  string
	Robots int
	Poses  []Pose
	// WorldKind / VehicleKind 显式选择实现种类。世界带 bundle 时种类由
	// bundle 的 worlds / vehicles 节唯一键决定，这里给了就必须一致；
	// 裸世界（无 bundle）没有可决定的键，两个 kind 都必须显式给。
	WorldKind   string
	VehicleKind string
	// SessionName 是这次启动的会话名。空则由编排生成一个；指定了就用指定的。
	SessionName string
	// Slot 是编排分配的资源槽，从 0 起取还没占用的最小整数。调用方不填。
	Slot int
}

func RobotName(index int) string {
	return fmt.Sprintf("robot%d", index)
}

func NewRobot(index int, pose Pose) (Robot, error) {
	if err := pose.Validate(); err != nil {
		return Robot{}, err
	}
	if index < 1 {
		return Robot{}, fmt.Errorf("robot index must be >= 1")
	}
	if pose == (Pose{}) {
		pose = Pose{X: float64(index-1) * SpawnXStride, Z: DefaultSpawnZ}
	}
	return Robot{
		Index: index,
		Name:  RobotName(index),
		Pose:  pose,
	}, nil
}

func (c StartConfig) Validate() error {
	if err := ValidateName("world", c.World); err != nil {
		return err
	}
	if c.Robots < 1 {
		return fmt.Errorf("robots must be >= 1")
	}
	if len(c.Poses) > 0 && len(c.Poses) != c.Robots {
		return fmt.Errorf("poses count %d does not match robots %d", len(c.Poses), c.Robots)
	}
	for _, pose := range c.Poses {
		if err := pose.Validate(); err != nil {
			return err
		}
	}
	if c.WorldKind != "" {
		if err := ValidateName("world kind", c.WorldKind); err != nil {
			return err
		}
	}
	if c.VehicleKind != "" {
		if err := ValidateName("vehicle kind", c.VehicleKind); err != nil {
			return err
		}
	}
	if c.SessionName != "" {
		if err := ValidateName("session", c.SessionName); err != nil {
			return err
		}
	}
	return nil
}

func (c StartConfig) Session() (Session, error) {
	if err := c.Validate(); err != nil {
		return Session{}, err
	}
	robots, err := c.RobotsSpec()
	if err != nil {
		return Session{}, err
	}
	session := Session{World: c.World, Robots: robots}
	if err := session.Validate(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (c StartConfig) RobotFor(index int) (Robot, error) {
	pose, err := c.PoseFor(index)
	if err != nil {
		return Robot{}, err
	}
	return NewRobot(index, pose)
}

func (c StartConfig) PoseFor(index int) (Pose, error) {
	if err := c.Validate(); err != nil {
		return Pose{}, err
	}
	if index < 1 || index > c.Robots {
		return Pose{}, fmt.Errorf("robot index %d outside robot count %d", index, c.Robots)
	}
	if len(c.Poses) > 0 {
		return c.Poses[index-1], nil
	}
	return Pose{}, nil
}

func (c StartConfig) RobotsSpec() ([]Robot, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	robots := make([]Robot, 0, c.Robots)
	for i := 1; i <= c.Robots; i++ {
		robot, err := c.RobotFor(i)
		if err != nil {
			return nil, err
		}
		robots = append(robots, robot)
	}
	return robots, nil
}

func (s Session) Validate() error {
	if strings.TrimSpace(s.World) == "" {
		return fmt.Errorf("session world must not be empty")
	}
	if err := ValidateName("session world", s.World); err != nil {
		return err
	}
	if len(s.Robots) < 1 {
		return fmt.Errorf("session robots must not be empty")
	}
	indices := make(map[int]struct{}, len(s.Robots))
	names := make(map[string]struct{}, len(s.Robots))
	for _, robot := range s.Robots {
		if err := robot.Pose.Validate(); err != nil {
			return err
		}
		if robot.Index < 1 {
			return fmt.Errorf("robot index must be >= 1")
		}
		if _, ok := indices[robot.Index]; ok {
			return fmt.Errorf("duplicate robot index %d", robot.Index)
		}
		indices[robot.Index] = struct{}{}
		if err := ValidateName("robot name", robot.Name); err != nil {
			return err
		}
		if _, ok := names[robot.Name]; ok {
			return fmt.Errorf("duplicate robot name %q", robot.Name)
		}
		names[robot.Name] = struct{}{}
	}
	return nil
}

// ValidateName validates a name used in session routing and storage paths.
func ValidateName(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s must not begin with '-'", field)
	}
	for _, r := range value {
		if isSafeTokenChar(r) {
			continue
		}
		return fmt.Errorf("%s contains invalid character %q", field, r)
	}
	return nil
}

func isSafeTokenChar(r rune) bool {
	return r == '_' || r == '-' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
