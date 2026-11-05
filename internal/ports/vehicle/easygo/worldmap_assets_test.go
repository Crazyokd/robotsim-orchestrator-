package easygo

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// 声明的坐标必须落在**出货地图真正观测过的地方**。
//
// 判定用"窗口信息占比"而不是单点取值：地图是灰度概率图，0 = 图没看过（unknown）或墙体，
// 单点取 0 不足以判定（实测 arena 自己的 initial_poses[2] 单点就是 0，而车在那个位置
// 定位 conf 99）。所以在声明点周围 0.5 m 的窗口里要求绝大多数格带信息。
// 实测两头：arena 最低 98.5%、corridor 最低 89.9%；把站点搬到图外会掉到 0%，判定力足够。
const (
	mapInfoRadiusMM = 500.0
	mapInfoMinRatio = 0.60
)

type worldMapGrid struct {
	width, height int
	zeroX, zeroY  float64
	resMM         float64
	pixels        []byte
}

// readWorldMapGrid 从 `.map_export`（tar.gz）里读出主层的 pgm。
func readWorldMapGrid(archive []byte) (worldMapGrid, error) {
	reader, err := mapArchiveReader(archive)
	if err != nil {
		return worldMapGrid{}, err
	}
	entries := tar.NewReader(reader)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return worldMapGrid{}, fmt.Errorf("read map export: %w", err)
		}
		stem, ok := strings.CutSuffix(path.Base(header.Name), "0.pgm")
		if !ok || stem == "" || isSublayerName(stem) {
			continue
		}
		raw, err := io.ReadAll(entries)
		if err != nil {
			return worldMapGrid{}, err
		}
		return parsePGM(raw)
	}
	return worldMapGrid{}, errors.New("map export has no primary pgm layer")
}

// parsePGM 读 easygo 导出的 pgm：注释行里依次是 zero_offset.x / zero_offset.y / 分辨率（cm）。
func parsePGM(raw []byte) (worldMapGrid, error) {
	var grid worldMapGrid
	var comments []float64
	pos := 0
	nextLine := func() (string, bool) {
		if pos >= len(raw) {
			return "", false
		}
		end := bytes.IndexByte(raw[pos:], '\n')
		if end < 0 {
			return "", false
		}
		line := string(raw[pos : pos+end])
		pos += end + 1
		return line, true
	}
	sawMaxval := false
	for !sawMaxval {
		line, ok := nextLine()
		if !ok {
			return worldMapGrid{}, errors.New("pgm header truncated")
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || trimmed == "P5":
			continue
		case strings.HasPrefix(trimmed, "#"):
			for _, field := range strings.Fields(strings.TrimPrefix(trimmed, "#")) {
				if value, err := strconv.ParseFloat(field, 64); err == nil {
					comments = append(comments, value)
				}
			}
		default:
			fields := strings.Fields(trimmed)
			if len(fields) == 2 {
				width, err1 := strconv.Atoi(fields[0])
				height, err2 := strconv.Atoi(fields[1])
				if err1 != nil || err2 != nil {
					return worldMapGrid{}, fmt.Errorf("bad dimensions %q", trimmed)
				}
				grid.width, grid.height = width, height
				continue
			}
			if len(fields) == 1 {
				if _, err := strconv.Atoi(fields[0]); err == nil {
					sawMaxval = true
				}
			}
		}
	}
	if len(comments) >= 3 {
		grid.zeroX, grid.zeroY = comments[0], comments[1]
		grid.resMM = comments[2] * 10.0 // 注释里的 2 表示 2 cm/px
	}
	if grid.resMM <= 0 {
		grid.resMM = 20.0
	}
	grid.pixels = raw[pos:]
	if grid.width == 0 || grid.height == 0 || len(grid.pixels) < grid.width*grid.height {
		return worldMapGrid{}, errors.New("pgm data incomplete")
	}
	return grid, nil
}

// infoRatio 返回地图坐标系里某点周围 0.5 m 窗口内"带信息"（像素值 != 0）的比例；
// 第二个返回值表示该点是否落在地图范围内。
func (g worldMapGrid) infoRatio(mapXMM, mapYMM float64) (float64, bool) {
	ix := int(g.zeroX + mapXMM/g.resMM + 0.5)
	iy := int(g.zeroY - mapYMM/g.resMM + 0.5)
	if ix < 0 || ix >= g.width || iy < 0 || iy >= g.height {
		return 0, false
	}
	radius := int(mapInfoRadiusMM/g.resMM + 0.5)
	x0, x1 := clampIndex(ix-radius, 0, g.width), clampIndex(ix+radius+1, 0, g.width)
	y0, y1 := clampIndex(iy-radius, 0, g.height), clampIndex(iy+radius+1, 0, g.height)
	informative, total := 0, 0
	for y := y0; y < y1; y++ {
		row := g.pixels[y*g.width : (y+1)*g.width]
		for x := x0; x < x1; x++ {
			total++
			if row[x] != 0 {
				informative++
			}
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(informative) / float64(total), true
}

func clampIndex(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// TestShippedWorldBundlesDeclareCoordinatesInsideTheirMaps 是"场景组与地图绑定"的第二道门：
// 站点与初始位姿必须落在出货地图观测过的区域里。bundle 坐标是世界系 m / rad，
// 判定前换算成地图系 mm（与 seedStations / mapPose 同一套公式）。地图被换掉、
// 站点搬到别的世界、或者新世界只建了半张图——这里当场红，不用等到起栈才发现。
func TestShippedWorldBundlesDeclareCoordinatesInsideTheirMaps(t *testing.T) {
	assetsRoot := filepath.Join("..", "..", "..", "..", "deploy", "assets")
	worldsDir := assets.WorldsRoot(assetsRoot)
	entries, err := os.ReadDir(worldsDir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		world := entry.Name()
		t.Run(world, func(t *testing.T) {
			bundle, err := assets.LoadBundle(assets.WorldDir(assetsRoot, world))
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
			archive, err := os.ReadFile(filepath.Join(worldsDir, world, section.MapExport))
			if err != nil {
				t.Fatalf("world map missing: %v", err)
			}
			grid, err := readWorldMapGrid(archive)
			if err != nil {
				t.Fatal(err)
			}
			check := func(label string, mapXMM, mapYMM float64) {
				t.Helper()
				ratio, inside := grid.infoRatio(mapXMM, mapYMM)
				if !inside {
					t.Fatalf("%s (%v,%v) is outside the shipped map (%dx%d px)", label, mapXMM, mapYMM,
						grid.width, grid.height)
				}
				if ratio < mapInfoMinRatio {
					t.Fatalf("%s (%v,%v) lands in an unmapped area: only %.0f%% of the surrounding %.0f mm carries information",
						label, mapXMM, mapYMM, ratio*100, mapInfoRadiusMM)
				}
				checked++
			}
			for _, station := range toMapStations(bundle.Stations, section.OriginOffsetMM) {
				check(fmt.Sprintf("station %d", station.ID), station.X, station.Y)
			}
			for _, pose := range bundle.InitialPoses {
				check(fmt.Sprintf("initial pose %d", pose.Index),
					pose.X*1000-section.OriginOffsetMM.X, pose.Y*1000-section.OriginOffsetMM.Y)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no declared coordinates checked")
	}
}
