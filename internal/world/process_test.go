package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type fakeTransport struct {
	started string
	clock   bool
	created []string
}

func (f *fakeTransport) StartSim(_ context.Context, worldPath string) error {
	f.started = worldPath
	return nil
}

func (f *fakeTransport) WaitClock(_ context.Context) error {
	f.clock = true
	return nil
}

func (f *fakeTransport) CreateEntity(_ context.Context, _, name, _ string, _ contract.Pose) error {
	f.created = append(f.created, name)
	return nil
}

func TestProcessSpawnsRobotsAndWritesReady(t *testing.T) {
	root := t.TempDir()
	worlds := filepath.Join(root, "worlds")
	if err := os.MkdirAll(worlds, 0o755); err != nil {
		t.Fatal(err)
	}
	worldFile := filepath.Join(worlds, "simple.sdf")
	if err := os.WriteFile(worldFile, []byte(`<sdf><world name="map_world"></world></sdf>`), 0o644); err != nil {
		t.Fatal(err)
	}
	model := filepath.Join(root, "model.sdf")
	if err := os.WriteFile(model, []byte(sourceSDF), 0o644); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "ready.json")
	transport := &fakeTransport{}
	proc := Process{
		AssetsRoot: worlds,
		ModelPath:  model,
		ReadyPath:  ready,
		Transport:  transport,
		WorldName:  "simple",
		Bodies:     []Body{{Name: "robot1", Pose: contract.Pose{Z: 0.05}}},
		Hold:       func(context.Context) error { return nil },
	}
	if err := proc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if transport.started != worldFile || !transport.clock {
		t.Fatalf("transport=%+v", transport)
	}
	if len(transport.created) != 1 || transport.created[0] != "robot1" {
		t.Fatalf("created=%v", transport.created)
	}
	raw, err := os.ReadFile(ready)
	if err != nil {
		t.Fatal(err)
	}
	var report ReadyReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Clock || report.World != "map_world" || len(report.Robots) != 1 {
		t.Fatalf("report=%+v", report)
	}
}

func TestProcessMissingWorldFails(t *testing.T) {
	proc := Process{
		AssetsRoot: t.TempDir(),
		WorldName:  "missing",
		Transport:  &fakeTransport{},
	}
	if err := proc.Run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
