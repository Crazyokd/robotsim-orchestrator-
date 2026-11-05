# simenv：Port 化架构（框架不动，加 Port）

日期：2026-11-05。前置合同：`2026-09-21-simenv-design.md`（世界/程序边界）、`2026-09-21-simenv-framework-design.md`（分层与类型）、`2026-10-12-world-bundle-design.md`（世界 bundle）。本合同取代前两者中与本合同冲突的条目；world-bundle 的 manifest 格式被 §5 取代。

## 1. 目标与非目标

**目标**：simenv 是机器人仿真编排系统。新增一个仿真实现（世界 Port）或一个车端实现（车端 Port）时：

- 框架（`internal/contract`、`internal/orchestrator`、`internal/assets`、`internal/cli`）**一行不改**；
- 组合根（`cmd/simenv`）加一行注册；
- 新实现 = 一个新包。

**非目标**：不做动态插件加载；单实现期间不加 `--world-port` / `--vehicle-port` 选择 flag；不替「世界 × 车端」配对抽象协议——车端要学会说哪个仿真器的话是车端的事（§7）。

## 2. 已拍板的决定（2026-11-05）

1. `scenarios` 出 bundle；场景 runner 之后收进仓内（`internal/scenario` + `cmd/simenv-scenario`），本轮不做。
2. 世界容器二进制/镜像改名 `simenv-world` → `simenv-world-gazebo`。
3. AGENTS.md 由 agent 起草、用户过目后提交。
4. 未发版不做兼容：契约与 bundle 直接改成新形态，不留旧字段、不写迁移代码，旧文件一次性重写。

## 3. 完成判据

1. `internal/{contract,orchestrator,assets,cli}` 不 import `internal/ports/**`，源码不出现 `gazebo|gz|easygo|docker|ros` 字样（CI `rg` 断言）。
2. `deploy/assets` 的 bundle 与 `--session` JSON 不含实现专属字段；实现专属内容按 kind 分节，由对应 Port 严格解析。
3. 新增 Port = 新包 + 组合根一行。
4. N=1 真跑门禁（`simple`、`corridor`）与现状相同。

## 4. 目标目录

```
cmd/simenv                          组合根：Engine、按 kind 构造 Port、挂各 Port 的 flag
cmd/simenv-world-gazebo             Gazebo 世界容器内进程
internal/contract                   Session · Robot · Pose · WorldBundle
internal/orchestrator               状态机 · 接口 · Status · StartConfig · 通用 world init
internal/assets                     资产定位与 bundle 装载，唯一入口，缺了就报错
internal/cli                        只解析框架 flag，调 Runner
internal/ports/runtime/docker       Engine 实现
internal/ports/world/gazebo         宿主侧 DockerWorld、ready 轮询、Attachment 定义
internal/ports/world/gazebo/process 容器侧 Process · GZ · SDF 重写（现 internal/world）
internal/ports/vehicle/easygo       EasyGoVehicle、factory、端口算术、registry 补丁、
                                    地图导入/站点/初始位姿客户端、世界系→地图系换算
internal/ports/vehicle/easygo/gazebo.go  EasyGo-on-Gazebo 配对：launch 参数、GZ 环境变量、世界 SDF 交接 bind
```

依赖方向唯一：`ports/** → orchestrator → contract`。Go 循环引用禁止保证框架回不了头。

## 5. 线上契约

### 5.1 Session（线上格式，进 `--session`）

```json
{"world": "corridor", "robots": [{"index": 1, "name": "robot1", "pose": {"x": -14, "y": 1.2, "z": 0.05, "yaw": 0}}]}
```

- 只剩 `world / robots[index, name, pose]`；`pose` 为世界系 m / rad。未发版：不加版本字段、不做握手，宿主二进制与环境镜像同步重建是约定。
- `partition`、`topic_prefix` 移出：它们是 Gazebo 接线，由 Gazebo Port 持有并经 Attachment 交给车端（§7）。

### 5.2 bundle（`deploy/assets/worlds/<world>/bundle.json`）

```
deploy/assets/worlds/corridor/
  bundle.json
  gazebo/world.sdf
  easygo/corridor.map_export
```

```json
{
  "world": "corridor",
  "stations":      [{"id": 1, "x": -14.0, "y": 1.2, "yaw": 0}],
  "initial_poses": [{"index": 1, "x": -14.0, "y": 1.2, "z": 0.05, "yaw": 0}],
  "source_sha": "05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1",
  "worlds":   {"gazebo": {"sdf": "gazebo/world.sdf"}},
  "vehicles": {"easygo": {"map_export": "easygo/corridor.map_export", "map_name": "corridor",
                          "origin_offset_mm": {"x": -288.0, "y": 454.0}}}
}
```

