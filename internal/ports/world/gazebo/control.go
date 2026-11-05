package gazebo

import (
	"context"
	"fmt"
	"slices"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo/process"
)

// Pose 读运行中实体的世界位姿（经 gz service，在世界容器内执行）。
func (w DockerWorld) Pose(ctx context.Context, robot int) (orchestrator.EntityPose, error) {
	ready, entity, err := w.runningEntity(ctx, robot)
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	out, err := w.Engine.Exec(ctx, WorldContainer, process.PoseInfoCommand(ready.World))
	if err != nil {
		return orchestrator.EntityPose{}, fmt.Errorf("read pose for %s: %w", entity, err)
	}
	pose, err := process.ParseEntityPose(out, entity)
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	return orchestrator.EntityPose{Robot: robot, Entity: entity, Pose: pose}, nil
}

// Reset 把运行中实体摆到给定世界位姿，并回读确认。
func (w DockerWorld) Reset(ctx context.Context, robot int, pose contract.Pose) (orchestrator.EntityPose, error) {
	ready, entity, err := w.runningEntity(ctx, robot)
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	out, err := w.Engine.Exec(ctx, WorldContainer, process.SetPoseCommand(ready.World, entity, pose))
	if err != nil {
		return orchestrator.EntityPose{}, fmt.Errorf("reset %s: %w", entity, err)
	}
	if err := process.AssertSetPoseOK(out); err != nil {
		return orchestrator.EntityPose{}, fmt.Errorf("reset %s: %w", entity, err)
	}
	return w.Pose(ctx, robot)
}

func (w DockerWorld) runningEntity(ctx context.Context, robot int) (process.ReadyReport, string, error) {
	if robot < 1 {
		return process.ReadyReport{}, "", fmt.Errorf("robot index must be >= 1")
	}
	ready, err := readReady(ctx, w.Engine)
	if err != nil {
		return process.ReadyReport{}, "", err
	}
	if !ready.Clock {
		return process.ReadyReport{}, "", fmt.Errorf("world clock is not ready")
	}
	entity := contract.RobotName(robot)
	if !slices.Contains(ready.Robots, entity) {
		return process.ReadyReport{}, "", fmt.Errorf("entity %s is not in the running session", entity)
	}
	return ready, entity, nil
}
