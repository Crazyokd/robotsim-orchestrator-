package world

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

func (g GZ) StartSim(ctx context.Context, worldPath string) (<-chan error, error) {
	cmd := exec.CommandContext(ctx, "gz", "sim", "-s", "--headless-rendering", "-r", "-v", "1", worldPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gz sim: %w", err)
	}
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil && ctx.Err() == nil {
			err = fmt.Errorf("gz sim exited: %w\n%s", err, stderr.String())
		} else {
			err = nil
		}
		done <- err
	}()
	return done, nil
}

func (g GZ) WaitClock(ctx context.Context) error {
	return g.waitClockWithTimeout(ctx, 2*time.Minute)
}

func (g GZ) waitClockWithTimeout(ctx context.Context, timeout time.Duration) error {
	clockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := clockCtx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for gz clock: %w", err)
			}
			return err
		}
		out, err := g.run(clockCtx, "gz", "topic", "-l")
		if err == nil && bytes.Contains(out, []byte("clock")) {
			return nil
		}
		select {
		case <-clockCtx.Done():
			if errors.Is(clockCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for gz clock: %w", clockCtx.Err())
			}
			return clockCtx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// CreateEntity 在 world 里生成实体。**出生位姿只在这里给**：`create` 的 pose 会整体
// 覆盖 SDF 里的 model pose（位置与朝向都是），所以只写 `orientation: {z: 0, w: 1}`
// 等于把 yaw 丢掉——车在 Gazebo 里朝 +x，车端却按声明的朝向去扫描匹配，走廊里就表现为
// "沿轴滑 2.2 m"（实测见 docs/superpowers/specs/2026-10-12-world-bundle-design.md §5.5）。
func (g GZ) CreateEntity(ctx context.Context, world, name, sdfPath string, pose contract.Pose) error {
	req := fmt.Sprintf(
		`sdf_filename: "%s", name: "%s", pose: {position: {x: %g, y: %g, z: %g}, orientation: {x: 0, y: 0, z: %g, w: %g}}`,
		sdfPath, name, pose.X, pose.Y, pose.Z, math.Sin(pose.Yaw/2), math.Cos(pose.Yaw/2),
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

// EntityExists 通过 `gz model --list` 回查实体。
// 注意：create 服务对"文件读不到"依然回 data: true，所以响应不能当成功判据。
func (g GZ) EntityExists(ctx context.Context, world, name string) (bool, error) {
	out, err := g.run(ctx, "gz", "model", "--list")
	if err != nil {
		return false, fmt.Errorf("list models in world %s: %w", world, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "- "+name {
			return true, nil
		}
	}
	return false, nil
}

// PoseInfoCommand 在世界容器里采一份 `/world/<world>/pose/info`。
func PoseInfoCommand(world string) []string {
	return []string{"gz", "topic", "-e", "--json-output", "-t", "/world/" + world + "/pose/info", "-n", "1"}
}

// SetPoseCommand 把实体放到世界坐标。朝向只写 yaw（与 CreateEntity 同一套四元数）。
func SetPoseCommand(world, name string, pose contract.Pose) []string {
	req := fmt.Sprintf(
		`name: "%s", position: {x: %g, y: %g, z: %g}, orientation: {x: 0, y: 0, z: %g, w: %g}`,
		name, pose.X, pose.Y, pose.Z, math.Sin(pose.Yaw/2), math.Cos(pose.Yaw/2),
	)
	return []string{
		"gz", "service", "-s", "/world/" + world + "/set_pose",
		"--reqtype", "gz.msgs.Pose",
		"--reptype", "gz.msgs.Boolean",
		"--timeout", "5000",
		"--req", req,
	}
}

type poseInfo struct {
	Pose []struct {
		Name     string `json:"name"`
		Position struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			Z float64 `json:"z"`
		} `json:"position"`
		Orientation struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
			Z float64 `json:"z"`
			W float64 `json:"w"`
		} `json:"orientation"`
	} `json:"pose"`
}

// ParseEntityPose 从 pose/info JSON 取出命名实体。实体不在就失败。
func ParseEntityPose(raw, name string) (contract.Pose, error) {
	var info poseInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return contract.Pose{}, fmt.Errorf("decode pose info: %w", err)
	}
	for _, item := range info.Pose {
		if item.Name != name {
			continue
		}
		return contract.Pose{
			X:   item.Position.X,
			Y:   item.Position.Y,
			Z:   item.Position.Z,
			Yaw: yawFromQuaternion(item.Orientation.X, item.Orientation.Y, item.Orientation.Z, item.Orientation.W),
		}, nil
	}
	return contract.Pose{}, fmt.Errorf("entity %s is not in pose info", name)
}

// AssertSetPoseOK 要求 set_pose 回 true。文本或 JSON 都行。
func AssertSetPoseOK(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if strings.Contains(trimmed, "data: true") || strings.Contains(trimmed, `"data": true`) || strings.Contains(trimmed, `"data":true`) {
		return nil
	}
	return fmt.Errorf("set_pose was not accepted: %s", trimmed)
}

func yawFromQuaternion(x, y, z, w float64) float64 {
	return math.Atan2(2*(w*z+x*y), 1-2*(y*y+z*z))
}
