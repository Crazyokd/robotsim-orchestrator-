package easygo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
)

// gzLink 是本车端能接上的世界挂接。世界不必来自某个具体包，只要给出
// GZ transport 三要素，以及是否为这台车 spawn 了相机。
type gzLink interface {
	GZEndpoint() (ip, partition, topicPrefix string)
	HasCameras() bool
}

// WorldLink 是启动一台车要用的挂接副本。
type WorldLink struct {
	Cameras     bool
	IP          string
	Partition   string
	TopicPrefix string
}

func linkFrom(attachment orchestrator.Attachment) (WorldLink, error) {
	if attachment == nil {
		return WorldLink{}, fmt.Errorf("easygo vehicle cannot attach to world kind %q", "<nil>")
	}
	shaped, ok := attachment.(gzLink)
	if !ok {
		return WorldLink{}, fmt.Errorf("easygo vehicle cannot attach to world kind %q", attachment.Kind())
	}
	ip, partition, prefix := shaped.GZEndpoint()
	if ip == "" || partition == "" || prefix == "" {
		return WorldLink{}, fmt.Errorf("easygo vehicle cannot attach to world kind %q: gz endpoint is incomplete", attachment.Kind())
	}
	return WorldLink{
		Cameras:     shaped.HasCameras(),
		IP:          ip,
		Partition:   partition,
		TopicPrefix: prefix,
	}, nil
}

// Kind 是 bundle vehicles 节里本 Port 的名字。
const Kind = "easygo"

const (
	vehicleRobotLabel = "simenv.robot"
	vehicleIndexLabel = "simenv.robot.index"
)

// Section 是 bundle vehicles.easygo 节严格解析后的样子：EasyGo 车端要灌的
// 地图与"世界 = 地图 + offset"的换算输入（mm / mrad）。
type Section struct {
	// MapExport 是地图档案相对世界 bundle 目录的路径。
	MapExport      string       `json:"map_export"`
	MapName        string       `json:"map_name"`
	OriginOffsetMM OriginOffset `json:"origin_offset_mm"`
}

// OriginOffset 是世界坐标 ↔ 车端地图坐标的偏移（mm）：世界 = 地图 + offset。
type OriginOffset struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// parseSection 严格解析车端节：字段多余、缺失都是错误。
func parseSection(raw assets.Section) (Section, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var section Section
	if err := dec.Decode(&section); err != nil {
		return Section{}, fmt.Errorf("parse vehicles.%s section: %w", Kind, err)
	}
	if strings.TrimSpace(section.MapExport) == "" {
		return Section{}, fmt.Errorf("vehicles.%s section map_export must not be empty", Kind)
	}
	if strings.TrimSpace(section.MapName) == "" {
		return Section{}, fmt.Errorf("vehicles.%s section map_name must not be empty", Kind)
	}
	return section, nil
}

// Factory 按会话造 EasyGo 车端 Port，并按 label 发现已在跑的车。
type Factory struct {
	Engine  docker.Engine
	Options Options
	// AppReady 是 WaitReady 的探活钩子（测试用）；nil 用真的 HTTP 探测。
	AppReady  func(ctx context.Context, apiPort int) error
	AssetsDir string
}

func (Factory) Kind() string { return Kind }

func (f Factory) New(_ context.Context, session contract.Session, robot contract.Robot, attachment orchestrator.Attachment, rawSection assets.Section) (orchestrator.VehiclePort, error) {
	link, err := linkFrom(attachment)
	if err != nil {
		return nil, err
	}
	vehicle := Vehicle{
		Engine:    f.Engine,
		Options:   f.Options,
		Session:   session,
		Robot:     robot,
		AppReady:  f.AppReady,
		AssetsDir: f.AssetsDir,
		Link:      link,
	}
	// 裸世界（无 bundle）没有车端节：不灌图、不定位，世界 SDF 交接照旧。
	if len(rawSection) > 0 {
		section, err := parseSection(rawSection)
		if err != nil {
			return nil, err
		}
		vehicle.Section = section
		vehicle.MapExportPath = filepath.Join(assets.WorldDir(f.AssetsDir, session.World), section.MapExport)
	}
	return vehicle, nil
}

func (f Factory) newVehicle(session contract.Session, robot contract.Robot, containerName string, link WorldLink) orchestrator.VehiclePort {
	return Vehicle{
		Engine:        f.Engine,
		Options:       f.Options,
		Session:       session,
		Robot:         robot,
		ContainerName: containerName,
		AppReady:      f.AppReady,
		AssetsDir:     f.AssetsDir,
		Link:          link,
	}
}

func (f Factory) Discover(ctx context.Context) ([]orchestrator.VehiclePort, error) {
	infos, err := f.Engine.List(ctx)
	if err != nil {
		return nil, err
	}
	ports := make([]orchestrator.VehiclePort, 0, len(infos))
	for _, info := range infos {
		if info.Labels[docker.LabelManaged] != "true" || info.Labels[docker.LabelRole] != docker.RoleApp {
			continue
		}
		robot, ok := robotFromLabels(info.Labels)
		if !ok {
			continue
		}
		ports = append(ports, f.newVehicle(contract.Session{}, robot, info.Name, WorldLink{}))
	}
	return ports, nil
}

func robotFromLabels(labels map[string]string) (contract.Robot, bool) {
	name := strings.TrimSpace(labels[vehicleRobotLabel])
	indexText := strings.TrimSpace(labels[vehicleIndexLabel])
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 1 || name != contract.RobotName(index) {
		return contract.Robot{}, false
	}
	robot, err := contract.NewRobot(index, contract.Pose{})
	if err != nil {
		return contract.Robot{}, false
	}
	return robot, true
}
