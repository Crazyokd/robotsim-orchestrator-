package easygo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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

// initializeSyncConfig pins this robot's sync membership port and returns the
// mount that puts it in place. Vehicle identity is supplied by the launch's
// serial_number argument, so simenv does not rewrite its parameter registry.
//
// The image already ships the default, so initialization reads that registry,
// patches the per-robot port, and mounts the result back over the image file.
func (v Vehicle) initializeSyncConfig(ctx context.Context, runtimeImage, hostSros string) ([]string, error) {
	overrides := v.initialRegistryDefaults()
	hostDir := filepath.Join(hostSros, appConfigHostDir)
	if err := os.RemoveAll(hostDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return nil, err
	}
	staged := make(map[string]string, len(overrides))
	binds := make([]string, 0, len(overrides))
	for _, override := range overrides {
		name := filepath.Base(override.Registry)
		if other, clash := staged[name]; clash {
			return nil, fmt.Errorf("registry overrides %s and %s stage the same file name", other, override.Registry)
		}
		staged[name] = override.Registry

		registry, err := v.Engine.ReadImageFile(ctx, runtimeImage, appRegistryDir+"/"+override.Registry)
		if err != nil {
			return nil, fmt.Errorf("read %s from %s: %w", override.Registry, runtimeImage, err)
		}
		patched, err := patchRegistryDefault(registry, override.Key, override.Value)
		if err != nil {
			return nil, fmt.Errorf("initialize sync config for %s: %w", v.Robot.Name, err)
		}
		hostPath := filepath.Join(hostDir, name)
		if err := os.WriteFile(hostPath, patched, 0o644); err != nil {
			return nil, fmt.Errorf("initialize sync config for %s: %w", v.Robot.Name, err)
		}
		binds = append(binds, hostPath+":"+appRegistryDir+"/"+override.Registry)
	}
	return binds, nil
}

// initialRegistryDefaults lists the registry defaults simenv must pin per robot.
func (v Vehicle) initialRegistryDefaults() []registryOverride {
	return []registryOverride{
		// A UDP bind for the sync service's cluster-gossip discovery. Vehicles
		// sharing one host network would otherwise collide and easygo-app would
		// abort at startup.
		{
			Registry: syncRegistry,
			Key:      syncMembershipPortKey,
			Value:    strconv.Itoa(v.runtime().syncMembershipPort),
		},
	}
}

type registryOverride struct {
	// Registry is the registry file relative to appRegistryDir.
	Registry string
	Key      string
	// Value is YAML scalar text, already quoted if the scalar needs quoting.
	Value string
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
