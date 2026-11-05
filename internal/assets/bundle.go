package assets

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// Section 是 bundle 里按 kind 分给 Port 的原始 JSON 节：框架不解释，
// Port 自己严格解析（DisallowUnknownFields）。
type Section = json.RawMessage

// Bundle 是一个世界的 manifest（`<world>/bundle.json`）。
//
// 字段非法直接失败：不做默认值、不做旧字段映射（本仓未发版，脏输入不迁就）。
// 非有限值（NaN/Inf/溢出如 1e400）由 JSON 解析层拒绝，这里不重复校验。
type Bundle struct {
	// World 是世界的名字，必须与目录名一致。
	World string `json:"world"`
	// Stations 是世界系站点（m / rad）。键必须存在；空数组表示这个世界没有站点。
	Stations []Station `json:"stations"`
	// InitialPoses 可选：按车指定世界位姿；给出则覆盖 session 的 spawn 策略。
	InitialPoses []InitialPose `json:"initial_poses,omitempty"`
	// SourceSHA 可选：世界来源 pin；本仓自造时留空。
	SourceSHA string `json:"source_sha,omitempty"`
	// Worlds / Vehicles 按 kind 分节，框架只搬运原始 JSON。
	Worlds   map[string]Section `json:"worlds"`
	Vehicles map[string]Section `json:"vehicles"`
}

// Station 是世界系站点：m / rad。
type Station struct {
	ID  int     `json:"id"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Yaw float64 `json:"yaw"`
}

// InitialPose 按车声明出生位姿（世界系 m / rad）。
type InitialPose struct {
	Index int     `json:"index"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Yaw   float64 `json:"yaw"`
}

// Pose 转成契约位姿。
func (p InitialPose) Pose() contract.Pose {
	return contract.Pose{X: p.X, Y: p.Y, Z: p.Z, Yaw: p.Yaw}
}

// LoadBundle 读取并校验世界目录里的 manifest。文件不存在、字段多余、字段缺失、
// 取值非法都是错误。
func LoadBundle(dir string) (Bundle, error) {
	path := filepath.Join(dir, "bundle.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Bundle{}, fmt.Errorf("read world bundle %s: %w", path, err)
	}
	bundle, err := ParseBundle(raw)
	if err != nil {
		return Bundle{}, fmt.Errorf("%s: %w", path, err)
	}
	return bundle, nil
}

// ParseBundle 解析 manifest：拒绝未知字段与尾部多余内容，解析后做完整校验。
func ParseBundle(raw []byte) (Bundle, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var bundle Bundle
	if err := dec.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("parse world bundle: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Bundle{}, fmt.Errorf("parse world bundle: trailing content after manifest")
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

// ValidateForWorld 校验 manifest 与世界名一致——场景组与地图绑定的第一道门。
func (b Bundle) ValidateForWorld(world string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.World != world {
		return fmt.Errorf("world bundle is for %q, requested %q", b.World, world)
	}
	return nil
}

func (b Bundle) Validate() error {
	if err := validateSafeToken("world bundle world", b.World); err != nil {
		return err
	}
	if b.Stations == nil {
		return fmt.Errorf("world bundle stations must be present")
	}
	stationIDs := make(map[int]struct{}, len(b.Stations))
	for _, station := range b.Stations {
		if station.ID < 1 {
			return fmt.Errorf("station id must be >= 1, got %d", station.ID)
		}
		if _, ok := stationIDs[station.ID]; ok {
			return fmt.Errorf("duplicate station id %d", station.ID)
		}
		stationIDs[station.ID] = struct{}{}
	}
	poseIndices := make(map[int]struct{}, len(b.InitialPoses))
	for _, pose := range b.InitialPoses {
		if pose.Index < 1 {
			return fmt.Errorf("initial pose index must be >= 1, got %d", pose.Index)
		}
		if _, ok := poseIndices[pose.Index]; ok {
			return fmt.Errorf("duplicate initial pose index %d", pose.Index)
		}
		poseIndices[pose.Index] = struct{}{}
	}
	if b.Worlds == nil {
		return fmt.Errorf("world bundle worlds sections must be present")
	}
	if b.Vehicles == nil {
		return fmt.Errorf("world bundle vehicles sections must be present")
	}
	if b.SourceSHA != "" && strings.TrimSpace(b.SourceSHA) != b.SourceSHA {
		return fmt.Errorf("world bundle source_sha is not canonical")
	}
	return nil
}

// PoseFor 返回某台车在该世界里的世界位姿；未声明时 ok=false，由调用方回退到
// session 策略。
func (b Bundle) PoseFor(index int) (contract.Pose, bool) {
	for _, candidate := range b.InitialPoses {
		if candidate.Index == index {
			return candidate.Pose(), true
		}
	}
	return contract.Pose{}, false
}

// StationByID 返回指定站点的副本。
func (b Bundle) StationByID(id int) (Station, bool) {
	for _, station := range b.Stations {
		if station.ID == id {
			return station, true
		}
	}
	return Station{}, false
}

// VehicleSection 取车端 kind 的那一节；缺节即失败。
func (b Bundle) VehicleSection(kind string) (Section, error) {
	section, ok := b.Vehicles[kind]
	if !ok {
		return nil, fmt.Errorf("world bundle %s has no vehicles.%s section", b.World, kind)
	}
	return section, nil
}

// WorldSection 取世界 kind 的那一节；缺节即失败。
func (b Bundle) WorldSection(kind string) (Section, error) {
	section, ok := b.Worlds[kind]
	if !ok {
		return nil, fmt.Errorf("world bundle %s has no worlds.%s section", b.World, kind)
	}
	return section, nil
}

func validateSafeToken(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	for _, r := range value {
		if isSafeTokenChar(r) {
			continue
		}
		return fmt.Errorf("%s contains invalid character %q", field, r)
	}
	return nil
}

func isSafeTokenChar(r rune) bool {
	return r == '_' || r == '-' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}
