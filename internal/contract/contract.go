package contract

import (
	"fmt"
	"strings"
)

const (
	DefaultPartition = "easygo_sim"
	GZIP             = "127.0.0.1"
	EnvROSDomainID   = 1
	HTTPBaseFrontend = 18088
	HTTPBaseAPI      = 18089
	HTTPBaseVoice    = 18091
	HTTPStride       = 100
	MQTTBase         = 1883
	ObserveBase      = 8090
	WebGZWebBase     = 6080
	WebGZRFBBase     = 5901
	RVizWebBase      = 6081
	RVizRFBBase      = 5902
	ROSDomainBase    = 11
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
	Index        int
	Name         string
	Pose         Pose
	FrontendPort int
	APIPort      int
	VoicePort    int
	MQTTPort     int
	ObservePort  int
	WebGZWebPort int
	WebGZRFBPort int
	RVizWebPort  int
	RVizRFBPort  int
	ROSDomainID  int
	RuntimeRoot  string
}

type StartConfig struct {
	Robots    int
	World     string
	AppImage  string
	EnvImage  string
	Partition string
	DataRoot  string
	Poses     []Pose
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
		Index:        index,
		Name:         RobotName(index),
		Pose:         pose,
		FrontendPort: HTTPBaseFrontend + index*HTTPStride,
		APIPort:      HTTPBaseAPI + index*HTTPStride,
		VoicePort:    HTTPBaseVoice + index*HTTPStride,
		MQTTPort:     MQTTBase + index,
		ObservePort:  ObserveBase + index*HTTPStride,
		WebGZWebPort: WebGZWebBase + index*HTTPStride,
		WebGZRFBPort: WebGZRFBBase + index*HTTPStride,
		RVizWebPort:  RVizWebBase + index*HTTPStride,
		RVizRFBPort:  RVizRFBBase + index*HTTPStride,
		ROSDomainID:  ROSDomainBase + (index - 1),
		RuntimeRoot:  "/sros",
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
	}
	return nil
}

func (r Robot) LaunchArgs() []string {
	return []string{
		"start_gazebo:=false",
		"use_gz_gui:=false",
		"use_rviz:=false",
		"enable_person_follow:=false",
		"enable_multi_robot_wifi:=true",
		"robot_name:=" + r.Name,
		"runtime_root:=" + r.RuntimeRoot,
		"map_path:=" + r.RuntimeRoot + "/map/",
		fmt.Sprintf("frontend_port:=%d", r.FrontendPort),
		fmt.Sprintf("api_port:=%d", r.APIPort),
		fmt.Sprintf("voice_port:=%d", r.VoicePort),
		fmt.Sprintf("mqtt_port:=%d", r.MQTTPort),
		fmt.Sprintf("slam_web_port:=%d", slamWebPort(r.Index)),
		fmt.Sprintf("observe_port:=%d", r.ObservePort),
		fmt.Sprintf("webgz_web_port:=%d", r.WebGZWebPort),
		fmt.Sprintf("webgz_rfb_port:=%d", r.WebGZRFBPort),
		fmt.Sprintf("webgz_display:=:%d", 10+r.Index*10),
		"webgz_log_dir:=/tmp/webgz-" + r.Name,
		fmt.Sprintf("rviz_web_port:=%d", r.RVizWebPort),
		fmt.Sprintf("rviz_rfb_port:=%d", r.RVizRFBPort),
		fmt.Sprintf("rviz_display:=:%d", 11+r.Index*10),
		"rviz_log_dir:=/tmp/rviz-" + r.Name,
		"serial_number:=" + r.Name,
		"easygo_app_param_db_path:=" + r.RuntimeRoot + "/db/cfg.db3",
		"database_url:=sqlite://" + r.RuntimeRoot + "/db/easygo.db?mode=rwc",
	}
}

func slamWebPort(index int) int {
	if index == 1 {
		return 8088
	}
	return 8018 + (index-2)*10
}

func AppContainer(name string) string {
	return "simenv-" + name
}

func CycloneDDSURI() string {
	return `<CycloneDDS><Domain><General><Interfaces><NetworkInterface name="lo" multicast="false" /></Interfaces><AllowMulticast>false</AllowMulticast><DontRoute>true</DontRoute></General><Discovery><Peers><Peer Address="127.0.0.1" /></Peers><ParticipantIndex>auto</ParticipantIndex><MaxAutoParticipantIndex>255</MaxAutoParticipantIndex></Discovery></Domain></CycloneDDS>`
}
