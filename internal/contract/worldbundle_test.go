package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validBundle = `{
  "world": "simple",
  "map_name": "arena_world",
  "origin_offset_mm": {"x": 1978.0, "y": -3181.5},
  "stations": [
    {"id": 4, "x": 6060, "y": 13220, "yaw": 0},
    {"id": 7, "x": 12500, "y": 13000, "yaw": 3.1415927}
  ],
  "initial_poses": [{"index": 1, "x": 0, "y": 0, "z": 0.05, "yaw": 0}],
  "scenarios": ["s2_predicted_blocking.sh", "s9_wide_lane_no_unlock.sh"],
  "source_sha": "05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1"
}`

func TestParseWorldBundleAcceptsExplicitManifest(t *testing.T) {
	bundle, err := ParseWorldBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	if bundle.World != "simple" || bundle.MapName != "arena_world" {
		t.Fatalf("bundle=%+v", bundle)
	}
	if bundle.OriginOffsetMM == nil || bundle.OriginOffsetMM.X != 1978.0 {
		t.Fatalf("origin offset=%+v", bundle.OriginOffsetMM)
	}
	if len(bundle.Stations) != 2 || len(bundle.Scenarios) != 2 {
		t.Fatalf("bundle=%+v", bundle)
	}
	if pose, ok := bundle.PoseFor(1); !ok || pose.Z != 0.05 {
		t.Fatalf("initial pose=%+v ok=%v", pose, ok)
	}
	if _, ok := bundle.PoseFor(2); ok {
		t.Fatal("unexpected pose for robot 2")
	}
	if station, ok := bundle.StationByID(7); !ok || station.X != 12500 {
		t.Fatalf("station=%+v ok=%v", station, ok)
	}
}

func TestParseWorldBundleAcceptsEmptyStations(t *testing.T) {
	raw := `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"]}`
	if _, err := ParseWorldBundle([]byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestWorldBundleRejectsInvalidManifests(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"unknown field": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"],"extra":1}`,
			want: "unknown field",
		},
		"trailing content": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"]} {}`,
			want: "trailing content",
		},
		"missing world": {
			raw:  `{"map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"]}`,
			want: "world must not be empty",
		},
		"world not a safe token": {
			raw:  `{"world":"../etc","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"]}`,
			want: "invalid character",
		},
		"missing map name": {
			raw:  `{"world":"w","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"]}`,
			want: "map name must not be empty",
		},
		"missing origin offset": {
			raw:  `{"world":"w","map_name":"m","stations":[],"scenarios":["a.sh"]}`,
			want: "origin_offset_mm must be present",
		},
		"missing stations key": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"scenarios":["a.sh"]}`,
			want: "stations must be present",
		},
		"station id zero": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[{"id":0,"x":1,"y":2,"yaw":0}],"scenarios":["a.sh"]}`,
			want: "station id must be >= 1",
		},
		"duplicate station id": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[{"id":1,"x":1,"y":2,"yaw":0},{"id":1,"x":3,"y":4,"yaw":0}],"scenarios":["a.sh"]}`,
			want: "duplicate station id 1",
		},
		"station coordinate overflow is rejected by the parser": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[{"id":1,"x":1e400,"y":2,"yaw":0}],"scenarios":["a.sh"]}`,
			want: "cannot unmarshal number 1e400",
		},
		"initial pose index zero": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"initial_poses":[{"index":0,"x":0,"y":0,"z":0,"yaw":0}],"scenarios":["a.sh"]}`,
			want: "initial pose index must be >= 1",
		},
		"duplicate initial pose": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"initial_poses":[{"index":1,"x":0,"y":0,"z":0,"yaw":0},{"index":1,"x":1,"y":1,"z":0,"yaw":0}],"scenarios":["a.sh"]}`,
			want: "duplicate initial pose index 1",
		},
		"missing scenarios": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[]}`,
			want: "scenarios must not be empty",
		},
		"empty scenarios": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":[]}`,
			want: "scenarios must not be empty",
		},
		"blank scenario name": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["  "]}`,
			want: "scenario name must not be empty",
		},
		"duplicate scenario": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh","a.sh"]}`,
			want: `duplicate scenario "a.sh"`,
		},
		"source sha not canonical": {
			raw:  `{"world":"w","map_name":"m","origin_offset_mm":{"x":0,"y":0},"stations":[],"scenarios":["a.sh"],"source_sha":" abc "}`,
			want: "source_sha is not canonical",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseWorldBundle([]byte(tc.raw))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestWorldBundleValidateForWorld(t *testing.T) {
	bundle, err := ParseWorldBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.ValidateForWorld("simple"); err != nil {
		t.Fatal(err)
	}
	err = bundle.ValidateForWorld("corridor")
	if err == nil || !strings.Contains(err.Error(), `requested "corridor"`) {
		t.Fatalf("expected mismatched world error, got %v", err)
	}
}

func TestLoadWorldBundleReportsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "simple.world.json")
	if err := os.WriteFile(path, []byte(validBundle), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := LoadWorldBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.World != "simple" {
		t.Fatalf("bundle=%+v", bundle)
	}
	missing := filepath.Join(dir, "missing.world.json")
	if _, err := LoadWorldBundle(missing); err == nil || !strings.Contains(err.Error(), "read world bundle") {
		t.Fatalf("expected read error, got %v", err)
	}
	broken := filepath.Join(dir, "broken.world.json")
	if err := os.WriteFile(broken, []byte(`{"world":"w"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorldBundle(broken); err == nil || !strings.Contains(err.Error(), broken) {
		t.Fatalf("expected path context in error, got %v", err)
	}
}
