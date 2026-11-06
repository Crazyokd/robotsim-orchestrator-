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
	return o.PoseSession(ctx, "", robot)
}

// PoseSession 把位姿查询路由到指名会话。名字空着时用最近启动的那一个。
func (o Orchestrator) PoseSession(ctx context.Context, name string, robot int) (EntityPose, error) {
	worldPort, err := o.markedWorldNamed(name)
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Pose(ctx, robot)
}

// Reset 只路由：摆位是世界的事，交给世界 Port；位姿为世界系。
func (o Orchestrator) Reset(ctx context.Context, robot int, pose contract.Pose) (EntityPose, error) {
	return o.ResetSession(ctx, "", robot, pose)
}

// ResetSession 把摆位路由到指名会话。名字空着时用最近启动的那一个。
func (o Orchestrator) ResetSession(ctx context.Context, name string, robot int, pose contract.Pose) (EntityPose, error) {
	worldPort, err := o.markedWorldNamed(name)
	if err != nil {
		return EntityPose{}, err
	}
	return worldPort.Reset(ctx, robot, pose)
}

func (o Orchestrator) markedWorld() (WorldPort, error) {
	return o.markedWorldNamed("")
}

// markedWorldNamed 找到该对这次会话负责的世界 Port：测试注入优先；生产按会话
// 名单构造。没有名单就是没有进行中的会话。
func (o Orchestrator) markedWorldNamed(name string) (WorldPort, error) {
	if !o.usingRegistry() {
		return o.worldPort(contract.StartConfig{})
	}
	rec, err := o.resolveRecord(name)
	if err != nil {
		return nil, err
	}
	return o.newWorld(rec.WorldKind, portConfig(rec))
}

func (o Orchestrator) resolveRecord(name string) (sessionRecord, error) {
	records, err := loadSessions()
	if err != nil {
		return sessionRecord{}, err
	}
	rec, ok, err := pickSession(records, name)
	if err != nil {
		return sessionRecord{}, err
	}
	if !ok {
		return sessionRecord{}, fmt.Errorf("no running session")
	}
	return rec, nil
}
