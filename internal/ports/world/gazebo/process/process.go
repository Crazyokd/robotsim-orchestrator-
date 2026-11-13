package process

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

const entityPollInterval = 250 * time.Millisecond

// ReadyPath 是容器内就绪报告的位置；宿主侧经 `cat` 读它。
const ReadyPath = "/tmp/simenv/ready.json"

// entityWaitTimeout 是"创建后回查实体"的窗口：gz 的 create 是异步生效的。
// 变量而非常量：测试里会缩短它，免得用例白等。
var entityWaitTimeout = 20 * time.Second

type ReadyReport struct {
	World  string   `json:"world"`
	Clock  bool     `json:"clock"`
	Robots []string `json:"robots"`
}

type Transport interface {
	StartSim(ctx context.Context, worldPath string) (<-chan error, error)
	WaitClock(ctx context.Context) error
	CreateEntity(ctx context.Context, world, name, sdfPath string, pose contract.Pose) error
	// EntityExists 回查实体是否真的在世界里。gz 的 create 服务**先回响应后读文件**，
	// 且文件读不到时响应依然是 true —— 只看调用是否报错会漏掉"实体根本没建出来"。
	EntityExists(ctx context.Context, world, name string) (bool, error)
}

type Body struct {
	Name        string
	TopicPrefix string
	Pose        contract.Pose
	KeepCameras bool
}

type Process struct {
	WorldPath string
	ModelPath string
	ReadyPath string
	Transport Transport
	Bodies    []Body
}

func (p Process) Run(ctx context.Context) error {
	if p.Transport == nil {
		return fmt.Errorf("world transport is required")
	}
	if strings.TrimSpace(p.WorldPath) == "" {
		return fmt.Errorf("world SDF path must not be empty")
	}
	if err := removeReady(p.ReadyPath); err != nil {
		return err
	}
	defer func() {
		_ = removeReady(p.ReadyPath)
	}()
	worldPath := p.WorldPath
	raw, err := os.ReadFile(worldPath)
	if err != nil {
		return fmt.Errorf("read world %s: %w", worldPath, err)
	}
	worldSDFName, err := SDFWorldName(raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	server, err := p.Transport.StartSim(ctx, worldPath)
	if err != nil {
		return err
	}
	go func() {
		if err := <-server; err != nil {
			cancel(err)
			return
		}
		cancel(errServerExited)
	}()

	if err := p.Transport.WaitClock(ctx); err != nil {
		return causeOr(ctx, err)
	}
	// 重写后的 SDF 要活到世界进程结束：gz 的 create 服务是"先回响应、后读文件"，
	// 调用返回后立刻删掉临时文件会让实体静默建不出来（实测 gz 日志：
	// "Error finding file [...]" / "Unable to read file: [...]"，而服务响应仍是 true）。
	sdfDir, err := os.MkdirTemp("", "simenv-world-")
	if err != nil {
		return causeOr(ctx, err)
	}
	defer func() { _ = os.RemoveAll(sdfDir) }()
	names := make([]string, 0, len(p.Bodies))
	for _, body := range p.Bodies {
		sdfPath, err := writeRobotSDF(sdfDir, p.ModelPath, body)
		if err != nil {
			return causeOr(ctx, err)
		}
		if err := p.Transport.CreateEntity(ctx, worldSDFName, body.Name, sdfPath, body.Pose); err != nil {
			return causeOr(ctx, err)
		}
		if err := p.waitEntity(ctx, worldSDFName, body.Name); err != nil {
			return causeOr(ctx, err)
		}
		names = append(names, body.Name)
	}
	report := ReadyReport{World: worldSDFName, Clock: true, Robots: names}
	if err := writeReady(p.ReadyPath, report); err != nil {
		return err
	}
	<-ctx.Done()
	return stopped(ctx)
}

var errServerExited = errors.New("world server exited")

func causeOr(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return err
}

func stopped(ctx context.Context) error {
	if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
		return cause
	}
	return nil
}

func writeReady(path string, report ReadyReport) error {
	if path == "" {
		return fmt.Errorf("ready path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ready-*.tmp")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}()
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeRobotSDF(dir, modelPath string, body Body) (string, error) {
	sdf, err := os.ReadFile(modelPath)
	if err != nil {
		return "", fmt.Errorf("read model: %w", err)
	}
	rewritten, err := RewriteRobotSDF(string(sdf), body.Name, body.TopicPrefix, body.KeepCameras)
	if err != nil {
		return "", err
	}
	sdfPath := filepath.Join(dir, body.Name+".sdf")
	if err := os.WriteFile(sdfPath, []byte(rewritten), 0o644); err != nil {
		return "", err
	}
	return sdfPath, nil
}

// waitEntity 轮询确认实体已经在世界里。gz 的 create 是异步生效的，所以这里给一段窗口；
// 超时说明实体确实没建出来（例如临时 SDF 被提前删掉、模型本身非法），必须报错而不是照写 ready。
func (p Process) waitEntity(ctx context.Context, worldName, name string) error {
	deadline := time.Now().Add(entityWaitTimeout)
	for {
		exists, err := p.Transport.EntityExists(ctx, worldName, name)
		if err == nil && exists {
			return nil
		}
		if ctx.Err() != nil {
			return causeOr(ctx, ctx.Err())
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("entity %s did not appear in world %s: %w", name, worldName, err)
			}
			return fmt.Errorf("entity %s did not appear in world %s within %s", name, worldName, entityWaitTimeout)
		}
		select {
		case <-ctx.Done():
			return causeOr(ctx, ctx.Err())
		case <-time.After(entityPollInterval):
		}
	}
}

func removeReady(path string) error {
	if path == "" {
		return fmt.Errorf("ready path must not be empty")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove ready file: %w", err)
	}
	return nil
}

// SDFWorldName validates a complete SDF document containing exactly one world.
func SDFWorldName(raw []byte) (string, error) {
	var sdf struct {
		XMLName xml.Name `xml:"sdf"`
		Worlds  []struct {
			Name string `xml:"name,attr"`
		} `xml:"world"`
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := dec.Token()
		if err != nil {
			return "", err
		}
		if start, ok := token.(xml.StartElement); ok {
			if err := dec.DecodeElement(&sdf, &start); err != nil {
				return "", err
			}
			break
		}
		switch value := token.(type) {
		case xml.Comment, xml.ProcInst:
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return "", fmt.Errorf("unexpected text before world XML")
			}
		default:
			return "", fmt.Errorf("unexpected token before world XML")
		}
	}
	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.Comment:
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return "", fmt.Errorf("unexpected trailing world XML")
			}
		default:
			return "", fmt.Errorf("unexpected trailing world XML")
		}
	}
	if len(sdf.Worlds) != 1 || strings.TrimSpace(sdf.Worlds[0].Name) == "" {
		return "", fmt.Errorf("SDF must contain exactly one named world")
	}
	return sdf.Worlds[0].Name, nil
}
