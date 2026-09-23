> 🚧 部分完成 — Go 编排、契约、世界进程与环境镜像已落地；本机未用真实 Gazebo / runtime-sim 跑通第 6 节 N=1 挂车门禁。

# simenv：独立仿真环境

一份世界 × N 套车端。新仓只拥有环境和编排。程序继续用 EasyGo 的 `runtime-sim` 与 `sim_easygo.launch.py start_gazebo:=false`。两边不共享镜像、不共享 launch、不共享版本号。

## 1. 为什么另开仓

EasyGo 现有仿真已经具备正确拓扑的多车路径（`sim_multi_gazebo` + N 份车端），但单车把 Gazebo 塞进车端进程，轻量模式另起 launch，入口是 `full_sim.sh` / `easygo-sim` / 直接 launch 一堆脚本。改共享路径必须双线兼容，这是分叉，不是目标。

本仓不修补 `easygo_sim_bringup`。旧入口继续给现有用户用，本仓不包装它们。

## 2. 独立边界

| | 环境（本仓） | 程序（EasyGo） |
|---|---|---|
| 是什么 | Gazebo server、world SDF、机器人模型、spawn、GZ `clock` | `runtime-sim` 里的 easygo-app / SLAM / Nav2 / API |
| 版本 | 本仓环境镜像 tag | `--app-image` 传入的 `runtime-sim:<tag>` |
| 禁止 | 起 easygo-app、SLAM、Nav2、WebApp、驾驶台、RViz | 起 Gazebo server、spawn 世界模型、读本仓源码 |
| 见面 | 第 3 节契约 | `start_gazebo:=false`，只认契约 |

更新程序镜像不重建环境镜像。换环境实现（另一套物理、另一份 world 进程）只要契约仍在，程序镜像零改。

环境镜像**不以** `runtime-sim` 为 `FROM`。底包只装 Gazebo Harmonic 以及 spawn 所需的最小依赖。

## 3. 会话契约与 EasyGo 实现约定

与现有 `start_gazebo:=false` 对齐，不另发明一套 topic。数值来自 EasyGo `multi_robot_instance.py` / `multi_robot_model.py`（`port_offset=0`、`ros_domain_offset=0`）。

- `Session` 只下发 World、Partition 与 Robot 清单；Robot 只含 `Index`、`Name`、`TopicPrefix`、`Pose`。
- 实体名、GZ topic 前缀默认：`robotN`（N 从 1）。GZ topic 形如 `/robotN/odom`。
- `GZ_IP=127.0.0.1`，`GZ_PARTITION=easygo_sim`（可用配置覆盖，默认必须与现网多车一致）。
- Gazebo 发 GZ `clock`。程序容器在自己的 ROS domain 上把 GZ `clock` 桥成 `/clock`（现有 launch 已做）。环境容器不给车端喂 ROS `/clock`。
- EasyGoVehicle 实现内计算程序 `ROS_DOMAIN_ID = 11 + (N-1)`。环境若需要 ROS，固定 `ROS_DOMAIN_ID=1`，不进车端域。
- EasyGoVehicle 实现内计算程序 HTTP：`frontend=18088+100N`，`api=18089+100N`。因此 N=1 的 API 是 `127.0.0.1:18189`，不是旧单车的 `:18089`。
- 每车 `/sros` 挂到宿主独立目录，互不复用。
- 出生点默认：`(3×(N-1), 0, yaw=0)`，与现网 `DEFAULT_SPAWN_X_STRIDE` 一致。`--poses` 可覆盖。

N=1 就是 `robot1`，不是「无名实体 + 18089」的旧单车模式。

## 4. 程序形态

本仓是带类型的 Go 程序，不是脚本仓库。

- 入口：`cmd/simenv`，产出单一二进制 `simenv`。
- 配置：结构体（文件或 flag 反序列化进 struct）。非法字段直接失败，不设默认值迁就脏输入。
- 容器：Docker Engine API，不起 shell 拼 `docker run`。
- 世界进程：Go 调 `gz sim` 与 spawn API（实体名、位姿来自 config）。不写 `*.launch.py`，不拷 `sim_multi_gazebo.launch.py`。
- 禁止：`*.sh` 业务入口、Python 编排、把 TypeScript 当本仓实现语言。构建说明用 `go build`，不为入口再包一层 Makefile 业务目标。

