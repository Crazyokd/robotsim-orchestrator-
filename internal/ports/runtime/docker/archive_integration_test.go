package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestArchiveDeliveryWithRealDaemon(t *testing.T) {
	if os.Getenv("SIMENV_DOCKER_TEST") != "1" {
		t.Skip("set SIMENV_DOCKER_TEST=1 to exercise the local daemon")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	helper := `package main
import("context";"fmt";"os";"os/signal";"syscall")
func main(){
 a,e:=os.ReadFile("/opt/simenv/worlds/simple/custom/scene.sdf");if e!=nil{panic(e)}
 b,e:=os.ReadFile("/opt/simenv/models/prop/mesh.dae");if e!=nil{panic(e)}
 fmt.Printf("%s|%s",a,b)
 if len(os.Args)>1{return}
 ctx,stop:=signal.NotifyContext(context.Background(),syscall.SIGTERM);defer stop();<-ctx.Done()
}`
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(helper), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(dir, "probe"), filepath.Join(dir, "probe.go"))
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	world := `<sdf><world name="selected_scene"/></sdf>`
	for name, content := range map[string]string{
		"worlds/simple/custom/scene.sdf": world,
		"models/prop/mesh.dae":           "environment model",
	} {
		path := filepath.Join(dir, "payload", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	source, target := "simenv-assets-source:"+suffix, "simenv-assets-target:"+suffix
	name := "simenv-assets-test-" + suffix
	engine, err := NewDockerEngine()
	if err != nil {
		t.Fatal(err)
	}
	var built []string
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := engine.Stop(cleanup, name); err != nil {
			t.Errorf("cleanup container: %v", err)
		}
		if len(built) > 0 {
			args := append([]string{"image", "rm"}, built...)
			if out, err := exec.CommandContext(cleanup, "docker", args...).CombinedOutput(); err != nil {
				t.Errorf("cleanup images: %v\n%s", err, out)
			}
		}
	})
	for _, image := range []string{source, target} {
		text := "FROM scratch\nCOPY empty /opt/\nCOPY probe /probe\nENTRYPOINT [\"/probe\"]\n"
		if image == source {
			text += "COPY payload /opt/simenv\n"
		}
		file := filepath.Join(dir, "Dockerfile")
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.CommandContext(ctx, "docker", "build", "--file", file, "--tag", image, dir).CombinedOutput(); err != nil {
			t.Fatalf("build fixture image: %v\n%s", err, out)
		}
		built = append(built, image)
	}
	archive, err := engine.ReadImageArchive(ctx, source, "/opt/simenv")
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Start(ctx, ContainerSpec{Name: name, Image: target, Archives: map[string][]byte{"/opt": archive}}); err != nil {
		t.Fatal(err)
	}
	info, err := engine.Inspect(ctx, name)
	if err != nil || !info.Running {
		t.Fatalf("probe could not read assets at startup: %+v, error=%v", info, err)
	}
	out, err := engine.Exec(ctx, name, []string{"/probe", "check"})
	if err != nil || !strings.Contains(out, world+"|environment model") {
		t.Fatalf("asset round trip: %q, error=%v", out, err)
	}
}
