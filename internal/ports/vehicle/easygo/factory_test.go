package easygo

import (
	"strings"
	"testing"
)

// 车端节严格解析：字段多余、缺失、map_export / map_name 为空都是错误——
// 脏输入不迁就，宁可 Start 失败也不猜。
func TestParseSectionRejectsDirtyInput(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"unknown field", `{"map_export": "m", "map_name": "n", "legacy": 1}`, "unknown field"},
		{"localization tolerance override", `{"map_export":"m","map_name":"0814caterpiller","localization_position_tolerance_m":0.5}`, "unknown field"},
		{"empty map_export", `{"map_export": "", "map_name": "n"}`, "map_export must not be empty"},
		{"missing map_name", `{"map_export": "m"}`, "map_name must not be empty"},
		{"not json", `{`, "parse vehicles.easygo section"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSection([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error=%v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseSectionReadsTheVehicleSection(t *testing.T) {
	section, err := parseSection([]byte(`{
  "map_export": "easygo/simple.map_export",
  "map_name": "arena_world",
  "origin_offset_mm": {"x": 1978.0, "y": -3181.5}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if section.MapExport != "easygo/simple.map_export" || section.MapName != "arena_world" {
		t.Fatalf("section=%+v", section)
	}
	if section.OriginOffsetMM != (OriginOffset{X: 1978.0, Y: -3181.5}) {
		t.Fatalf("offset=%+v", section.OriginOffsetMM)
	}
}
