package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// WorldBundle 是一个世界的 manifest（`<world>.world.json`）。
//
// 与 `<world>.sdf`（世界）、`<world>.map_export`（地图集）一起构成"世界 bundle"：
// world init 按它灌地图、注站点、放车定位；场景组按它做一致性校验。
// 设计见 docs/superpowers/specs/2026-10-12-world-bundle-design.md。
//
// 字段非法直接失败：不做默认值、不做旧字段映射（本仓未发版，脏输入不迁就）。
// 非有限值（NaN/Inf/溢出如 1e400）由 JSON 解析层拒绝，这里不重复校验。
type WorldBundle struct {
	// World 是世界的名字，必须与 SDF / map_export 的文件名一致。
	World string `json:"world"`
	// MapName 是地图主名，必须与 map_export 里解析出的主名一致。
	MapName string `json:"map_name"`
	// OriginOffsetMM 是世界坐标 ↔ 车端地图坐标的偏移（mm）。
	// 指针：必须显式给出——0 是合法值，缺失与"偏移为 0"不能混为一谈。
	OriginOffsetMM *OriginOffset `json:"origin_offset_mm"`
	// Stations 是车端地图坐标系下的站点（mm）。键必须存在；空数组表示这个世界没有站点。
	Stations []Station `json:"stations"`
	// InitialPoses 可选：按车指定世界位姿；给出则覆盖 session 的 spawn 策略。
	InitialPoses []WorldPose `json:"initial_poses,omitempty"`
	// Scenarios 是该世界绑定的场景组（一键脚本按它跑）。必须存在且非空。
	Scenarios []string `json:"scenarios"`
	// SourceSHA 可选：世界来源 pin；本仓自造时留空并在合同里说明理由。
	SourceSHA string `json:"source_sha,omitempty"`
}

type OriginOffset struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Station struct {
	ID  int     `json:"id"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Yaw float64 `json:"yaw"`
}

type WorldPose struct {
	Index int     `json:"index"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Z     float64 `json:"z"`
	Yaw   float64 `json:"yaw"`
}

// LoadWorldBundle 读取并校验 manifest。文件不存在、字段多余、字段缺失、取值非法都是错误。
func LoadWorldBundle(path string) (WorldBundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return WorldBundle{}, fmt.Errorf("read world bundle %s: %w", path, err)
	}
	bundle, err := ParseWorldBundle(raw)
	if err != nil {
		return WorldBundle{}, fmt.Errorf("%s: %w", path, err)
	}
	return bundle, nil
}

// ParseWorldBundle 解析 manifest：拒绝未知字段与尾部多余内容，解析后做完整校验。
func ParseWorldBundle(raw []byte) (WorldBundle, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var bundle WorldBundle
	if err := dec.Decode(&bundle); err != nil {
		return WorldBundle{}, fmt.Errorf("parse world bundle: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return WorldBundle{}, fmt.Errorf("parse world bundle: trailing content after manifest")
	}
	if err := bundle.Validate(); err != nil {
		return WorldBundle{}, err
	}
	return bundle, nil
}

// ValidateForWorld 校验 manifest 与世界名一致——场景组与地图绑定的第一道门。
func (b WorldBundle) ValidateForWorld(world string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.World != world {
		return fmt.Errorf("world bundle is for %q, requested %q", b.World, world)
	}
	return nil
}

func (b WorldBundle) Validate() error {
	if err := validateSafeToken("world bundle world", b.World); err != nil {
		return err
	}
	if err := validateSafeToken("world bundle map name", b.MapName); err != nil {
		return err
	}
	if b.OriginOffsetMM == nil {
		return fmt.Errorf("world bundle origin_offset_mm must be present")
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
	if len(b.Scenarios) == 0 {
		return fmt.Errorf("world bundle scenarios must not be empty")
	}
	scenarios := make(map[string]struct{}, len(b.Scenarios))
	for _, scenario := range b.Scenarios {
		if strings.TrimSpace(scenario) == "" {
			return fmt.Errorf("world bundle scenario name must not be empty")
		}
		if _, ok := scenarios[scenario]; ok {
			return fmt.Errorf("duplicate scenario %q", scenario)
		}
		scenarios[scenario] = struct{}{}
	}
	if b.SourceSHA != "" && strings.TrimSpace(b.SourceSHA) != b.SourceSHA {
		return fmt.Errorf("world bundle source_sha is not canonical")
	}
	return nil
}

// PoseFor 返回某台车在该世界里的世界位姿；未声明时 ok=false，由调用方回退到 session 策略。
func (b WorldBundle) PoseFor(index int) (pose WorldPose, ok bool) {
	for _, candidate := range b.InitialPoses {
		if candidate.Index == index {
			return candidate, true
		}
	}
	return WorldPose{}, false
}

// StationByID 返回指定站点的副本。
func (b WorldBundle) StationByID(id int) (Station, bool) {
	for _, station := range b.Stations {
		if station.ID == id {
			return station, true
		}
	}
	return Station{}, false
}
