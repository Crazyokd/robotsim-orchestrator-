package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRobotName(t *testing.T) {
	if got := RobotName(1); got != "robot1" {
		t.Fatalf("RobotName(1)=%q", got)
	}
}

func TestRobotRejectsNonPositiveIndex(t *testing.T) {
	if _, err := NewRobot(0, Pose{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRobot1UsesNeutralDefaults(t *testing.T) {
	robot, err := NewRobot(1, Pose{})
	if err != nil {
		t.Fatal(err)
	}
	if robot.Name != "robot1" || robot.TopicPrefix != "robot1" {
		t.Fatalf("robot=%+v", robot)
	}
	if robot.Pose != (Pose{X: 0, Y: 0, Z: 0.05, Yaw: 0}) {
		t.Fatalf("default pose=%+v", robot.Pose)
	}
}

func TestRobot2Stride(t *testing.T) {
	robot, err := NewRobot(2, Pose{})
	if err != nil {
		t.Fatal(err)
	}
	if robot.Pose.X != 3 {
		t.Fatalf("x=%v", robot.Pose.X)
	}
}

func TestExplicitPoseWins(t *testing.T) {
	pose := Pose{X: 1.5, Y: 2, Z: 0.1, Yaw: 1.57}
	robot, err := NewRobot(1, pose)
	if err != nil {
		t.Fatal(err)
	}
	if robot.Pose != pose {
		t.Fatalf("pose=%+v", robot.Pose)
	}
}

func TestDefaults(t *testing.T) {
	if DefaultPartition != "easygo_sim" {
		t.Fatalf("partition=%q", DefaultPartition)
	}
	if GZIP != "127.0.0.1" {
		t.Fatalf("gz ip=%q", GZIP)
	}
	if EnvROSDomainID != 1 {
		t.Fatalf("env domain=%d", EnvROSDomainID)
	}
}

func TestStartConfigRejectsEmptyAppImage(t *testing.T) {
	err := StartConfig{Robots: 1, World: "simple", AppImage: "", EnvImage: "simenv-world:local"}.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartConfigRejectsZeroRobots(t *testing.T) {
	err := StartConfig{Robots: 0, World: "simple", AppImage: "img", EnvImage: "env"}.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartConfigBuildsNeutralSession(t *testing.T) {
	session, err := StartConfig{
		Robots:    2,
		World:     "simple",
		AppImage:  "img",
		EnvImage:  "env",
		Partition: DefaultPartition,
		Poses: []Pose{
			{X: 10, Z: DefaultSpawnZ},
			{X: 20, Z: DefaultSpawnZ},
		},
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	if session.World != "simple" || session.Partition != DefaultPartition {
		t.Fatalf("session=%+v", session)
	}
	if len(session.Robots) != 2 {
		t.Fatalf("robots=%+v", session.Robots)
	}
	if session.Robots[0].Name != "robot1" || session.Robots[0].TopicPrefix != "robot1" || session.Robots[0].Pose.X != 10 {
		t.Fatalf("robot1=%+v", session.Robots[0])
	}
	if session.Robots[1].Name != "robot2" || session.Robots[1].TopicPrefix != "robot2" || session.Robots[1].Pose.X != 20 {
		t.Fatalf("robot2=%+v", session.Robots[1])
	}
}

func TestCameraSelectionStaysOutsideSession(t *testing.T) {
	session, err := StartConfig{
		Robots:       3,
		World:        "simple",
		AppImage:     "img",
		EnvImage:     "env",
		Partition:    DefaultPartition,
		CameraRobots: []int{2},
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Cameras") {
		t.Fatalf("session must not carry camera selection: %s", raw)
	}
	cfg := StartConfig{Robots: 2, World: "w", AppImage: "a", EnvImage: "e", CameraRobots: []int{3}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("camera robot 3 must fail with 2 robots")
	}
}

func TestSessionJSONDoesNotExposeEasyGoRuntime(t *testing.T) {
	session, err := StartConfig{
		Robots: 2, World: "simple", AppImage: "img", EnvImage: "env", Partition: DefaultPartition,
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, field := range []string{
		"APIPort",
		"FrontendPort",
		"VoicePort",
		"MQTTPort",
		"ROSDomainID",
		"RuntimeRoot",
	} {
		if strings.Contains(body, field) {
			t.Fatalf("session JSON leaked %s: %s", field, body)
		}
	}
}

func TestStartConfigRejectsViewerRobotOutsideRange(t *testing.T) {
	base := StartConfig{Robots: 2, World: "simple", AppImage: "img", EnvImage: "env"}
	cfg := base
	cfg.RvizRobots = []int{3}
	if err := cfg.Validate(); err == nil {
		t.Fatal("rviz robot 3 must fail with 2 robots")
	}
	cfg = base
	cfg.GzGuiRobots = []int{0}
	if err := cfg.Validate(); err == nil {
		t.Fatal("gz-gui robot 0 must fail")
	}
	cfg = base
	cfg.RvizRobots = []int{1}
	cfg.GzGuiRobots = []int{2}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStartConfigViewerQueries(t *testing.T) {
	cfg := StartConfig{RvizRobots: []int{1, 3}, GzGuiRobots: []int{2}}
	if !cfg.WantsRviz(1) || !cfg.WantsRviz(3) || cfg.WantsRviz(2) {
		t.Fatal("WantsRviz mismatch")
	}
	if !cfg.WantsGzGui(2) || cfg.WantsGzGui(1) {
		t.Fatal("WantsGzGui mismatch")
	}
}

func TestSessionRejectsDuplicateRobotIdentity(t *testing.T) {
	base := Session{
		World:     "simple",
		Partition: DefaultPartition,
		Robots: []Robot{
			{Index: 1, Name: "robot1", TopicPrefix: "robot1"},
			{Index: 2, Name: "robot2", TopicPrefix: "robot2"},
		},
	}
	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{"index", func(s *Session) { s.Robots[1].Index = s.Robots[0].Index }},
		{"name", func(s *Session) { s.Robots[1].Name = s.Robots[0].Name }},
		{"topic prefix", func(s *Session) { s.Robots[1].TopicPrefix = s.Robots[0].TopicPrefix }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := base
			session.Robots = append([]Robot(nil), base.Robots...)
			test.mutate(&session)
			if err := session.Validate(); err == nil {
				t.Fatalf("expected duplicate %s to fail", test.name)
			}
		})
	}
}

func TestSessionRejectsUnsafeNames(t *testing.T) {
	base := Session{
		World:     "simple",
		Partition: DefaultPartition,
		Robots:    []Robot{{Index: 1, Name: "robot1", TopicPrefix: "robot1"}},
	}
	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{"world path", func(s *Session) { s.World = "../simple" }},
		{"robot path", func(s *Session) { s.Robots[0].Name = "../robot1" }},
		{"robot quote", func(s *Session) { s.Robots[0].Name = `robot"1` }},
		{"topic whitespace", func(s *Session) { s.Robots[0].TopicPrefix = "fleet/robot 1" }},
		{"topic empty segment", func(s *Session) { s.Robots[0].TopicPrefix = "fleet//robot1" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := base
			session.Robots = append([]Robot(nil), base.Robots...)
			test.mutate(&session)
			if err := session.Validate(); err == nil {
				t.Fatalf("expected unsafe %s to fail", test.name)
			}
		})
	}
}

func TestSessionAcceptsNestedTopicPrefix(t *testing.T) {
	session := Session{
		World:     "simple",
		Partition: DefaultPartition,
		Robots:    []Robot{{Index: 1, Name: "robot1", TopicPrefix: "fleet/robot1"}},
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDataVolumePrefixRequiresSingleRobot(t *testing.T) {
	base := StartConfig{Robots: 1, World: "simple", AppImage: "img", EnvImage: "env", DataVolumePrefix: "ci-1"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.Robots = 2
	if err := base.Validate(); err == nil {
		t.Fatal("expected error")
	}
}
