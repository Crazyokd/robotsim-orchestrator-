package contract

import "testing"

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

func TestRobot1MatchesEasyGoMultiOffsetZero(t *testing.T) {
	robot, err := NewRobot(1, Pose{})
	if err != nil {
		t.Fatal(err)
	}
	if robot.Name != "robot1" {
		t.Fatalf("name=%q", robot.Name)
	}
	if robot.FrontendPort != 18188 || robot.APIPort != 18189 {
		t.Fatalf("ports frontend=%d api=%d", robot.FrontendPort, robot.APIPort)
	}
	if robot.ROSDomainID != 11 {
		t.Fatalf("domain=%d", robot.ROSDomainID)
	}
	if robot.Pose != (Pose{X: 0, Y: 0, Z: 0.05, Yaw: 0}) {
		t.Fatalf("default pose=%+v", robot.Pose)
	}
}

func TestRobot2StrideAndPorts(t *testing.T) {
	robot, err := NewRobot(2, Pose{})
	if err != nil {
		t.Fatal(err)
	}
	if robot.APIPort != 18289 || robot.ROSDomainID != 12 {
		t.Fatalf("api=%d domain=%d", robot.APIPort, robot.ROSDomainID)
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

func TestStartConfigBuildsAttachmentSession(t *testing.T) {
	session, err := StartConfig{
		Robots:   2,
		World:    "simple",
		AppImage: "img",
		EnvImage: "env",
		Poses: []Pose{
			{X: 10, Z: DefaultSpawnZ},
			{X: 20, Z: DefaultSpawnZ},
		},
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	want := []Attachment{{Index: 1, Name: "robot1"}, {Index: 2, Name: "robot2"}}
	if len(session.Attachments) != len(want) {
		t.Fatalf("attachments=%+v", session.Attachments)
	}
	for i := range want {
		if session.Attachments[i] != want[i] {
			t.Fatalf("attachment[%d]=%+v want %+v", i, session.Attachments[i], want[i])
		}
	}
}

func TestLaunchArgsAreHeadlessAndNoGazebo(t *testing.T) {
	robot, err := NewRobot(1, Pose{})
	if err != nil {
		t.Fatal(err)
	}
	args := robot.LaunchArgs()
	want := []string{
		"start_gazebo:=false",
		"use_gz_gui:=false",
		"use_rviz:=false",
		"robot_name:=robot1",
		"frontend_port:=18188",
		"api_port:=18189",
	}
	for _, item := range want {
		if !contains(args, item) {
			t.Fatalf("missing %q in %v", item, args)
		}
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