- `stations` / `initial_poses` 为**世界系 m / rad**。现有两个世界的站点是车端地图系 mm，一次性换算重写：`world = (map_mm + offset_mm)/1000`（corridor 站点 1 换算后恰为 (-14.0, 1.2)，与 robot1 初始位姿重合）。
- `worlds` / `vehicles` 框架只读成 `map[string]json.RawMessage`；按选中 kind 取一节交给 Port 严格解析（`DisallowUnknownFields`）。缺节即失败。
- `scenarios`、`map_name` 顶层字段删除。

## 6. 框架接口（`internal/orchestrator`）

```go
type Attachment interface{ Kind() string }   // 世界 Port 产出，框架只按车转交

type WorldPort interface {
    Start(ctx, contract.Session) ([]Attachment, error)   // 每车一个
    Ready(ctx, contract.Session) (WorldReady, error)
    Status(ctx) (WorldStatus, error)
    Stop(ctx) error
    Pose(ctx, robot int) (EntityPose, error)             // 运行中会话控制面
    Reset(ctx, robot int, pose contract.Pose) (EntityPose, error)
}
type WorldReady  struct{ World string; Robots []string }
type WorldStatus struct{ Present, Running bool; Ready WorldReady; Detail map[string]string }
type EntityPose  struct{ Robot int; Entity string; Pose contract.Pose }   // 世界系

type VehicleFactory interface {
    New(ctx, contract.Session, contract.Robot, Attachment, assets.Section) (VehiclePort, error)
    Discover(ctx) ([]VehiclePort, error)
}
type VehiclePort interface {
    Start(ctx) error
    WaitReady(ctx) error
    Provision(ctx, Provisioning) error   // 合并现 SeedMap + SeedStations + Localize
    Status(ctx) (VehicleStatus, error)
    Stop(ctx) error
    Forward(ctx, request []byte) ([]byte, error)   // call --robot：字节原样进出，框架不解析
}
type Provisioning struct {
    Stations []contract.Station   // 世界系
    Pose     contract.Pose        // 世界系
}
type VehicleStatus struct{ Name string; Running bool; Endpoint string; Detail map[string]string }
```

- `Engine`、`AppReady` 从 `Orchestrator` 移除：Docker 是 Port 的基础设施，HTTP 探活是 EasyGo 的 `WaitReady` 内部事务。
- `Status` 不再有 `APIPort` / `Image` 字段；实现专属信息进 `Detail`，CLI 原样打印。
- `worldInit` 变通用：bundle → 按 kind 取节 → 每台车一次 `Provision`。任一步失败即回滚（沿用现状）。
- 控制面只路由不解析：`pose` / `reset` 的世界系位姿交给 `WorldPort`；`call --robot` 的字节交给该车的 `Forward`。怎么 dial、什么协议是车端 Port 自己的事。
- `StartConfig` 只剩 `World / Robots / Poses`；`--poses` 与 bundle `initial_poses` 同给时报错。

## 7. 配对：Attachment 是显式的

世界 Port 启动后产出每车的 `Attachment`；车端 `Factory.New` 收到后 type-switch：

- 认得 → 继续；不认得或缺它那种资产节 → **报错**。
- `ports/vehicle/easygo` import `ports/world/gazebo` 拿 `gazebo.Attachment{Partition, TopicPrefix, IP}`——EasyGo-on-Gazebo 是一个配对，import 是有意为之。
- Gazebo Port 的容器进程、`--cameras`、partition 常量、`robotN` 话题前缀全部留在 `ports/world/gazebo`；`GZ_IP` / `GZ_PARTITION` 是它的约定，不是仓库级约定。

## 8. 配置与 flag

各 Port 暴露 `Options` 与 `RegisterFlags(*flag.FlagSet) *Options`；组合根把各组挂到同一个 FlagSet：

- `gazebo.Options{EnvImage, CameraRobots}` —— `--env-image` 必填
- `easygo.Options{AppImage, DataRoot, RvizRobots, GzGuiRobots}` —— `--app-image` 必填（gz-gui 跑在车端容器内，归车端）

`withDefaults` 四处全删；`--world` 不给即报错。`assetsPath` / `gzGuiAssetBind` 两套解析并为 `internal/assets` 一处；缺文件是错误，不再返回空串。车端 HTTP 客户端带超时；CLI 给 `start` 配 context 超时。

## 9. 现状符号落点

