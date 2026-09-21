package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/world"
)

type fakeEngine struct {
	started []ContainerSpec
	stopped []string
	infos   map[string]ContainerInfo
	ready   string
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	return "sha256:" + ref, nil
}

func (f *fakeEngine) Start(_ context.Context, spec ContainerSpec) error {
	f.started = append(f.started, spec)
	f.infos[spec.Name] = ContainerInfo{Name: spec.Name, Running: true, Image: spec.Image, ID: spec.Name + "-id"}
	return nil
}

func (f *fakeEngine) Stop(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	info := f.infos[name]
	info.Running = false
	f.infos[name] = info
	return nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (ContainerInfo, error) {
	info, ok := f.infos[name]
	if !ok {
		return ContainerInfo{}, ErrNotFound
	}
	return info, nil
}

func (f *fakeEngine) Exec(_ context.Context, name string, cmd []string) (string, error) {
	if name == contract.WorldContainer && strings.Contains(strings.Join(cmd, " "), contract.WorldReadyPath) {
		return f.ready, nil
	}
	return "", nil
}

func (f *fakeEngine) List(_ context.Context) ([]ContainerInfo, error) {
	var out []ContainerInfo
	for _, info := range f.infos {
		out = append(out, info)
	}
	return out, nil
}

func TestStartWorldThenApp(t *testing.T) {
	report, _ := json.Marshal(world.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	engine := &fakeEngine{infos: map[string]ContainerInfo{}, ready: string(report)}
	orch := Orchestrator{Engine: engine}
	cfg := contract.StartConfig{
		Robots: 1, World: "simple", AppImage: "runtime-sim:tag", EnvImage: "simenv-world:local",
		Partition: contract.DefaultPartition, DataRoot: t.TempDir(),
	}
	status, err := orch.Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if status.World.Image != "simenv-world:local" || len(status.Apps) != 1 {
		t.Fatalf("status=%+v", status)
	}
	if status.Apps[0].APIPort != 18189 {
		t.Fatalf("api=%d", status.Apps[0].APIPort)
	}
	if len(engine.started) != 2 {
		t.Fatalf("started=%d", len(engine.started))
	}
	worldSpec := engine.started[0]
	if worldSpec.Name != contract.WorldContainer || worldSpec.Network != "host" {
		t.Fatalf("world spec=%+v", worldSpec)
	}
	if containsAny(worldSpec.Command, "easygo-app", "slam", "nav2") {
		t.Fatal("world command must not start the vehicle stack")
	}
	appSpec := engine.started[1]
	if appSpec.Name != "simenv-robot1" {
		t.Fatalf("app name=%s", appSpec.Name)
	}
	joined := strings.Join(appSpec.Command, " ")
	if !strings.Contains(joined, "start_gazebo:=false") || !strings.Contains(joined, "use_gz_gui:=false") {
		t.Fatalf("app command=%v", appSpec.Command)
	}
	if appSpec.Env["GZ_PARTITION"] != "easygo_sim" || appSpec.Env["ROS_DOMAIN_ID"] != "11" {
		t.Fatalf("app env=%v", appSpec.Env)
	}
}

func TestStartRejectsEmptyAppImage(t *testing.T) {
	orch := Orchestrator{Engine: &fakeEngine{infos: map[string]ContainerInfo{}}}
	_, err := orch.Start(context.Background(), contract.StartConfig{Robots: 1, World: "simple", EnvImage: "env"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStopStopsWorldAndApps(t *testing.T) {
	engine := &fakeEngine{infos: map[string]ContainerInfo{
		contract.WorldContainer: {Name: contract.WorldContainer, Running: true},
		"simenv-robot1":         {Name: "simenv-robot1", Running: true},
	}}
	orch := Orchestrator{Engine: engine}
	if err := orch.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(engine.stopped) != 2 {
		t.Fatalf("stopped=%v", engine.stopped)
	}
}

func containsAny(items []string, needles ...string) bool {
	blob := strings.Join(items, " ")
	for _, n := range needles {
		if strings.Contains(blob, n) {
			return true
		}
	}
	return false
}
