package gazebo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo/process"
)

// DockerWorld 是 Gazebo 世界 Port 的宿主侧实现：把 simenv-world-gazebo 进程拉进
// 一个 host 网络容器，等它写出就绪报告。
type DockerWorld struct {
	Engine  docker.Engine
	Options Options
	Sleep   func(context.Context, time.Duration) error
	// SessionName 是这次会话的名字。空只留给未绑定会话的零值；绑定之后容器名和
	// partition 都带上这个名字。
	SessionName string
}

// Kind 满足框架的世界种类约定：bundle 的 worlds.gazebo 节与会话标记按它路由。
func (DockerWorld) Kind() string { return Kind }

func (w DockerWorld) containerName() string {
	if w.SessionName == "" {
		return WorldContainer
	}
	return WorldContainer + "-" + w.SessionName
}

func (w DockerWorld) partition() string {
	if w.SessionName == "" {
		return Partition
	}
	return Partition + "-" + w.SessionName
}

func (w DockerWorld) Start(ctx context.Context, session contract.Session, rawSection assets.Section) ([]orchestrator.Attachment, error) {
	if err := session.Validate(); err != nil {
		return nil, err
	}
	if err := w.Options.Validate(); err != nil {
		return nil, err
	}
	// worlds.gazebo 节严格解析：坏节直接失败。SDF 路径目前仍按目录约定
	// 解析（世界进程在容器内读），节的运行时消费随世界进程接线演进。
	if len(rawSection) > 0 {
		if _, err := ParseSection(rawSection); err != nil {
			return nil, err
		}
	}
	for _, index := range w.Options.CameraRobots {
		if index < 1 || index > len(session.Robots) {
			return nil, fmt.Errorf("cameras robot %d outside robot count %d", index, len(session.Robots))
		}
	}
	rawSession, err := json.Marshal(session)
	if err != nil {
		return nil, err
	}
	worldCmd := []string{"--session", string(rawSession)}
	if len(w.Options.CameraRobots) > 0 {
		cameraRobots := make([]string, 0, len(w.Options.CameraRobots))
		for _, index := range w.Options.CameraRobots {
			cameraRobots = append(cameraRobots, strconv.Itoa(index))
		}
		worldCmd = append(worldCmd, "--cameras", strings.Join(cameraRobots, ","))
	}
	if err := w.Engine.Start(ctx, docker.ContainerSpec{
		Name:    w.containerName(),
		Image:   w.Options.EnvImage,
		Command: worldCmd,
		Env: map[string]string{
			"GZ_IP":         GZIP,
			"GZ_PARTITION":  w.partition(),
			"ROS_DOMAIN_ID": strconv.Itoa(EnvROSDomainID),
		},
		Network: "host",
		Ipc:     "host",
		Devices: hostDevices(),
		Labels:  map[string]string{docker.LabelManaged: "true", docker.LabelRole: docker.RoleWorld},
	}); err != nil {
		return nil, err
	}
	attachments := make([]orchestrator.Attachment, 0, len(session.Robots))
	for _, robot := range session.Robots {
		attachments = append(attachments, attachmentFor(robot, wantsCamera(w.Options.CameraRobots, robot.Index), w.partition()))
	}
	return attachments, nil
}

func wantsCamera(cameraRobots []int, index int) bool {
	for _, candidate := range cameraRobots {
		if candidate == index {
			return true
		}
	}
	return false
}

func (w DockerWorld) Ready(ctx context.Context, session contract.Session) (orchestrator.WorldReady, error) {
	if err := session.Validate(); err != nil {
		return orchestrator.WorldReady{}, err
	}
	return waitReady(ctx, w.Engine, w.Sleep, session, w.containerName())
}

func (w DockerWorld) Status(ctx context.Context) (orchestrator.WorldStatus, error) {
	info, err := w.Engine.Inspect(ctx, w.containerName())
	if errors.Is(err, docker.ErrNotFound) {
		return orchestrator.WorldStatus{}, nil
	}
	if err != nil {
		return orchestrator.WorldStatus{}, err
	}
	status := orchestrator.WorldStatus{
		Present: true,
		Running: info.Running,
		Detail:  map[string]string{"image": info.Image},
	}
	if !info.Running {
		return status, nil
	}
	ready, err := readReady(ctx, w.Engine, w.containerName())
	if errors.Is(err, errWorldNotReady) {
		return status, nil
	}
	if err != nil {
		return orchestrator.WorldStatus{}, fmt.Errorf("read world readiness: %w", err)
	}
	// 报告只在世界真正就绪（时钟已起）后给出：半截报告等于未就绪。
	if ready.Clock {
		status.Ready = readyReport(ready)
	}
	return status, nil
}

func (w DockerWorld) Stop(ctx context.Context) error {
	return w.Engine.Stop(ctx, w.containerName())
}

func waitReady(ctx context.Context, engine docker.Engine, sleep func(context.Context, time.Duration) error, session contract.Session, container string) (orchestrator.WorldReady, error) {
	return waitReadyWithTimeout(ctx, engine, sleep, session, container, 3*time.Minute)
}

func waitReadyWithTimeout(ctx context.Context, engine docker.Engine, sleep func(context.Context, time.Duration) error, session contract.Session, container string, timeout time.Duration) (orchestrator.WorldReady, error) {
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	for {
		report, err := readReady(readyCtx, engine, container)
		if err == nil && readyReportMatches(report, session) {
			return readyReport(report), nil
		}
		if readyErr := readyCtx.Err(); readyErr != nil {
			return orchestrator.WorldReady{}, fmt.Errorf("world not ready: %w", readyErr)
		}
		if err := sleep(readyCtx, 200*time.Millisecond); err != nil {
			return orchestrator.WorldReady{}, err
		}
	}
}

var errWorldNotReady = errors.New("world readiness not available")

// readyReport 把容器进程写出的线上格式映射成框架的就绪态。
func readyReport(report process.ReadyReport) orchestrator.WorldReady {
	return orchestrator.WorldReady{World: report.World, Robots: report.Robots}
}

func readyReportMatches(report process.ReadyReport, session contract.Session) bool {
	if !report.Clock || len(report.Robots) != len(session.Robots) {
		return false
	}
	expected := make(map[string]struct{}, len(session.Robots))
	for _, robot := range session.Robots {
		expected[robot.Name] = struct{}{}
	}
	if len(expected) != len(session.Robots) {
		return false
	}
	for _, name := range report.Robots {
		if _, ok := expected[name]; !ok {
			return false
		}
		delete(expected, name)
	}
	return len(expected) == 0
}

func readReady(ctx context.Context, engine docker.Engine, container string) (process.ReadyReport, error) {
	out, err := engine.Exec(ctx, container, []string{"cat", process.ReadyPath})
	if err != nil {
		return process.ReadyReport{}, err
	}
	raw := strings.TrimSpace(out)
	lower := strings.ToLower(raw)
	if raw == "" || strings.Contains(lower, "no such file or directory") {
		return process.ReadyReport{}, errWorldNotReady
	}
	var report process.ReadyReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return process.ReadyReport{}, fmt.Errorf("decode world readiness: %w", err)
	}
	return report, nil
}

// hostDevices 与车端 GUI 共用一套：宿主有 GPU 就把 /dev/dri 挂进容器，
// 否则 ogre2 退回 llvmpipe，多车跑不到实时。
func hostDevices() []string {
	return docker.HostGPUDevices()
}
