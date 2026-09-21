package world

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type GZ struct {
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (g GZ) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if g.Run != nil {
		return g.Run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return out, nil
}

func (g GZ) StartSim(ctx context.Context, worldPath string) error {
	cmd := exec.CommandContext(ctx, "gz", "sim", "-s", "--headless-rendering", "-r", "-v", "1", worldPath)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gz sim: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (g GZ) WaitClock(ctx context.Context) error {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for gz clock")
		}
		out, err := g.run(ctx, "gz", "topic", "-l")
		if err == nil && bytes.Contains(out, []byte("clock")) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (g GZ) CreateEntity(ctx context.Context, world, name, sdfPath string, pose contract.Pose) error {
	req := fmt.Sprintf(
		`sdf_filename: "%s", name: "%s", pose: {position: {x: %g, y: %g, z: %g}, orientation: {z: 0, w: 1}}`,
		sdfPath, name, pose.X, pose.Y, pose.Z,
	)
	_, err := g.run(ctx, "gz", "service", "-s", "/world/"+world+"/create",
		"--reqtype", "gz.msgs.EntityFactory",
		"--reptype", "gz.msgs.Boolean",
		"--timeout", "10000",
		"--req", req,
	)
	if err != nil {
		return fmt.Errorf("spawn %s: %w", name, err)
	}
	return nil
}
