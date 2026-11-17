package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
)

type waitingWorld struct {
	WorldPort
	phase   string
	entered chan struct{}
	release chan struct{}
}

func (w waitingWorld) wait(ctx context.Context) error {
	close(w.entered)
	select {
	case <-w.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w waitingWorld) ValidateStart(ctx context.Context, session contract.Session, section assets.Section) error {
	if w.phase == "preflight" {
		if err := w.wait(ctx); err != nil {
			return err
		}
	}
	return w.WorldPort.ValidateStart(ctx, session, section)
}

func (w waitingWorld) Ready(ctx context.Context, session contract.Session) (WorldReady, error) {
	if w.phase == "ready" {
		if err := w.wait(ctx); err != nil {
			return WorldReady{}, err
		}
	}
	return w.WorldPort.Ready(ctx, session)
}

func (w waitingWorld) Stop(ctx context.Context) error {
	if w.phase == "stop" {
		if err := w.wait(ctx); err != nil {
			return err
		}
	}
	return w.WorldPort.Stop(ctx)
}

func TestOtherSessionCanStartAndStopWhileStartWaits(t *testing.T) {
	for _, phase := range []string{"preflight", "ready"} {
		t.Run(phase, func(t *testing.T) {
			isolateMarker(t)
			alpha := newRegistryOrchestrator(t, t.TempDir())
			waiting := waitingWorld{
				WorldPort: alpha.worldPort, phase: phase,
				entered: make(chan struct{}), release: make(chan struct{}),
			}
			alpha.Worlds["fake-world"] = func(contract.StartConfig) (WorldPort, error) { return waiting, nil }
			cfg := contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"}
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() { _, err := alpha.Start(ctx, cfg); done <- err }()
			defer func() {
				close(waiting.release)
				if err := <-done; err != nil {
					t.Errorf("alpha start: %v", err)
				}
			}()
			select {
			case <-waiting.entered:
			case <-ctx.Done():
				t.Fatal("alpha never reached the controlled wait")
			}
			records, err := loadSessions()
			if err != nil || len(records) != 1 || records[0].Name != "alpha" || records[0].Slot != 0 {
				t.Fatalf("starting alpha must reserve slot 0 before waiting: %+v, error=%v", records, err)
			}

			beta := newRegistryOrchestrator(t, t.TempDir())
			cfg.SessionName = "beta"
			if _, err := beta.Start(ctx, cfg); err != nil {
				t.Fatalf("independent beta start blocked by alpha: %v", err)
			}
			records, err = loadSessions()
			if err != nil || len(records) != 2 || records[1].Name != "beta" || records[1].Slot != 1 {
				t.Fatalf("beta must reserve a distinct slot: %+v, error=%v", records, err)
			}
			if err := beta.StopSession(ctx, "beta"); err != nil {
				t.Fatalf("independent beta stop blocked by alpha: %v", err)
			}
			if !beta.worldPort.stopped {
				t.Fatal("beta resources were not stopped")
			}
			if err := beta.StopSession(ctx, "alpha"); err == nil || !strings.Contains(err.Error(), "in progress") {
				t.Fatalf("same-session stop must remain exclusive: %v", err)
			}
		})
	}
}

func TestConcurrentSessionsKeepDistinctSlotsAndRegistryRows(t *testing.T) {
	isolateMarker(t)
	const count = 8
	workers := make([]*registryOrchestrator, count)
	for i := range workers {
		workers[i] = newRegistryOrchestrator(t, t.TempDir())
	}
	start := make(chan struct{})
	done := make(chan error, count)
	for i, worker := range workers {
		go func() {
			<-start
			_, err := worker.Start(context.Background(), contract.StartConfig{
				World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: fmt.Sprintf("session-%d", i),
			})
			done <- err
		}()
	}
	close(start)
	for range count {
		if err := <-done; err != nil {
			t.Errorf("concurrent start: %v", err)
		}
	}
	records, err := loadSessions()
	if err != nil || len(records) != count {
		t.Fatalf("lost concurrent reservations: %+v, error=%v", records, err)
	}
	slots := make(map[int]bool)
	for _, rec := range records {
		if slots[rec.Slot] {
			t.Fatalf("duplicate slot: %+v", records)
		}
		slots[rec.Slot] = true
	}
	for i, worker := range workers {
		go func() { done <- worker.StopSession(context.Background(), fmt.Sprintf("session-%d", i)) }()
	}
	for range count {
		if err := <-done; err != nil {
			t.Errorf("concurrent stop: %v", err)
		}
	}
	if records, err := loadSessions(); err != nil || len(records) != 0 {
		t.Fatalf("concurrent stops left registry rows: %+v, error=%v", records, err)
	}
}

func TestNewPreflightFailureReleasesItsReservation(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	r.worldPort.validateErr = errors.New("preflight rejected")
	cfg := contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"}
	if _, err := r.Start(context.Background(), cfg); !errors.Is(err, r.worldPort.validateErr) {
		t.Fatalf("preflight error lost: %v", err)
	}
	if records, err := loadSessions(); err != nil || len(records) != 0 {
		t.Fatalf("preflight left a reservation: %+v, error=%v", records, err)
	}
	r.worldPort.validateErr = nil
	cfg.SessionName = "beta"
	if _, err := r.Start(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if r.lastWorldConfig.Slot != 0 {
		t.Fatalf("cleaned preflight slot was not reused: %d", r.lastWorldConfig.Slot)
	}
}

func TestReplacementKeepsItsSlotReservedDuringStop(t *testing.T) {
	isolateMarker(t)
	alpha := newRegistryOrchestrator(t, t.TempDir())
	cfg := contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"}
	if _, err := alpha.Start(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	waiting := waitingWorld{
		WorldPort: alpha.worldPort, phase: "stop",
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	alpha.Worlds["fake-world"] = func(contract.StartConfig) (WorldPort, error) { return waiting, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := alpha.Start(ctx, cfg); done <- err }()
	defer func() {
		close(waiting.release)
		if err := <-done; err != nil {
			t.Errorf("replacement: %v", err)
		}
	}()
	select {
	case <-waiting.entered:
	case <-ctx.Done():
		t.Fatal("replacement did not reach old resource stop")
	}
	beta := newRegistryOrchestrator(t, t.TempDir())
	cfg.SessionName = "beta"
	if _, err := beta.Start(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if beta.lastWorldConfig.Slot != 1 {
		t.Fatalf("replacement slot was handed to beta: %d", beta.lastWorldConfig.Slot)
	}
}

func TestInvalidBundleKindCannotPoisonReservations(t *testing.T) {
	for _, kind := range []string{"world", "vehicle"} {
		t.Run(kind, func(t *testing.T) {
			isolateMarker(t)
			dir := t.TempDir()
			world, vehicle := "fake-world", "fake"
			if kind == "world" {
				world = "bad/name"
			} else {
				vehicle = "bad/name"
			}
			writeWorldBundle(t, dir, "simple", fmt.Sprintf(
				`{"world":"simple","stations":[],"worlds":{%q:{}},"vehicles":{%q:{}}}`, world, vehicle,
			))
			r := newRegistryOrchestrator(t, dir)
			if _, err := r.Start(context.Background(), contract.StartConfig{World: "simple", Robots: 1, SessionName: "alpha"}); err == nil {
				t.Fatal("invalid kind was accepted")
			}
			if records, err := loadSessions(); err != nil || len(records) != 0 {
				t.Fatalf("invalid kind poisoned registry: %+v, error=%v", records, err)
			}
		})
	}
}

func TestRegistryLockWaitRespectsStartDeadline(t *testing.T) {
	isolateMarker(t)
	lock, err := lockSessionChanges(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	consumed := false
	defer func() {
		lock.Close()
		if !consumed {
			<-done
		}
	}()
	go func() {
		_, err := r.Start(ctx, contract.StartConfig{World: "bare", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "alpha"})
		done <- err
	}()
	select {
	case err := <-done:
		consumed = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline error lost: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("cancelled start is still waiting for the registry lock")
	}
}
