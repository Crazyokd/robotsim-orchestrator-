package easygo

import (
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// 世界系 → 地图系换算的对账：map_mm = world_m*1000 - offset_mm，yaw rad → mrad。
// simple 站 4 是仓里长期使用的一对值：世界系 (8.038, 10.0385)，车端 mm (6060, 13220)。
func TestToMapStationsConvertsWorldFrameToMapFrame(t *testing.T) {
	offset := OriginOffset{X: 1978.0, Y: -3181.5}
	stations := toMapStations([]assets.Station{
		{ID: 4, X: 8.038, Y: 10.0385, Yaw: 1.5707963267948966},
		{ID: 7, X: 14.478, Y: 9.8185, Yaw: 0},
	}, offset)
	if len(stations) != 2 {
		t.Fatalf("stations=%v", stations)
	}
	if stations[0] != (mapStation{ID: 4, X: 6060, Y: 13220, Yaw: 1570.7963267948965}) {
		t.Fatalf("station 4 = %+v", stations[0])
	}
	if stations[1] != (mapStation{ID: 7, X: 12500, Y: 13000, Yaw: 0}) {
		t.Fatalf("station 7 = %+v", stations[1])
	}
}
