package easygo

import (
	"context"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
)

func TestPreflightRejectsRuntimeImageWithoutSyncLaunchArgument(t *testing.T) {
	engine := &fakeEngine{imageLaunch: []byte("DeclareLaunchArgument('serial_number')")}
	factory := Factory{Engine: engine, Options: Options{AppImage: "old-runtime:tag"}}
	if err := factory.ValidateStart(context.Background(), contract.Session{World: "simple", Robots: []contract.Robot{{Index: 1, Name: "robot1"}}}, nil); err == nil {
		t.Fatal("old runtime image passed the sync launch gate")
	}
}

var _ docker.Engine = (*fakeEngine)(nil)
