package gazebo

import (
	"os"
	"path/filepath"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// 出货的 bundle 里凡带 worlds.gazebo 节的，节里声明的 sdf 文件必须真实存在——
// 世界描述文件被删掉、或节指向了别处，这里当场红，不用等起栈。
// 一个世界不必服务所有 kind：没有本 kind 的节就跳过，但至少一个世界要服务本 kind。
func TestShippedWorldsDeclareAnExistingSDF(t *testing.T) {
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
			raw, ok := bundle.Worlds[Kind]
			if !ok {
				t.Skipf("world %s does not serve kind %s", world, Kind)
			}
			checked++
			section, err := ParseSection(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(worldsDir, world, section.SDF)); err != nil {
				t.Fatalf("worlds.%s sdf %q: %v", Kind, section.SDF, err)
			}
		})
	}
	if checked == 0 {
		t.Fatalf("no shipped world serves kind %s", Kind)
	}
}
