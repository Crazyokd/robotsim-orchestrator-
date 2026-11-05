package easygo

import (
	"flag"
	"testing"
)

func TestRegisterFlagsParsesVehicleOptions(t *testing.T) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	options := RegisterFlags(fs)
	if err := fs.Parse([]string{
		"--app-image", "runtime-sim:v1", "--data-root", "/tmp/data",
		"--rviz", "1,3", "--gz-gui", "2",
	}); err != nil {
		t.Fatal(err)
	}
	if options.AppImage != "runtime-sim:v1" || options.DataRoot != "/tmp/data" {
		t.Fatalf("options=%+v", options)
	}
	if !options.wantsRviz(1) || !options.wantsRviz(3) || options.wantsRviz(2) {
		t.Fatalf("rviz=%v", options.RvizRobots)
	}
	if !options.wantsGzGui(2) || options.wantsGzGui(1) {
		t.Fatalf("gz-gui=%v", options.GzGuiRobots)
	}
}

func TestOptionsRequireAnAppImage(t *testing.T) {
	if err := (Options{}).Validate(); err == nil {
		t.Fatal("app-image is required")
	}
}

func TestRobotIndicesRejectBadInput(t *testing.T) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(nil)
	RegisterFlags(fs)
	for _, arg := range []string{"0", "-1", "robot1", "1,x"} {
		if err := fs.Parse([]string{"--rviz", arg}); err == nil {
			t.Fatalf("--rviz %q must fail", arg)
		}
	}
}
