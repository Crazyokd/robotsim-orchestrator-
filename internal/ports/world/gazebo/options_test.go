package gazebo

import (
	"flag"
	"testing"
)

func TestRegisterFlagsParsesWorldOptions(t *testing.T) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	options := RegisterFlags(fs)
	if err := fs.Parse([]string{"--env-image", "simenv-world-gazebo:local", "--cameras", "2,3"}); err != nil {
		t.Fatal(err)
	}
	if options.EnvImage != "simenv-world-gazebo:local" {
		t.Fatalf("env-image=%q", options.EnvImage)
	}
	if len(options.CameraRobots) != 2 || options.CameraRobots[0] != 2 || options.CameraRobots[1] != 3 {
		t.Fatalf("cameras=%v", options.CameraRobots)
	}
}

func TestOptionsRequireAnEnvImage(t *testing.T) {
	if err := (Options{}).Validate(); err == nil {
		t.Fatal("env-image is required")
	}
}

func TestRobotIndicesRejectBadInput(t *testing.T) {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(nil)
	RegisterFlags(fs)
	for _, arg := range []string{"0", "-1", "robot1", "1,x"} {
		if err := fs.Parse([]string{"--cameras", arg}); err == nil {
			t.Fatalf("--cameras %q must fail", arg)
		}
	}
}
