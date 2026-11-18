package easygo

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
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
			raw, ok := bundle.Vehicles[Kind]
			if !ok {
				t.Skipf("world %s does not serve kind %s", world, Kind)
			}
			section, err := parseSection(raw)
			if err != nil {
				t.Fatal(err)
			}
			archive := shippedMapArchive(t, worldsDir, world, section.MapExport)
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

// 只有固定数据镜像供给的 CAD 地图允许在源码 checkout 中缺席；出货镜像必须包含它。
func shippedMapArchive(t *testing.T, worldsDir, world, file string) []byte {
	t.Helper()
	archive, err := os.ReadFile(filepath.Join(worldsDir, world, file))
	if errors.Is(err, os.ErrNotExist) && world == "caterpiller_cad" && file == "easygo/0814caterpiller.map_export" {
		t.Skip("external CAD map payload not materialized; follow README extraction steps to verify the full map")
	}
	if err != nil {
		t.Fatalf("world map missing: %v", err)
	}
	return archive
}

func TestCADNavigationBundlePreservesOriginalMap(t *testing.T) {
	worldsDir := assets.WorldsRoot(filepath.Join("..", "..", "..", "..", "deploy", "assets"))
	bundle, err := assets.LoadBundle(filepath.Join(worldsDir, "caterpiller_cad"))
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
	if section.MapName != "0814caterpiller" || section.MapExport != "easygo/0814caterpiller.map_export" || section.OriginOffsetMM != (OriginOffset{}) {
		t.Fatalf("CAD navigation map binding drifted: %+v", section)
	}
	if len(bundle.Stations) != 0 {
		t.Fatal("CAD bundle must preserve the eight original stations imported with the map")
	}
	if len(bundle.InitialPoses) != 1 || bundle.InitialPoses[0].Index != 1 || bundle.InitialPoses[0].X != 0 || bundle.InitialPoses[0].Y != 0 || bundle.InitialPoses[0].Z != 0.05 || bundle.InitialPoses[0].Yaw != 0 {
		t.Fatalf("CAD navigation must spawn robot1 at the original world origin: %+v", bundle.InitialPoses)
	}
	archive := shippedMapArchive(t, worldsDir, "caterpiller_cad", section.MapExport)
	const wantHash = "6cbb3e95a8d2a8b6b02ee7f1a4c6696ac3d06d15e87064742b1b16ba868b23f4"
	if len(archive) != 193296560 || fmt.Sprintf("%x", sha256.Sum256(archive)) != wantHash {
		t.Fatal("CAD navigation requires the original full map export")
	}
	reader, err := mapArchiveReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	entries := tar.NewReader(reader)
	var names []string
	var stations []struct {
		ID   int     `json:"id"`
		Name string  `json:"name"`
		X    float64 `json:"pos.x"`
		Y    float64 `json:"pos.y"`
		Yaw  float64 `json:"pos.yaw"`
	}
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}
		name := path.Base(header.Name)
		names = append(names, strings.TrimPrefix(name, "0814caterpiller"))
		if name == "0814caterpiller.json" {
			var document struct {
				Data struct {
					Stations json.RawMessage `json:"station"`
				} `json:"data"`
			}
			if err := json.NewDecoder(entries).Decode(&document); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(document.Data.Stations, &stations); err != nil {
				t.Fatal(err)
			}
		}
	}
	slices.Sort(names)
	wantResources := []string{".edited_region", ".g2o", ".info", ".json", ".lom", ".nav.pgm", ".om", ".route.geojson", ".sbag", ".tiff", ".traj", "0.pgm", "1.pgm", "2.pgm", "_1.png", "_2.png", "_4.png", "_8.png", "_dynamic.tiff", "_update.tiff"}
	if !slices.Equal(names, wantResources) {
		t.Fatalf("CAD map resources %v, want %v", names, wantResources)
	}
	wantStations := []mapStation{
		{1, -12580.401428081972, 14483.058577891865, -3128.2594633219055},
		{2, -8116.158927867378, -2643.8092001621317, 1608.64986408744},
		{3, 23881.50354415816, -2661.0141968096937, 1598.214045376212},
		{4, 39803.092716992534, -28039.966696054424, 1454.5241174605412},
		{5, 17390.028068825697, -28117.85156695273, 1572.3152827344163},
		{6, 12847.739410444628, -22947.830722970823, -1618.3488199551848},
		{7, 2220.501996619929, -18563.401633882146, 3123.4865080236423},
		{8, 50634.679147135226, 7421.612853665346, -3091.8059490283736},
	}
	if len(stations) != len(wantStations) {
		t.Fatalf("CAD map station count %d, want 8", len(stations))
	}
	for i, station := range stations {
		if station.Name != fmt.Sprintf("站点%d", i+1) || (mapStation{station.ID, station.X, station.Y, station.Yaw}) != wantStations[i] {
			t.Fatalf("CAD map station %d drifted: %+v", i+1, station)
		}
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
