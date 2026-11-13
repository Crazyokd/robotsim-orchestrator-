package orchestrator

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestNamedReplacementKeepsItsOriginalSlot(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{Name: "alpha", WorldKind: "fake-world", VehicleKind: "fake", Slot: 2}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	if _, err := r.Start(context.Background(), contract.StartConfig{
		World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha",
	}); err != nil {
		t.Fatal(err)
	}
	records, err := loadSessions()
	if err != nil || len(records) != 1 || records[0].Slot != 2 {
		t.Fatalf("replacement changed resource slot: %+v, error=%v", records, err)
	}
}

func TestPreflightFailurePreservesTheExistingSession(t *testing.T) {
	for _, port := range []string{"world", "vehicle"} {
		t.Run(port, func(t *testing.T) {
			isolateMarker(t)
			if err := saveSessionMarker(sessionRecord{Name: "alpha", WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
				t.Fatal(err)
			}
			r := newRegistryOrchestrator(t, t.TempDir())
			wantErr := errors.New("invalid start options")
			if port == "world" {
				r.worldPort.validateErr = wantErr
			} else {
				r.vehicleFactory.validateErr = wantErr
			}
			_, err := r.Start(context.Background(), contract.StartConfig{
				World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha",
			})
			if !errors.Is(err, wantErr) {
				t.Fatalf("preflight error=%v", err)
			}
			if r.worldPort.stopped || len(r.worldPort.started) != 0 || len(r.vehicleFactory.ports) != 0 {
				t.Fatal("invalid replacement changed running resources")
			}
			records, err := loadSessions()
			if err != nil || len(records) != 1 || records[0].Name != "alpha" {
				t.Fatalf("invalid replacement changed registry: %+v, error=%v", records, err)
			}
		})
	}
}

func TestStartRecordsIdentityUntilRollbackIsComplete(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean rollback", true: "failed rollback"}[cleanupFails], func(t *testing.T) {
			isolateMarker(t)
			r := newRegistryOrchestrator(t, t.TempDir())
			r.worldPort.startErr = errors.New("world start failed")
			if cleanupFails {
				r.worldPort.stopErr = errors.New("world cleanup failed")
			}
			r.worldPort.onStart = func() {
				records, err := loadSessions()
				if err != nil || len(records) != 1 || records[0].Name != "alpha" {
					t.Errorf("world started before recording its identity: %+v, error=%v", records, err)
				}
			}
			_, err := r.Start(context.Background(), contract.StartConfig{
				World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha",
			})
			if !errors.Is(err, r.worldPort.startErr) {
				t.Fatalf("original start error lost: %v", err)
			}
			records, readErr := loadSessions()
			if readErr != nil || (len(records) == 1) != cleanupFails {
				t.Fatalf("rollback registry=%+v, error=%v", records, readErr)
			}
			if cleanupFails {
				if !errors.Is(err, r.worldPort.stopErr) {
					t.Fatalf("cleanup error lost: %v", err)
				}
				r.worldPort.stopErr = nil
				if err := r.StopSession(context.Background(), "alpha"); err != nil {
					t.Fatalf("failed start cannot be cleaned up by name: %v", err)
				}
			}
		})
	}
}

func TestSessionRegistryRejectsInvalidIdentitiesAndDuplicateSlots(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"sessions":null}`,
		`{"sessions":[{"name":"alpha","world_kind":"fake-world","vehicle_kind":"fake","started_unix":1}]}`,
		`{"sessions":[{"name":"alpha","world_kind":"fake-world","vehicle_kind":"fake","slot":null,"started_unix":1}]}`,
		`{"sessions":[{"name":"../alpha","world_kind":"fake-world","vehicle_kind":"fake","slot":0,"started_unix":1}]}`,
		`{"sessions":[{"name":"alpha","world_kind":"../world","vehicle_kind":"fake","slot":0,"started_unix":1}]}`,
		`{"sessions":[{"name":"alpha","world_kind":"fake-world","vehicle_kind":"fake","slot":0,"started_unix":0}]}`,
		`{"sessions":[{"name":"alpha","world_kind":"fake-world","vehicle_kind":"fake","slot":0,"started_unix":1},{"name":"beta","world_kind":"fake-world","vehicle_kind":"fake","slot":0,"started_unix":2}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			isolateMarker(t)
			path, err := sessionMarkerPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSessions(); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("invalid registry must fail with its path: %v", err)
			}
		})
	}
}

func TestSessionListKeepsHealthyRowsWhenOneSessionFails(t *testing.T) {
	isolateMarker(t)
	for i, kind := range []string{"missing", "fake-world"} {
		if err := saveSessionMarker(sessionRecord{Name: []string{"broken", "healthy"}[i], WorldKind: kind, VehicleKind: "fake", Slot: i}); err != nil {
			t.Fatal(err)
		}
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	rows, err := r.Sessions(context.Background())
	if err == nil || !strings.Contains(err.Error(), "broken") || len(rows) != 2 || rows[1].Name != "healthy" {
		t.Fatalf("one failed query hid other sessions: %+v, error=%v", rows, err)
	}
}

func TestCancelledStartDoesNotReplaceTheOldSession(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{Name: "alpha", WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Start(ctx, contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"})
	if !errors.Is(err, context.Canceled) || r.worldPort.stopped || len(r.worldPort.started) != 0 {
		t.Fatalf("cancelled start changed the old session: error=%v", err)
	}
}

func TestStartRollsBackWhenFinalReadinessIsLost(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	r.worldPort.status = WorldStatus{Present: true, Running: false}
	_, err := r.Start(context.Background(), contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"})
	if err == nil || !r.worldPort.stopped {
		t.Fatalf("start claimed success after world stopped: %v", err)
	}
	records, err := loadSessions()
	if err != nil || len(records) != 0 {
		t.Fatalf("failed start left a successful registry entry: %+v, error=%v", records, err)
	}
}

func TestInvalidPoseAndSessionNameCannotReplaceHealthySession(t *testing.T) {
	for _, cfg := range []contract.StartConfig{
		{SessionName: "-alpha"},
		{SessionName: "alpha", Poses: []contract.Pose{{X: math.NaN()}}},
		{SessionName: "alpha", Poses: []contract.Pose{{Yaw: math.Inf(1)}}},
	} {
		t.Run(cfg.SessionName+" pose", func(t *testing.T) {
			isolateMarker(t)
			if err := saveSessionMarker(sessionRecord{Name: "alpha", WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
				t.Fatal(err)
			}
			r := newRegistryOrchestrator(t, t.TempDir())
			cfg.World, cfg.Robots, cfg.WorldKind, cfg.VehicleKind = "bare", 1, "fake-world", "fake"
			if _, err := r.Start(context.Background(), cfg); err == nil {
				t.Fatal("invalid request passed validation")
			}
			if r.worldPort.stopped || len(r.worldPort.started) != 0 {
				t.Fatal("invalid request changed running resources")
			}
		})
	}
}
