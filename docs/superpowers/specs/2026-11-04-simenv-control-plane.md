# simenv：运行中会话的控制面

> `pose` / `reset` / 通用 `call` 已按本合同落地。车端动词不进本仓。

`start` / `status` / `stop` 只管生命周期。对正在跑的会话下令走控制面，stdout 给宿主解析。会话契约不变：`Session` 只有 World / Partition / Robot。控制面不进 `internal/contract`。

## 1. 为什么

叶子目录属于 TP `@tp/adapter-sim`。本仓不知道 `mapping.start`，也不拆请求。Adapter 把叶子译成一段字节，写进 `call` 的标准输入。编排器按 robot 找到 `VehiclePort`，把这段字节写到那台车的连接上，再把读到的字节写回标准输出。

- TP 只调用 `simenv`。不 `docker exec`，不打车端端口。
- 位姿属于世界：`WorldPort`（现 `DockerWorld`）读写实体位姿。CLI 不出现 gz。

## 2. 命令

已有：`start` `status` `stop`。

世界：

```text
simenv pose  --robot N
simenv reset --robot N --x X --y Y --yaw YAW [--z Z]
```

`--robot` ≥ 1。编排器把「读/写第 N 个实体的位姿」交给 `WorldPort`。stdout 一行：

```text
robot=1 entity=robot1 x=0.1 y=0.2 z=0.05 yaw=0.3
```

车端字节（不解析、不分旗标）：

```text
simenv call --robot N < request
```

标准输入是一段完整字节，原样写到第 N 台程序的 API 连接。标准输出是从那条连接读到的字节。本仓不看 method、path、JSON，不改字段，不解 `error_code`。调用方要自己把请求写完整，并让对端在响应后关闭连接。

传不出去，或车上不在会话里，才非零退出。对端返回的内容，包括失败响应，都算转发完成。

## 3. 归属

| 动作 | 谁做 | 不进 |
| --- | --- | --- |
| 起停世界与程序 | Orchestrator | TP |
| 读/写实体位姿 | `WorldPort`（现 `DockerWorld`） | 车端 API、TP |
| 灌图 / 注站 / 放车 | `start` 的 world delivery | 控制面重复做一遍 |
| 转发一段字节到第 N 台车 | `VehiclePort.Forward` | 解析请求、车端动词、信封、`internal/contract` |
| 叶子目录与 HTTP 对照 | `@tp/adapter-sim` | simenv |

## 4. TP 对照

| simenv | `@tp/adapter-sim` |
| --- | --- |
| `start` / `status` / `stop` | Session + `sim.ready` |
| `pose --robot` | `sim.pose` |
| `reset --robot --x --y --yaw` | `sim.reset` |
| `call --robot`，标准输入原样转发 | 叶子自己拼请求字节，自己解释响应 |

叶子口 `robot` ≥ 1。Session 口仍是 `world` / `appImage` / `robots`。
