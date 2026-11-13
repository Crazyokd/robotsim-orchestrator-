package easygo

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Existing values survive registry seeding, so a saved database cannot change slots.
func (v Vehicle) validateStoredSyncConfig() error {
	root, err := v.dataPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "db", "cfg.db3")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	path := filepath.Join(root, appConfigHostDir, filepath.Base(syncRegistry))
	registry, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot verify saved sync configuration %s: %w", path, err)
	}
	patched, err := patchRegistryDefault(registry, syncMembershipPortKey, strconv.Itoa(v.runtime().syncMembershipPort))
	if err != nil {
		return err
	}
	if !bytes.Equal(registry, patched) {
		return fmt.Errorf("saved parameter data %s belongs to another slot; use a new session name or data root", root)
	}
	return nil
}

const (
	// appRegistryDir is where the runtime image keeps the parameter registry
	// installed by easygo-param-manager. The runtime launch reads this same path
	// unless EASYGO_SIM_APP_CONFIG_DIR / PARAM_CFG_DIR overrides it, so a bind
	// here reaches the live defaults. simenv overrides individual files inside it
	// instead of replacing the directory, so every other default stays authoritative.
	appRegistryDir = "/opt/sros/share/easygo-param-manager/cfg"
	// appConfigHostDir holds, per robot, the registry files simenv overrides.
	appConfigHostDir = "app-config"

	// syncRegistry carries the sync service's cluster-gossip membership port.
	syncRegistry = "param-registry/function.sync.yaml"
	// syncMembershipPortKey is the registry entry simenv pins per robot.
	syncMembershipPortKey = "sync.membership_port"
)

// readSyncConfig validates the image registry and applies the per-robot port in memory.
func (v Vehicle) readSyncConfig(ctx context.Context, runtimeImage string) ([]byte, error) {
	registry, err := v.Engine.ReadImageFile(ctx, runtimeImage, appRegistryDir+"/"+syncRegistry)
	if err != nil {
		return nil, fmt.Errorf("read %s from %s: %w", syncRegistry, runtimeImage, err)
	}
	patched, err := patchRegistryDefault(registry, syncMembershipPortKey, strconv.Itoa(v.runtime().syncMembershipPort))
	if err != nil {
		return nil, fmt.Errorf("initialize sync config for %s: %w", v.Robot.Name, err)
	}
	return patched, nil
}

// initializeSyncConfig stages only the sync port; launch owns vehicle identity.
func (v Vehicle) initializeSyncConfig(ctx context.Context, runtimeImage, hostSros string) ([]string, error) {
	patched, err := v.readSyncConfig(ctx, runtimeImage)
	if err != nil {
		return nil, err
	}
	hostDir := filepath.Join(hostSros, appConfigHostDir)
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return nil, err
	}
	hostPath := filepath.Join(hostDir, filepath.Base(syncRegistry))
	if err := os.WriteFile(hostPath, patched, 0o644); err != nil {
		return nil, fmt.Errorf("initialize sync config for %s: %w", v.Robot.Name, err)
	}
	return []string{hostPath + ":" + appRegistryDir + "/" + syncRegistry}, nil
}

func syncConfigArchive(patched []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	if err := w.WriteHeader(&tar.Header{Name: syncRegistry, Mode: 0o644, Size: int64(len(patched))}); err != nil {
		return nil, err
	}
	if _, err := w.Write(patched); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// patchRegistryDefault rewrites default_value on one registry entry, preserving
// the document's formatting.
func patchRegistryDefault(registry []byte, key, value string) ([]byte, error) {
	keyLine := "- key: " + key
	lines := strings.Split(string(registry), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == keyLine {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("registry has no %s entry", key)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "- key: ") {
			end = i
			break
		}
	}
	entryIndent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
	assignment := "default_value: " + value
	for i := start + 1; i < end; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "default_value:") {
			lineIndent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
			lines[i] = lineIndent + assignment
			return []byte(strings.Join(lines, "\n")), nil
		}
	}
	patched := make([]string, 0, len(lines)+1)
	patched = append(patched, lines[:start+1]...)
	patched = append(patched, entryIndent+"  "+assignment)
	patched = append(patched, lines[start+1:]...)
	return []byte(strings.Join(patched, "\n")), nil
}
