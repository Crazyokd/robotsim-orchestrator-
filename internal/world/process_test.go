package world

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type fakeTransport struct {
	started        string
	startedContext context.Context
	clock          bool
	created        []string
	createdPaths   []string
	server         chan error
	waitClock      func(ctx context.Context) error
	createErr      error
}

func (f *fakeTransport) StartSim(ctx context.Context, worldPath string) (<-chan error, error) {
	f.started = worldPath
	f.startedContext = ctx
	if f.server == nil {
		f.server = make(chan error, 1)
	}
	return f.server, nil
}

func (f *fakeTransport) WaitClock(ctx context.Context) error {
	if f.waitClock != nil {
		return f.waitClock(ctx)
	}
	f.clock = true
	return nil
}

func (f *fakeTransport) CreateEntity(_ context.Context, _, name, sdfPath string, _ contract.Pose) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, name)
	f.createdPaths = append(f.createdPaths, sdfPath)
	return nil
}

func fixture(t *testing.T) (worldsRoot, modelPath, readyPath string) {
	t.Helper()
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
	return worlds, model, filepath.Join(root, "ready.json")
}

func TestProcessSpawnsRobotsAndWritesReady(t *testing.T) {
	worldsRoot, modelPath, readyPath := fixture(t)
	transport := &fakeTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	readyRaw := make(chan []byte, 1)
	proc := Process{
		AssetsRoot: worldsRoot,
		ModelPath:  modelPath,
		ReadyPath:  readyPath,
		Transport:  transport,
		WorldName:  "simple",
		Bodies:     []Body{{Name: "robot1", TopicPrefix: "robot1", Pose: contract.Pose{Z: 0.05}}},
	}
	// Cancel as soon as the ready file appears; Run blocks until ctx is done.
	go func() {
		waitFile(t, readyPath, 5*time.Second)
		raw, err := os.ReadFile(readyPath)
		if err != nil {
			t.Error(err)
			readyRaw <- nil
			cancel()
			return
		}
		readyRaw <- raw
		cancel()
	}()
	if err := proc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if transport.started != filepath.Join(worldsRoot, "simple.sdf") || !transport.clock {
		t.Fatalf("transport=%+v", transport)
	}
	if len(transport.created) != 1 || transport.created[0] != "robot1" {
		t.Fatalf("created=%v", transport.created)
	}
	if len(transport.createdPaths) != 1 {
		t.Fatalf("created paths=%v", transport.createdPaths)
	}
	if _, err := os.Stat(transport.createdPaths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary SDF still exists: %v", err)
	}
	raw := <-readyRaw
	if raw == nil {
		t.Fatal("ready report was not captured")
	}
	var report ReadyReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if !report.Clock || report.World != "map_world" || len(report.Robots) != 1 {
		t.Fatalf("report=%+v", report)
	}
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready file still exists: %v", err)
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

func TestProcessExitsWhenServerDiesBeforeClock(t *testing.T) {
	worldsRoot, modelPath, readyPath := fixture(t)
	transport := &fakeTransport{
		server:    make(chan error, 1),
		waitClock: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	}
	proc := Process{
		AssetsRoot: worldsRoot,
		ModelPath:  modelPath,
		ReadyPath:  readyPath,
		Transport:  transport,
		WorldName:  "simple",
	}
	transport.server <- errors.New("gz sim exited: exit status 1\nmodel missing")
	err := proc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model missing") {
		t.Fatalf("expected server-death error, got: %v", err)
	}
	// ready.json must not be written.
	if _, statErr := os.Stat(readyPath); statErr == nil {
		t.Fatal("ready.json was written despite server death")
	}
}

func TestProcessExitsWhenServerDiesAfterReady(t *testing.T) {
	worldsRoot, modelPath, readyPath := fixture(t)
	transport := &fakeTransport{
		server: make(chan error, 1),
	}
	proc := Process{
		AssetsRoot: worldsRoot,
		ModelPath:  modelPath,
		ReadyPath:  readyPath,
		Transport:  transport,
		WorldName:  "simple",
		Bodies:     []Body{{Name: "robot1", TopicPrefix: "robot1", Pose: contract.Pose{Z: 0.05}}},
	}
	go func() {
		waitFile(t, readyPath, 5*time.Second)
		transport.server <- errors.New("gz sim exited: segfault")
	}()
	err := proc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "segfault") {
		t.Fatalf("expected server-death error, got: %v", err)
	}
	if _, statErr := os.Stat(readyPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("ready file still exists: %v", statErr)
	}
}

func TestProcessStopsCleanlyWhenCancelled(t *testing.T) {
	worldsRoot, modelPath, readyPath := fixture(t)
	transport := &fakeTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	proc := Process{
		AssetsRoot: worldsRoot,
		ModelPath:  modelPath,
		ReadyPath:  readyPath,
		Transport:  transport,
		WorldName:  "simple",
		Bodies:     []Body{{Name: "robot1", TopicPrefix: "robot1", Pose: contract.Pose{Z: 0.05}}},
	}
	go func() {
		waitFile(t, readyPath, 5*time.Second)
		cancel()
	}()
	if err := proc.Run(ctx); err != nil {
		t.Fatalf("expected clean exit, got: %v", err)
	}
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ready file still exists: %v", err)
	}
}

func TestProcessCancelsSimulationWhenEntityCreationFails(t *testing.T) {
	worldsRoot, modelPath, readyPath := fixture(t)
	transport := &fakeTransport{createErr: errors.New("spawn failed")}
	proc := Process{
		AssetsRoot: worldsRoot,
		ModelPath:  modelPath,
		ReadyPath:  readyPath,
		Transport:  transport,
		WorldName:  "simple",
		Bodies:     []Body{{Name: "robot1", TopicPrefix: "robot1", Pose: contract.Pose{Z: 0.05}}},
	}
	err := proc.Run(context.Background())
	close(transport.server)
	if err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("expected spawn error, got: %v", err)
	}
	select {
	case <-transport.startedContext.Done():
	case <-time.After(time.Second):
		t.Fatal("simulation context was not cancelled")
	}
}

func waitFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
