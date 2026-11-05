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
	Robots    int
	World     string
	AppImage  string
	EnvImage  string
	Partition string
	DataRoot  string
	// DataVolumePrefix 非空时每辆车一个 named volume（<prefix>-<robotName>:/sros），
	// 代替宿主机 data-root 绑定。用于 simenv 进程与 Docker daemon 不共享文件系统
	// 的环境（CI runner 容器）：bind 源由 daemon 自管，不解析宿主机路径。
	// 该形态只支持单车——车辆身份由 launch 的 SERIAL_NUMBER 播种，多车需要的
	// per-robot registry 补丁文件无法经 volume 子路径注入。
	DataVolumePrefix string
	Poses            []Pose
	RvizRobots       []int
	GzGuiRobots      []int
	CameraRobots     []int
}

// WantsRviz reports whether the given robot index was asked to run RViz.
func (c StartConfig) WantsRviz(index int) bool {
	return containsIndex(c.RvizRobots, index)
}

// WantsGzGui reports whether the given robot index was asked to run the Gazebo GUI.
func (c StartConfig) WantsGzGui(index int) bool {
	return containsIndex(c.GzGuiRobots, index)
}

// WantsCameras reports whether the given robot index was asked to keep its
// camera sensors.
func (c StartConfig) WantsCameras(index int) bool {
	return containsIndex(c.CameraRobots, index)
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
	if err := validateViewerRobots("cameras", c.CameraRobots, c.Robots); err != nil {
		return err
	}
	if strings.TrimSpace(c.DataVolumePrefix) != "" && c.Robots != 1 {
		return fmt.Errorf("data-volume-prefix supports exactly one robot, got %d", c.Robots)
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
	if err := validateSafeToken("session world", s.World); err != nil {
		return err
	}
	if strings.TrimSpace(s.Partition) == "" {
		return fmt.Errorf("session partition must not be empty")
	}
	if err := validateSafeToken("session partition", s.Partition); err != nil {
		return err
	}
	if len(s.Robots) < 1 {
		return fmt.Errorf("session robots must not be empty")
	}
	indices := make(map[int]struct{}, len(s.Robots))
	names := make(map[string]struct{}, len(s.Robots))
	topicPrefixes := make(map[string]struct{}, len(s.Robots))
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
		if err := validateTopicPrefix(robot.TopicPrefix); err != nil {
			return err
		}
		if _, ok := topicPrefixes[robot.TopicPrefix]; ok {
			return fmt.Errorf("duplicate robot topic prefix %q", robot.TopicPrefix)
		}
		topicPrefixes[robot.TopicPrefix] = struct{}{}
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

func validateTopicPrefix(value string) error {
	if value == "" {
		return fmt.Errorf("robot topic prefix must not be empty")
	}
	if strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("robot topic prefix %q is not canonical", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if err := validateSafeToken("robot topic prefix", segment); err != nil {
			return err
		}
	}
	return nil
}

func isSafeTokenChar(r rune) bool {
	return r == '_' || r == '-' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

func AppContainer(name string) string {
	return "simenv-" + name
}
