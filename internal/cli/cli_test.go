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
