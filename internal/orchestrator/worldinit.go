package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
)

// worldInit 把一个世界的 bundle 落成确定性的运行态——与环境里的世界进程
// （世界 Port 的就绪报告 + 实体回查）合起来构成完整的 world init。
//
// 阶段：读 bundle → 与世界名对齐 → 每台车一次 Provision（站点、出生位姿）。
// 任一步不满足即返回错误：编排层非 0 退出并清理容器，不留半成品世界。
// 框架不解释 Provisioning 的内容：地图怎么灌、坐标怎么换算都是车端 Port 的事。
//
// 裸世界（没有 bundle.json）允许存在，它只有"世界就绪"那一段。
// 目录里有资产却没有 manifest 不行：bundle 已经不完整，宁可失败也不猜。
func (o Orchestrator) worldInit(
	ctx context.Context,
	session contract.Session,
	vehicles []VehiclePort,
	bundle *assets.Bundle,
) error {
	if len(vehicles) != len(session.Robots) {
		return fmt.Errorf("world init: %d vehicles for %d robots", len(vehicles), len(session.Robots))
	}
	if bundle == nil {
		return nil
	}
	for i, vehicle := range vehicles {
		if err := vehicle.Provision(ctx, Provisioning{
			Stations: bundle.Stations,
			Pose:     session.Robots[i].Pose,
		}); err != nil {
			return err
		}
	}
	return nil
}

// loadBundle 读世界的 manifest：目录里没有 bundle.json 返回 (nil, nil)（裸世界）；
// 目录里躺着资产却没有 manifest 是错误——bundle 已不完整，宁可失败也不猜。
func (o Orchestrator) loadBundle(world string) (*assets.Bundle, error) {
	dir := assets.WorldDir(o.assetsDir, world)
	bundle, err := assets.LoadBundle(dir)
	switch {
	case err == nil:
		if err := bundle.ValidateForWorld(world); err != nil {
			return nil, err
		}
		return &bundle, nil
	case errors.Is(err, os.ErrNotExist):
		entries, statErr := os.ReadDir(dir)
		if statErr == nil && len(entries) > 0 {
			return nil, fmt.Errorf("world %s ships assets but no bundle.json manifest", world)
		}
		return nil, nil
	default:
		return nil, err
	}
}

// applyBundlePoses 用 bundle 的 initial_poses 覆盖 session 的出生点（缺省沿用 stride）。
// bundle 位姿就是世界系 m / rad，与 session 同一坐标系，直接采用。
// --poses 与 bundle initial_poses 同给是错误：两套来源并存必有一套被静默忽略。
func applyBundlePoses(cfg *contract.StartConfig, bundle *assets.Bundle) error {
	if bundle == nil || len(bundle.InitialPoses) == 0 {
		return nil
	}
	if len(cfg.Poses) > 0 {
		return fmt.Errorf(
			"world bundle %s declares initial poses; --poses must not be given at the same time",
			bundle.World,
		)
	}
	poses := make([]contract.Pose, cfg.Robots)
	for index := 1; index <= cfg.Robots; index++ {
		declared, ok := bundle.PoseFor(index)
		if !ok {
			return fmt.Errorf(
				"world bundle %s declares no initial pose for robot %d while robots=%d",
				bundle.World, index, cfg.Robots,
			)
		}
		poses[index-1] = declared
	}
	cfg.Poses = poses
	return nil
}
