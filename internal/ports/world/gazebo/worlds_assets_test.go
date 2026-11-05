package gazebo

import (
	"os"
	"path/filepath"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// 出货的每个 bundle 必须有 worlds.gazebo 节，且节里声明的 sdf 文件真实存在——
// 世界描述文件被删掉、或节指向了别处，这里当场红，不用等起栈。
func TestShippedWorldsDeclareAnExistingSDF(t *testing.T) {
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
			raw, err := bundle.WorldSection(Kind)
			if err != nil {
				t.Fatal(err)
			}
			section, err := ParseSection(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(worldsDir, world, section.SDF)); err != nil {
				t.Fatalf("worlds.%s sdf %q: %v", Kind, section.SDF, err)
			}
		})
	}
	if seen == 0 {
		t.Fatal("no shipped worlds found")
	}
}
