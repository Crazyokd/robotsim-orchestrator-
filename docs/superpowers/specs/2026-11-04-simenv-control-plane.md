# simenv：运行中会话的控制面

> 🚧 进行中 — `pose` / `reset` 已按本合同落地；车端 `call` 尚未实现。

`start` / `status` / `stop` 只管生命周期。TP 要用 sim 替换 EasyGo Adapter，但不能知道容器名、端口算术或 EasyGo HTTP。车端操作从本仓**桥接出去**：对正在跑的那一份会话下令，stdout 给宿主解析。

会话契约不变。`Session` 仍只有 World / Partition / Robot 清单。控制面是编排边缘，不进 `internal/contract`。

## 1. 为什么

- TP `@tp/adapter-sim` 只调用 `simenv`。不 `docker exec`，不打 `127.0.0.1:18189`。
- EasyGo Adapter 继续 attach 已有容器、API `:18089`。两套 Adapter 是替换关系，启动路径不能共用。
- 车端仍是 `--app-image` 的 `runtime-sim`。解释 EasyGo HTTP / ROS 的代码留在本仓 `EasyGoVehicle` 边缘，不进框架核。

## 2. 命令

已有：`start` `status` `stop`。

新增（世界层，不解释车端）：

```text
simenv pose  --robot N
simenv reset --robot N --x X --y Y --yaw YAW [--z Z]
```

`--robot` ≥ 1，实体名 `robotN`。世界名来自正在跑的会话 `ready.json`，不另传 `--world`。`--z` 缺省用出生高度 `0.05`。非法输入直接失败。

stdout 一行：

```text
robot=1 entity=robot1 x=0.1 y=0.2 z=0.05 yaw=0.3
```

`pose` 读 Gazebo 真值。`reset` 写 Gazebo 真值再回读。都不走车端 API。

后续（车端层，仍不进 contract）：

```text
simenv call --robot N --op <name> [--in JSON]
```

`op` 是本仓边缘操作名。stdout 先 `ok=true|false`，再 `key=value` 或一段 JSON。未知 `op` 失败。第一波 HTTP/inttest 的 `op` 对照 TP 叶子，另开提交补。

## 3. 归属

| 动作 | 谁做 | 不进 |
| --- | --- | --- |
| 起停世界与程序 | Orchestrator | TP |
| GZ 真值读写 | 世界容器内 `gz`，宿主 `Engine.Exec` | 车端 API、TP |
| 灌图 / 注站 / 放车 | `start` 的 world delivery | 控制面重复做一遍 |
| 建图 / 站点 / 任务 / 定位 / 参数 | 后续 `call` → EasyGoVehicle | `internal/contract`、TP HTTP 客户端 |
| 驾驶闭环 | 更后一波 `call` | 本波 |

## 4. TP 对照（第一波）

| simenv | `@tp/adapter-sim` |
| --- | --- |
| `start` / `status` / `stop` | Session + `sim.ready` |
| `pose --robot` | `sim.pose` |
| `reset --robot --x --y --yaw` | `sim.reset` |

叶子口 `robot` 是 ≥1 整数。Session 口仍是 `world` / `appImage` / `robots`。
