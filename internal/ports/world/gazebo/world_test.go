package gazebo

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo/process"
)

type fakeEngine struct {
	imageArchive []byte
	started      []docker.ContainerSpec
	stopped      []string
	infos        map[string]docker.ContainerInfo
	ready        string
	execErr      error
}

func (f *fakeEngine) ReadImageArchive(context.Context, string, string) ([]byte, error) {
	if f.imageArchive != nil {
		return f.imageArchive, nil
	}
	return worldArchive(map[string]string{"simenv/worlds/simple/gazebo/world.sdf": `<sdf><world name="map_world"/></sdf>`})
}

func (f *fakeEngine) ImageID(_ context.Context, ref string) (string, error) {
	return "sha256:" + ref, nil
}

func (f *fakeEngine) Start(_ context.Context, spec docker.ContainerSpec) error {
	f.started = append(f.started, spec)
	f.infos[spec.Name] = docker.ContainerInfo{
		Name: spec.Name, Running: true, Image: spec.Image, ID: spec.Name + "-id", Labels: spec.Labels,
	}
	return nil
}

func (f *fakeEngine) Stop(_ context.Context, name string) error {
	f.stopped = append(f.stopped, name)
	info := f.infos[name]
	info.Running = false
	f.infos[name] = info
	return nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (docker.ContainerInfo, error) {
	info, ok := f.infos[name]
	if !ok {
		return docker.ContainerInfo{}, docker.ErrNotFound
	}
	return info, nil
}

func (f *fakeEngine) Exec(_ context.Context, name string, cmd []string) (string, error) {
	if name == WorldContainer && strings.Contains(strings.Join(cmd, " "), process.ReadyPath) {
		return f.ready, f.execErr
	}
	return "", nil
}

func (f *fakeEngine) List(_ context.Context) ([]docker.ContainerInfo, error) {
	var out []docker.ContainerInfo
	for _, info := range f.infos {
		out = append(out, info)
	}
	return out, nil
}

type blockingReadyEngine struct {
	fakeEngine
}

func (f *blockingReadyEngine) Exec(ctx context.Context, _ string, _ []string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func twoRobotSession(t *testing.T) contract.Session {
	t.Helper()
	session := contract.Session{
		World: "simple",
		Robots: []contract.Robot{
			{Index: 1, Name: "robot1", Pose: contract.Pose{X: 7, Y: 8, Z: contract.DefaultSpawnZ, Yaw: 1.5}},
			{Index: 2, Name: "robot2"},
		},
	}
	if err := session.Validate(); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestNamedSessionKeepsItsOwnContainerAndPartition(t *testing.T) {
	named := DockerWorld{SessionName: "alpha"}
	if named.containerName() != WorldContainer+"-alpha" || named.partition() != Partition+"-alpha" {
		t.Fatalf("container=%s partition=%s", named.containerName(), named.partition())
	}
	if (DockerWorld{}).containerName() != WorldContainer || (DockerWorld{}).partition() != Partition {
		t.Fatal("the unnamed session keeps the single-session container and partition")
	}
}

func TestSlotShiftsTheWorldRosDomain(t *testing.T) {
	if (DockerWorld{}).rosDomainID() != EnvROSDomainID {
		t.Fatal("slot 0 keeps the single-session domain")
	}
	if (DockerWorld{Slot: 1}).rosDomainID() != EnvROSDomainID+100 {
		t.Fatal("each slot keeps its own ROS domain")
	}
}

func TestStartRejectsWorldSlotBeyondRosDomainRange(t *testing.T) {
	for _, slot := range []int{3, -1} {
		t.Run(strconv.Itoa(slot), func(t *testing.T) {
			engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
			world := DockerWorld{Engine: engine, Slot: slot, Options: Options{EnvImage: "simenv-world:local"}}
			session := contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}}
			if _, err := world.Start(context.Background(), session, nil); err == nil || !strings.Contains(err.Error(), "exceeds gazebo ROS domain limit") {
				t.Fatalf("start error=%v", err)
			}
			if len(engine.started) != 0 {
				t.Fatalf("world guard started containers: %v", engine.started)
			}
		})
	}
}

func TestStartRunsTheWorldContainerAndHandsOutAttachments(t *testing.T) {
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}}
	world := DockerWorld{Engine: engine, Options: Options{
		EnvImage: "simenv-world:local", CameraRobots: []int{1},
	}}

	attachments, err := world.Start(context.Background(), twoRobotSession(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.started) != 1 {
		t.Fatalf("started=%d", len(engine.started))
	}
	spec := engine.started[0]
	if spec.Name != WorldContainer || spec.Network != "host" || spec.Image != "simenv-world:local" {
		t.Fatalf("spec=%+v", spec)
	}
	if containsAny(spec.Command, "easygo-app", "slam", "nav2") {
		t.Fatal("world command must not start the vehicle stack")
	}
	if spec.Env["GZ_IP"] != GZIP || spec.Env["GZ_PARTITION"] != Partition || spec.Env["ROS_DOMAIN_ID"] != "1" {
		t.Fatalf("env=%v", spec.Env)
	}
	if spec.Labels[docker.LabelRole] != docker.RoleWorld {
		t.Fatalf("labels=%v", spec.Labels)
	}
	var session contract.Session
	for i, item := range spec.Command {
		if item == "--session" && i+1 < len(spec.Command) {
			if err := json.Unmarshal([]byte(spec.Command[i+1]), &session); err != nil {
				t.Fatal(err)
			}
		}
	}
	if session.World != "simple" || len(session.Robots) != 2 || session.Robots[0].Pose.X != 7 {
		t.Fatalf("session=%+v", session)
	}
	if got, ok := commandValue(spec.Command, "--cameras"); !ok || got != "1" {
		t.Fatalf("camera selection=%q, present=%t", got, ok)
	}

	if len(attachments) != 2 {
		t.Fatalf("attachments=%d", len(attachments))
	}
	for i, want := range []string{"robot1", "robot2"} {
		attachment, ok := attachments[i].(Attachment)
		if !ok {
			t.Fatalf("attachment %d is %T", i, attachments[i])
		}
		if attachment.IP != GZIP || attachment.Partition != Partition || attachment.TopicPrefix != want {
			t.Fatalf("attachment %d=%+v", i, attachment)
		}
		if attachment.Kind() != Kind {
			t.Fatalf("kind=%q", attachment.Kind())
		}
	}
}

