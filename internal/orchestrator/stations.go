package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// SeedStations 让 bundle 里的站点在车端地图上存在且坐标正确。
//
// 站点记录在车端 map json 与 API 里都是**扁平**字段（`{"id":4,"type":1,"pos.x":6060,...}`）：
// 服务端 DTO 是 `#[serde(flatten)]`，map-core 的 parse_pose 也只读对象顶层的 `pos.x`。
// 发 `{"fields":{...}}` 会写出读不出坐标的记录 —— 表现为"任务能建、导航目标却是 (0,0)"。
//
// 语义：缺就建、已存在且坐标一致就跳过、坐标不一致**直接失败**（世界换了而站点没跟上，
// 宁可启动失败也不留一个目标错位的世界）。建完回读校验一次。
func (v EasyGoVehicle) SeedStations(ctx context.Context, mapName string, stations []contract.Station) error {
	if len(stations) == 0 {
		return nil
	}
	current, err := v.readStations(ctx, mapName)
	if err != nil {
		return err
	}
	for _, station := range stations {
		existing, ok := current[station.ID]
		if !ok {
			if err := v.createStation(ctx, mapName, station); err != nil {
				return err
			}
			continue
		}
		if existing != station {
			return fmt.Errorf(
				"seed stations %s: station %d is %+v on the vehicle, bundle says %+v",
				v.Robot.Name, station.ID, existing, station,
			)
		}
	}
	verifying, err := v.readStations(ctx, mapName)
	if err != nil {
		return err
	}
	for _, station := range stations {
		existing, ok := verifying[station.ID]
		if !ok {
			return fmt.Errorf(
				"seed stations %s: station %d missing after seeding (站点 id 由服务端按创建顺序分配，缺号会顺延)",
				v.Robot.Name, station.ID,
			)
		}
		if existing != station {
			return fmt.Errorf(
				"seed stations %s: station %d is %+v after seeding, want %+v",
				v.Robot.Name, station.ID, existing, station,
			)
		}
	}
	return nil
}

// readStations 读取车端地图上的站点，按 id 索引，只认扁平坐标字段。
func (v EasyGoVehicle) readStations(ctx context.Context, mapName string) (map[int]contract.Station, error) {
	body, status, err := v.appJSON(ctx, http.MethodGet, fmt.Sprintf("/api/v6/map/%s/stations", mapName), nil)
	if err != nil {
		return nil, fmt.Errorf("read stations %s: %w", v.Robot.Name, err)
	}
	if status >= 300 {
		return nil, fmt.Errorf("read stations %s: %s (%s)", v.Robot.Name, http.StatusText(status), strings.TrimSpace(string(body)))
	}
	var response struct {
		Data []struct {
			ID     int       `json:"id"`
			Fields *struct { // 历史脏形状：坐标嵌在 fields 里等于读不出来
				X *float64 `json:"pos.x"`
			} `json:"fields"`
			X   *float64 `json:"pos.x"`
			Y   *float64 `json:"pos.y"`
			Yaw *float64 `json:"pos.yaw"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("read stations %s: decode: %w", v.Robot.Name, err)
	}
	stations := make(map[int]contract.Station, len(response.Data))
	for _, item := range response.Data {
		var station contract.Station
		station.ID = item.ID
		readable := item.X != nil && item.Y != nil
		if !readable {
			if item.Fields != nil && item.Fields.X != nil {
				return nil, fmt.Errorf(
					"read stations %s: station %d 的坐标嵌在 fields 里，车端读不出（需按扁平字段重建该站点）",
					v.Robot.Name, item.ID,
				)
			}
			return nil, fmt.Errorf("read stations %s: station %d has no readable coordinates", v.Robot.Name, item.ID)
		}
		station.X = *item.X
		station.Y = *item.Y
		if item.Yaw != nil {
			station.Yaw = *item.Yaw
		}
		stations[station.ID] = station
	}
	return stations, nil
}

func (v EasyGoVehicle) createStation(ctx context.Context, mapName string, station contract.Station) error {
	payload, err := json.Marshal(map[string]any{
		"type":    1,
		"pos.x":   station.X,
		"pos.y":   station.Y,
		"pos.yaw": station.Yaw,
	})
	if err != nil {
		return fmt.Errorf("seed stations %s: station %d: %w", v.Robot.Name, station.ID, err)
	}
	body, status, err := v.appJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v6/map/%s/stations", mapName), payload)
	if err != nil {
		return fmt.Errorf("seed stations %s: station %d: %w", v.Robot.Name, station.ID, err)
	}
	if status >= 300 {
		return fmt.Errorf(
			"seed stations %s: station %d: %s (%s)",
			v.Robot.Name, station.ID, http.StatusText(status), strings.TrimSpace(string(body)),
		)
	}
	return nil
}

// appJSON 调车端 API，返回响应体与状态码（非 2xx 不当作传输错误）。
func (v EasyGoVehicle) appJSON(ctx context.Context, method, path string, payload []byte) ([]byte, int, error) {
	if v.DoJSON != nil {
		return v.DoJSON(ctx, method, path, payload)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", easyGoRuntimeFor(v.Robot.Index).apiPort, path)
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, 0, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}
