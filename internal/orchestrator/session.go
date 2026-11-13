package orchestrator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// sessionRecord 是一次进行中的会话。容器或状态文件仍是"会话在不在"的真相，
// 记录只回答"哪个名字、哪种实现、哪一档资源"。status / stop / pose / reset / call
// 按它路由，不逐个实现盲探。
type sessionRecord struct {
	Name        string `json:"name"`
	WorldKind   string `json:"world_kind"`
	VehicleKind string `json:"vehicle_kind"`
	Slot        int    `json:"slot"`
	StartedUnix int64  `json:"started_unix"`
}

type sessionFile struct {
	Sessions []sessionRecord `json:"sessions"`
}

// sessionMarkerPath 是会话名单的位置：用户缓存目录下，运行时数据不进资产目录。
// 测试用 XDG_CACHE_HOME 指向临时目录隔离。
func sessionMarkerPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "simenv", "session.json"), nil
}

// lockSessionChanges guards slot allocation, lifecycle changes and registry writes.
// Keep the lock file in place: closing the descriptor releases the lock.
func lockSessionChanges() (*os.File, error) {
	path, err := sessionMarkerPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create session marker dir: %w", err)
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another session change is in progress; retry when it completes")
		}
		return nil, fmt.Errorf("lock session marker %s: %w", path, err)
	}
	return file, nil
}

func loadSessions() ([]sessionRecord, error) {
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
	var file sessionFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse session marker %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse session marker %s: trailing content", path)
	}
	seen := make(map[string]struct{}, len(file.Sessions))
	for _, rec := range file.Sessions {
		if rec.Name == "" || rec.WorldKind == "" || rec.VehicleKind == "" {
			return nil, fmt.Errorf("session marker %s: name and kinds must not be empty", path)
		}
		if _, ok := seen[rec.Name]; ok {
			return nil, fmt.Errorf("session marker %s: duplicate session %q", path, rec.Name)
		}
		seen[rec.Name] = struct{}{}
		if rec.Slot < 0 {
			return nil, fmt.Errorf("session marker %s: session %q slot must be >= 0", path, rec.Name)
		}
	}
	return file.Sessions, nil
}

func saveSessions(records []sessionRecord) error {
	path, err := sessionMarkerPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session marker dir: %w", err)
	}
	raw, err := json.Marshal(sessionFile{Sessions: records})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write session marker %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write session marker %s: %w", path, err)
	}
	return nil
}

func upsertSession(rec sessionRecord) error {
	records, err := loadSessions()
	if err != nil {
		return err
	}
	replaced := false
	for i := range records {
		if records[i].Name == rec.Name {
			records[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		records = append(records, rec)
	}
	return saveSessions(records)
}

func deleteSession(name string) error {
	records, err := loadSessions()
	if err != nil {
		return err
	}
	kept := records[:0]
	for _, rec := range records {
		if rec.Name != name {
			kept = append(kept, rec)
		}
	}
	return saveSessions(kept)
}

func findSession(records []sessionRecord, name string) (sessionRecord, bool) {
	for _, rec := range records {
		if rec.Name == name {
			return rec, true
		}
	}
	return sessionRecord{}, false
}

// latestSession 是最近启动的那一次。同时启动时取名单里靠后的一条。
func latestSession(records []sessionRecord) (sessionRecord, bool) {
	if len(records) == 0 {
		return sessionRecord{}, false
	}
	best := records[0]
	for _, rec := range records[1:] {
		if rec.StartedUnix >= best.StartedUnix {
			best = rec
		}
	}
	return best, true
}

// assignSlot 取还没占用的最小非负槽。records 里不应再包含即将替换的同名会话。
func assignSlot(records []sessionRecord) int {
	used := make(map[int]struct{}, len(records))
	for _, rec := range records {
		used[rec.Slot] = struct{}{}
	}
	for slot := 0; ; slot++ {
		if _, ok := used[slot]; !ok {
			return slot
		}
	}
}

var sessionAdjectives = []string{
	"amber", "brisk", "bold", "bright", "calm", "cosy", "crisp", "dizzy",
	"gentle", "golden", "happy", "hazy", "jolly", "lucky", "mellow", "nimble",
	"plucky", "quiet", "rapid", "sunny", "swift", "tidy", "vivid", "witty",
}

var sessionNouns = []string{
	"badger", "comet", "falcon", "finch", "harbor", "heron", "kettle", "lark",
	"maple", "otter", "pebble", "pioneer", "quartz", "robin", "sail", "sparrow",
	"thicket", "trail", "willow", "zephyr",
}

// randomSessionName 抽一个还没占用的名字，形如 brisk-otter。
func randomSessionName(records []sessionRecord) (string, error) {
	used := make(map[string]struct{}, len(records))
	for _, rec := range records {
		used[rec.Name] = struct{}{}
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for try := 0; try < 64; try++ {
		name := sessionAdjectives[rng.Intn(len(sessionAdjectives))] + "-" + sessionNouns[rng.Intn(len(sessionNouns))]
		if _, ok := used[name]; !ok {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not pick a free session name")
}

func exceptSession(records []sessionRecord, name string) []sessionRecord {
	kept := make([]sessionRecord, 0, len(records))
	for _, rec := range records {
		if rec.Name != name {
			kept = append(kept, rec)
		}
	}
	return kept
}

// saveSessionMarker 写入或替换一条会话。测试里名字空着时记成 marker。
func saveSessionMarker(rec sessionRecord) error {
	if rec.Name == "" {
		rec.Name = "marker"
	}
	if rec.StartedUnix == 0 {
		rec.StartedUnix = time.Now().UnixNano()
	}
	return upsertSession(rec)
}

// loadSessionMarker 读最近启动的会话；没有则返回 (nil, nil)。
func loadSessionMarker() (*sessionRecord, error) {
	records, err := loadSessions()
	if err != nil {
		return nil, err
	}
	rec, ok := latestSession(records)
	if !ok {
		return nil, nil
	}
	return &rec, nil
}
