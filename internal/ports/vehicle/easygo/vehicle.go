package easygo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
	"git.standard-robots.com/yicao/sim/internal/orchestrator"
	"git.standard-robots.com/yicao/sim/internal/ports/runtime/docker"
	"git.standard-robots.com/yicao/sim/internal/ports/world/gazebo"
)

// httpClient 给所有车端 HTTP 调用用：带超时——车端挂住时 Start 必须失败，
// 不能吊死在一次没有截止的 Do 上。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// Vehicle 是一台 EasyGo 车的 Port：容器里跑 runtime-sim，宿主经 HTTP API 驱动它。
type Vehicle struct {
	Engine        docker.Engine
	Options       Options
	Session       contract.Session
	Robot         contract.Robot
	ContainerName string
	// AppReady 是 WaitReady 的探活钩子（测试用）；nil 用真的 HTTP 探测。
	AppReady  func(ctx context.Context, apiPort int) error
	AssetsDir string
	DoJSON    func(ctx context.Context, method, path string, payload []byte) ([]byte, int, error)
	Dial      func(ctx context.Context, network, address string) (net.Conn, error)
	// Link 是世界的挂接信息（GZ transport 三要素 + 相机配对），来自世界 Port 的 Attachment。
	Link gazebo.Attachment
	// Section 是 bundle vehicles.easygo 节（裸世界为零值，不灌图不定位）。
	Section       Section
	MapExportPath string
}

func (v Vehicle) Start(ctx context.Context) error {
	if err := v.Session.Validate(); err != nil {
		return err
	}
	if err := v.Options.Validate(); err != nil {
		return err
	}
	for _, index := range v.Options.RvizRobots {
		if index < 1 || index > len(v.Session.Robots) {
			return fmt.Errorf("rviz robot %d outside robot count %d", index, len(v.Session.Robots))
		}
	}
	for _, index := range v.Options.GzGuiRobots {
		if index < 1 || index > len(v.Session.Robots) {
			return fmt.Errorf("gz-gui robot %d outside robot count %d", index, len(v.Session.Robots))
		}
	}
	runtime := runtimeFor(v.Robot.Index)
	useRviz := v.Options.wantsRviz(v.Robot.Index)
	useGzGui := v.Options.wantsGzGui(v.Robot.Index)
	cmd := append([]string{"ros2", "launch", "easygo_sim_bringup", "sim_easygo.launch.py"}, launchArgs(v.Robot, v.Link.TopicPrefix, runtime, useRviz, useGzGui, v.Link.Cameras)...)
	var binds []string
	if prefix := strings.TrimSpace(v.Options.DataVolumePrefix); prefix != "" {
		// Volume 形态：daemon 自管数据卷，首次挂载继承镜像 /sros（0777，车端
		// std 可写）。registry 补丁与世界 SDF handover 都依赖宿主机路径绑定
		// （daemon 与 simenv 不共享文件系统时，宿主上路径不存在，文件 bind 会
		// 被 daemon 建成目录再挂到文件目标而失败），此形态下一律不挂——车辆
		// 身份由 launch 的 SERIAL_NUMBER 播种（单车无 sync 端口冲突），世界
		// SDF 用车端镜像自带那份（与环境 pinned 资产同源）。
		binds = append(binds, prefix+"-"+v.Robot.Name+":"+runtimeRoot)
	} else {
		dataRoot := v.Options.DataRoot
		if dataRoot == "" {
			cache, err := os.UserCacheDir()
			if err != nil {
				return err
			}
			dataRoot = filepath.Join(cache, "simenv")
		}
		hostSros := filepath.Join(dataRoot, v.Robot.Name)
		if err := os.MkdirAll(hostSros, 0o755); err != nil {
			return err
		}
		// The vehicle process runs as the image's std user (uid 1000) while
		// simenv may run as another uid (CI runs as root). The bind target
		// must stay writable for the vehicle, otherwise its launch refuses
		// to start.
		if err := os.Chmod(hostSros, 0o777); err != nil {
			return fmt.Errorf("make %s writable for the vehicle user: %w", hostSros, err)
		}
		appConfigBinds, err := v.initializeAppConfig(ctx, v.Options.AppImage, hostSros)
		if err != nil {
			return err
		}
		binds = append([]string{hostSros + ":" + runtimeRoot}, appConfigBinds...)
		if handover := worldHandoverBind(v.AssetsDir, v.Session.World); handover != "" {
			binds = append(binds, handover)
		}
	}
	if useGzGui {
		bind, err := gzGuiAssetBind(v.AssetsDir)
		if err != nil {
			return err
		}
		binds = append(binds, bind)
	}
	if err := v.Engine.Start(ctx, docker.ContainerSpec{
		Name:    v.containerName(),
		Image:   v.Options.AppImage,
		Command: cmd,
		Env:     appEnv(v.Robot, v.Session, runtime, v.Link),
		Binds:   binds,
		Network: "host",
		Ipc:     "host",
		Devices: guiDevices(useRviz || useGzGui),
		Labels: map[string]string{
			docker.LabelManaged: "true",
			docker.LabelRole:    docker.RoleApp,
			vehicleRobotLabel:   v.Robot.Name,
			vehicleIndexLabel:   strconv.Itoa(v.Robot.Index),
		},
	}); err != nil {
		return err
	}
	return nil
}

