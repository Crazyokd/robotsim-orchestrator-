---
name: simenv-run
description: Start, status, or stop the independent simenv world plus N runtime-sim app containers. Use when bringing up the sim environment, checking GZ clock / robotN / API, or tearing it down.
---

# 跑独立仿真环境

本仓只起环境和编排。程序镜像由 `--app-image` 传入。不要在本仓起 easygo-app，不要包装 EasyGo 旧入口。

## 何时用

用户要 `start` / `status` / `stop` 仿真环境，或看 clock、`robotN`、API 是否接通。

## 闭环

**1. 构建。** 需要环境镜像或本机没有 `./simenv` 时：

```bash
go test ./...
go build -o simenv ./cmd/simenv
docker build -f deploy/Dockerfile -t simenv-world:local .
```

**2. 启动。** `--app-image` 必填。N=1 默认 `--world simple`：

```bash
./simenv start --robots 1 --world simple --app-image docker-registry.nowhere.sr/easygo/runtime-sim:<tag>
```

加 `--gui` 开观察视图：web-gz 3D 在 `http://127.0.0.1:6180/vnc.html`，RViz web 在 `http://127.0.0.1:6181/vnc.html`（端口按契约算术 `base+100N`）。默认 headless。

N=1 的 API 是 `http://127.0.0.1:18189/api/v6`，不是 `:18089`。

**3. 验收。**

- `./simenv status`：world running、clock、robots 含 `robot1`、app running、`connected=true`
- 环境容器进程没有 easygo-app / slam_mapping / nav2
- 程序在跑时 `GET http://127.0.0.1:18189/api/v6/vehicle/status` 可达
- 带 `--gui` 时 `:6180`、`:6181` 的 noVNC 页面返回 200
- N≥2 实时性：`gz topic -e -t /clock` 采样，仿真时间增量应等于墙钟增量（实时率 1.0）。宿主无 `/dev/dri` 时退软件渲染，N=2 只有 ~0.48
- 本机没 Gazebo / `runtime-sim` 时写明未验证。没跑过多车不是环境缺陷

**4. 停止。** `./simenv stop`。两边停干净，`18189` 释放。

## 禁止

- 用 `docker run` 拼世界或车端，绕过 `simenv`
- 环境镜像 `FROM runtime-sim`、运行时挂 EasyGo 源码
- 跑 `full_sim.sh` / `easygo-sim` / `sim_multi_gazebo.launch.py`
- 把 `:18089` 当 N=1 默认 API