子命令只这三个：

```text
simenv start  --robots 1 --world simple --app-image docker-registry.nowhere.sr/easygo/runtime-sim:<tag>
simenv status
simenv stop
```

`start` 先起环境容器，确认 GZ `clock` 与 `robot1..N` 实体存在，再按 N 起程序容器：

```text
ros2 launch easygo_sim_bringup sim_easygo.launch.py
  start_gazebo:=false
  use_gz_gui:=false
  use_rviz:=false
  robot_name:=robotN
```

以及 EasyGoVehicle 实现要求的端口、domain、`GZ_*`、`/sros` 挂载。host network，与现网车端一致，GZ localhost 才能通。

默认 headless。`--gui` 启用程序侧的 web-gz 3D 视图与 RViz web（`use_gz_gui:=true`、`use_rviz:=true`，端口按契约算术），观察进程在程序容器内跑，连环境容器起的 server。不启用驾驶台与语音。

## 5. 仓布局

路径：`~/workspace/simenv`。

```text
cmd/simenv/                 入口
internal/orchestrator/      Docker API：起停环境/程序容器
internal/world/             gz server、spawn、等待 clock/实体
internal/contract/          session、robotN、topic 前缀、分区、出生点，带 Go 测试
deploy/                     环境镜像 Dockerfile
docs/superpowers/specs/     本文件
```

worlds / 机器人 SDF 在**构建环境镜像时**从 pinned 的 EasyGo 修订拷入，镜像标签或标签旁的 digest 文件记录 `EASYGO_WORLD_SOURCE_SHA`。运行时不挂 EasyGo 工作区，不 `docker run -v ~/workspace/easygo:...`。

本仓测试只测契约算术、编排状态机、世界进程对 clock/实体的等待。不把 EasyGo 的 launch 测试当本仓门禁。

## 6. v1 验收

默认 `--robots 1`、`--world simple`、headless；`--gui` 可开观察视图。

1. `simenv start --robots 1 --world simple --app-image <runtime-sim tag>` 退出 0。
2. 环境容器在跑：Gazebo server、GZ `clock` 在走、实体 `robot1` 已 spawn。进程列表里没有 easygo-app / slam_mapping / nav2。
3. 程序容器在跑：上述 `start_gazebo:=false` 命令，`GET http://127.0.0.1:18189/api/v6/vehicle/status` 可达。容器内没有第二份 Gazebo server。
4. `simenv status` 分开报告环境与程序，并报告契约是否接通（clock、`robot1`）。
5. `simenv stop` 两边停干净，端口 `18189` 释放。
6. 只换 `--app-image` 的 tag 再 `start`，环境镜像 id 不变。只重建环境镜像，不必同时重建或改程序镜像。

`--robots 2` 与 N=1 走同一条编排代码。v1 门禁不要求本机跑过 N=2。本机内存不够时禁止把「没跑多车」写成环境缺陷。

## 7. 保留与放弃

保留（当依赖或契约，不拷源码进来改）：

- 1 世界 × N 车端
- `runtime-sim` 程序镜像与 `sim_easygo.launch.py start_gazebo:=false`
- 实体名 / GZ 前缀 / 端口步长 / domain 公式 / 默认出生点
- worlds 与机器人 SDF（构建时从 pinned SHA 打入）
- 测试用独立 `/sros`，不用人的 `~/sros` 当默认数据根
- 程序侧已有的 GZ→ROS clock 桥；定位不得回退 Gazebo 真值（这是程序纪律，本仓不改）

放弃：

- 在本仓复刻或包装 `full_sim.sh`、`easygo-sim`、`sim_multi_gazebo.launch.py`
- 单车进程内起 Gazebo
- 默认开 GUI / 相机 / 语音
- 环境镜像 `FROM runtime-sim`
- 运行时挂 EasyGo 源码或 overlay
- bash/Python 业务入口
- 把 `:18089` 单车口当 N=1 的默认 API
- 为旧入口做双路径或兼容层

## 8. 非目标（v1）

- 在线加减车、每车独立 overlay
- 重写车端 launch 或 easygo-app
- Playwright / 观察台 / 语音
- 真机
- 把本仓嵌进 TP 或 EasyGo CI

这些要另开变更，不在 v1 加开关预埋。
