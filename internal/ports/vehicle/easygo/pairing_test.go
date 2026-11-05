package easygo

import (
	"context"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
	nullworld "git.standard-robots.com/yicao/sim/internal/ports/world/null"
)

// 配对约定是双向的：easygo 只认 gazebo 世界的挂接，null 世界的 Attachment
// 在 Factory.New 就必须炸——跨种类配对不带进运行期。
func TestNewRejectsNullWorldAttachment(t *testing.T) {
	robot, err := contract.NewRobot(1, contract.Pose{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Factory{}.New(context.Background(),
		contract.Session{World: "nullworld", Robots: []contract.Robot{robot}},
		robot,
		nullworld.Attachment{World: "nullworld", Robot: 1},
		nil)
	if err == nil || !strings.Contains(err.Error(), `world kind "null"`) {
		t.Fatalf("null world attachment must fail at construction: %v", err)
	}
}
