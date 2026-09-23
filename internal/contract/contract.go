package contract

import (
	"fmt"
	"strings"
)

const (
	DefaultPartition = "easygo_sim"
	GZIP             = "127.0.0.1"
	EnvROSDomainID   = 1
	SpawnXStride     = 3.0
	DefaultSpawnZ    = 0.05
	DefaultWorld     = "simple"
	DefaultEnvImage  = "simenv-world:local"
	WorldReadyPath   = "/tmp/simenv/ready.json"
	WorldContainer   = "simenv-world"
	LabelManaged     = "simenv.managed"
	LabelRole        = "simenv.role"
	RoleWorld        = "world"
	RoleApp          = "app"
)

type Pose struct {
	X, Y, Z, Yaw float64
}

type Session struct {
	World     string
	Partition string
	Robots    []Robot
}

type Robot struct {
	Index       int
	Name        string
	TopicPrefix string
	Pose        Pose
}

type StartConfig struct {
	Robots      int
	World       string
	AppImage    string
	EnvImage    string
	Partition   string
	DataRoot    string
	Poses       []Pose
	RvizRobots  []int
	GzGuiRobots []int
}

// WantsRviz reports whether the given robot index was asked to run RViz.
func (c StartConfig) WantsRviz(index int) bool {
	return containsIndex(c.RvizRobots, index)
}

// WantsGzGui reports whether the given robot index was asked to run the Gazebo GUI.
func (c StartConfig) WantsGzGui(index int) bool {
	return containsIndex(c.GzGuiRobots, index)
}

func containsIndex(indices []int, index int) bool {
	for _, i := range indices {
		if i == index {
			return true
		}
	}
	return false
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
	name := RobotName(index)
	return Robot{
		Index:       index,
		Name:        name,
		TopicPrefix: name,
		Pose:        pose,
	}, nil
}

func (c StartConfig) Validate() error {
	if c.Robots < 1 {
		return fmt.Errorf("robots must be >= 1")
	}
	if strings.TrimSpace(c.World) == "" {
		return fmt.Errorf("world must not be empty")
	}
	if strings.TrimSpace(c.AppImage) == "" {
		return fmt.Errorf("app-image must not be empty")
	}
	if strings.TrimSpace(c.EnvImage) == "" {
		return fmt.Errorf("env-image must not be empty")
	}
	if len(c.Poses) > 0 && len(c.Poses) != c.Robots {
		return fmt.Errorf("poses count %d does not match robots %d", len(c.Poses), c.Robots)
	}
	if err := validateViewerRobots("rviz", c.RvizRobots, c.Robots); err != nil {
		return err
	}
	if err := validateViewerRobots("gz-gui", c.GzGuiRobots, c.Robots); err != nil {
		return err
	}
	return nil
}

func validateViewerRobots(flag string, indices []int, robots int) error {
	for _, index := range indices {
		if index < 1 || index > robots {
			return fmt.Errorf("%s robot %d outside robot count %d", flag, index, robots)
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
	session := Session{World: c.World, Partition: c.Partition, Robots: robots}
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
	if strings.TrimSpace(s.Partition) == "" {
		return fmt.Errorf("session partition must not be empty")
	}
	if len(s.Robots) < 1 {
		return fmt.Errorf("session robots must not be empty")
	}
	for _, robot := range s.Robots {
		if robot.Index < 1 {
			return fmt.Errorf("robot index must be >= 1")
		}
		if strings.TrimSpace(robot.Name) == "" {
			return fmt.Errorf("robot name must not be empty")
		}
		if strings.TrimSpace(robot.TopicPrefix) == "" {
			return fmt.Errorf("robot topic prefix must not be empty")
		}
	}
	return nil
}

func AppContainer(name string) string {
	return "simenv-" + name
}
