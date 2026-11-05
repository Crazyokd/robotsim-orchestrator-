package orchestrator

import (
	"context"
	"errors"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// 编排器的 pose/reset 只路由：世界 Port 怎么读位姿是它自己的事
// （命令断言在世界 Port 包里）。
type routingWorldPort struct {
	fakeWorldPort
	poseRobot  int
	resetRobot int
	resetPose  contract.Pose
	pose       EntityPose
	poseErr    error
}

func (f *routingWorldPort) Pose(_ context.Context, robot int) (EntityPose, error) {
	f.poseRobot = robot
	return f.pose, f.poseErr
}

func (f *routingWorldPort) Reset(_ context.Context, robot int, pose contract.Pose) (EntityPose, error) {
	f.resetRobot = robot
	f.resetPose = pose
	return f.pose, f.poseErr
}

func TestPoseRoutesToTheWorldPort(t *testing.T) {
	worldPort := &routingWorldPort{pose: EntityPose{Robot: 1, Entity: "robot1", Pose: contract.Pose{X: 2, Y: 3, Z: 0.05}}}
	got, err := (Orchestrator{World: worldPort}).Pose(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Robot != 1 || got.Entity != "robot1" || got.Pose.X != 2 || got.Pose.Y != 3 {
		t.Fatalf("got=%+v", got)
	}
}

func TestResetRoutesToTheWorldPort(t *testing.T) {
	worldPort := &routingWorldPort{pose: EntityPose{Robot: 1, Entity: "robot1", Pose: contract.Pose{X: 4, Z: contract.DefaultSpawnZ}}}
	got, err := (Orchestrator{World: worldPort}).Reset(context.Background(), 1, contract.Pose{X: 4, Z: contract.DefaultSpawnZ})
	if err != nil {
		t.Fatal(err)
	}
	if worldPort.resetRobot != 1 || worldPort.resetPose.X != 4 {
		t.Fatalf("routed reset=%d %+v", worldPort.resetRobot, worldPort.resetPose)
	}
	if got.Pose.X != 4 {
		t.Fatalf("got=%+v", got)
	}
}

func TestPosePropagatesTheWorldError(t *testing.T) {
	poseErr := errors.New("entity is not in the running session")
	worldPort := &routingWorldPort{poseErr: poseErr}
	if _, err := (Orchestrator{World: worldPort}).Pose(context.Background(), 2); !errors.Is(err, poseErr) {
		t.Fatalf("error=%v, want %v", err, poseErr)
	}
}

func TestPoseFailsWithoutAWorldPort(t *testing.T) {
	if _, err := (Orchestrator{}).Pose(context.Background(), 1); err == nil {
		t.Fatal("expected error")
	}
}