func (v Vehicle) Status(ctx context.Context) (orchestrator.VehicleStatus, error) {
	info, err := v.Engine.Inspect(ctx, v.containerName())
	if err != nil {
		return orchestrator.VehicleStatus{}, err
	}
	return orchestrator.VehicleStatus{
		Name:     v.Robot.Name,
		Running:  info.Running,
		Endpoint: fmt.Sprintf("http://127.0.0.1:%d", runtimeFor(v.Robot.Index).apiPort),
		Detail:   map[string]string{"container": info.Name, "image": info.Image},
	}, nil
}

func (v Vehicle) Stop(ctx context.Context) error {
	return v.Engine.Stop(ctx, v.containerName())
}

// containerName 是本 Port 的容器命名约定。Discover 找回的容器以实际名字为准
// （label 携带车身份，名字不必遵守约定）。
func (v Vehicle) containerName() string {
	if v.ContainerName != "" {
		return v.ContainerName
	}
	return "simenv-" + v.Robot.Name
}

// WaitReady blocks until the vehicle's app answers on its API, so that a
// successfully started vehicle is one that is actually serving.
func (v Vehicle) WaitReady(ctx context.Context) error {
	probe := v.AppReady
	if probe == nil {
		probe = waitForAppAPI
	}
	if err := probe(ctx, runtimeFor(v.Robot.Index).apiPort); err != nil {
		return fmt.Errorf("wait for %s app: %w", v.Robot.Name, err)
	}
	return nil
}

// waitForAppAPI 等车端真的可用：API 答话，并且 init 报 succeeded。
// API 答话不等于就绪——init 没完成前车端不接受初始位姿。
func waitForAppAPI(ctx context.Context, apiPort int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v6/map", apiPort)
	deadline := time.Now().Add(appReadyTimeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return waitForVehicleInit(ctx, apiPort)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("app API on port %d silent for %s", apiPort, appReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(appReadyInterval):
		}
	}
}

// Provision 把 world init 的输入一次性落进车端：灌地图 → 注站点 → 放车定位。
// 地图主名与 bundle 声明不一致直接失败（世界换了而地图没跟上，不留半成品）；
// 站点与位姿从世界系换算成地图系（mm / mrad）后再下发——换算错了是本 Port 报错。
func (v Vehicle) Provision(ctx context.Context, p orchestrator.Provisioning) error {
	if v.MapExportPath == "" {
		return fmt.Errorf("provision %s: vehicle has no vehicles.%s section", v.Robot.Name, Kind)
	}
	archive, err := os.ReadFile(v.MapExportPath)
	if err != nil {
		return fmt.Errorf("world %s declares map %q but ships no map archive: %w", v.Session.World, v.Section.MapName, err)
	}
	mapName, err := worldMapName(archive)
	if err != nil {
		return fmt.Errorf("world %s map: %w", v.Session.World, err)
	}
	if mapName != v.Section.MapName {
		return fmt.Errorf(
			"world %s: map archive holds %q but bundle declares %q", v.Session.World, mapName, v.Section.MapName,
		)
	}
	if err := v.seedMap(ctx, archive); err != nil {
		return err
	}
	if err := v.seedStations(ctx, mapName, p.Stations); err != nil {
		return err
	}
	return v.localize(ctx, mapName, mapPose(p.Pose, v.Section.OriginOffsetMM))
}

// seedMap imports a world map archive into the vehicle's easygo-app.
func (v Vehicle) seedMap(ctx context.Context, archive []byte) error {
	runtime := runtimeFor(v.Robot.Index)
	importURL := fmt.Sprintf("http://127.0.0.1:%d/api/v6/map/import", runtime.apiPort)

	body := new(bytes.Buffer)
	mp := multipart.NewWriter(body)
	part, err := mp.CreateFormFile("file", "world.map_export")
	if err != nil {
		return fmt.Errorf("seed map %s: build multipart: %w", v.Robot.Name, err)
	}
	if _, err := part.Write(archive); err != nil {
		return fmt.Errorf("seed map %s: write body: %w", v.Robot.Name, err)
	}
	mp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, importURL, body)
	if err != nil {
		return fmt.Errorf("seed map %s: %w", v.Robot.Name, err)
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("seed map %s: %w", v.Robot.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && !mapAlreadyExists(respBody) {
		return fmt.Errorf("seed map %s: %s (%s)", v.Robot.Name, resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// localize puts this vehicle on the map at the given **map** pose (mapPose 换算
// 后的结果)。easygo-app takes millimetres and milliradians and drives the AGV
// through the VDA5050 initPosition action; 这里在下发之后再断言一次收敛——下发成功
// 不等于定位成功，坐标系搞错时车端只会静默地定到别处。
func (v Vehicle) localize(ctx context.Context, mapName string, pose contract.Pose) error {
	body, err := initialPoseRequest(mapName, pose)
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v6/vehicle/localization/initial-pose", runtimeFor(v.Robot.Index).apiPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("localize %s: %s (%s)", v.Robot.Name, resp.Status, strings.TrimSpace(string(respBody)))
	}
	return v.awaitPose(ctx, pose)
}

// initialPoseRequest builds the app's initial-pose body. The world spawns the
// robot in metres and radians; the app takes millimetres and milliradians.
func initialPoseRequest(mapName string, pose contract.Pose) ([]byte, error) {
	return json.Marshal(map[string]any{
		"map_id":  mapName,
		"x":       pose.X * 1000,
		"y":       pose.Y * 1000,
		"theta":   pose.Yaw * 1000,
		"loctype": 2,
	})
}
