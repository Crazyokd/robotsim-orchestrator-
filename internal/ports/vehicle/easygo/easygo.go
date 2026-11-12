// Package easygo 是 EasyGo 车端 Port：在容器里跑 runtime-sim（ros2 launch
// easygo_sim_bringup sim_easygo.launch.py start_gazebo:=false），经 HTTP API
// 灌地图、注站点、放车定位。
//
// 本包的约定只属于本包：HTTP/ROS 端口算术、registry 补丁、地图坐标系
// （世界系 ↔ 地图系 mm）、"世界 = 地图 + origin_offset" 的换算。它们不进
// contract，也不进编排框架。
//
// 配对认挂接形状，不认某个世界包的类型：Factory.New 要连接端点
// （Endpoint）和相机标志（HasCameras）。给不出的世界直接失败。
package easygo

import "time"

const (
	httpBaseFrontend = 18088
	httpBaseAPI      = 18089
	httpBaseVoice    = 18091
	httpStride       = 100
	mqttBase         = 1883
	observeBase      = 8090
	webGZWebBase     = 6080
	webGZRFBBase     = 5901
	rvizWebBase      = 6081
	rvizRFBBase      = 5902
	rosDomainBase    = 11
	runtimeRoot      = "/sros"
	// appGZResourcePath lets the vehicle-side Gazebo GUI resolve model://
	// URIs. The robot description lives in the app image under /opt/sros/share,
	// world models (pallets, cages) in the bringup package. Empty here means
	// every robot link mesh fails to load and the vehicle renders as nothing.
	appGZResourcePath = "/opt/sros/share:/opt/sros/share/easygo_sim_bringup/models"

	// appReadyTimeout bounds how long a starting vehicle may take to serve its
	// API before the whole start is rolled back.
	appReadyTimeout  = 5 * time.Minute
	appReadyInterval = 2 * time.Second

	// multiAMRPeerPort is the UDP peer-link port standing in for BLE.
	multiAMRPeerPort = 16889
	// Per-robot offsets for the sync service's discovery sockets. Several
	// vehicles sharing one host network would otherwise bind the same port and
	// easygo-app would abort with "Address already in use".
	mdnsPortBase           = 25353
	syncMembershipPortBase = 25354
	syncPortStride         = 100
	maxRobotIndexPerSlot   = 10
	maxSessionSlot         = 2
)

// vehicleRuntime 是一台车的全部派生端口与 ROS domain：全部由车序号算出。
type vehicleRuntime struct {
	frontendPort       int
	apiPort            int
	voicePort          int
	mqttPort           int
	observePort        int
	webGZWebPort       int
	webGZRFBPort       int
	rvizWebPort        int
	rvizRFBPort        int
	rosDomainID        int
	runtimeRoot        string
	slamWebPort        int
	mdnsPort           int
	syncMembershipPort int
	bluetoothUDPPort   int
	webgzDisplay       int
	rvizDisplay        int
}

func runtimeFor(index, slot int) vehicleRuntime {
	rt := vehicleRuntime{
		frontendPort:       httpBaseFrontend + index*httpStride,
		apiPort:            httpBaseAPI + index*httpStride,
		voicePort:          httpBaseVoice + index*httpStride,
		mqttPort:           mqttBase + index,
		observePort:        observeBase + index*httpStride,
		webGZWebPort:       webGZWebBase + index*httpStride,
		webGZRFBPort:       webGZRFBBase + index*httpStride,
		rvizWebPort:        rvizWebBase + index*httpStride,
		rvizRFBPort:        rvizRFBBase + index*httpStride,
		rosDomainID:        rosDomainBase + (index - 1),
		runtimeRoot:        runtimeRoot,
		slamWebPort:        slamWebPort(index),
		mdnsPort:           mdnsPortBase + (index-1)*syncPortStride,
		syncMembershipPort: syncMembershipPortBase + (index-1)*syncPortStride,
		bluetoothUDPPort:   multiAMRPeerPort + slot,
		webgzDisplay:       10 + index*10 + slot*1000,
		rvizDisplay:        11 + index*10 + slot*1000,
	}
	if slot > 0 {
		// 命名会话挪出 default 的端口块。HTTP 一类加 10000*slot，ROS domain 加 100*slot。
		shift := slot * 10000
		rt.frontendPort += shift
		rt.apiPort += shift
		rt.voicePort += shift
		rt.mqttPort += slot * 100
		rt.observePort += shift
		rt.webGZWebPort += shift
		rt.webGZRFBPort += shift
		rt.rvizWebPort += shift
		rt.rvizRFBPort += shift
		rt.rosDomainID += slot * 100
		rt.slamWebPort += shift
		rt.mdnsPort += shift
		rt.syncMembershipPort += shift
	}
	return rt
}

func slamWebPort(index int) int {
	return 8088 + (index-1)*10
}
