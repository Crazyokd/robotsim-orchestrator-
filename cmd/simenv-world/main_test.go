package main

import (
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestParseCameraRobots(t *testing.T) {
	session := contract.Session{
		World:     "simple",
		Partition: contract.DefaultPartition,
		Robots: []contract.Robot{
			{Index: 1, Name: "robot1", TopicPrefix: "robot1"},
			{Index: 2, Name: "robot2", TopicPrefix: "robot2"},
		},
	}
	got, err := parseCameraRobots("2", session)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] || !got[2] {
		t.Fatalf("camera robots=%v", got)
	}
}

func TestParseCameraRobotsRejectsUnknownRobot(t *testing.T) {
	session := contract.Session{
		World:     "simple",
		Partition: contract.DefaultPartition,
		Robots:    []contract.Robot{{Index: 1, Name: "robot1", TopicPrefix: "robot1"}},
	}
	if _, err := parseCameraRobots("2", session); err == nil {
		t.Fatal("expected unknown camera robot to fail")
	}
}
