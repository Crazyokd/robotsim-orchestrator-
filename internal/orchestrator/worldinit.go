package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// worldInit 把一个世界的 bundle 落成确定性的运行态——与环境里的世界进程
// （internal/world：世界就绪 + 实体回查）合起来构成完整的 world init。
//
// 阶段：读 bundle → 与世界名对齐 → 地图主名对齐 → 灌地图 → 注站点 → 放车定位。
// 任一步不满足即返回错误：编排层非 0 退出并清理容器，不留半成品世界。
//
// 裸世界（既没有地图也没有 manifest）允许存在，它只有"世界就绪"那一段。
// 地图与 manifest 必须成对出现：只有一个说明 bundle 已经不完整，宁可失败也不猜。
func (o Orchestrator) worldInit(
	ctx context.Context,
	session contract.Session,
	vehicles []VehiclePort,
	bundle *contract.WorldBundle,
) (string, error) {
	if len(vehicles) != len(session.Robots) {
		return "", fmt.Errorf("world init: %d vehicles for %d robots", len(vehicles), len(session.Robots))
	}
	if bundle == nil {
		return "", nil
	}
	archive, err := os.ReadFile(o.worldMapExportPath(session.World))
	if err != nil {
		return "", fmt.Errorf(
			"world %s declares map %q but ships no map archive: %w", session.World, bundle.MapName, err,
		)
	}
	mapName, err := worldMapName(archive)
	if err != nil {
		return "", fmt.Errorf("world %s map: %w", session.World, err)
	}
	if mapName != bundle.MapName {
		return "", fmt.Errorf(
			"world %s: map archive holds %q but bundle declares %q", session.World, mapName, bundle.MapName,
		)
	}
	for _, vehicle := range vehicles {
		if err := vehicle.SeedMap(ctx, archive); err != nil {
			return "", err
		}
	}
	for _, vehicle := range vehicles {
		if err := vehicle.SeedStations(ctx, mapName, bundle.Stations); err != nil {
			return "", err
		}
	}
	for i, vehicle := range vehicles {
		pose := session.Robots[i].Pose
		if declared, ok := bundle.PoseFor(session.Robots[i].Index); ok {
			pose = contract.Pose{X: declared.X, Y: declared.Y, Z: declared.Z, Yaw: declared.Yaw}
		}
		if err := vehicle.Localize(ctx, mapName, mapPose(pose, *bundle.OriginOffsetMM)); err != nil {
			return "", err
		}
	}
	return mapName, nil
}

func (o Orchestrator) worldBundlePath(world string) string {
	return filepath.Join(o.assetsPath(), "worlds", world+".world.json")
}

// loadWorldBundle 读世界的 manifest：文件不存在返回 (nil, nil)（裸世界）；
// 地图与 manifest 必须成对出现——只有一个说明 bundle 已不完整，宁可失败也不猜。
func (o Orchestrator) loadWorldBundle(world string) (*contract.WorldBundle, error) {
	bundle, err := contract.LoadWorldBundle(o.worldBundlePath(world))
	switch {
	case err == nil:
		if err := bundle.ValidateForWorld(world); err != nil {
			return nil, err
		}
		return &bundle, nil
	case errors.Is(err, os.ErrNotExist):
		if _, mapErr := os.Stat(o.worldMapExportPath(world)); mapErr == nil {
			return nil, fmt.Errorf("world %s ships a map but no %s.world.json bundle", world, world)
		}
		return nil, nil
	default:
		return nil, err
	}
}

// applyBundlePoses 用 bundle 的 initial_poses 覆盖 session 的出生点（缺省沿用 stride）。
func applyBundlePoses(cfg *contract.StartConfig, bundle *contract.WorldBundle) error {
	if bundle == nil || len(bundle.InitialPoses) == 0 {
		return nil
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
		poses[index-1] = contract.Pose{X: declared.X, Y: declared.Y, Z: declared.Z, Yaw: declared.Yaw}
	}
	cfg.Poses = poses
	return nil
}
