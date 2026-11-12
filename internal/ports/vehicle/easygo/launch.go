package easygo

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"git.standard-robots.com/yicao/sim/internal/assets"
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
		fmt.Sprintf("voice_port:=%d", runtime.voicePort),
		fmt.Sprintf("mqtt_port:=%d", runtime.mqttPort),
		fmt.Sprintf("slam_web_port:=%d", runtime.slamWebPort),
		fmt.Sprintf("observe_port:=%d", runtime.observePort),
		fmt.Sprintf("webgz_web_port:=%d", runtime.webGZWebPort),
		fmt.Sprintf("webgz_rfb_port:=%d", runtime.webGZRFBPort),
		fmt.Sprintf("webgz_display:=:%d", 10+robot.Index*10),
		"webgz_log_dir:=/tmp/webgz-" + robot.Name,
		fmt.Sprintf("rviz_web_port:=%d", runtime.rvizWebPort),
		fmt.Sprintf("rviz_rfb_port:=%d", runtime.rvizRFBPort),
		fmt.Sprintf("rviz_display:=:%d", 11+robot.Index*10),
		"rviz_log_dir:=/tmp/rviz-" + robot.Name,
		"serial_number:=" + robot.Name,
		"easygo_app_param_db_path:=" + runtime.runtimeRoot + "/db/cfg.db3",
		"database_url:=sqlite://" + runtime.runtimeRoot + "/db/easygo.db?mode=rwc",
	}
}

func appEnv(robot contract.Robot, session contract.Session, runtime vehicleRuntime, link WorldLink) map[string]string {
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
		"EASYGO_SIM_WORLD":              session.World + ".sdf",
		"EASYGO_SIM_APP_ROOT":           runtime.runtimeRoot + "/easygo-app",
		"EASYGO_WEBAPP_DIR":             runtime.runtimeRoot + "/webapp",
		"GZ_SIM_RESOURCE_PATH":          appGZResourcePath,
		// Multi-AMR peer discovery: a UDP broadcast stands in for the BLE peer
		// link a real vehicle uses, so two robots in one host can see each other.
		"EASYGO_BLUETOOTH_TRANSPORT": "udp",
		"EASYGO_BLUETOOTH_UDP_PORT":  strconv.Itoa(multiAMRPeerPort),
		"ENABLE_MULTI_AMR_AVOIDANCE": "true",
		// The sync service advertises this port for mDNS discovery. The launch
		// owns PORT (the app's HTTP port); only mDNS needs a per-robot offset.
		"MDNS_PORT": strconv.Itoa(runtime.mdnsPort),
	}
}

// gzGuiAssetBind mounts this checkout's deploy/assets at /opt/simenv so the
// vehicle-side Gazebo GUI can read the world props. The world SDF includes
// model://black_pallet and model://cage1, which the world server resolves to
// /opt/simenv/models/... at load time -- absolute paths it then sends in the
// scene message. Those exist only inside the environment container, so a GUI
// without this mount renders the world without its props.
//
// 资产目录解析走 internal/assets（exe 相对）；要开 gz-gui 而资产不在场
// 是错误——静默少挂一个目录，GUI 渲染出来的世界会缺道具还找不到原因。
func gzGuiAssetBind(assetsDir string) (string, error) {
	root := assets.Path(assetsDir)
	if _, err := os.Stat(filepath.Join(root, "models")); err != nil {
		return "", fmt.Errorf("gz-gui needs the world assets (models/): %w", err)
	}
	return root + ":/opt/simenv", nil
}

func cycloneDDSURI() string {
	return `<CycloneDDS><Domain><General><Interfaces><NetworkInterface name="lo" multicast="false" /></Interfaces><AllowMulticast>false</AllowMulticast><DontRoute>true</DontRoute></General><Discovery><Peers><Peer Address="127.0.0.1" /></Peers><ParticipantIndex>auto</ParticipantIndex><MaxAutoParticipantIndex>255</MaxAutoParticipantIndex></Discovery></Domain></CycloneDDS>`
}
