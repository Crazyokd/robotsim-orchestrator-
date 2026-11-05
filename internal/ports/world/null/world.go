package null

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

// stateFileName 是世界状态文件名：一个状态目录同时只承载一次 null 会话，
// 与容器世界里"世界容器同时只有一个"是同一语义。
const stateFileName = "world.json"

// worldState 是状态文件的内容：会话世界、机器人与它们的当前位姿。
type worldState struct {
	World   string       `json:"world"`
	Note    string       `json:"note,omitempty"`
	Robots  []robotState `json:"robots"`
	Started time.Time    `json:"started"`
}

type robotState struct {
	Index int           `json:"index"`
	Name  string        `json:"name"`
	Pose  contract.Pose `json:"pose"`
}

// World 是 null 世界 Port：Start 写状态文件，Ready/Status/Pose/Reset 读写它，
// Stop 删它。状态目录就是真相——另一个 simenv 进程据此发现这次会话。
type World struct{}

func (World) Kind() string { return Kind }

func (w World) statePath() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, stateFileName), nil
}

func (w World) Start(_ context.Context, session contract.Session, rawSection assets.Section) ([]orchestrator.Attachment, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	var section Section
	if len(rawSection) > 0 {
		parsed, err := ParseSection(rawSection)
		if err != nil {
			return nil, err
		}
		section = parsed
	}
	path, err := w.statePath()
	if err != nil {
		return nil, err
	}
	state := worldState{
		World:   session.World,
		Note:    section.Note,
		Started: time.Now().UTC().Truncate(time.Second),
	}
	attachments := make([]orchestrator.Attachment, 0, len(session.Robots))
	for _, robot := range session.Robots {
		state.Robots = append(state.Robots, robotState{
			Index: robot.Index,
			Name:  robot.Name,
			Pose:  robot.Pose,
		})
		attachments = append(attachments, Attachment{World: session.World, Robot: robot.Index})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create null world state dir: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return nil, fmt.Errorf("write null world state %s: %w", path, err)
	}
	return attachments, nil
}

// Ready 从状态文件回读实体清单：文件不在就是世界没起，直接失败。
func (w World) Ready(_ context.Context, session contract.Session) (orchestrator.WorldReady, error) {
	state, err := w.readState()
	if err != nil {
		return orchestrator.WorldReady{}, err
	}
	ready := orchestrator.WorldReady{World: state.World}
	for _, robot := range state.Robots {
		ready.Robots = append(ready.Robots, robot.Name)
	}
	return ready, nil
}

func (w World) Status(_ context.Context) (orchestrator.WorldStatus, error) {
	path, err := w.statePath()
	if err != nil {
		return orchestrator.WorldStatus{}, err
	}
	state, err := w.readState()
	if errors.Is(err, errNoState) {
		return orchestrator.WorldStatus{}, nil
	}
	if err != nil {
		return orchestrator.WorldStatus{}, err
	}
	ready := orchestrator.WorldReady{World: state.World}
	for _, robot := range state.Robots {
		ready.Robots = append(ready.Robots, robot.Name)
	}
	detail := map[string]string{"state": path}
	if state.Note != "" {
		detail["note"] = state.Note
	}
	return orchestrator.WorldStatus{
		Present: true,
		Running: true,
		Ready:   ready,
		Detail:  detail,
	}, nil
}

// Stop 摘掉状态文件；本就没有状态文件不是错误（stop 要幂等）。
func (w World) Stop(_ context.Context) error {
	path, err := w.statePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove null world state %s: %w", path, err)
	}
	return nil
}

func (w World) Pose(_ context.Context, robot int) (orchestrator.EntityPose, error) {
	state, err := w.readState()
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	for _, candidate := range state.Robots {
		if candidate.Index == robot {
			return orchestrator.EntityPose{Robot: robot, Entity: candidate.Name, Pose: candidate.Pose}, nil
		}
	}
	return orchestrator.EntityPose{}, fmt.Errorf("robot %d is not in the null world", robot)
}

func (w World) Reset(_ context.Context, robot int, pose contract.Pose) (orchestrator.EntityPose, error) {
	path, err := w.statePath()
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	state, err := w.readState()
	if err != nil {
		return orchestrator.EntityPose{}, err
	}
	for i, candidate := range state.Robots {
		if candidate.Index == robot {
			state.Robots[i].Pose = pose
			raw, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				return orchestrator.EntityPose{}, err
			}
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				return orchestrator.EntityPose{}, fmt.Errorf("write null world state %s: %w", path, err)
			}
			return orchestrator.EntityPose{Robot: robot, Entity: candidate.Name, Pose: pose}, nil
		}
	}
	return orchestrator.EntityPose{}, fmt.Errorf("robot %d is not in the null world", robot)
}

var errNoState = errors.New("null world has no state file")

func (w World) readState() (worldState, error) {
	path, err := w.statePath()
	if err != nil {
		return worldState{}, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return worldState{}, errNoState
	}
	if err != nil {
		return worldState{}, fmt.Errorf("read null world state %s: %w", path, err)
	}
	var state worldState
	if err := json.Unmarshal(raw, &state); err != nil {
		return worldState{}, fmt.Errorf("parse null world state %s: %w", path, err)
	}
	if err := validateSessionName(state.World); err != nil {
		return worldState{}, fmt.Errorf("parse null world state %s: %w", path, err)
	}
	return state, nil
}
