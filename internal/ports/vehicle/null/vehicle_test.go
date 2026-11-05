package null

import (
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/assets"
	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
	nullworld "git.standard-robots.com/yicao/sim/internal/ports/world/null"
)

func testFactory(t *testing.T) Factory {
	t.Helper()
	return Factory{Options: Options{DataRoot: t.TempDir()}}
}

func newVehicle(t *testing.T, factory Factory, index int) orchestrator.VehiclePort {
	t.Helper()
	robot, err := contract.NewRobot(index, contract.Pose{X: 3, Z: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	port, err := factory.New(context.Background(),
		contract.Session{World: "nullworld", Robots: []contract.Robot{robot}},
		robot,
		nullworld.Attachment{World: "nullworld", Robot: index},
		[]byte(`{"note": "pairing"}`))
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestLifecycle(t *testing.T) {
	factory := testFactory(t)
	ctx := context.Background()
	vehicle := newVehicle(t, factory, 1)

	if err := vehicle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vehicle.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := vehicle.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || status.Name != "robot1" || status.Endpoint != "null://robot1" {
		t.Fatalf("status=%+v", status)
	}
	if status.Detail["note"] != "pairing" || status.Detail["world"] != "nullworld" {
		t.Fatalf("section/attachment did not reach the vehicle: %v", status.Detail)
	}

	if err := vehicle.Provision(ctx, orchestrator.Provisioning{
		Stations: []assets.Station{{ID: 1, X: 1, Y: 2}},
		Pose:     contract.Pose{X: 3, Z: 0.05},
	}); err != nil {
		t.Fatal(err)
	}
	status, err = vehicle.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Detail["provisioned"] != "true" || status.Detail["stations"] != "1" {
		t.Fatalf("provisioning not recorded: %v", status.Detail)
	}

	// Forward 原样回显，证明 call 的字节路由。
	request := []byte("raw-bytes\x00\x01")
	response, err := vehicle.Forward(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != string(request) {
		t.Fatalf("forward must echo verbatim: %q", response)
	}

	discovered, err := factory.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 {
		t.Fatalf("discovered=%d", len(discovered))
	}
	discoveredStatus, err := discovered[0].Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if discoveredStatus.Name != "robot1" || !discoveredStatus.Running {
		t.Fatalf("discovered status=%+v", discoveredStatus)
	}

	if err := vehicle.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := vehicle.Stop(ctx); err != nil {
		t.Fatal("stop must be idempotent")
	}
	discovered, err = factory.Discover(ctx)
	if err != nil || len(discovered) != 0 {
		t.Fatalf("expected empty discovery after stop: %v (%v)", discovered, err)
	}
}

func TestNewRejectsForeignAttachment(t *testing.T) {
	factory := testFactory(t)
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = factory.New(context.Background(),
		contract.Session{World: "simple", Robots: []contract.Robot{robot}},
		robot,
		gazebo.Attachment{Partition: "p", TopicPrefix: "robot1"},
		nil)
	if err == nil || !strings.Contains(err.Error(), `world attachment kind gazebo`) {
		t.Fatalf("foreign attachment must fail at construction: %v", err)
	}
}

func TestNewRejectsUnknownSectionFields(t *testing.T) {
	factory := testFactory(t)
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = factory.New(context.Background(),
		contract.Session{World: "nullworld", Robots: []contract.Robot{robot}},
		robot,
		nullworld.Attachment{World: "nullworld", Robot: 1},
		[]byte(`{"bogus": 1}`))
	if err == nil {
		t.Fatal("unknown section field must fail")
	}
}

func TestDiscoverOrdersByIndex(t *testing.T) {
	factory := testFactory(t)
	ctx := context.Background()
	for _, index := range []int{3, 1, 2} {
		vehicle := newVehicle(t, factory, index)
		if err := vehicle.Start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	discovered, err := factory.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 3 {
		t.Fatalf("discovered=%d", len(discovered))
	}
	for i, port := range discovered {
		status, err := port.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if want := contract.RobotName(i + 1); status.Name != want {
			t.Fatalf("discovery order[%d]=%s, want %s", i, status.Name, want)
		}
	}
}
