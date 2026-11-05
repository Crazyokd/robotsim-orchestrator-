package easygo

import (
	"os"
	"path/filepath"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// 出货的 bundle 必须是自洽的：vehicles.easygo 节声明的地图档案必须真实存在，
// 且档案里的地图主名与 map_name 对得上。这是"场景组与地图绑定"在仓内的最小可跑形态：
// 谁把地图换了、把站点搬错了世界，这里当场红。
func TestShippedWorldBundlesMatchTheirAssets(t *testing.T) {
	assetsRoot := filepath.Join("..", "..", "..", "..", "deploy", "assets")
	worldsDir := assets.WorldsRoot(assetsRoot)
	entries, err := os.ReadDir(worldsDir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		world := entry.Name()
		seen++
		t.Run(world, func(t *testing.T) {
			bundle, err := assets.LoadBundle(assets.WorldDir(assetsRoot, world))
			if err != nil {
				t.Fatal(err)
			}
			if err := bundle.ValidateForWorld(world); err != nil {
				t.Fatal(err)
			}
			raw, err := bundle.VehicleSection(Kind)
			if err != nil {
				t.Fatal(err)
			}
			section, err := parseSection(raw)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := os.ReadFile(filepath.Join(worldsDir, world, section.MapExport))
			if err != nil {
				t.Fatalf("world map missing: %v", err)
			}
			mapName, err := worldMapName(archive)
			if err != nil {
				t.Fatal(err)
			}
			if mapName != section.MapName {
				t.Fatalf("manifest map_name %q does not match map archive %q", section.MapName, mapName)
			}
		})
	}
	if seen == 0 {
		t.Fatal("no world bundle manifests found")
	}
}

// arena 的 bundle 是"照现状抄下来再换算"的：站点按世界系落盘，换算回车端地图系
// （map_mm = world_m*1000 - offset_mm）后必须与测试侧一直在用的那一套 mm 值一致，
// 否则 world init 一旦接管注入，坐标就会悄悄漂移。
func TestArenaBundleMatchesLongStandingCoordinates(t *testing.T) {
	assetsRoot := filepath.Join("..", "..", "..", "..", "deploy", "assets")
	bundle, err := assets.LoadBundle(assets.WorldDir(assetsRoot, "simple"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bundle.VehicleSection(Kind)
	if err != nil {
		t.Fatal(err)
	}
	section, err := parseSection(raw)
	if err != nil {
		t.Fatal(err)
	}
	if section.OriginOffsetMM.X != 1978.0 || section.OriginOffsetMM.Y != -3181.5 {
		t.Fatalf("origin offset drifted: %+v", section.OriginOffsetMM)
	}
	want := map[int]mapStation{
		1: {ID: 1, X: 5820, Y: 80}, 2: {ID: 2, X: -7960, Y: -1080},
		3: {ID: 3, X: -15600, Y: -7200}, 4: {ID: 4, X: 6060, Y: 13220},
		5: {ID: 5, X: 12600, Y: -6180}, 6: {ID: 6, X: 12576, Y: -3724},
		7: {ID: 7, X: 12500, Y: 13000}, 8: {ID: 8, X: 12600, Y: -6500},
	}
	if len(bundle.Stations) != len(want) {
		t.Fatalf("station count %d, want %d", len(bundle.Stations), len(want))
	}
	for _, station := range toMapStations(bundle.Stations, section.OriginOffsetMM) {
		expected, ok := want[station.ID]
		if !ok {
			t.Fatalf("unexpected station %d", station.ID)
		}
		if station != expected {
			t.Fatalf("station %d converts to %+v, want %+v", station.ID, station, expected)
		}
	}
}
