package orchestrator

import (
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestPoseReadsNamedEntityFromTheWorldContainer(t *testing.T) {
	engine := &controlEngine{
		ready: `{"world":"simple","clock":true,"robots":["robot1"]}`,
		exec: func(_ context.Context, name string, cmd []string) (string, error) {
			joined := strings.Join(cmd, " ")
			if name != contract.WorldContainer {
				t.Fatalf("exec target %s", name)
			}
			if strings.Contains(joined, contract.WorldReadyPath) {
				return `{"world":"simple","clock":true,"robots":["robot1"]}`, nil
			}
			if !strings.Contains(joined, "/world/simple/pose/info") {
				t.Fatalf("cmd=%v", cmd)
			}
			return `{"pose":[{"name":"robot1","position":{"x":2,"y":3,"z":0.05},"orientation":{"x":0,"y":0,"z":0,"w":1}}]}`, nil
		},
	}
	got, err := (Orchestrator{Engine: engine}).Pose(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Robot != 1 || got.Entity != "robot1" || got.Pose.X != 2 || got.Pose.Y != 3 {
		t.Fatalf("got=%+v", got)
	}
}

func TestPoseFailsWhenRobotIsNotInSession(t *testing.T) {
	engine := &controlEngine{ready: `{"world":"simple","clock":true,"robots":["robot1"]}`}
	if _, err := (Orchestrator{Engine: engine}).Pose(context.Background(), 2); err == nil {
		t.Fatal("expected error")
	}
}

func TestResetWritesThenReadsPose(t *testing.T) {
	var sawSet bool
	engine := &controlEngine{
		exec: func(_ context.Context, _ string, cmd []string) (string, error) {
			joined := strings.Join(cmd, " ")
			if strings.Contains(joined, contract.WorldReadyPath) {
				return `{"world":"simple","clock":true,"robots":["robot1"]}`, nil
			}
			if strings.Contains(joined, "/set_pose") {
				sawSet = true
				if !strings.Contains(joined, `name: "robot1"`) || !strings.Contains(joined, "x: 4") {
					t.Fatalf("set cmd=%v", cmd)
				}
				return "data: true", nil
			}
			return `{"pose":[{"name":"robot1","position":{"x":4,"y":0,"z":0.05},"orientation":{"x":0,"y":0,"z":0,"w":1}}]}`, nil
		},
	}
	got, err := (Orchestrator{Engine: engine}).Reset(context.Background(), 1, contract.Pose{X: 4, Z: contract.DefaultSpawnZ})
	if err != nil {
		t.Fatal(err)
	}
	if !sawSet || got.Pose.X != 4 {
		t.Fatalf("sawSet=%t got=%+v", sawSet, got)
	}
}

type controlEngine struct {
	ready string
	exec  func(context.Context, string, []string) (string, error)
}

func (c *controlEngine) ImageID(context.Context, string) (string, error) { return "", nil }
func (c *controlEngine) ReadImageFile(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (c *controlEngine) Start(context.Context, ContainerSpec) error { return nil }
func (c *controlEngine) Stop(context.Context, string) error         { return nil }
func (c *controlEngine) Inspect(context.Context, string) (ContainerInfo, error) {
	return ContainerInfo{}, nil
}
func (c *controlEngine) List(context.Context) ([]ContainerInfo, error) { return nil, nil }
func (c *controlEngine) Exec(ctx context.Context, name string, cmd []string) (string, error) {
	if c.exec != nil {
		return c.exec(ctx, name, cmd)
	}
	if strings.Contains(strings.Join(cmd, " "), contract.WorldReadyPath) {
		return c.ready, nil
	}
	return "", nil
}
