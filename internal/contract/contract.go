package contract

import (
	"fmt"
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

// StartConfig 是一次会话的全部框架输入：世界名、车数、可选的逐车位姿。
// 镜像、数据目录、viewer 等实现参数是各 Port 的 Options，不进契约。
type StartConfig struct {
	World  string
	Robots int
	Poses  []Pose
}

func RobotName(index int) string {
	return fmt.Sprintf("robot%d", index)
}

func NewRobot(index int, pose Pose) (Robot, error) {
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
	if strings.TrimSpace(c.World) == "" {
		return fmt.Errorf("world must not be empty")
	}
	if c.Robots < 1 {
		return fmt.Errorf("robots must be >= 1")
	}
	if len(c.Poses) > 0 && len(c.Poses) != c.Robots {
		return fmt.Errorf("poses count %d does not match robots %d", len(c.Poses), c.Robots)
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
	if err := validateSafeToken("session world", s.World); err != nil {
		return err
	}
	if len(s.Robots) < 1 {
		return fmt.Errorf("session robots must not be empty")
	}
	indices := make(map[int]struct{}, len(s.Robots))
	names := make(map[string]struct{}, len(s.Robots))
	for _, robot := range s.Robots {
		if robot.Index < 1 {
			return fmt.Errorf("robot index must be >= 1")
		}
		if _, ok := indices[robot.Index]; ok {
			return fmt.Errorf("duplicate robot index %d", robot.Index)
		}
		indices[robot.Index] = struct{}{}
		if err := validateSafeToken("robot name", robot.Name); err != nil {
			return err
		}
		if _, ok := names[robot.Name]; ok {
			return fmt.Errorf("duplicate robot name %q", robot.Name)
		}
		names[robot.Name] = struct{}{}
	}
	return nil
}

func validateSafeToken(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
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
