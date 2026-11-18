package gazebo

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo/process"
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

// CAD 几何及所有模型文件必须保持同一 pinned EasyGo 修订的原始内容。
func TestCADNavigationWorldPreservesPinnedGeometry(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "deploy", "assets")
	bundle, err := assets.LoadBundle(filepath.Join(root, "worlds", "caterpiller_cad"))
	if err != nil {
		t.Fatal(err)
	}
	section, err := ParseSection(bundle.Worlds[Kind])
	if err != nil || section.SDF != "gazebo/world.sdf" || bundle.SourceSHA != "05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1" {
		t.Fatalf("CAD world source binding drifted: %+v, error %v", bundle, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "worlds", "caterpiller_cad", "gazebo", "world.sdf"))
	if err != nil {
		t.Fatal(err)
	}
	name, err := process.SDFWorldName(raw)
	if err != nil || name != "map_world" {
		t.Fatalf("CAD world name %q, error %v", name, err)
	}
	sums, err := os.ReadFile(filepath.Join(root, "worlds", "caterpiller_cad", "gazebo", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !filepath.IsLocal(fields[1]) {
			t.Fatalf("invalid source checksum %q", line)
		}
		body, err := os.ReadFile(filepath.Join(root, fields[1]))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != fields[0] {
			t.Fatalf("pinned EasyGo asset %s: SHA256 %s, want %s", fields[1], got, fields[0])
		}
		if filepath.Ext(fields[1]) != ".sdf" {
			continue
		}
		decoder := xml.NewDecoder(bytes.NewReader(body))
		for {
			token, err := decoder.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			text, ok := token.(xml.CharData)
			if !ok {
				continue
			}
			model, ok := strings.CutPrefix(strings.TrimSpace(string(text)), "model://")
			if !ok {
				continue
			}
			target := filepath.Join(root, "models", model)
			info, err := os.Stat(target)
			if err == nil && info.IsDir() {
				_, err = os.Stat(filepath.Join(target, "model.sdf"))
			}
			if err != nil {
				t.Fatalf("%s references missing model://%s: %v", fields[1], model, err)
			}
		}
	}
}