func TestStartRequiresAnEnvImage(t *testing.T) {
	world := DockerWorld{Engine: &fakeEngine{infos: map[string]docker.ContainerInfo{}}}
	if _, err := world.Start(context.Background(), twoRobotSession(t), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestReadyRequiresAllSessionRobots(t *testing.T) {
	report, _ := json.Marshal(process.ReadyReport{
		World: "map_world", Clock: true, Robots: []string{"robot1"},
	})
	engine := &fakeEngine{infos: map[string]docker.ContainerInfo{}, ready: string(report)}
	world := DockerWorld{
		Engine: engine,
		Sleep:  func(context.Context, time.Duration) error { return errors.New("retry") },
	}

	_, err := world.Ready(context.Background(), twoRobotSession(t))
	if err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("error=%v", err)
	}
}

func TestReadyEnforcesItsOwnTimeout(t *testing.T) {
	session := contract.Session{
		World:  "simple",
		Robots: []contract.Robot{{Index: 1, Name: "robot1"}},
	}
	engine := &blockingReadyEngine{}

	_, err := waitReadyWithTimeout(context.Background(), engine, nil, session, WorldContainer, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want deadline exceeded", err)
	}
}

func TestStatusReportsNotReadyWithoutError(t *testing.T) {
	missing := "cat: " + process.ReadyPath + ": No such file or directory"
	for _, tc := range []struct {
		name, output string
		execErr      error
	}{
		{"empty report", "", nil},
		{"missing report", missing, &docker.ExecError{ExitCode: 1, Output: missing}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &fakeEngine{
				infos: map[string]docker.ContainerInfo{
					WorldContainer: {Name: WorldContainer, Running: true},
				},
				ready: tc.output, execErr: tc.execErr,
			}

			status, err := (DockerWorld{Engine: engine}).Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !status.Present || !status.Running {
				t.Fatalf("status=%+v", status)
			}
			if len(status.Ready.Robots) != 0 {
				t.Fatalf("ready=%+v", status.Ready)
			}
		})
	}
}

