package assets

import (
	"strings"
	"testing"
)

const testBundleJSON = `{
  "world": "corridor",
  "stations": [
    {"id": 1, "x": -14.0, "y": 1.2, "yaw": 0},
    {"id": 2, "x": 14.0, "y": -1.0, "yaw": 0}
  ],
  "initial_poses": [
    {"index": 1, "x": -14.0, "y": 1.2, "z": 0.05, "yaw": 0}
  ],
  "source_sha": "05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1",
  "worlds": {"fake-world": {"sdf": "fake-world/world.sdf"}},
  "vehicles": {"fake-vehicle": {"map_export": "fake-vehicle/corridor.bin"}}
}`

func TestParseBundle(t *testing.T) {
	bundle, err := ParseBundle([]byte(testBundleJSON))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.World != "corridor" || len(bundle.Stations) != 2 {
		t.Fatalf("bundle=%+v", bundle)
	}
	if bundle.Stations[0] != (Station{ID: 1, X: -14.0, Y: 1.2, Yaw: 0}) {
		t.Fatalf("station 1=%+v", bundle.Stations[0])
	}
	pose, ok := bundle.PoseFor(1)
	if !ok || pose.X != -14.0 || pose.Z != 0.05 {
		t.Fatalf("pose=%+v ok=%t", pose, ok)
	}
	if _, ok := bundle.PoseFor(2); ok {
		t.Fatal("robot 2 has no declared pose")
	}
	if bundle.SourceSHA != "05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1" {
		t.Fatalf("sha=%q", bundle.SourceSHA)
	}
}

func TestParseBundleRejectsUnknownAndLegacyFields(t *testing.T) {
	for _, legacy := range []string{`"scenarios": [],`, `"map_name": "corridor",`, `"origin_offset_mm": {"x": 0, "y": 0},`} {
		raw := strings.Replace(testBundleJSON, `"world": "corridor",`, `"world": "corridor",`+legacy, 1)
		if _, err := ParseBundle([]byte(raw)); err == nil {
			t.Fatalf("legacy field %s must fail", legacy)
		}
	}
}

func TestParseBundleRejectsMissingSections(t *testing.T) {
	for name, raw := range map[string]string{
		"stations": `{"world": "w", "worlds": {}, "vehicles": {}}`,
		"worlds":   `{"world": "w", "stations": [], "vehicles": {}}`,
		"vehicles": `{"world": "w", "stations": [], "worlds": {}}`,
	} {
		if _, err := ParseBundle([]byte(raw)); err == nil {
			t.Fatalf("missing %s must fail", name)
		}
	}
}

func TestVehicleSectionMissingKindFails(t *testing.T) {
	bundle, err := ParseBundle([]byte(testBundleJSON))
	if err != nil {
		t.Fatal(err)
	}
	section, err := bundle.VehicleSection("fake-vehicle")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(section), "map_export") {
		t.Fatalf("section=%s", section)
	}
	if _, err := bundle.VehicleSection("other"); err == nil || !strings.Contains(err.Error(), "no vehicles.other section") {
		t.Fatalf("expected missing-section error, got %v", err)
	}
	if _, err := bundle.WorldSection("fake-world"); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.WorldSection("other"); err == nil {
		t.Fatal("expected missing worlds section error")
	}
}

func TestBundleRejectsDuplicateIdentities(t *testing.T) {
	raw := strings.Replace(testBundleJSON,
		`{"id": 2, "x": 14.0, "y": -1.0, "yaw": 0}`,
		`{"id": 1, "x": 14.0, "y": -1.0, "yaw": 0}`, 1)
	if _, err := ParseBundle([]byte(raw)); err == nil {
		t.Fatal("duplicate station id must fail")
	}
}
