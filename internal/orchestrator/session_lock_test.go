package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestSessionChangeProcessHelper(t *testing.T) {
	action := os.Getenv("SIMENV_TEST_SESSION_CHANGE")
	if action == "" {
		t.Skip("subprocess helper")
	}
	path, err := sessionMarkerPath()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	name := "alpha"
	other := strings.HasSuffix(action, "-other")
	if other {
		name = "contender"
	}
	if strings.HasPrefix(action, "start") {
		_, err = r.Start(context.Background(), contract.StartConfig{
			World: "noworld", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: name,
		})
	} else {
		err = r.StopSession(context.Background(), name)
	}
	if other {
		if err != nil {
			t.Fatalf("independent %s failed: %v", action, err)
		}
		records, err := loadSessions()
		if err != nil {
			t.Fatal(err)
		}
		if action == "start-other" {
			rec, ok := findSession(records, "contender")
			if !ok || rec.Slot != 2 || len(records) != 3 {
				t.Fatalf("independent start lost reservation: %+v", records)
			}
		} else if len(records) != 2 {
			t.Fatalf("independent stop changed other rows: %+v", records)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "session change is in progress") {
		t.Fatalf("concurrent %s must fail busy, got %v", action, err)
	}
	if r.worldBuilt != 0 || r.vehiclesBuilt != 0 {
		t.Fatal("busy command must fail before constructing ports")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("busy command changed session records: %s, error=%v", after, err)
	}
	if _, err := r.Sessions(context.Background()); err != nil {
		t.Fatalf("read-only status must remain available: %v", err)
	}
}

func TestSessionLifecycleLocksExcludeOnlyTheSameSession(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{Name: "existing", WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	r.Worlds["fake-world"] = func(contract.StartConfig) (WorldPort, error) {
		for _, action := range []string{"start", "stop", "start-other", "stop-other"} {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSessionChangeProcessHelper$")
			cmd.Env = append(os.Environ(), "SIMENV_TEST_SESSION_CHANGE="+action)
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("concurrent %s: %v\n%s", action, err, out)
			}
		}
		return r.worldPort, nil
	}
	cfg := contract.StartConfig{
		World: "noworld", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha",
	}
	if _, err := r.Start(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if err := r.StopSession(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	r.Worlds["fake-world"] = func(contract.StartConfig) (WorldPort, error) { return r.worldPort, nil }
	cfg.SessionName = "beta"
	if _, err := r.Start(context.Background(), cfg); err != nil {
		t.Fatalf("lock must be released after start and stop: %v", err)
	}
	records, err := loadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Name != "existing" || records[0].Slot != 0 || records[1].Name != "beta" || records[1].Slot != 1 {
		t.Fatalf("session records lost or slots reused: %+v", records)
	}
}

func TestSessionChangeLockReleasedAfterFailure(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	cfg := contract.StartConfig{
		World: "noworld", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha",
	}
	r.worldPort.startErr = errors.New("start failed")
	if _, err := r.Start(context.Background(), cfg); !errors.Is(err, r.worldPort.startErr) {
		t.Fatalf("start error=%v", err)
	}
	r.worldPort.startErr = nil
	if _, err := r.Start(context.Background(), cfg); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	r.worldPort.stopErr = errors.New("stop failed")
	if err := r.StopSession(context.Background(), "alpha"); !errors.Is(err, r.worldPort.stopErr) {
		t.Fatalf("stop error=%v", err)
	}
	r.worldPort.stopErr = nil
	if err := r.StopSession(context.Background(), "alpha"); err != nil {
		t.Fatalf("retry stop: %v", err)
	}
}
