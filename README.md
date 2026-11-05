# simenv

独立仿真环境：一份世界，N 套现有 `runtime-sim` 车端。框架只认识 Port 接口；
世界（Gazebo）与车端（EasyGo）都是 `internal/ports/` 下的可替换实现。

| 文档 | 用途 |
| --- | --- |
| [`AGENTS.md`](AGENTS.md) | 仓库规则 |
| [`docs/superpowers/specs/2026-11-05-ports-architecture.md`](docs/superpowers/specs/2026-11-05-ports-architecture.md) | Port 化架构合同：接口、bundle 格式、施工顺序 |
| [`docs/superpowers/specs/2026-10-27-junctions-world.md`](docs/superpowers/specs/2026-10-27-junctions-world.md) | T / 十字 / 窄门 / 带待避区窄路综合世界与单车基线 |

```text
go test ./...
go build -o simenv ./cmd/simenv
docker build -f deploy/Dockerfile -t simenv-world-gazebo:local .
./simenv start --robots 1 --world simple \
  --env-image simenv-world-gazebo:local \
  --app-image docker-registry.nowhere.sr/easygo/runtime-sim:<tag>
./simenv status
./simenv pose --robot 1
./simenv reset --robot 1 --x 0 --y 0 --yaw 0
# 标准输入是已经拼好的请求字节，标准输出是车端回的字节
./simenv call --robot 1 < request.bin
./simenv stop
```

`--world` / `--env-image` / `--app-image` 必填，没有默认值。

世界是一个 bundle：`deploy/assets/worlds/<world>/`（manifest + 各 Port 的文件，
按 kind 分节）。站点与初始位姿是世界系 m / rad；车端要的地图与坐标偏移在
`vehicles.<kind>` 节里声明。

N=1 的 API 在 `http://127.0.0.1:18189/api/v6`。观察视图按车单独开关：`--rviz 1,2` 给指定的车开 RViz web（N=1 在 `http://127.0.0.1:6181/vnc.html`），`--gz-gui 1` 开 web-gz 3D（N=1 在 `http://127.0.0.1:6180/vnc.html`）。端口按契约算术 `base+100N`——N=2 的 RViz 在 `:6281`。默认 headless。

gz-gui 一个窗口就包含所有车；不需要每车开一个。环境镜像不含 easygo-app。既有 worlds / 机器人模型来自 EasyGo `05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1`，记录在 `deploy/assets/EASYGO_WORLD_SOURCE_SHA`。`junctions` 几何由本仓新增，复用同一 pinned 机器人模型。

宿主有 `/dev/dri` 时，世界容器会挂载它做硬件射线检测；没有则退回 llvmpipe 软件渲染，N≥2 跑不到实时。
