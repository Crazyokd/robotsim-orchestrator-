package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 端到端证明"加实现不动框架"：用真实构建的 simenv 二进制跑 null 世界 +
// null 车端的全链路（start/status/pose/reset/call/stop），全程不碰容器
// 运行时。二进制与资产根都放临时目录：资产解析相对可执行文件，复制仓内
// 出货的 nullworld bundle 过去，顺带验证出货清单本身。

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if builtBin != "" {
		_ = os.RemoveAll(filepath.Dir(builtBin))
	}
	os.Exit(code)
}

func simenvBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "simenv-null-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		builtBin = filepath.Join(dir, "simenv")
		cmd := exec.Command("go", "build", "-o", builtBin, ".")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildErr = &execError{err, stderr.String()}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return builtBin
}

type execError struct {
	err    error
	stderr string
}

func (e *execError) Error() string { return e.err.Error() + ": " + e.stderr }

// sandbox 摆一次测试的全部运行时状态：可执行文件、资产根（含 nullworld
// bundle）、XDG 缓存（会话标记与状态目录都走它）。
func sandbox(t *testing.T) (bin string, env []string, xdg string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "simenv")
	raw, err := os.ReadFile(simenvBinary(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	bundleDir := filepath.Join(dir, "deploy", "assets", "worlds", "nullworld")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle, err := os.ReadFile(filepath.Join("..", "..", "deploy", "assets", "worlds", "nullworld", "bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "bundle.json"), bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	xdg = filepath.Join(dir, "xdg")
	return bin, []string{
		"HOME=" + dir,
		"XDG_CACHE_HOME=" + xdg,
		"PATH=" + os.Getenv("PATH"),
	}, xdg
}

func runSimenv(t *testing.T, bin string, env []string, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func mustRun(t *testing.T, bin string, env []string, stdin string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runSimenv(t, bin, env, stdin, args...)
	if err != nil {
		t.Fatalf("simenv %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

func TestNullPairEndToEnd(t *testing.T) {
	bin, env, xdg := sandbox(t)

	// start：kind 由 bundle 的 worlds.null / vehicles.null 唯一键推导，
	// 不需要 --world-kind / --vehicle-kind，更不需要容器运行时。
	out := mustRun(t, bin, env, "", "start", "--world", "nullworld", "--robots", "2")
	for _, want := range []string{
		"world running=true robots=robot1,robot2",
		"vehicle robot1 running=true endpoint=null://robot1",
		"vehicle robot2 running=true endpoint=null://robot2",
		"connected=true",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("start output missing %q:\n%s", want, out)
		}
	}
	sessionName := sessionNameFrom(t, out)
	// 状态文件按会话名落盘：世界一个、每车一个。
	worldState := filepath.Join(xdg, "simenv", "null", sessionName+".json")
	if _, err := os.Stat(worldState); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"robot1.json", "robot2.json"} {
		if _, err := os.Stat(filepath.Join(xdg, "simenv", "null-vehicles", sessionName, name)); err != nil {
			t.Fatal(err)
		}
	}

	// status：不点名只列会话；点名才展开到每台车。
	out = mustRun(t, bin, env, "", "status")
	if !strings.Contains(out, "session "+sessionName) || !strings.Contains(out, "connected=true") || strings.Contains(out, "vehicle robot2") {
		t.Fatalf("status output:\n%s", out)
	}
	out = mustRun(t, bin, env, "", "status", sessionName)
	if !strings.Contains(out, "vehicle robot2") {
		t.Fatalf("detail output:\n%s", out)
	}

	// pose / reset：位姿经状态文件跨进程持久化。
	out = mustRun(t, bin, env, "", "pose", "--robot", "2")
	if !strings.Contains(out, "robot=2 entity=robot2") {
		t.Fatalf("pose output:\n%s", out)
	}
	mustRun(t, bin, env, "", "reset", "--robot", "2", "--x", "9", "--y", "8", "--yaw", "1.5")
	out = mustRun(t, bin, env, "", "pose", "--robot", "2")
	if !strings.Contains(out, "x=9") || !strings.Contains(out, "yaw=1.5") {
		t.Fatalf("reset did not persist:\n%s", out)
	}

	// call：标准输入的字节原样回到标准输出。
	out = mustRun(t, bin, env, "ping-bytes", "call", "--robot", "1")
	if out != "ping-bytes" {
		t.Fatalf("call must echo verbatim: %q", out)
	}

	// stop：状态清干净，status 回到无会话。
	mustRun(t, bin, env, "", "stop")
	if _, err := os.Stat(worldState); !os.IsNotExist(err) {
		t.Fatalf("world state not removed: %v", err)
	}
	out = mustRun(t, bin, env, "", "status")
	if strings.TrimSpace(out) != "" {
		t.Fatalf("status after stop:\n%s", out)
	}
}

func sessionNameFrom(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "session ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] != "" {
				return fields[1]
			}
		}
	}
	t.Fatalf("output has no session name:\n%s", out)
	return ""
}

func TestStartRejectsKindMismatchAgainstBundle(t *testing.T) {
	bin, env, _ := sandbox(t)
	_, _, err := runSimenv(t, bin, env, "", "start", "--world", "nullworld", "--vehicle-kind", "easygo")
	if err == nil {
		t.Fatal("explicit kind contradicting the bundle must fail")
	}
}

func TestBareWorldRequiresExplicitKinds(t *testing.T) {
	bin, env, _ := sandbox(t)
	// 无 bundle 的世界：两个 kind 都必须显式给；给了就能起（裸世界合法）。
	_, _, err := runSimenv(t, bin, env, "", "start", "--world", "ghostworld")
	if err == nil {
		t.Fatal("bare world without explicit kinds must fail")
	}
	out := mustRun(t, bin, env, "", "start", "--world", "ghostworld",
		"--world-kind", "null", "--vehicle-kind", "null")
	if !strings.Contains(out, "connected=true") {
		t.Fatalf("bare null world did not start:\n%s", out)
	}
	mustRun(t, bin, env, "", "stop")
}

func TestNamedSessionLifecycleEndToEnd(t *testing.T) {
	bin, env, xdg := sandbox(t)
	mustRun(t, bin, env, "", "start", "--world", "nullworld", "--session", "alpha", "--robots", "2")
	mustRun(t, bin, env, "", "start", "--world", "nullworld", "--session", "beta")
	betaState := filepath.Join(xdg, "simenv", "null", "beta.json")
	state, err := os.ReadFile(betaState)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(betaState, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := runSimenv(t, bin, env, "", "status")
	if err == nil || !strings.Contains(out, "session alpha") || !strings.Contains(out, "error=") || !strings.Contains(stderr, "beta") {
		t.Fatalf("broken beta hid healthy alpha: out=%s stderr=%s error=%v", out, stderr, err)
	}
	if err := os.WriteFile(betaState, state, 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustRun(t, bin, env, "", "pose", "beta", "--robot", "1")
	mustRun(t, bin, env, "", "reset", "alpha", "--robot", "1", "--x", "77", "--y", "88", "--yaw", "1")
	if after := mustRun(t, bin, env, "", "pose", "beta", "--robot", "1"); after != before {
		t.Fatalf("reset alpha affected beta: %q -> %q", before, after)
	}
	if got := mustRun(t, bin, env, "alpha-bytes", "call", "alpha", "--robot", "1"); got != "alpha-bytes" {
		t.Fatalf("alpha call=%q", got)
	}
	bundlePath := filepath.Join(filepath.Dir(bin), "deploy", "assets", "worlds", "nullworld", "bundle.json")
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	badBundle := `{"world":"nullworld","stations":[],"worlds":{"null":{}},"vehicles":{"null":{"unknown":true}}}`
	if err := os.WriteFile(bundlePath, []byte(badBundle), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runSimenv(t, bin, env, "", "start", "--world", "nullworld", "--session", "alpha"); err == nil {
		t.Fatal("invalid replacement must fail")
	}
	if out := mustRun(t, bin, env, "", "status", "alpha"); !strings.Contains(out, "vehicle robot2 running=true") {
		t.Fatalf("invalid replacement stopped old alpha: %s", out)
	}
	if err := os.WriteFile(bundlePath, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, bin, env, "", "start", "--world", "nullworld", "--session", "alpha")
	if _, err := os.Stat(filepath.Join(xdg, "simenv", "null-vehicles", "alpha", "robot2.json")); !os.IsNotExist(err) {
		t.Fatalf("shrinking alpha left robot2: %v", err)
	}
	mustRun(t, bin, env, "", "stop", "alpha")
	mustRun(t, bin, env, "", "start", "--world", "nullworld", "--session", "beta")
	raw, err := os.ReadFile(filepath.Join(xdg, "simenv", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Sessions []struct {
			Name string `json:"name"`
			Slot int    `json:"slot"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Sessions) != 1 || registry.Sessions[0].Name != "beta" || registry.Sessions[0].Slot != 1 {
		t.Fatalf("beta restart moved to alpha's freed slot: %s", raw)
	}
	if out := mustRun(t, bin, env, "", "status", "beta"); !strings.Contains(out, "connected=true") {
		t.Fatalf("beta stopped with alpha: %s", out)
	}
	mustRun(t, bin, env, "", "stop", "beta")
	if out := mustRun(t, bin, env, "", "status"); strings.TrimSpace(out) != "" {
		t.Fatalf("sessions remain after stop: %s", out)
	}
}
