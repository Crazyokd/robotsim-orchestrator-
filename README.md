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

`--world` / `--env-image` / `--app-image` 必填，没有默认值。可选 `--session <name>` 指定会话名；不写就生成一个，例如 `brisk-otter`，打在 start 的第一行。`./simenv status` 列出全部会话，`./simenv status <name>` 看这一路的车和端口。`./simenv stop` 停最近启动的那一路，`./simenv stop <name>` 停指定的那一路。

点名控制时，会话名放在 flags 前：`./simenv pose alpha --robot 1`、`./simenv reset alpha --robot 1 --x 0 --y 0 --yaw 0`、`./simenv call alpha --robot 1 < request.bin`。同名重启保留原 slot；启动前由各 Port 校验选项、资产和镜像，再替换旧会话。启动失败但未清理干净的会话保留在名单中，可用 `stop <name>` 重试；一路状态查询失败仍显示其他会话，同时命令返回失败。

名字允许字母、数字、下划线和连字符，但不得以连字符开头；位姿各分量必须为有限数值。世界容器使用 `simenv-world-<session>`，车端使用 `simenv-vehicle-<session>-robotN`，两类资源使用独立的命名空间。

EasyGo/Gazebo 当前支持 slot 0..2、每路最多 10 台车。EasyGo 数据在 `<data-root>/<session>/robotN`（默认 `$XDG_CACHE_HOME/simenv`）；named volume 为 `<prefix>-<session>-slot<slot>-robotN`，sync registry 会在容器启动前通过 Docker archive 注入，因此支持多车和非零 slot。slot 也属于卷名，避免旧数据库在 slot 改变后被复用。已有参数库的数据目录不能跨 slot 复用，因为 registry 模板不会改写已有参数值；遇到冲突需使用新的会话名或数据根。`start`/`stop` 对同一份名单互斥，冲突时报忙并提示重试。

无 Docker 的开发模式：`./simenv start --world nullworld --robots 2`（两个必填镜像 flag 只对 gazebo/easygo 实现生效，null 对零参数）。null 对是 `internal/ports/{world,vehicle}/null` 的状态文件实现——零容器零进程，`status`/`pose`/`reset`/`call`（原样回显）全部可用，状态在 `$XDG_CACHE_HOME/simenv/null*/`。它同时是「加实现不动框架」的架构自检：加一对新 Port = 两个新包 + 组合根注册表两行 + 一个 bundle 目录。实现种类由 bundle 的 `worlds`/`vehicles` 唯一键推导；不带 bundle 的裸世界必须显式给 `--world-kind` / `--vehicle-kind`。

世界的 SDF 路径以 bundle 的 `worlds.gazebo.sdf` 为准，内容和模型取自 `--env-image` 中的 `/opt/simenv`；车端在启动前通过 Docker API 接收同一份资产，并使用显式 SDF 路径。资产交接不依赖宿主目录 bind，缺失或复制失败会直接报错。修改世界或模型后须重建环境镜像；程序镜像需使用支持显式 `world` 路径的当前 runtime-sim。

世界是一个 bundle：`deploy/assets/worlds/<world>/`（manifest + 各 Port 的文件，
按 kind 分节）。站点与初始位姿是世界系 m / rad；车端要的地图与坐标偏移在
`vehicles.<kind>` 节里声明。

N=1 的 API 在 `http://127.0.0.1:18189/api/v6`。观察视图按车单独开关：`--rviz 1,2` 给指定的车开 RViz web（N=1 在 `http://127.0.0.1:6181/vnc.html`），`--gz-gui 1` 开 web-gz 3D（N=1 在 `http://127.0.0.1:6180/vnc.html`）。端口按契约算术 `base+100N`——N=2 的 RViz 在 `:6281`。默认 headless。

gz-gui 一个窗口就包含所有车；不需要每车开一个。环境镜像不含 easygo-app。既有 worlds / 机器人模型来自 EasyGo `05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1`，记录在 `deploy/assets/EASYGO_WORLD_SOURCE_SHA`。`junctions` 几何由本仓新增，复用同一 pinned 机器人模型。

宿主有 `/dev/dri` 时，世界容器会挂载它做硬件射线检测；没有则退回 llvmpipe 软件渲染，N≥2 跑不到实时。
