package assets

import (
	"os"
	"path/filepath"
	"testing"
)

// 出货的世界目录必须个个是自洽的 bundle：manifest 解析得了、world 与目录名一致、
// worlds / vehicles 节都在。节内部的 schema 与文件对账归各 Port 自己的资产测试。
func TestShippedWorldDirsAreValidBundles(t *testing.T) {
	assetsRoot := filepath.Join("..", "..", "deploy", "assets")
	entries, err := os.ReadDir(WorldsRoot(assetsRoot))
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("worlds/ holds a non-bundle entry %s: worlds are directories now", entry.Name())
		}
		world := entry.Name()
		seen++
		t.Run(world, func(t *testing.T) {
			bundle, err := LoadBundle(WorldDir(assetsRoot, world))
			if err != nil {
				t.Fatal(err)
			}
			if err := bundle.ValidateForWorld(world); err != nil {
				t.Fatal(err)
			}
			if len(bundle.Worlds) == 0 {
				t.Fatal("bundle declares no worlds section")
			}
			if len(bundle.Vehicles) == 0 {
				t.Fatal("bundle declares no vehicles section")
			}
		})
	}
	if seen == 0 {
		t.Fatal("no shipped worlds found")
	}
}
