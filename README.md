# simenv

独立仿真环境：一份 Gazebo 世界，N 套现有 `runtime-sim` 车端。设计见 [`docs/superpowers/specs/2026-09-21-simenv-design.md`](docs/superpowers/specs/2026-09-21-simenv-design.md)。

```text
go test ./...
go build -o simenv ./cmd/simenv
docker build -f deploy/Dockerfile -t simenv-world:local .
./simenv start --robots 1 --world simple --app-image docker-registry.nowhere.sr/easygo/runtime-sim:<tag>
./simenv status
./simenv stop
```

N=1 的 API 在 `http://127.0.0.1:18189/api/v6`。环境镜像不含 easygo-app。worlds / 机器人模型来自 EasyGo `05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1`，记录在 `deploy/assets/EASYGO_WORLD_SOURCE_SHA`。
