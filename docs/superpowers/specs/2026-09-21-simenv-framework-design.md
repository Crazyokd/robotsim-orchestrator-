# simenv 框架分层

任意仿真 × 任意车体程序，同一套编排。图：[`docs/archify/simenv-framework.architecture.json`](../../archify/simenv-framework.architecture.json)。

**原则：不变的流程、接口、消息、结构进框架；变化的下沉到实现。** 核要抽象到「角色和附着」，不能抽象成空壳，也不能写成某一家车的运动学。

## 1. 框架核（不变）

一次会话里有两种角色：环境（World）、程序（Vehicle）。程序通过**附着**挂到环境里的一个身体上。编排核只做这件事，不认底盘、不认 topic、不认容器。

```text
Capability          start / status / stop
Orchestrator        只跑生命周期
Session             一次运行
Attachment          index · name          // 一条附着，不是一辆差速车
WorldPort           环境角色
VehiclePort         程序角色（一附着一份，端口绑定一台车）
VehiclePortFactory  创建 / 发现 VehiclePort
```

流程（不随引擎或车而变）：

```text
start:  World.Start(session) → World.Ready
        ∀ a: port(a).Start
status: World.Status + ∀ a port(a).Status
stop:   ∀ a port(a).Stop → World.Stop
```

就绪含义由框架定：环境在推进，每个 Attachment 的身体已在。怎么判定，端口实现回答。编排核只问端口。

接口（不随引擎或车而变）：

```go
type WorldPort interface {
    Start(ctx context.Context, sess *Session) error
    Ready(ctx context.Context) error
    Status(ctx context.Context) (WorldStatus, error)
    Stop(ctx context.Context) error
}

type VehiclePort interface {
    Start(ctx context.Context) error
    Status(ctx context.Context) (VehicleStatus, error)
    Stop(ctx context.Context) error
}

type VehiclePortFactory interface {
    New(sess Session, robot Robot) VehiclePort
    Discover(ctx context.Context) ([]VehiclePort, error)
}
```

`Session` 是编排核订立的附着清单，不是进程内总线。世界容器和程序容器各收同一份。角色在 Port 上。两边不得另起清单私下见面。

编排消息（框架）：`Start`、`Ready`、`Status`、`Stop`。这是核里仅有的消息。

出生点、HTTP 口、`ROS_DOMAIN_ID` 不是 `Attachment` 的字段。出生点是环境实现的配置；寻址是程序实现的配置。

## 2. Profile（可换，不是核）

核不规定身体长什么样。一对环境和程序要共用一份 **Profile**：它们见面时认的运行时合同。Profile 是框架的一等概念（可注册），不是编排核里的 `if Gazebo`。

v1 注册的是 `Mobility`：

| 消息 | 方向 | 载荷 |
|---|---|---|
| Clock | 环境 → 程序 | Sec, Nsec |
| Pose | 环境 → 程序 | Attachment, X, Y, Z, Yaw |
| Command | 程序 → 环境 | Attachment, VX, VY, Wz |

换仿真、换车：实现同一个 Profile，核不动。见面方式都变：新 Profile + 新实现，核仍不动。两边没有可共用的 Profile，`Start` 失败。

投影（`GzBridge` 等）属于实现：把原生传输兑成当前 Profile。激光、相机、关节挂在 Profile 扩展或另一份 Profile 上，不写进核。

## 3. 变化：下沉到实现

| 变化 | 落在哪 |
|---|---|
| 物理引擎、world 文件、spawn | `WorldPort` 实现 |
| 车体进程 / 镜像 / launch / HTTP / ROS domain | `VehiclePort` 实现 |
| 原生传输 ↔ Profile 消息 | 投影实现 |
| 身体形态、传感套件 | Profile + 实现 |
| Docker 还是本机进程 | 对应端口实现 |

v1 三元组：`GazeboWorld`、`EasyGoVehicle`、`GzBridge`，共用 `Mobility`。`GZ_PARTITION` 与 `/robotN/odom` 来自中立 Session；`:18189`、`ROS_DOMAIN_ID=11`、`/sros` 只出现在 EasyGoVehicle 实现里。

## 4. 现码越界

已下沉：契约里的 `Robot` 不再带 HTTP / `ROS_DOMAIN_ID` / runtime root；EasyGo launch/env/端口算术只在 EasyGoVehicle 实现里。仍需后续收敛：`Process.Run` 读 SDF，编排核直接握 Docker，出生点还在会话构建里。以本文件为准。

## 5. 不做

- 把 `Yaw` / `VX, VY, Wz` 写进编排核
- 把某一家 topic / 端口表写进框架类型
- 把 Session 做成进程内 channel 总线
- 实现类绕过 Session 私下见面
- 编排核起容器或进程、逐帧转发
- 把 `gz` / SDF / MuJoCo XML 写进编排核
- 为旧单车 `:18089` 做兼容
- 在框架里预埋「是否 Gazebo」开关
- 把本仓嵌进 TP 或 EasyGo CI
