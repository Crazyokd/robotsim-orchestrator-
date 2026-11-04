package orchestrator

import (
	"context"
	"fmt"
	"slices"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

type EntityPose struct {
	Robot  int
	Entity string
	Pose   contract.Pose
}

func (o Orchestrator) Pose(ctx context.Context, robot int) (EntityPose, error) {
	ready, entity, err := o.runningEntity(ctx, robot)
	if err != nil {
		return EntityPose{}, err
	}
	out, err := o.engine().Exec(ctx, contract.WorldContainer, world.PoseInfoCommand(ready.World))
	if err != nil {
		return EntityPose{}, fmt.Errorf("read pose for %s: %w", entity, err)
	}
	pose, err := world.ParseEntityPose(out, entity)
	if err != nil {
		return EntityPose{}, err
	}
	return EntityPose{Robot: robot, Entity: entity, Pose: pose}, nil
}

func (o Orchestrator) Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error) {
	ready, entity, err := o.runningEntity(ctx, robot)
	if err != nil {
		return EntityPose{}, err
	}
	out, err := o.engine().Exec(ctx, contract.WorldContainer, world.SetPoseCommand(ready.World, entity, pose))
	if err != nil {
		return EntityPose{}, fmt.Errorf("reset %s: %w", entity, err)
	}
	if err := world.AssertSetPoseOK(out); err != nil {
		return EntityPose{}, fmt.Errorf("reset %s: %w", entity, err)
	}
	return o.Pose(ctx, robot)
}

func (o Orchestrator) runningEntity(ctx context.Context, robot int) (world.ReadyReport, string, error) {
	if robot < 1 {
		return world.ReadyReport{}, "", fmt.Errorf("robot index must be >= 1")
	}
	ready, err := readReady(ctx, o.engine())
	if err != nil {
		return world.ReadyReport{}, "", err
	}
	if !ready.Clock {
		return world.ReadyReport{}, "", fmt.Errorf("world clock is not ready")
	}
	entity := contract.RobotName(robot)
	if !slices.Contains(ready.Robots, entity) {
		return world.ReadyReport{}, "", fmt.Errorf("entity %s is not in the running session", entity)
	}
	return ready, entity, nil
}

func (o Orchestrator) engine() Engine {
	return o.Engine
}