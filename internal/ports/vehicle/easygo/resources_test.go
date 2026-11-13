package easygo

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func TestAllSessionListenersAndDisplaysAreIsolated(t *testing.T) {
	tcp := map[int]string{}
	udp := map[int]string{}
	displays := map[string]string{}
	for slot := 0; slot <= maxSessionSlot; slot++ {
		for index := 1; index <= maxRobotIndexPerSlot; index++ {
			owner := fmt.Sprintf("slot %d robot %d", slot, index)
			robot := contract.Robot{Index: index, Name: contract.RobotName(index)}
			runtime := runtimeFor(index, slot)
			args := map[string]string{}
			for _, arg := range launchArgs(robot, robot.Name, runtime, true, true, false) {
				key, value, ok := strings.Cut(arg, ":=")
				if ok {
					args[key] = value
				}
			}
			for _, key := range []string{"frontend_port", "api_port", "param_http_port", "voice_port", "mqtt_port", "slam_web_port", "observe_port", "webgz_web_port", "webgz_rfb_port", "rviz_web_port", "rviz_rfb_port"} {
				port, err := strconv.Atoi(args[key])
				if err != nil || port < 1 || port > 65535 {
					t.Fatalf("%s %s has invalid port %q", owner, key, args[key])
				}
				if previous, ok := tcp[port]; ok {
					t.Fatalf("%s %s TCP %d collides with %s", owner, key, port, previous)
				}
				tcp[port] = owner + " " + key
			}
			for _, key := range []string{"webgz_display", "rviz_display"} {
				if previous, ok := displays[args[key]]; ok {
					t.Fatalf("%s %s display collides with %s", owner, key, previous)
				}
				displays[args[key]] = owner + " " + key
			}
			for _, port := range []int{runtime.mdnsPort, runtime.syncMembershipPort} {
				if previous, ok := udp[port]; ok {
					t.Fatalf("%s UDP %d collides with %s", owner, port, previous)
				}
				udp[port] = owner
			}
			if index == 1 {
				if previous, ok := udp[runtime.bluetoothUDPPort]; ok {
					t.Fatalf("session peer UDP collides with %s", previous)
				}
				udp[runtime.bluetoothUDPPort] = owner + " peers"
			}
		}
	}
}
