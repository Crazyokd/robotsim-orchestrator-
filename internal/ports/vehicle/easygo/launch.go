package easygo

import (
	"fmt"
	"strconv"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

func launchArgs(robot contract.Robot, topicPrefix string, runtime vehicleRuntime, useRviz, useGzGui, useCameras bool) []string {
	return []string{
		"start_gazebo:=false",
		fmt.Sprintf("use_gz_gui:=%t", useGzGui),
		fmt.Sprintf("use_rviz:=%t", useRviz),
		"enable_person_follow:=false",
		fmt.Sprintf("bridge_rgbd:=%t", useCameras),
		"enable_multi_robot_wifi:=true",
		"robot_name:=" + topicPrefix,
		"runtime_root:=" + runtime.runtimeRoot,
		"map_path:=" + runtime.runtimeRoot + "/map/",
		fmt.Sprintf("frontend_port:=%d", runtime.frontendPort),
		fmt.Sprintf("api_port:=%d", runtime.apiPort),
		fmt.Sprintf("param_http_port:=%d", runtime.paramHTTPPort),
		fmt.Sprintf("voice_port:=%d", runtime.voicePort),
		fmt.Sprintf("mqtt_port:=%d", runtime.mqttPort),
		fmt.Sprintf("slam_web_port:=%d", runtime.slamWebPort),
		fmt.Sprintf("observe_port:=%d", runtime.observePort),
		fmt.Sprintf("webgz_web_port:=%d", runtime.webGZWebPort),
		fmt.Sprintf("webgz_rfb_port:=%d", runtime.webGZRFBPort),
		fmt.Sprintf("webgz_display:=:%d", runtime.webgzDisplay),
		"webgz_log_dir:=/tmp/webgz-" + robot.Name,
		fmt.Sprintf("rviz_web_port:=%d", runtime.rvizWebPort),
		fmt.Sprintf("rviz_rfb_port:=%d", runtime.rvizRFBPort),
		fmt.Sprintf("rviz_display:=:%d", runtime.rvizDisplay),
		"rviz_log_dir:=/tmp/rviz-" + robot.Name,
		"serial_number:=" + robot.Name,
		"easygo_app_param_db_path:=" + runtime.runtimeRoot + "/db/cfg.db3",
		"database_url:=sqlite://" + runtime.runtimeRoot + "/db/easygo.db?mode=rwc",
	}
}

func appEnv(robot contract.Robot, runtime vehicleRuntime, link WorldLink) map[string]string {
	return map[string]string{
		"ROS_DOMAIN_ID":                 strconv.Itoa(runtime.rosDomainID),
		"ROS_AUTOMATIC_DISCOVERY_RANGE": "LOCALHOST",
		"RMW_IMPLEMENTATION":            "rmw_cyclonedds_cpp",
		"CYCLONEDDS_URI":                cycloneDDSURI(),
		"GZ_IP":                         link.IP,
		"GZ_PARTITION":                  link.Partition,
		"SERIAL_NUMBER":                 robot.Name,
		"MQTT_BROKER_PORT":              strconv.Itoa(runtime.mqttPort),
		"BACKEND_PORT":                  strconv.Itoa(runtime.voicePort),
		"PARAM_CFG_DIR":                 appRegistryDir,
		"PARAM_DB_PATH":                 runtime.runtimeRoot + "/db/cfg.db3",
		"DATABASE_URL":                  "sqlite://" + runtime.runtimeRoot + "/db/easygo.db?mode=rwc",
		"EASYGO_SIM_WORLD":              link.WorldPath,
		"EASYGO_SIM_APP_ROOT":           runtime.runtimeRoot + "/easygo-app",
		"EASYGO_WEBAPP_DIR":             runtime.runtimeRoot + "/webapp",
		"GZ_SIM_RESOURCE_PATH":          appGZResourcePath,
		// Multi-AMR peer discovery: a UDP broadcast stands in for the BLE peer
		// link a real vehicle uses, so two robots in one host can see each other.
		"EASYGO_BLUETOOTH_TRANSPORT": "udp",
		"EASYGO_BLUETOOTH_UDP_PORT":  strconv.Itoa(runtime.bluetoothUDPPort),
		"ENABLE_MULTI_AMR_AVOIDANCE": "true",
		// The sync service advertises this port for mDNS discovery. The launch
		// owns PORT (the app's HTTP port); only mDNS needs a per-robot offset.
		"MDNS_PORT": strconv.Itoa(runtime.mdnsPort),
	}
}

func cycloneDDSURI() string {
	return `<CycloneDDS><Domain><General><Interfaces><NetworkInterface name="lo" multicast="false" /></Interfaces><AllowMulticast>false</AllowMulticast><DontRoute>true</DontRoute></General><Discovery><Peers><Peer Address="127.0.0.1" /></Peers><ParticipantIndex>auto</ParticipantIndex><MaxAutoParticipantIndex>255</MaxAutoParticipantIndex></Discovery></Domain></CycloneDDS>`
}
