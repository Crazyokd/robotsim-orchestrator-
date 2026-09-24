package world

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

type ReadyReport struct {
	World  string   `json:"world"`
	Clock  bool     `json:"clock"`
	Robots []string `json:"robots"`
}

type Transport interface {
	StartSim(ctx context.Context, worldPath string) (<-chan error, error)
	WaitClock(ctx context.Context) error
	CreateEntity(ctx context.Context, world, name, sdfPath string, pose contract.Pose) error
}

type Body struct {
	Name        string
	TopicPrefix string
	Pose        contract.Pose
	KeepCameras bool
}

type Process struct {
	AssetsRoot string
	ModelPath  string
	ReadyPath  string
	Transport  Transport
	WorldName  string
	Bodies     []Body
}

func (p Process) Run(ctx context.Context) error {
	if p.Transport == nil {
		return fmt.Errorf("world transport is required")
	}
	if strings.TrimSpace(p.WorldName) == "" {
		return fmt.Errorf("world name must not be empty")
	}
	if err := removeReady(p.ReadyPath); err != nil {
		return err
	}
	defer func() {
		_ = removeReady(p.ReadyPath)
	}()
	worldPath := filepath.Join(p.AssetsRoot, p.WorldName+".sdf")
	raw, err := os.ReadFile(worldPath)
	if err != nil {
		return fmt.Errorf("read world %s: %w", worldPath, err)
	}
	worldSDFName, err := sdfWorldName(raw)
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
	names := make([]string, 0, len(p.Bodies))
	for _, body := range p.Bodies {
		if err := createEntity(ctx, p.Transport, p.ModelPath, worldSDFName, body); err != nil {
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

func createEntity(ctx context.Context, transport Transport, modelPath, worldName string, body Body) error {
	sdf, err := os.ReadFile(modelPath)
	if err != nil {
		return fmt.Errorf("read model: %w", err)
	}
	rewritten, err := RewriteRobotSDF(string(sdf), body.Name, body.TopicPrefix, body.Pose.Yaw, body.KeepCameras)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "simenv-*.sdf")
	if err != nil {
		return err
	}
	sdfPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(sdfPath)
	}()
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if _, err := file.Write([]byte(rewritten)); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return transport.CreateEntity(ctx, worldName, body.Name, sdfPath, body.Pose)
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

func sdfWorldName(raw []byte) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return "", fmt.Errorf("world name not found")
		}
		if err != nil {
			return "", err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "world" {
			continue
		}
		for _, a := range start.Attr {
			if a.Name.Local == "name" && a.Value != "" {
				return a.Value, nil
			}
		}
		return "", fmt.Errorf("world element missing name")
	}
}