| 现状 | 落点 |
| --- | --- |
| `contract.Session.Partition` / `Robot.TopicPrefix` / `GZIP` / `EnvROSDomainID` | 删（Gazebo Port 自己的常量与 Attachment） |
| `contract.DefaultPartition` / `WorldContainer` / `AppContainer` / `Label*` | Gazebo Port / Docker runtime |
| `contract.StartConfig`（含 Rviz/GzGui/DataRoot/AppImage） | 拆：框架 `StartConfig` + 各 Port `Options` |
| `WorldBundle.MapName` / `Stations`(mm) / `Scenarios` / `OriginOffsetMM` | bundle 新格式（§5.2）；旧格式直接删，未发版不留兼容 |
| `orchestrator.DockerWorld` / `waitReady` / `readReady` | `ports/world/gazebo` |
| `control.go`：`Pose` / `Reset` 的编排路由 | 留在 `orchestrator`（只路由）；`DockerWorld` 的实现进 `ports/world/gazebo` |
| `call.go`：`Call` 的编排路由 | 留在 `orchestrator`（只路由）；`Forward` / `dialAPI` 进 `ports/vehicle/easygo` |
| `orchestrator.EasyGoVehicle*` / `localization.go` / `stations.go` / 端口算术 / registry 补丁 / `worldMapName` / `mapAlreadyExists` / `mapPose` | `ports/vehicle/easygo` |
| `internal/world`（Process / GZ / sdf） | `ports/world/gazebo/process` |
| `orchestrator/docker.go` | `ports/runtime/docker` |
| `assetsPath` / `worldHandoverBind` / `gzGuiAssetBind` | `internal/assets`（世界 SDF 交接 bind 归 `easygo/gazebo.go`） |
| `worldInit`（阶段顺序、失败回滚） | 留在 `orchestrator`，改调 `Provision` |
| `cmd/simenv-world` | `cmd/simenv-world-gazebo`；镜像 tag `simenv-world-gazebo:local` |

## 10. 施工顺序

每步一个 commit，`go test ./...` + `go build ./cmd/simenv ./cmd/simenv-world-gazebo` 绿才进下一步。

| 步 | 内容 | 验证 |
| --- | --- | --- |
| 0 | 本合同；AGENTS.md 改写 | `git diff --check`；用户过目 |
| 1 | Session / Robot / Pose 补 json tag（线上格式显式化）；`WorldReady` 定义进 `orchestrator`，框架接口不再引用 `internal/world` 类型 | 单测：接口签名编译期对齐 |
| 2 | `docker.go` → `ports/runtime/docker` | 纯搬，测试跟随 |
| 3 | `DockerWorld` + ready 轮询 + `Pose`/`Reset` 实现 + `internal/world` → `ports/world/gazebo`；`Attachment`；`Partition`/`TopicPrefix` 出 Session；`cmd/simenv-world-gazebo` 改名、Dockerfile 同步 | `docker build` 过；fake 世界 Port 测状态机 |
| 4 | `EasyGoVehicle*`、`Forward`、`localization.go`、`stations.go`、端口算术、registry 补丁、地图解析、坐标换算 → `ports/vehicle/easygo`；`Provision` 合并三步；`worldInit` 通用化 | 原测试跟随；换算单测对账现有 mm 值 |
| 5 | `internal/assets` + bundle 新格式：目录重排、站点换算、按 kind 分节；资产一致性测试拆三份 | `TestShippedWorldBundles*` 全绿 |
| 6 | `StartConfig` 瘦身、Port `Options` + flag、删 `withDefaults`、`Orchestrator` 去 `Engine`/`AppReady`；CI 加 `rg` 断言 | `cli_test` 更新；CI 绿 |
| 7 | README、三个项目 skill 的路径/命令/镜像名对齐 | 命令与源码一致 |
| 8 | 真跑门禁：`simple`、`corridor` 各 N=1 | 环境具备时跑，否则写明未验证 |

第 3、4 步是大头；其余每步半天以内。场景 runner 收仓（决定 1）在本合同完成后单开。

## 11. 验证

- 文档：`git diff --check`；命令/端口/路径与源码一致。
- 代码：`go test ./...`；改入口或镜像时 `go build` 两个二进制 + `docker build -f deploy/Dockerfile -t simenv-world-gazebo:local .`。
- 框架守卫：`! rg -n "gazebo|gz|easygo|docker|ros" internal/contract internal/orchestrator internal/assets internal/cli`（大小写不敏感、排除注释豁免名单）进 CI lint。
- N=1 门禁：`simenv start` 退出 0；GZ `clock` 与实体 `robot1`；程序 API `:18189` 可达；`status` 分开报告；`stop` 停干净。
