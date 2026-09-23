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
	worldPath := filepath.Join(p.AssetsRoot, p.WorldName+".sdf")
	raw, err := os.ReadFile(worldPath)
	if err != nil {
		return fmt.Errorf("read world %s: %w", worldPath, err)
	}
	worldSDFName, err := sdfWorldName(raw)
	if err != nil {
		return err
	}
	server, err := p.Transport.StartSim(ctx, worldPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
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
		sdf, err := os.ReadFile(p.ModelPath)
		if err != nil {
			return fmt.Errorf("read model: %w", err)
		}
		rewritten, err := RewriteRobotSDF(string(sdf), body.Name, body.TopicPrefix, body.Pose.Yaw)
		if err != nil {
			return err
		}
		sdfPath := filepath.Join(os.TempDir(), body.Name+".sdf")
		if err := os.WriteFile(sdfPath, []byte(rewritten), 0o644); err != nil {
			return err
		}
		if err := p.Transport.CreateEntity(ctx, worldSDFName, body.Name, sdfPath, body.Pose); err != nil {
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
	return os.WriteFile(path, raw, 0o644)
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
