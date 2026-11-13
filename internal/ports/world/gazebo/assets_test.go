package gazebo

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
)

func worldArchive(files map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for name, body := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Mode: 0o644}); err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestWorldStartUsesTheDeclaredSDFFromItsImage(t *testing.T) {
	raw, err := worldArchive(map[string]string{
		"simenv/worlds/simple/custom/scene.sdf": `<sdf><world name="custom_scene"/></sdf>`,
		"simenv/models/prop/mesh.dae":           "mesh from environment image",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}, imageArchive: raw}
	world := DockerWorld{Engine: engine, Options: Options{EnvImage: "world:tag"}}
	links, err := world.Start(context.Background(), twoRobotSession(t), assets.Section(`{"sdf":"custom/scene.sdf"}`))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(engine.started[0].Command, " ")
	if !strings.Contains(args, "--world-sdf /opt/simenv/worlds/simple/custom/scene.sdf") {
		t.Fatalf("declared SDF never reached world process: %s", args)
	}
	for _, link := range links {
		directory, sdf, copied := link.(Attachment).WorldAssets()
		if directory != "/opt" || sdf != "/opt/simenv/worlds/simple/custom/scene.sdf" || !bytes.Equal(copied, raw) {
			t.Fatal("vehicle attachment does not carry the actual world image assets")
		}
	}
}

func TestWorldPreflightRejectsInvalidImageAssets(t *testing.T) {
	for _, tc := range []struct{ sdf, body string }{
		{"../outside.sdf", `<sdf><world name="wrong"/></sdf>`},
		{"/outside.sdf", `<sdf><world name="wrong"/></sdf>`},
		{"custom/scene.sdf", `not XML`},
		{"custom/scene.sdf", `<sdf><world name="broken">`},
		{"custom/scene.sdf", `<sdf><world name="broken"><bad></sdf>`},
		{"custom/scene.sdf", `<sdf><world name="first"/></sdf><sdf><world name="second"/></sdf>`},
		{"custom/scene.sdf", `junk<sdf><world name="named"/></sdf>`},
		{"custom/scene.sdf", `<sdf><world name="named"/></sdf><?xml version="1.0"?>`},
		{"missing.sdf", ``},
	} {
		t.Run(tc.sdf, func(t *testing.T) {
			raw, err := worldArchive(map[string]string{"simenv/worlds/simple/custom/scene.sdf": tc.body})
			if err != nil {
				t.Fatal(err)
			}
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}, imageArchive: raw}
			world := DockerWorld{Engine: engine, Options: Options{EnvImage: "world:tag"}}
			section, err := json.Marshal(Section{SDF: tc.sdf})
			if err != nil {
				t.Fatal(err)
			}
			if err := world.ValidateStart(context.Background(), twoRobotSession(t), section); err == nil {
				t.Fatal("invalid assets passed preflight")
			}
			if len(engine.started) != 0 {
				t.Fatal("preflight started the world")
			}
		})
	}
}

func TestWorldRejectsArchivePathsOutsideTheAssetTree(t *testing.T) {
	for _, entry := range []*tar.Header{
		{Name: "../outside", Mode: 0o644},
		{Name: "other/tree", Mode: 0o644},
		{Name: "simenv/models/link", Typeflag: tar.TypeSymlink, Linkname: "../../.."},
		{Name: "simenv/models/link", Typeflag: tar.TypeLink, Linkname: "/opt/sros/bin/app"},
		{Name: "simenv/models/link", Typeflag: tar.TypeSymlink, Linkname: "mesh.dae"},
		{Name: "simenv/models/link", Typeflag: tar.TypeLink, Linkname: "simenv/models/mesh.dae"},
	} {
		t.Run(entry.Name+entry.Linkname, func(t *testing.T) {
			var buf bytes.Buffer
			w := tar.NewWriter(&buf)
			if err := w.WriteHeader(entry); err != nil {
				t.Fatal(err)
			}
			body := `<sdf><world name="valid"/></sdf>`
			if err := w.WriteHeader(&tar.Header{Name: "simenv/worlds/simple/gazebo/world.sdf", Mode: 0o644, Size: int64(len(body))}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			engine := &fakeEngine{imageArchive: buf.Bytes()}
			world := DockerWorld{Engine: engine, Options: Options{EnvImage: "world:tag"}}
			if err := world.ValidateStart(context.Background(), twoRobotSession(t), nil); err == nil {
				t.Fatal("archive escaped its asset tree")
			}
		})
	}
}
