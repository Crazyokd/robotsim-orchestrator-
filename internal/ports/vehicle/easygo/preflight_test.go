package easygo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
)

func TestPreflightRejectsInvalidVehicleInputsWithoutStarting(t *testing.T) {
	for _, mode := range []string{"image", "registry", "viewer", "slot", "missing map", "wrong map"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
			factory := Factory{Engine: engine, AssetsDir: dir, Options: Options{AppImage: "runtime-sim:tag", DataRoot: filepath.Join(dir, "data")}}
			var raw []byte
			switch mode {
			case "image":
				engine.imageErr = errors.New("image absent")
			case "registry":
				engine.imageFiles = map[string]string{appRegistryDir + "/" + syncRegistry: "items: []"}
			case "viewer":
				factory.Options.RvizRobots = []int{2}
			case "slot":
				factory.Slot = 3
			default:
				var err error
				raw, err = json.Marshal(Section{MapExport: "easygo/test.map_export", MapName: "expected"})
				if err != nil {
					t.Fatal(err)
				}
				if mode == "wrong map" {
					path := filepath.Join(dir, "worlds", "simple", "easygo", "test.map_export")
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, mapExportArchive(t, map[string]string{"sros/map/other0.pgm": "pgm"}), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := factory.ValidateStart(context.Background(), oneRobotSession(t), raw); err == nil {
				t.Fatal("invalid start passed preflight")
			}
			if len(engine.started) != 0 {
				t.Fatal("preflight started a container")
			}
			if _, err := os.Stat(factory.Options.DataRoot); !os.IsNotExist(err) {
				t.Fatalf("preflight changed data root: %v", err)
			}
		})
	}
}

func TestDiscoveryAndStopUseOnlyTheSelectedSession(t *testing.T) {
	alpha, beta := appLabels("robot1", 1), appLabels("robot1", 1)
	alpha[sessionLabel], beta[sessionLabel] = "alpha", "beta"
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{
		"alpha-container": {Name: "alpha-container", Labels: alpha, Running: true},
		"beta-container":  {Name: "beta-container", Labels: beta, Running: true},
	}}
	factory := Factory{Engine: engine, SessionName: "alpha", Slot: 2}
	ports, err := factory.Discover(context.Background())
	if err != nil || len(ports) != 1 {
		t.Fatalf("discovered=%v, error=%v", ports, err)
	}
	status, err := ports[0].Status(context.Background())
	if err != nil || !strings.Contains(status.Endpoint, ":38189") {
		t.Fatalf("discovery lost slot: %+v, error=%v", status, err)
	}
	if err := ports[0].Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 1 || engine.stopped[0] != "alpha-container" || !engine.infos["beta-container"].Running {
		t.Fatalf("stop affected another session: %v", engine.stopped)
	}
}

func TestPreflightRejectsStoredParameterDataFromAnotherSlot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "alpha", "robot1")
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app-config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", "cfg.db3"), []byte("existing database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app-config", "function.sync.yaml"), []byte(fakeSyncRegistry), 0o644); err != nil {
		t.Fatal(err)
	}
	factory := Factory{Engine: &fakeEngine{}, SessionName: "alpha", Slot: 1, Options: Options{AppImage: "runtime:tag", DataRoot: dir}}
	if err := factory.ValidateStart(context.Background(), oneRobotSession(t), nil); err == nil || !strings.Contains(err.Error(), "another slot") {
		t.Fatalf("stored cold parameter must not be silently reused: %v", err)
	}
	factory.Slot = 0
	if err := factory.ValidateStart(context.Background(), oneRobotSession(t), nil); err != nil {
		t.Fatalf("original slot must remain usable: %v", err)
	}
}

func TestPreflightAllowsNamedVolumeMultipleRobotsAndSlots(t *testing.T) {
	engine := &fakeEngine{}
	factory := Factory{Engine: engine, SessionName: "beta", Slot: 1, Options: Options{AppImage: "runtime:tag", DataVolumePrefix: "ci"}}
	session := contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}, {Index: 2, Name: "robot2"}}}
	if err := factory.ValidateStart(context.Background(), session, nil); err != nil {
		t.Fatalf("named volume preflight rejected supported multi-robot slot: %v", err)
	}
}

func TestWorldAndVehicleContainerNamesCannotCollide(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	session := oneRobotSession(t)
	world := gazebo.DockerWorld{Engine: engine, Options: gazebo.Options{EnvImage: "world:tag"}, SessionName: "alpha-robot1"}
	if _, err := world.Start(context.Background(), session, nil); err != nil {
		t.Fatal(err)
	}
	vehicle := Vehicle{Robot: session.Robots[0], SessionName: "world-alpha"}
	if vehicle.containerName() == engine.started[0].Name {
		t.Fatalf("two sessions share a container name: %s", vehicle.containerName())
	}
}
