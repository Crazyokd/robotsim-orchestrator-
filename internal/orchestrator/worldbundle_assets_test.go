package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// 出货的 bundle 必须是自洽的：manifest 里的 world / map_name 必须与同目录下的
// `<world>.sdf` / `<world>.map_export` 对得上。这是"场景组与地图绑定"在仓内的最小可跑形态：
// 谁把地图换了、把站点搬错了世界，这里当场红。
func TestShippedWorldBundlesMatchTheirAssets(t *testing.T) {
	worldsDir := filepath.Join("..", "..", "deploy", "assets", "worlds")
	entries, err := os.ReadDir(worldsDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		world := name[:len(name)-len(".json")]
		if filepath.Ext(world) != ".world" {
			continue
		}
		world = world[:len(world)-len(".world")]
		seen++
		t.Run(world, func(t *testing.T) {
			bundle, err := contract.LoadWorldBundle(filepath.Join(worldsDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := bundle.ValidateForWorld(world); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(worldsDir, world+".sdf")); err != nil {
				t.Fatalf("world sdf missing: %v", err)
			}
			archive, err := os.ReadFile(filepath.Join(worldsDir, world+".map_export"))
			if err != nil {
				t.Fatalf("world map missing: %v", err)
			}
			mapName, err := worldMapName(archive)
			if err != nil {
				t.Fatal(err)
			}
			if mapName != bundle.MapName {
				t.Fatalf("manifest map_name %q does not match map archive %q", bundle.MapName, mapName)
			}
		})
	}
	if seen == 0 {
		t.Fatal("no world bundle manifests found")
	}
}

// arena 的 bundle 是"照现状抄下来"的：偏移和站点必须与测试侧一直在用的那一套一致，
// 否则 world init 一旦接管注入，坐标就会悄悄漂移。
func TestArenaBundleMatchesLongStandingCoordinates(t *testing.T) {
	bundle, err := contract.LoadWorldBundle(filepath.Join("..", "..", "deploy", "assets", "worlds", "simple.world.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.OriginOffsetMM.X != 1978.0 || bundle.OriginOffsetMM.Y != -3181.5 {
		t.Fatalf("origin offset drifted: %+v", bundle.OriginOffsetMM)
	}
	want := map[int][2]float64{
		1: {5820, 80}, 2: {-7960, -1080}, 3: {-15600, -7200}, 4: {6060, 13220},
		5: {12600, -6180}, 6: {12576, -3724}, 7: {12500, 13000}, 8: {12600, -6500},
	}
	if len(bundle.Stations) != len(want) {
		t.Fatalf("station count %d, want %d", len(bundle.Stations), len(want))
	}
	for id, coords := range want {
		station, ok := bundle.StationByID(id)
		if !ok {
			t.Fatalf("station %d missing", id)
		}
		if station.X != coords[0] || station.Y != coords[1] {
			t.Fatalf("station %d drifted to (%v,%v)", id, station.X, station.Y)
		}
	}
}
