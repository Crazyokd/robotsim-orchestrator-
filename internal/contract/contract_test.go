package contract

import (
	"encoding/json"
	"reflect"
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
	if robot.Name != "robot1" {
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

func TestStartConfigRejectsEmptyWorld(t *testing.T) {
	if err := (StartConfig{Robots: 1}).Validate(); err == nil {
		t.Fatal("world is required: no default, no fallback")
	}
}

func TestStartConfigRejectsZeroRobots(t *testing.T) {
	err := StartConfig{Robots: 0, World: "simple"}.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartConfigValidatesKindTokens(t *testing.T) {
	if err := (StartConfig{Robots: 1, World: "simple", WorldKind: "fake-world", VehicleKind: "fake"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (StartConfig{Robots: 1, World: "simple", WorldKind: "bad/kind"}).Validate(); err == nil {
		t.Fatal("unsafe world kind must fail")
	}
	if err := (StartConfig{Robots: 1, World: "simple", VehicleKind: "bad kind"}).Validate(); err == nil {
		t.Fatal("unsafe vehicle kind must fail")
	}
}

func TestStartConfigBuildsNeutralSession(t *testing.T) {
	session, err := StartConfig{
		Robots: 2,
		World:  "simple",
		Poses: []Pose{
			{X: 10, Z: DefaultSpawnZ},
			{X: 20, Z: DefaultSpawnZ},
		},
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	if session.World != "simple" {
		t.Fatalf("session=%+v", session)
	}
	if len(session.Robots) != 2 {
		t.Fatalf("robots=%+v", session.Robots)
	}
	if session.Robots[0].Name != "robot1" || session.Robots[0].Pose.X != 10 {
		t.Fatalf("robot1=%+v", session.Robots[0])
	}
	if session.Robots[1].Name != "robot2" || session.Robots[1].Pose.X != 20 {
		t.Fatalf("robot2=%+v", session.Robots[1])
	}
}

func TestSessionJSONCarriesOnlyWorldAndRobots(t *testing.T) {
	session, err := StartConfig{
		Robots: 2, World: "simple",
	}.Session()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	expected := `{"world":"simple","robots":[{"index":1,"name":"robot1","pose":{"x":0,"y":0,"z":0.05,"yaw":0}},{"index":2,"name":"robot2","pose":{"x":3,"y":0,"z":0.05,"yaw":0}}]}`
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session wire format=%s, want %s", raw, expected)
	}

}

func TestCameraSelectionStaysOutsideSession(t *testing.T) {
	session, err := StartConfig{Robots: 3, World: "simple"}.Session()
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
}

func TestSessionRejectsDuplicateRobotIdentity(t *testing.T) {
	base := Session{
		World: "simple",
		Robots: []Robot{
			{Index: 1, Name: "robot1"},
			{Index: 2, Name: "robot2"},
		},
	}
	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{"index", func(s *Session) { s.Robots[1].Index = s.Robots[0].Index }},
		{"name", func(s *Session) { s.Robots[1].Name = s.Robots[0].Name }},
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
		World:  "simple",
		Robots: []Robot{{Index: 1, Name: "robot1"}},
	}
	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{"world path", func(s *Session) { s.World = "../simple" }},
		{"robot path", func(s *Session) { s.Robots[0].Name = "../robot1" }},
		{"robot quote", func(s *Session) { s.Robots[0].Name = `robot"1` }},
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
