package gazebo

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo/process"
)

type worldAssets struct {
	path    string
	archive []byte
}

func (w DockerWorld) loadAssets(ctx context.Context, session contract.Session, rawSection assets.Section) (worldAssets, error) {
	section := Section{SDF: "gazebo/world.sdf"}
	if len(rawSection) > 0 {
		parsed, err := ParseSection(rawSection)
		if err != nil {
			return worldAssets{}, err
		}
		section = parsed
	}
	archive, err := w.Engine.ReadImageArchive(ctx, w.Options.EnvImage, "/opt/simenv")
	if err != nil {
		return worldAssets{}, fmt.Errorf("read world assets from %s: %w", w.Options.EnvImage, err)
	}
	file := path.Join("simenv", "worlds", session.World, section.SDF)
	var sdf []byte
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return worldAssets{}, fmt.Errorf("read world asset archive: %w", err)
		}
		name := path.Clean(header.Name)
		if !filepath.IsLocal(name) || (name != "simenv" && !strings.HasPrefix(name, "simenv/")) {
			return worldAssets{}, fmt.Errorf("world asset archive has invalid path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && header.Typeflag != tar.TypeDir {
			return worldAssets{}, fmt.Errorf("world assets require regular files or directories: %q", header.Name)
		}
		if name == file {
			if sdf != nil || !header.FileInfo().Mode().IsRegular() {
				return worldAssets{}, fmt.Errorf("world SDF %s must be one regular file", file)
			}
			sdf, err = io.ReadAll(reader)
			if err != nil {
				return worldAssets{}, err
			}
		}
	}
	if sdf == nil {
		return worldAssets{}, fmt.Errorf("world image %s has no declared SDF %s", w.Options.EnvImage, file)
	}
	if _, err := process.SDFWorldName(sdf); err != nil {
		return worldAssets{}, fmt.Errorf("world SDF %s: %w", file, err)
	}
	return worldAssets{path: path.Join("/opt", file), archive: archive}, nil
}
