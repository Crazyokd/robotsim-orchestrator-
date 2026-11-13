package null

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	nullworld "git.standard-robots.com/yicao/sim/internal/ports/world/null"
)

// Factory 按台造 null 车端，并从状态目录发现活着的车。
type Factory struct {
	// SessionName 空时状态文件在目录根上；有名字则用子目录。
	SessionName string
}

func (Factory) Kind() string { return Kind }

func (Factory) ValidateStart(_ context.Context, session contract.Session, rawSection assets.Section) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if len(rawSection) > 0 {
		_, err := ParseSection(rawSection)
		return err
	}
	return nil
}

// New 造一台车的 Port。配对约定与所有车端一致：只认 null 世界的
// Attachment，认不出直接失败——跨种类配对在构造期就炸，不带进运行期。
func (f Factory) New(_ context.Context, session contract.Session, robot contract.Robot, attachment orchestrator.Attachment, rawSection assets.Section) (orchestrator.VehiclePort, error) {
	link, ok := attachment.(nullworld.Attachment)
	if !ok {
		kind := "<nil>"
		if attachment != nil {
			kind = attachment.Kind()
		}
		return nil, fmt.Errorf("vehicle kind %s cannot attach to world attachment kind %s", Kind, kind)
	}
	var section Section
	if len(rawSection) > 0 {
		parsed, err := ParseSection(rawSection)
		if err != nil {
			return nil, err
		}
		section = parsed
	}
	return Vehicle{
		Robot:       robot,
		world:       link.World,
		note:        section.Note,
		SessionName: f.SessionName,
	}, nil
}

// Discover 读状态目录里的 robot<N>.json：文件在就是车在，与容器车端的
// "容器在就是车在"同一语义。
func (f Factory) Discover(_ context.Context) ([]orchestrator.VehiclePort, error) {
	root, err := f.stateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read null vehicle state dir %s: %w", root, err)
	}
	var indices []int
	for _, entry := range entries {
		index, ok := parseStateFileName(entry.Name())
		if ok {
			indices = append(indices, index)
		}
	}
	sort.Ints(indices)
	ports := make([]orchestrator.VehiclePort, 0, len(indices))
	for _, index := range indices {
		ports = append(ports, Vehicle{
			Robot:       contract.Robot{Index: index, Name: contract.RobotName(index)},
			SessionName: f.SessionName,
		})
	}
	return ports, nil
}

func parseStateFileName(name string) (int, bool) {
	if !strings.HasPrefix(name, "robot") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	index, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "robot"), ".json"))
	if err != nil || index < 1 {
		return 0, false
	}
	return index, true
}

// vehicleState 是一台车的状态文件：世界归属、provision 记录。
type vehicleState struct {
	Index       int           `json:"index"`
	Name        string        `json:"name"`
	World       string        `json:"world"`
	Note        string        `json:"note,omitempty"`
	Provisioned bool          `json:"provisioned"`
	Stations    int           `json:"stations"`
	Pose        contract.Pose `json:"pose"`
}

// Vehicle 是一台 null 车：Start 写状态文件，Status/Discover 读它，Stop 删它。
type Vehicle struct {
	Robot       contract.Robot
	world       string
	note        string
	SessionName string
}

func (f Factory) stateDir() (string, error) {
	root, err := dataRoot()
	if err != nil {
		return "", err
	}
	if f.SessionName != "" {
		return filepath.Join(root, f.SessionName), nil
	}
	return root, nil
}

func (v Vehicle) statePath() (string, error) {
	dir, err := (Factory{SessionName: v.SessionName}).stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, contract.RobotName(v.Robot.Index)+".json"), nil
}

func (v Vehicle) Start(_ context.Context) error {
	return v.writeState(vehicleState{
		Index: v.Robot.Index,
		Name:  v.Robot.Name,
		World: v.world,
		Note:  v.note,
		Pose:  v.Robot.Pose,
	})
}

// WaitReady 即返回：null 车没有冷启动——状态文件落盘就是就绪。
func (Vehicle) WaitReady(context.Context) error { return nil }

// Provision 把 world init 的输入记进状态文件：站点数与出生位姿——证明
// Provisioning 确实按台到达了车端。
func (v Vehicle) Provision(_ context.Context, provisioning orchestrator.Provisioning) error {
	state, err := v.readState()
	if err != nil {
		return err
	}
	state.Provisioned = true
	state.Stations = len(provisioning.Stations)
	state.Pose = provisioning.Pose
	return v.writeState(state)
}

func (v Vehicle) Status(_ context.Context) (orchestrator.VehicleStatus, error) {
	state, err := v.readState()
	if errors.Is(err, errNoState) {
		return orchestrator.VehicleStatus{Name: v.Robot.Name}, nil
	}
	if err != nil {
		return orchestrator.VehicleStatus{}, err
	}
	detail := map[string]string{"world": state.World}
	if state.Note != "" {
		detail["note"] = state.Note
	}
	if state.Provisioned {
		detail["provisioned"] = "true"
		detail["stations"] = strconv.Itoa(state.Stations)
	}
	return orchestrator.VehicleStatus{
		Name:     state.Name,
		Running:  true,
		Endpoint: "null://" + state.Name,
		Detail:   detail,
	}, nil
}

// Stop 摘掉状态文件；本就没有不是错误（stop 要幂等）。
func (v Vehicle) Stop(_ context.Context) error {
	path, err := v.statePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove null vehicle state %s: %w", path, err)
	}
	return nil
}

// Forward 原样回显请求字节：null 车没有协议，它证明的是 call 把字节
// 原样路由到了这台车的 Forward、又把返回的字节原样带了回去。
func (v Vehicle) Forward(_ context.Context, request []byte) ([]byte, error) {
	if _, err := v.readState(); err != nil {
		return nil, err
	}
	return request, nil
}

var errNoState = errors.New("null vehicle has no state file")

func (v Vehicle) readState() (vehicleState, error) {
	path, err := v.statePath()
	if err != nil {
		return vehicleState{}, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return vehicleState{}, errNoState
	}
	if err != nil {
		return vehicleState{}, fmt.Errorf("read null vehicle state %s: %w", path, err)
	}
	var state vehicleState
	if err := json.Unmarshal(raw, &state); err != nil {
		return vehicleState{}, fmt.Errorf("parse null vehicle state %s: %w", path, err)
	}
	return state, nil
}

func (v Vehicle) writeState(state vehicleState) error {
	path, err := v.statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create null vehicle state dir: %w", err)
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