func TestStatusDoesNotHideOtherExecFailuresAsMissingReadiness(t *testing.T) {
	missing := "cat: " + process.ReadyPath + ": No such file or directory"
	for _, tc := range []struct {
		name, output string
		execErr      error
	}{
		{"permission denied", "cat: " + process.ReadyPath + ": Permission denied", &docker.ExecError{ExitCode: 1}},
		{"other missing path", "cat: /other: No such file or directory", &docker.ExecError{ExitCode: 1}},
		{"timeout", missing, &docker.ExecError{ExitCode: 124}},
		{"inspect failure", missing, errors.New("inspect failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := &fakeEngine{
				infos: map[string]docker.ContainerInfo{WorldContainer: {Name: WorldContainer, Running: true}},
				ready: tc.output, execErr: tc.execErr,
			}
			_, err := (DockerWorld{Engine: engine}).Status(context.Background())
			if !errors.Is(err, tc.execErr) {
				t.Fatalf("error=%v, want %v", err, tc.execErr)
			}
		})
	}
}

func TestStatusMapsTheReadyReport(t *testing.T) {
	report, _ := json.Marshal(process.ReadyReport{World: "map_world", Clock: true, Robots: []string{"robot1"}})
	engine := &fakeEngine{
		infos: map[string]docker.ContainerInfo{
			WorldContainer: {Name: WorldContainer, Running: true, Image: "simenv-world:local"},
		},
		ready: string(report),
	}

	status, err := (DockerWorld{Engine: engine}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Detail["image"] != "simenv-world:local" {
		t.Fatalf("detail=%v", status.Detail)
	}
	if status.Ready.World != "map_world" || len(status.Ready.Robots) != 1 || status.Ready.Robots[0] != "robot1" {
		t.Fatalf("ready=%+v", status.Ready)
	}
}

func TestStatusReturnsReadyErrorForRunningWorld(t *testing.T) {
	readyErr := errors.New("ready read failed")
	engine := &fakeEngine{
		infos: map[string]docker.ContainerInfo{
			WorldContainer: {Name: WorldContainer, Running: true},
		},
		execErr: readyErr,
	}

	_, err := (DockerWorld{Engine: engine}).Status(context.Background())
	if !errors.Is(err, readyErr) {
		t.Fatalf("error=%v, want %v", err, readyErr)
	}
}

func TestStatusIgnoresStaleReadyWhenWorldIsStopped(t *testing.T) {
	engine := &fakeEngine{
		infos: map[string]docker.ContainerInfo{
			WorldContainer: {Name: WorldContainer, Running: false},
		},
		execErr: errors.New("ready file should not be read"),
	}

	status, err := (DockerWorld{Engine: engine}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Ready.Robots) != 0 {
		t.Fatalf("ready=%+v", status.Ready)
	}
}

func TestStatusReportsAbsentWorldWithoutError(t *testing.T) {
	status, err := (DockerWorld{Engine: &fakeEngine{infos: map[string]docker.ContainerInfo{}}}).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Present || status.Running {
		t.Fatalf("status=%+v", status)
	}
}

func containsAny(items []string, needles ...string) bool {
	blob := strings.Join(items, " ")
	for _, needle := range needles {
		if strings.Contains(blob, needle) {
			return true
		}
	}
	return false
}

func commandValue(command []string, flag string) (string, bool) {
	for i, item := range command {
		if item == flag && i+1 < len(command) {
			return command[i+1], true
		}
	}
	return "", false
}
