package orchestrator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// sessionMarker 是框架唯一持久化的控制面状态：本次会话用的是哪个世界实现、
// 哪个车端实现。容器或状态文件仍是"会话在不在"的真相，标记只回答"哪种实现
// 去发现它"——status / stop / pose / reset / call 按标记路由，不逐个实现盲探
// （盲探会让某个实现的运行时不可用时拖垮整个 status）。
type sessionMarker struct {
	WorldKind   string `json:"world_kind"`
	VehicleKind string `json:"vehicle_kind"`
}

// sessionMarkerPath 是标记文件的位置：用户缓存目录下，运行时数据不进资产目录。
// 测试用 XDG_CACHE_HOME 指向临时目录隔离。
func sessionMarkerPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "simenv", "session.json"), nil
}

// loadSessionMarker 读标记；没有标记返回 (nil, nil)——没有进行中的会话。
// 文件损坏是错误：不猜，让操作员处理（路径写进错误信息）。
func loadSessionMarker() (*sessionMarker, error) {
	path, err := sessionMarkerPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session marker %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var marker sessionMarker
	if err := dec.Decode(&marker); err != nil {
		return nil, fmt.Errorf("parse session marker %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse session marker %s: trailing content", path)
	}
	if marker.WorldKind == "" || marker.VehicleKind == "" {
		return nil, fmt.Errorf("session marker %s: kinds must not be empty", path)
	}
	return &marker, nil
}

func saveSessionMarker(marker sessionMarker) error {
	path, err := sessionMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session marker dir: %w", err)
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// clearSessionMarker 摘掉标记；本就没有标记不是错误。
func clearSessionMarker() error {
	path, err := sessionMarkerPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session marker %s: %w", path, err)
	}
	return nil
}
