package main

import (
	"bytes"
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

func simenvBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		builtBin = filepath.Join(os.TempDir(), "simenv-null-e2e-bin")
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
	// 状态文件落盘在缓存目录（参数无关，发现路径才找得到）：世界一个、每车一个。
	worldState := filepath.Join(xdg, "simenv", "null", "world.json")
	if _, err := os.Stat(worldState); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"robot1.json", "robot2.json"} {
		if _, err := os.Stat(filepath.Join(xdg, "simenv", "null-vehicles", name)); err != nil {
			t.Fatal(err)
		}
	}

	// status：另一次进程调用按会话标记路由到 null 种类。
	out = mustRun(t, bin, env, "", "status")
	if !strings.Contains(out, "connected=true") || !strings.Contains(out, "vehicle robot2") {
		t.Fatalf("status output:\n%s", out)
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
	if !strings.Contains(out, "connected=false") || strings.Contains(out, "vehicle robot1") {
		t.Fatalf("status after stop:\n%s", out)
	}
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
