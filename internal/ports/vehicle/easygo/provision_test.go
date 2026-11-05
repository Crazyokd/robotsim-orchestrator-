package easygo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

// 灌图输入来自 bundle 车端节声明的档案路径；没配节的车端（裸世界）灌图直接失败。
func TestProvisionRequiresAMapExportSection(t *testing.T) {
	vehicle := Vehicle{
		Session: contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}},
		Robot:   contract.Robot{Index: 1, Name: "robot1"},
	}
	err := vehicle.Provision(context.Background(), orchestrator.Provisioning{})
	if err == nil || !strings.Contains(err.Error(), "no vehicles.easygo section") {
		t.Fatalf("expected a missing-section error, got %v", err)
	}
}

// 地图主名与 bundle 声明不一致直接失败：世界换了而地图没跟上，宁可启动失败。
func TestProvisionFailsWhenTheArchiveHoldsAnotherMap(t *testing.T) {
	vehicle := provisionVehicle(t, map[string]string{"sros/map/arena_world0.pgm": "pgm"}, "other_world")
	err := vehicle.Provision(context.Background(), orchestrator.Provisioning{})
	if err == nil || !strings.Contains(err.Error(), `holds "arena_world" but bundle declares "other_world"`) {
		t.Fatalf("expected map-name mismatch, got %v", err)
	}
}

func TestProvisionFailsWhenTheArchiveHasNoPrimaryLayer(t *testing.T) {
	vehicle := provisionVehicle(t, map[string]string{"sros/map/thing.json": "{}"}, "arena_world")
	err := vehicle.Provision(context.Background(), orchestrator.Provisioning{})
	if err == nil || !strings.Contains(err.Error(), "no primary layer") {
		t.Fatalf("expected a map-parse error, got %v", err)
	}
}

// provisionVehicle 造一台带车端节与地图档案的 Vehicle：档案落在临时目录，
// 经 MapExportPath 递给 Provision——与 Factory.New 的产物同形。
func provisionVehicle(t *testing.T, files map[string]string, mapName string) Vehicle {
	t.Helper()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "world.map_export")
	if err := os.WriteFile(archivePath, mapExportArchive(t, files), 0o644); err != nil {
		t.Fatal(err)
	}
	return Vehicle{
		Session:       contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}},
		Robot:         contract.Robot{Index: 1, Name: "robot1"},
		Section:       Section{MapName: mapName},
		MapExportPath: archivePath,
	}
}
