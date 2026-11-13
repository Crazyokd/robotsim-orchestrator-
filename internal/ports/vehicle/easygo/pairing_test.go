package easygo

import (
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	nullworld "git.standard-robots.com/yicao/sim/internal/ports/world/null"
)

// 给不出连接端点的世界在 Factory.New 就失败，不带进运行期。
func TestNewRejectsNullWorldAttachment(t *testing.T) {
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Factory{}.New(context.Background(),
		contract.Session{World: "nullworld", Robots: []contract.Robot{robot}},
		robot,
		nullworld.Attachment{World: "nullworld", Robot: 1},
		nil)
	if err == nil || !strings.Contains(err.Error(), `world kind "null"`) {
		t.Fatalf("null world attachment must fail at construction: %v", err)
	}
}

// 另一套世界只要给出端点和相机标志，就能接上 EasyGo，不必是 gazebo.Attachment。
func TestNewAcceptsForeignEndpoint(t *testing.T) {
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	link := testLink(t, robot)
	port, err := Factory{}.New(context.Background(),
		contract.Session{World: "other", Robots: []contract.Robot{robot}},
		robot,
		otherWorld{cameras: true, ip: "10.0.0.8", partition: "other_sim", prefix: "robot1", sdf: link.SDFPath, archive: link.Archive},
		nil)
	if err != nil {
		t.Fatal(err)
	}
	vehicle, ok := port.(Vehicle)
	if !ok {
		t.Fatalf("port=%T", port)
	}
	got := vehicle.Link
	if !got.Cameras || got.IP != "10.0.0.8" || got.Partition != "other_sim" || got.TopicPrefix != "robot1" {
		t.Fatalf("link=%+v", got)
	}
}

func TestNewRejectsIncompleteEndpoint(t *testing.T) {
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Factory{}.New(context.Background(),
		contract.Session{World: "other", Robots: []contract.Robot{robot}},
		robot,
		otherWorld{prefix: "robot1"},
		nil)
	if err == nil || !strings.Contains(err.Error(), "endpoint is incomplete") {
		t.Fatalf("incomplete endpoint must fail at construction: %v", err)
	}
}

type otherWorld struct {
	archive               []byte
	sdf                   string
	cameras               bool
	ip, partition, prefix string
}

func (otherWorld) Kind() string { return "other" }
func (o otherWorld) Endpoint() (string, string, string) {
	return o.ip, o.partition, o.prefix
}
func (o otherWorld) HasCameras() bool { return o.cameras }

func (o otherWorld) WorldAssets() (string, string, []byte) { return "/opt", o.sdf, o.archive }
