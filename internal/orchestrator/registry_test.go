package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// isolateMarker 把会话标记隔离到临时目录：os.UserCacheDir 认 XDG_CACHE_HOME。
func isolateMarker(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func TestResolveKind(t *testing.T) {
	tests := []struct {
		name        string
		bundleKinds []string
		explicit    string
		want        string
		wantErr     string
	}{
		{name: "bundle single key wins", bundleKinds: []string{"fake-world"}, want: "fake-world"},
		{name: "explicit matches bundle", bundleKinds: []string{"fake-world"}, explicit: "fake-world", want: "fake-world"},
		{name: "explicit mismatches bundle", bundleKinds: []string{"fake-world"}, explicit: "other", wantErr: "does not match"},
		{name: "multiple bundle kinds", bundleKinds: []string{"a", "b"}, wantErr: "multiple world kinds"},
		{name: "bare world requires flag", wantErr: "--world-kind is required"},
		{name: "bare world with flag", explicit: "fake-world", want: "fake-world"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveKind("world", "--world-kind", tt.bundleKinds, tt.explicit)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveKind() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("resolveKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

// registryOrchestrator 造一个注册表路径的编排器：构造次数可数，fake 实现可断言。
type registryOrchestrator struct {
	Orchestrator
	worldPort       *fakeWorldPort
	vehicleFactory  *fakeVehicleFactory
	worldBuilt      int
	vehiclesBuilt   int
	lastWorldConfig *contract.StartConfig
}

func newRegistryOrchestrator(t *testing.T, assetsDir string) *registryOrchestrator {
	t.Helper()
	r := &registryOrchestrator{
		worldPort:      &fakeWorldPort{},
		vehicleFactory: &fakeVehicleFactory{},
	}
	r.Orchestrator = Orchestrator{
		Worlds: map[string]NewWorldFunc{
			"fake-world": func(cfg contract.StartConfig) (WorldPort, error) {
				r.worldBuilt++
				r.lastWorldConfig = &cfg
				return r.worldPort, nil
			},
		},
		Vehicles: map[string]NewVehicleFactoryFunc{
			"fake": func(cfg contract.StartConfig) (VehiclePortFactory, error) {
				r.vehiclesBuilt++
				return r.vehicleFactory, nil
			},
		},
		assetsDir: assetsDir,
	}
	return r
}

func TestRegistryStartDerivesKindsFromBundleAndSavesMarker(t *testing.T) {
	isolateMarker(t)
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", `{
  "world": "simple",
  "stations": [],
  "worlds": {"fake-world": {}},
  "vehicles": {"fake": {}}
}`)
	r := newRegistryOrchestrator(t, dir)
	if _, err := r.Start(context.Background(), contract.StartConfig{World: "simple", Robots: 2}); err != nil {
		t.Fatal(err)
	}
	if r.worldBuilt != 1 || r.vehiclesBuilt == 0 {
		t.Fatalf("registry constructors not used: world=%d vehicles=%d", r.worldBuilt, r.vehiclesBuilt)
	}
	marker, err := loadSessionMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil || marker.WorldKind != "fake-world" || marker.VehicleKind != "fake" {
		t.Fatalf("marker = %+v", marker)
	}
}

func TestRegistryStartRejectsKindMismatchWithBundle(t *testing.T) {
	isolateMarker(t)
	dir := t.TempDir()
	writeWorldBundle(t, dir, "simple", `{
  "world": "simple",
  "stations": [],
  "worlds": {"fake-world": {}},
  "vehicles": {"fake": {}}
}`)
	r := newRegistryOrchestrator(t, dir)
	_, err := r.Start(context.Background(), contract.StartConfig{World: "simple", Robots: 1, WorldKind: "other"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected kind mismatch, got %v", err)
	}
	if r.worldBuilt != 0 {
		t.Fatal("mismatched kind must fail before constructing any port")
	}
}

func TestRegistryStartBareWorldRequiresExplicitKinds(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	_, err := r.Start(context.Background(), contract.StartConfig{World: "noworld", Robots: 1})
	if err == nil || !strings.Contains(err.Error(), "--world-kind is required") {
		t.Fatalf("expected explicit-kind error, got %v", err)
	}
	if _, err := r.Start(context.Background(), contract.StartConfig{
		World: "noworld", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryStartFailsOnUnregisteredKind(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	_, err := r.Start(context.Background(), contract.StartConfig{
		World: "noworld", Robots: 1, WorldKind: "ghost", VehicleKind: "fake",
	})
	if err == nil || !strings.Contains(err.Error(), `no world port registered for kind "ghost"`) {
		t.Fatalf("expected unregistered-kind error, got %v", err)
	}
}

func TestRegistryStartStopsThePreviouslyMarkedSession(t *testing.T) {
	isolateMarker(t)
	// 同名再启动会先停掉这一路。名字空着的 save 记成 marker。
	if err := saveSessionMarker(sessionRecord{WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	stale := &fakeVehiclePort{status: VehicleStatus{Name: "robot9", Running: true}}
	r.vehicleFactory.discovered = []*fakeVehiclePort{stale}
	if _, err := r.Start(context.Background(), contract.StartConfig{
		World: "noworld", Robots: 1, WorldKind: "fake-world", VehicleKind: "fake", SessionName: "marker",
	}); err != nil {
		t.Fatal(err)
	}
	if !stale.stopped {
		t.Fatal("stale vehicle from the marked session was not stopped")
	}
	marker, err := loadSessionMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("marker was not saved for the new session")
	}
}

func TestMarkedStatusWithoutMarkerConstructsNothing(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	status, err := r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.World.Present || len(status.Vehicles) != 0 || status.Connected {
		t.Fatalf("expected empty status, got %+v", status)
	}
	if r.worldBuilt != 0 || r.vehiclesBuilt != 0 {
		t.Fatalf("status without a session must not construct ports: world=%d vehicles=%d",
			r.worldBuilt, r.vehiclesBuilt)
	}
}

func TestMarkedStatusRoutesByMarker(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	r.worldPort.status = WorldStatus{Present: true, Running: true}
	if _, err := r.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.worldBuilt != 1 || r.vehiclesBuilt != 1 {
		t.Fatalf("status did not route by marker: world=%d vehicles=%d", r.worldBuilt, r.vehiclesBuilt)
	}
}

func TestMarkedStatusFailsOnCorruptMarker(t *testing.T) {
	isolateMarker(t)
	path, err := sessionMarkerPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	if _, err := r.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "session marker") {
		t.Fatalf("expected corrupt-marker error, got %v", err)
	}
}

func TestStopMarkedSession(t *testing.T) {
	isolateMarker(t)
	r := newRegistryOrchestrator(t, t.TempDir())
	// 没有标记：stop 是空操作。
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.worldBuilt != 0 {
		t.Fatal("stop without a session must not construct ports")
	}
	// 有标记：发现的车停掉、世界停掉、标记摘掉。
	if err := saveSessionMarker(sessionRecord{WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	vehicle := &fakeVehiclePort{status: VehicleStatus{Name: "robot1", Running: true}}
	r.vehicleFactory.discovered = []*fakeVehiclePort{vehicle}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !vehicle.stopped || !r.worldPort.stopped {
		t.Fatalf("session not stopped: vehicle=%v world=%v", vehicle.stopped, r.worldPort.stopped)
	}
	marker, err := loadSessionMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker != nil {
		t.Fatal("marker was not cleared after a clean stop")
	}
}

func TestTwoSessionsStopTheLatestAndLeaveTheOther(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{
		Name: "alpha", WorldKind: "fake-world", VehicleKind: "fake", Slot: 1, StartedUnix: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := saveSessionMarker(sessionRecord{
		Name: "beta", WorldKind: "fake-world", VehicleKind: "fake", Slot: 2, StartedUnix: 2,
	}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	alpha := &fakeVehiclePort{status: VehicleStatus{Name: "robot1", Running: true}}
	beta := &fakeVehiclePort{status: VehicleStatus{Name: "robot1", Running: true}}
	r.vehicleFactory.discovered = []*fakeVehiclePort{beta}
	if err := r.StopSession(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if !beta.stopped || !r.worldPort.stopped {
		t.Fatal("latest session was not stopped")
	}
	records, err := loadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "alpha" {
		t.Fatalf("records=%+v", records)
	}
	if alpha.stopped {
		t.Fatal("older session must stay")
	}
	listed, err := r.Sessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Name != "alpha" {
		t.Fatalf("listed=%+v", listed)
	}
}

func TestStopMarkedSessionKeepsMarkerOnFailure(t *testing.T) {
	isolateMarker(t)
	if err := saveSessionMarker(sessionRecord{WorldKind: "fake-world", VehicleKind: "fake"}); err != nil {
		t.Fatal(err)
	}
	r := newRegistryOrchestrator(t, t.TempDir())
	r.worldPort.stopErr = errors.New("world stop failed")
	if err := r.Stop(context.Background()); err == nil {
		t.Fatal("expected stop error")
	}
	marker, err := loadSessionMarker()
	if err != nil {
		t.Fatal(err)
	}
	if marker == nil {
		t.Fatal("marker must survive a failed stop so the next stop retries")
	}
}
