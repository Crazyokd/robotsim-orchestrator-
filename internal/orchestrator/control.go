package orchestrator

import (
	"context"
	"fmt"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type EntityPose struct {
	Robot  int
	Entity string
	Pose   contract.Pose
}

// Pose 只路由：位姿是世界的事，交给世界 Port。
func (o Orchestrator) Pose(ctx context.Context, robot int) (EntityPose, error) {
	worldPort, err := o.markedWorld()
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Pose(ctx, robot)
}

// Reset 只路由：摆位是世界的事，交给世界 Port；位姿为世界系。
func (o Orchestrator) Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error) {
	worldPort, err := o.markedWorld()
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Reset(ctx, robot, pose)
}

// markedWorld 找到该对这次会话负责的世界 Port：测试注入优先；生产按会话
// 标记的种类构造。没有标记就是没有进行中的会话。
func (o Orchestrator) markedWorld() (WorldPort, error) {
	if !o.usingRegistry() {
		return o.worldPort(contract.StartConfig{})
	}
	marker, err := loadSessionMarker()
	if err != nil {
		return nil, err
	}
	if marker == nil {
		return nil, fmt.Errorf("no running session")
	}
	return o.newWorld(marker.WorldKind, contract.StartConfig{})
}
