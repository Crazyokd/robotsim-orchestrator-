package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
)

type fakeOrch struct {
	started contract.StartConfig
	status  orchestrator.Status
}

func (f *fakeOrch) Start(_ context.Context, cfg contract.StartConfig) (orchestrator.Status, error) {
	f.started = cfg
	return orchestrator.Status{
		World: orchestrator.WorldStatus{
			Present: true,
			Running: true,
			Detail:  map[string]string{"image": "env"},
			Ready:   orchestrator.WorldReady{World: "map_world", Robots: []string{"robot1"}},
		},
		Vehicles: []orchestrator.VehicleStatus{{
			Name: "robot1", Running: true, Endpoint: "http://127.0.0.1:18189",
			Detail: map[string]string{"image": "runtime"},
		}},
		Connected: true,
	}, nil
}

func (f *fakeOrch) StopSession(context.Context, string) error { return nil }

func (f *fakeOrch) StatusSession(context.Context, string) (orchestrator.Status, error) {
	return f.status, nil
}

func (f *fakeOrch) Sessions(context.Context) ([]orchestrator.SessionSummary, error) {
	return nil, nil
}

func (f *fakeOrch) PoseSession(_ context.Context, _ string, robot int) (orchestrator.EntityPose, error) {
	return orchestrator.EntityPose{Robot: robot, Entity: contract.RobotName(robot), Pose: contract.Pose{X: 1.25, Y: -0.5, Z: 0.05}}, nil
}

func (f *fakeOrch) ResetSession(_ context.Context, _ string, robot int, pose contract.Pose) (orchestrator.EntityPose, error) {
	return orchestrator.EntityPose{Robot: robot, Entity: contract.RobotName(robot), Pose: pose}, nil
}

func (f *fakeOrch) CallSession(_ context.Context, _ string, robot int, request []byte) ([]byte, error) {
	if robot != 1 {
		return nil, fmt.Errorf("unexpected robot %d", robot)
	}
	return append([]byte(nil), request...), nil
}

// fakePortFlags 扮演 Port 的 RegisterFlags：证明 start 的 FlagSet 对组合根开放。
func fakePortFlags(target *string) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		fs.StringVar(target, "app-image", "", "")
	}
}

func TestStartPassesConfig(t *testing.T) {
	orch := &fakeOrch{}
	var out bytes.Buffer
	var appImage string
	err := Run([]string{
		"start", "--robots", "1", "--world", "simple",
		"--poses", "7,8,1.5", "--app-image", "runtime-sim:v1",
	}, bytes.NewReader(nil), &out, &bytes.Buffer{}, orch, fakePortFlags(&appImage))
	if err != nil {
		t.Fatal(err)
	}
	if orch.started.World != "simple" || orch.started.Robots != 1 {
		t.Fatalf("cfg=%+v", orch.started)
	}
	if len(orch.started.Poses) != 1 || orch.started.Poses[0] != (contract.Pose{X: 7, Y: 8, Z: contract.DefaultSpawnZ, Yaw: 1.5}) {
		t.Fatalf("poses=%+v", orch.started.Poses)
	}
	// Port 的 flag 不经 CLI 的手，直接填进 Port 的 Options。
	if appImage != "runtime-sim:v1" {
		t.Fatalf("app-image=%q", appImage)
	}
	if !strings.Contains(out.String(), "endpoint=http://127.0.0.1:18189") {
		t.Fatalf("output=%s", out.String())
	}
	if !strings.Contains(out.String(), "image=runtime") {
		t.Fatalf("detail must be printed verbatim: %s", out.String())
	}
}

func TestStartRejectsAnUnknownFlag(t *testing.T) {
	err := Run([]string{"start", "--world", "simple", "--app-image", "x"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{})
	if err == nil {
		t.Fatal("flags a port did not register must fail")
	}
}

func TestStartPassesExplicitKinds(t *testing.T) {
	orch := &fakeOrch{}
	var out bytes.Buffer
	err := Run([]string{
		"start", "--world", "noworld",
		"--world-kind", "fake-world", "--vehicle-kind", "fake",
	}, bytes.NewReader(nil), &out, &bytes.Buffer{}, orch)
	if err != nil {
		t.Fatal(err)
	}
	if orch.started.WorldKind != "fake-world" || orch.started.VehicleKind != "fake" {
		t.Fatalf("kinds not passed through: %+v", orch.started)
	}
}

func TestUnknownCommandFails(t *testing.T) {
	if err := Run([]string{"logs"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestPosePrintsEntityLine(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"pose", "--robot", "1"}, bytes.NewReader(nil), &out, &bytes.Buffer{}, &fakeOrch{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "robot=1 entity=robot1 x=1.25 y=-0.5 z=0.05 yaw=0\n" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestPoseRequiresRobot(t *testing.T) {
	if err := Run([]string{"pose"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestResetRequiresCoordinates(t *testing.T) {
	if err := Run([]string{"reset", "--robot", "1", "--x", "1"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestResetPrintsWrittenPose(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"reset", "--robot", "1", "--x", "4", "--y", "0", "--yaw", "0"}, bytes.NewReader(nil), &out, &bytes.Buffer{}, &fakeOrch{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "robot=1 entity=robot1 x=4 y=0") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestCallWritesTheRequestBackOut(t *testing.T) {
	request := "POST /api/v6/mapping/start HTTP/1.1\r\nHost: vehicle\r\nContent-Length: 2\r\n\r\n{}"
	var out bytes.Buffer
	if err := Run([]string{"call", "--robot", "1"}, strings.NewReader(request), &out, &bytes.Buffer{}, &fakeOrch{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != request {
		t.Fatalf("output=%q", out.String())
	}
}

func TestCallRejectsFlagsAndAnEmptyRequest(t *testing.T) {
	if err := Run([]string{"call", "--robot", "1", "--method", "GET"}, strings.NewReader("GET / HTTP/1.1\r\n\r\n"), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
	if err := Run([]string{"call", "--robot", "1"}, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{}, &fakeOrch{}); err == nil {
		t.Fatal("expected error")
	}
}
