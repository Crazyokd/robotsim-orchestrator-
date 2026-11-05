package orchestrator

import (
	"context"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type EntityPose struct {
	Robot  int
	Entity string
	Pose   contract.Pose
}

// Pose 只路由：位姿是世界的事，交给世界 Port。
func (o Orchestrator) Pose(ctx context.Context, robot int) (EntityPose, error) {
	worldPort, err := o.worldPort(contract.StartConfig{})
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Pose(ctx, robot)
}

// Reset 只路由：摆位是世界的事，交给世界 Port；位姿为世界系。
func (o Orchestrator) Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error) {
	worldPort, err := o.worldPort(contract.StartConfig{})
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Reset(ctx, robot, pose)
}
