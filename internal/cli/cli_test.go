package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/world"
)

type fakeOrch struct {
	started contract.StartConfig
	status  orchestrator.Status
}

func (f *fakeOrch) Start(_ context.Context, cfg contract.StartConfig) (orchestrator.Status, error) {
	f.started = cfg
	return orchestrator.Status{
		World:     orchestrator.ContainerInfo{Name: contract.WorldContainer, Running: true, Image: cfg.EnvImage},
		Apps:      []orchestrator.AppStatus{{Name: "simenv-robot1", Running: true, APIPort: 18189, Image: cfg.AppImage}},
		Ready:     world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}},
		Connected: true,
	}, nil
}

func (f *fakeOrch) Stop(context.Context) error { return nil }

func (f *fakeOrch) Status(context.Context) (orchestrator.Status, error) {
	return f.status, nil
}

func TestStartRequiresAppImage(t *testing.T) {
	err := Run([]string{"start", "--robots", "1", "--world", "simple"}, &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStartPassesConfig(t *testing.T) {
	orch := &fakeOrch{}
	var out bytes.Buffer
	err := Run([]string{"start", "--robots", "1", "--world", "simple", "--app-image", "runtime-sim:v1", "--env-image", "simenv-world:local"}, &out, &bytes.Buffer{}, orch)
	if err != nil {
		t.Fatal(err)
	}
	if orch.started.AppImage != "runtime-sim:v1" || orch.started.World != "simple" {
		t.Fatalf("cfg=%+v", orch.started)
	}
	if !strings.Contains(out.String(), "18189") {
		t.Fatalf("output=%s", out.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if err := Run([]string{"logs"}, &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestStartParsesViewerRobotsPerFlag(t *testing.T) {
	orch := &fakeOrch{}
	err := Run([]string{
		"start", "--robots", "3", "--world", "simple", "--app-image", "runtime-sim:v1",
		"--rviz", "1,3", "--gz-gui", "2",
	}, &bytes.Buffer{}, &bytes.Buffer{}, orch)
	if err != nil {
		t.Fatal(err)
	}
	if len(orch.started.RvizRobots) != 2 || orch.started.RvizRobots[0] != 1 || orch.started.RvizRobots[1] != 3 {
		t.Fatalf("rviz robots=%v", orch.started.RvizRobots)
	}
	if len(orch.started.GzGuiRobots) != 1 || orch.started.GzGuiRobots[0] != 2 {
		t.Fatalf("gz-gui robots=%v", orch.started.GzGuiRobots)
	}
}

func TestStartParsesCameraRobots(t *testing.T) {
	orch := &fakeOrch{}
	err := Run([]string{
		"start", "--robots", "3", "--world", "simple", "--app-image", "runtime-sim:v1",
		"--cameras", "2,3",
	}, &bytes.Buffer{}, &bytes.Buffer{}, orch)
	if err != nil {
		t.Fatal(err)
	}
	if len(orch.started.CameraRobots) != 2 || orch.started.CameraRobots[0] != 2 || orch.started.CameraRobots[1] != 3 {
		t.Fatalf("camera robots=%v", orch.started.CameraRobots)
	}
	if orch.started.WantsCameras(1) || !orch.started.WantsCameras(2) {
		t.Fatal("WantsCameras mismatch")
	}
}

func TestStartDefaultsToNoViewers(t *testing.T) {
	orch := &fakeOrch{}
	err := Run([]string{"start", "--robots", "2", "--world", "simple", "--app-image", "runtime-sim:v1"}, &bytes.Buffer{}, &bytes.Buffer{}, orch)
	if err != nil {
		t.Fatal(err)
	}
	if len(orch.started.RvizRobots) != 0 || len(orch.started.GzGuiRobots) != 0 || len(orch.started.CameraRobots) != 0 {
		t.Fatalf("viewers=%v/%v cameras=%v", orch.started.RvizRobots, orch.started.GzGuiRobots, orch.started.CameraRobots)
	}
}

func TestStartRejectsBadViewerRobot(t *testing.T) {
	for _, arg := range []string{"0", "-1", "robot1", "1,x"} {
		err := Run([]string{
			"start", "--robots", "2", "--world", "simple", "--app-image", "runtime-sim:v1",
			"--rviz", arg,
		}, &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{})
		if err == nil {
			t.Fatalf("--rviz %q must fail", arg)
		}
	}
}
