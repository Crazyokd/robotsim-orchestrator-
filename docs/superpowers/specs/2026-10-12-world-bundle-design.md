> 🚧 部分完成 — manifest 类型与校验、③ 灌地图、④ 放车定位、⑤ 站点/坐标、⑥ 的 init 门槛与定位收敛断言都已落地（`worldInit` 阶段 + `SeedStations` + `waitForAppAPI` 双重就绪 + `mapPose`/`awaitPose`）；**② 车端接受显式世界路径**（跨仓）未落地，先用世界文件只读挂载的过渡态顶上——实测第二个世界 `corridor` 已能以 `simenv start --world corridor` 一步起来、车端不再拒绝世界名。

# simenv：世界 bundle 与 world init

一份世界 = 一个 **bundle**（世界 SDF + 地图集 + 站点/坐标/初始位姿）。`simenv start` 的 **world init + world delivery**（见 §3 的归属分层）把这个 bundle 落成确定性的运行态；**场景组与 bundle 绑定**并校验一致；**给定初始坐标必然定位成功**。

与车端 init 对称：车端起完 init 才有可用状态；世界也要起完 init 才有"可放车、可跑任务"的状态。**世界由环境提供**（第 2 节边界不变），车端只消费。

## 1. 目标与非目标

目标：

- **任意切换世界**：`simenv start --world <name>` 是唯一的切换点（显式一步），切换不依赖改代码，也不为每个测试世界出车端镜像。
- **场景组与地图绑定**：一个场景组声明它依赖哪个 bundle；世界/地图/站点/坐标任一不匹配就**失败**，不静默跑空。
- **给定初始坐标必然定位成功**：给定世界坐标 → 车端上报同一坐标且收敛，可断言、可重复，不靠重试掩盖。

非目标：

- 一次 session 只跑一个世界（不做多世界并行）。
- 不拥有车端：不改 easygo-app / SLAM / Nav2 的实现，只要求车端**接受环境给的世界路径**。

## 2. 边界（沿用既有合同）

| | 环境（本仓） | 程序（EasyGo） |
|---|---|---|
| 拥有 | 世界 SDF、地图集、站点/坐标、spawn、GZ `clock` | app / SLAM / Nav2 / API |
| 世界来源 | 本仓 `deploy/assets/worlds/`（pin 在 `EASYGO_WORLD_SOURCE_SHA` 或本仓自造） | 消费环境给的世界，不要求自带 |

## 3. 阶段与归属：world init / vehicle init / world delivery

三件事被一个名字盖住了，先把账分清楚（**判断归属比命名重要**）：

| 阶段 | 谁做 | 输入 | 失败了算谁 | 重跑粒度 |
|---|---|---|---|---|
| **world init** | 世界进程（环境容器） | `<world>.sdf` + bundle 的 `initial_poses[]`（世界位姿） | 世界/镜像 | 世界级：换世界必须重做 |
| **vehicle init** | 车端程序自己（镜像内 app/nav2…） | 车端自己的配置 | 车端（我们只等，不驱动） | 每车容器启动一次 |
| **world delivery**（"按世界给车端装载"） | 编排（本仓），每台车一遍 | bundle 的车端那半：`map_export` / `stations[]` / 地图位姿 | 接线/Bundle 数据（装载不进、名字冲突、坐标不一致） | 每车级：**加一台车只需给那一台重跑**，不必重起世界 |

所以 `worldInit` 这个名字容易和**车端 init** 混：它做的其实是 **world delivery**（灌地图 → 注站点 → 下发初始位姿并断言收敛），前面还夹着编排侧的 **bundle 校验**（读 manifest、世界名一致、地图主名对齐）。函数名保持不动，但读文档时按上表分账。

下方 ①–⑥ 是框架早期的阶段账，仍按下表口径读：**① 是世界 init；② 是车端的本事（跨仓）；③④⑤ 是 world delivery；⑥ 是判据，不是阶段。**

| 阶段 | 内容 | 现状 | 依据 |
|---|---|---|---|
| ① 世界就绪 | `gz sim` 加载 `<world>.sdf`；等 clock；spawn 各车实体并**回查存在** | 已落地 | `internal/world/process.go`（含实体回查与临时 SDF 生命周期） |
| ② 世界交给车端 | 车端能认这个世界 | **缺** | 车端按自己镜像的 `share/easygo_sim_bringup/worlds/<name>.sdf` 做存在性校验，见第 5 节 |
| ③ 地图灌入 | `<world>.map_export` → 每台车 | 已落地 | `Orchestrator.seedWorldMap` |
| ④ 放车定位 | 每台车 initial-pose = 世界位姿 | 已落地 | `EasyGoVehicle.Localize` / `initialPoseRequest` |
| ⑤ 世界语汇 | 站点坐标、世界↔地图坐标偏移、初始位姿 | **缺**（现散在 harness 常量里） | `~/.cache/simenv-scenarios/lib.sh` 硬编码 `OFF_X/OFF_Y`、站点表、`MAP_NAME` |
| ⑥ 就绪判据 | clock + 实体 + 车端 init + 定位收敛 + 站点存在 | **缺**（分散在各处） | 站点形状错曾让导航目标恒为 `(0,0)`，属"没人校验"的典型 |

## 4. bundle 定义

```
deploy/assets/worlds/<world>.sdf          世界（环境镜像里）
deploy/assets/worlds/<world>.map_export   地图集（13 个文件：nav 图 + 定位图 + json + 位姿图 + traj）
deploy/assets/worlds/<world>.world.json   manifest（本合同的中心）
```

manifest（带类型，非法字段直接失败，不设默认值、不做兼容映射）：

| 字段 | 含义 |
|---|---|
| `world` | 世界名，必须与文件名一致 |
| `map_name` | 地图主名，必须与 `map_export` 里解析出的主名一致（`worldMapName` 同一口径） |
| `origin_offset_mm` | 世界坐标 ↔ 车端地图坐标的偏移（放车/站点/断言都用它换算） |
| `stations[]` | `{id, x, y, yaw}`（车端地图坐标系，mm）；**由 init 注入车端**，不再是 harness 现场补 |
| `initial_poses[]`（可选） | 每台车的世界位姿；缺省沿用 session 的 spawn 策略 |
| `scenarios[]` | 该世界绑定的场景组（脚本集合），一键脚本按它跑 |
| `source_sha`（可选） | 世界来源 pin；本仓自造时写明并说明理由 |

`map_export` 的存在性 + `map_name` 一致性 + `stations` 可读性 = **场景组与地图绑定的具体内容**。

两条成对规则（world init 里强制）：

- **地图与 manifest 必须成对出现**：只有 `<world>.map_export` 没有 manifest，或只有 manifest 没有地图，都直接失败——缺一个说明 bundle 已经不完整。
- **裸世界允许存在**：既没有地图也没有 manifest 的世界只有"世界就绪"这一段（看场景/摆视角用），不灌地图、不注站点、不放车。

## 5. 车端配合（跨仓，唯一需要 easygo 的部分）

车端现在按**自己镜像**的 worlds 目录校验世界名（`easygo_sim_bringup/launch/sim_nav2.launch.py:41-49` 的 `_world_path()`：名字里带 `/` 直接拒，只在 `<pkg_share>/worlds/<name>.sdf` 里找），而真正被加载的世界 SDF 来自**环境**。同一个世界集合有两套答案，新世界必然被拒。

改动清单（位置已核对到行）：

| # | 位置 | 现状 | 应改成 |
|---|---|---|---|
| 1 | `easygo_sim_bringup/launch/sim_nav2.launch.py:41-49`（`_world_path`） | 带 `/` 直接拒；只查 `<pkg_share>/worlds/` | **先按"存在的文件路径"接受**，再回落到 `pkg_share/worlds` —— 与同仓 `sim_multi_gazebo.launch.py:57-65`、`sim_gazebo_scene.launch.py:88-96` 既有做法一致（这两个本来就接受存在的文件路径） |
| 2 | 本仓 `internal/orchestrator` | 只传世界名（`EASYGO_SIM_WORLD=<world>.sdf`） | 传**环境已经在用的那份 SDF 的绝对路径**（`/opt/simenv/worlds/<world>.sdf`）：同一份文件既喂 gz server 又喂车端校验，世界只有一份 |
| 3（建议） | `easygo_sim_bringup/launch/sim_easygo.launch.py:248-267`（`_resolve_world_request`） | 世界名找不到时**静默回落到 `simple.sdf`**（只打一行 log） | 直接失败：静默回落正是"世界/地图错配却照跑"的来源 |

关键事实：`sim_easygo.launch.py:248-254` **已经支持** `EASYGO_SIM_WORLD` 传绝对路径（`as_path.is_file()` 优先）——所以第 1 条改完这条链路就通，真正的关卡只有 `_world_path` 一个函数。

**过渡态**（镜像未更新前，已实现）：为**当前世界**把环境那份 SDF **只读挂进**车端容器的 worlds 目录
（`worldHandoverBind`，目标 `/opt/sros/share/easygo_sim_bringup/worlds/<world>.sdf`）。这样"同一份文件
既喂 gz server 又喂车端校验"，不会出现第二份内容；挂载源用绝对路径（默认资产目录是相对的，Docker 会拒）。
约束：`第 1 条落地即删`（常量 `appWorldsDir` 与 `worldHandoverBind` 一起删）。

## 5.5 corridor 定位缺口：根因是两个 bug，已修（2026-10-12 实测）

第二个世界本来就能"一步切换"，但起栈时 robot2 偏 2.2 m。根因**不在 bundle、不在建图、也不在
"地图坐标系旋转"**——是**自己的世界进程把出生位姿的 yaw 丢了**，加上**朝向比较没有按角度折**，
两处都在本仓，已修并回归。

- **bug 1｜出生位姿的 yaw 被丢掉**（`internal/world/gz.go`）：create 请求把朝向写死 `{z: 0, w: 1}`，
  而 `create` 的 pose **整体覆盖** SDF 里的 model pose（位置与朝向都是），于是 `RewriteRobotSDF`
  写进 SDF 的 yaw 只是"看着生效"。arena 两个初始位姿 yaw 都是 0，所以这个 bug 只在新世界暴露。
  修法：朝向在 create 请求里按四元数给全，SDF 的 model pose 固定单位位姿（出生位姿只有一个来源）。
- **bug 2｜朝向比较没折角**（`internal/orchestrator/localization.go`）：车端把 π 上报成 -3141 mrad，
  与下发的 +π 直接相减得 6.28 rad，于是"位置只差 2 mm"也被判没收敛。修法：按角度折（`angleDelta`）。

### 证伪"地图坐标系旋转"（先前的假设）

工具：把世界 SDF 的墙体按位姿采样成点，投到 `map_export` 的 pgm 上算命中率（世界→地图只给平移）。

- **对照实验（arena）**：arena 图在已知偏移 (1978,-3181.5) mm 下，30 512 个墙采样点命中 99.9%——工具与
  坐标系约定都对。
- **corridor 图确实来自当前（带 0.3 m 折角）世界**：4 个判别点的读图结果全部与当前 SDF 一致——
  `(3.0,1.00)/(5.0,1.00)` 是墙（当前 SDF 的 0.9~1.1 墙）→ 图上为 0；`(3.0,0.70)/(5.0,0.70)` 是自由
  （当前 SDF 的墙在 0.9~1.1）→ 图上为 5。旧版（凹龛版）世界在这 4 点上是反的。
- **图上的走廊两端都在世界真值处**：自由带北缘 ~1.9（= 口袋北墙内表面）、东端在 x≈11.2 被横向墙截断
  （世界 (11.3,0.3) 的墙）——没有旋转，也没有整体位移；`OFF=(308,-158)` mm 继续成立。

### 真正的机制（车端自己的 locator 日志，同一次起栈）

日志：容器内 `/sros/log/slam/locator_node.INFO*`。两车收到同一条命令、同一张图：

```
[Service] /localization/command called: command=1 params='{"initial_pose":{"x":7.192,"y":0.458,"yaw":3.1415927},"loctype":2,...}'
The initial pose is:7.192,0.458,3.14159
input_scan_pose :6.892,0.608,2.35619          <- 给定位姿换算到雷达安装位（base→scan = +0.3,-0.15,-45°）
bb match pose::9.052,0.608,2.37219            <- 粗搜结果：沿走廊轴 +2.16 m
bb tsdf match pose:9.0934,0.610548,2.35884
score:0.457535    search range:20,20,40       <- ±20 m、40 个朝向
init loc in corridor area
Initial localization successful (state=4)     <- state 4 = SIMILAR_ENV_POSE（算法自己判"相似环境"）
The initial match pose is:9.3938,0.461342,-3.13895   -> 上报 9.39 m（真值 7.19 m）
```

对照（同一次起栈的 robot1，西端待避区）：

```
input_scan_pose :-7.508,0.008,-0.7854
bb match pose::-7.388,0.008,-0.7814
bb tsdf match pose:-7.50253,-0.00121245,-0.784883
score:0.397614    search range:20,20,40
The initial match pose is:-7.80261,0.148632,0.000516772  -> 上报 = 给定位姿（差 6/9 mm）
```

修前/修后对照（车端 locator 日志，同一次起栈、同一张图、同一条命令）：

| | 车端输入（雷达位） | 匹配结果 | score | 车端上报 |
|---|---|---|---|---|
| 修前 robot2 | 6.892,0.608 | **9.0934**（沿走廊轴 +2.2 m） | 0.457535 | 9.39 m ❌ |
| 修后 robot2 | 6.892,0.608 | 6.89792 | **0.805987** | 7.198 m ✓ |

车头实际朝向与"下发的位姿"差 180° 时，扫描匹配只能拿一个低分的、沿走廊轴挪开的解来解释这份扫描。

Gazebo 真值（`gz model -m robot2 -p`，bundle 里 robot2 声明 yaw=π）：

```
修前: [7.500000 0.300000 ...] [0.000000 0.000000 0.000000]     <- 声明的 yaw 丢了
修后: [7.500000 0.300000 ...] [0.000000 0.000000 -3.141590]    <- 声明的位姿就是物理位姿
```

### 修完的实测回归

- `simenv start --robots 2 --world corridor` **退出 0**：robot1 上报 (-7809.6,147.1)（Δ≈11 mm，conf 40）、
  robot2 (7193.7,458.7)（Δ≈2 mm，conf 80）；
- arena 回归：`--world simple` 退出 0，两车 (-1969.4,3166.5) / (1033.1,3169.1)，与修前同量级（8~15 mm）；
- **附带记录**（不是缺陷，别再当根因）：车端初次定位的接受阈值在 sim 里被放宽
  （`sim_easygo.launch.py:1287-1288`：`init_score_threshold` 0.4 vs 产品默认 0.5、`ini_distri_size` 20.0 vs 2.0），
  所以"低分但错"的解会被采纳，不会回落到给定位姿。修完后 robot2 得 0.806（两头都过）；
  西端待避区只 0.397（两头都不过 → 回落到给定位姿，而它匹配到的位姿本来也只差 1.7 mm）。
  即：这张图在待避区的匹配分本来就不高，但**只要物理位姿与声明位姿一致，匹配结果与回退结果都对**。
  阈值与搜索范围是产品调参，不是本仓判据。
- `loctype=3` 不解决定位问题（该分支不下发 `initial_pose`，用"当前位姿"，对刚生成的车就是地图原点）；
- 先前"地图坐标系旋转 / 要把旋转写进 bundle"的假设仍然不成立（见上面"证伪"一节），
  `origin_offset_mm` 保持纯平移。

## 5.6 corridor 地形缺陷：1.2 m 车道可以被绕过（2026-10-12 实测，已修）

定位通了之后，corridor 的互等场景**跑不起来**——不是车端不做事，而是这个世界的几何**构造不出冲突**：
车道的两道墙是**立在开阔场地里的两条独立墙**，墙外还是空地，车直接从墙外绕过去了。

实测（corridor 场景 s1_corridor_mutual_wait.sh，Gazebo 真值每 5 s 采一次）：

```
t= 5s  真值 r1=(-5.75, -1.67)  r2=( 2.66,  0.26)   app 间距 4.2 m
t=10s  真值 r1=( 1.44, -1.80)  r2=(-4.55, -0.03)   app 间距 10.9 m
t=15s  真值 r1=( 8.23,  0.15)  r2=(-8.50, -0.05)   两车都已换边，相位 finished/running
```

- **robot1 全程在 y≈-1.7 ~ -1.8 行驶**，而车道的南墙只覆盖 `y∈[-0.8,-0.6]`——它是从**南墙外侧**绕过去的；
- 两侧 `blocking_peer_ids`、`conflict detected`、pause、参与方动作、escalate **全部 0 次**：
  多车解锁逻辑根本没被触发（它只在同一条不可通过的路段上相遇时才介入）；
- 两车 15~20 s 各自到站。场景里 S1-4~S1-7 因此 **FAIL**——断言没有静默通过，这正是要的行为；
  S1-8 也已收紧为"一次冲突都没观察到 → 构造无效，不计通过"。

**已修**（世界重做，见 `deploy/assets/worlds/corridor.sdf` 的 `corridor_arena`）：车道墙跨满整个 x 范围，
两端各用一个"封口墙 + 外扩的待避区"收口，场地南北两侧因此被封死，1.2 m 车道成为唯一通路。
**场地外墙保持原样**，`origin_offset_mm` 不受影响（新图重开后再量一次偏移）。

布局：

```
西端待避区              车道（净宽 1.2 m）                     东端待避区
x[-15.85,-11.9]        x[-12.1, 11.9]                        x[12.1,15.85]
y[-0.6,  2.9]          y[-0.6,0.6] 西半段                    y[-2.9, 0.9]
                       y[-0.3,0.9] 东半段
                       x∈[-1,0] 0.3 m 缓变错位（不是台阶：净宽恒 1.2 m）
                       x∈[4.6,5.8] 北墙 0.6 m 深凹口
```

连通性用"按几何栅格化 + 按车半宽膨胀 + BFS"校核（工具：`connectivity.py`，栅格 0.05 m）：

| 世界 | 西端→东端 | 车道外（北/南）可达 | 可达面积 | 最窄通道（留 0.82 m 车宽后） |
|---|---|---|---|---|
| 旧（墙立在开阔场地） | 通 | **可达**（绕行） | 257 756 格 | 2.40 m（+1.58 m） |
| 新（车道为唯一通路） | 通 | **不可达** | 10 155 格 | 1.08 m（+0.26 m，保守估计） |

改完必须重开一张图（地图随世界变），再同步 bundle 的站点/初始位姿——见 §9 落地顺序。

## 5.7 两真车解锁：能触发——之前的"app 侧红"是我这边的假红（2026-10-12 更正）

**更正**：本节先前写"对端观测运行时没起来 / 解锁逻辑从未被触发"，是**假红**。原因是**日志级别**：
镜像里 `easygo_app.env` 默认 `RUST_LOG=warn`，而解锁链路的判定行几乎全是 INFO/DEBUG；
arena 的场景会先跑 `stage_candidate_fix`（内含 `stage_app_log_level`，把 `RUST_LOG` 放开成 info），
我新写的 corridor 场景**漏了这一步**，断言依赖的日志全被吞掉，看起来就像"什么都没发生"。

证据：在 corridor 栈里把 `RUST_LOG` 改成 info 并重启 app 后，
`multi-AMR observation runtime started robot_id=robot1 boot_id=...` 立刻出现；
把 `stage_candidate_fix` 加进 corridor 场景、再跑"对头顶住"构造：

| 断言 | 结果 |
|---|---|
| S1-1~S1-3 定位 / 构造有效 / 任务起来 | **PASS**（初始间距 28.07 m，r1 在西、r2 在东）|
| S1-4 两侧各自判到对方阻塞 | **PASS**（r1→robot2 **376** 次、r2→robot1 **376** 次）|
| S1-5 任务确实被暂停 | **PASS**（相位 `paused` 采样 r1 **169** 次、r2 **155** 次；判据用相位而不是日志文案）|
| S1-6 至少一侧执行参与方动作 | **PASS**（r1 **601** 次、r2 **375** 次）|
| S1-7 首选方案推不动时升级 | **PASS**（r1 escalate **151** 次）|
| S1-8 错身/冲突消解 | **FAIL** —— 180 s 窗口内没谈完：两车对头贴到 **436 mm** 就一直停在那里（各推进 ~11.8/16.2 m），最后 8 s 仍有 17/18 次冲突；`换位=0` |

即：**解锁链路在 corridor 世界里是完整活的**（判到阻塞 → 暂停任务 → 执行参与方动作 → 推不动升级），
**卡在"最终消解"这一步**：两台真车在 1.2 m 车道里对头顶到 436 mm 之后，恢复方案一直没把谁挪开。

**"消解停滞"的机制已定位（车端 app，跨仓）**：协调车在两种方案之间**来回翻**，谁都没跑完：

```
218  multi-AMR recovery plan selection escalate=false previous_local_action_kind=None                          chosen="CONTINUE_TASK"
221  multi-AMR recovery plan selection escalate=true  previous_local_action_kind=Some("DRIVE_ON_HEADING_BACKWARD") chosen="CONTINUE_TASK"
217  multi-AMR recovery plan selection escalate=true  previous_local_action_kind=Some("CONTINUE_TASK")            chosen="DRIVE_ON_HEADING_BACKWARD"
```

（`docker logs --since 900s simenv-robot1` 的计数，同一分钟内 `generating recovery action options` 也有 ~97 次/分钟。）
即：选了 `CONTINUE_TASK` → 下一轮因为"上一个动作是 CONTINUE_TASK"就升级成 `DRIVE_ON_HEADING_BACKWARD`
→ 再下一轮又因为"上一个是 DRIVE_ON_HEADING_BACKWARD"升级回 `CONTINUE_TASK` —— **两个方案互相把对方顶掉，
哪个都执行不到底**，于是车一直贴在 436 mm 上。这也解释了 arena 的旧结果：它的 S3 之所以"绿"，
是因为断言只要求"有参与方动作 + 有位移"（2.5 m 是真的，翻来翻去也够）；而 S1 要求"最终消解"，就红。

**跨仓待办（精确到可复现）**：协调车的方案选择在 `CONTINUE_TASK` 与 `DRIVE_ON_HEADING_BACKWARD`
之间振荡时，需要有"上一轮已升级过就不再用对方的 previous 作升级依据"之类的收敛条件（或给动作一个
最小执行时长），否则死锁永远不消解。本仓侧无需改动。

app 侧事实（读源码得到，供以后定位，不再是待办结论）：
`observation runtime started` 的唯一门控是 `ENABLE_MULTI_AMR_AVOIDANCE`（本仓起栈时传 true，已实测生效）；
`Multi-AMR navigation boundary ready` 由 nav2 无条件创建，**不能当作"多车开着"的证据**。
运行期另有三条独立线索：peer 载荷按 **map 名**过滤、事件需构成"可达图上 ≥2 台车的环"、
boundary 构造 envelope 需要**车体 footprint**（由工作区那处未提交的 `orchestrator.go` 改动补上）。

**判定性检查（已做）**：corridor 栈里 `ENABLE_MULTI_AMR_AVOIDANCE=true`、
`EASYGO_BLUETOOTH_TRANSPORT=udp`、`EASYGO_BLUETOOTH_UDP_PORT=16889` —— 与 arena 栈**完全一致**；
把 `RUST_LOG` 放开成 info 后观察行立刻出现。所以"没有观察行"只说明日志被 `warn` 级别吞了，
不说明模块没起来。

**顺带一条 harness 结论**：车道中段的位姿**不能用传送方式摆位**——`place_robot robot1` 在
世界 (-5.0, 0.0) 连试 3 次都判"定位未收敛"（直线车道里 x 弱可观测 + odom 被传送打乱），
而两端待避区一次就收敛。摆位要落在有特征处。

## 5.8 跑跨世界验收时暴露的两件事（2026-10-12，一件已修、一件待查）

**(a) 已修：摆位的传送确认方式**。`gz_set_pose` 原来 `timeout 10` + 输出丢 `/dev/null` + `sleep 1`
就读真值。实测机制是**延迟竞争**而不是失败：世界有负载时 gz 服务要十几秒，`gz service` CLI 先被
timeout 杀掉，**而请求仍会被处理**——于是脚本读到的是"还没搬走"的旧位姿，再拿这个旧位姿去算
目标地图位姿，整场摆位就都错了（arena S1 实测：真值一直显示车停在出生点，而脚本已经把
"出生点对应的地图位姿"下发了）。改成**发完请求轮询真值**（≤50 mm、最多 60 s）才算成功，并在
`place_robot` 里传送未生效就直接失败返回——不再静默往下跑。

**(b) 已结案：不是"车端不定位"，是车端还没挂上地图时的就绪竞争**。把传送修好后再摆位，仍然
连续 3 次 `localized=False`；但同一时刻车端状态里有个干净的指纹：**`map_id` 为空**
（正常时哪怕还没定位，`map_id` 也是 `arena_world`）。在这个干净栈上做对照：`docker restart`
两车 → 车端 `state=0`、位姿 (0,0)、但 `map_id` 仍在 → 下发一次初始位姿就 **conf 99**、
且 48 s 稳定；locator 日志完整（`Switching locator map` → `The initial pose is:` → `score:0.99`
→ `The initial match pose`）。而失败那次的日志只有 `command called`，后面几行全无 —— 处理没往下走。

所以：重启 → 下发 这条路本身是好的，失败点是**下发时车端还没挂上地图**（长跑栈里 staging 的
两次重启之后）。harness 已加就绪门：`place_robot` 先等车端报出非空 `map_id`（≤300 s，冷启动后
app 要 2~4 分钟），等不到就明确失败返回，不再报成"定位未收敛"。加完门之后实测：
`place_robot robot1 12200 12900 3.1415927` → **rc=0**，真值 (14.178, 9.719, yaw π)，
车端上报 (12198.4, 12900.0)（Δ≈1.6 mm，conf 99）。

**(c) 起栈的 app 就绪窗口从 3 分钟放宽到 5 分钟**（`internal/orchestrator` 的 `appReadyTimeout`）。
跨世界验收里 corridor 起栈连着两次失败，`simenv start` 的报错是
`wait for robot1 app: app API on port 18189 silent for 3m0s`——注意它在 **stderr** 上，
所以 `tee` 出来的 stdout 日志是空的，一开始完全看不见。同一条命令手动跑就是 **EXIT=0**、
两个 app 容器正常起来并跑起 flow；差别只是冷启动耗时：清掉车端缓存后 app 首次起服务
（+ nav2 + 参数库重播种）实测 1~3 分钟，机器忙一点就顶穿 3 分钟窗口 → 整次 start 回滚。
这属于"起得慢被误判成起不来"，所以放宽窗口（起不来依然会失败，判据没动）。
顺带排除：不是端口被上一个容器占着（加了"等容器消失 + 端口关闭"之后仍然失败），
那处等待作为卫生措施保留。

**(d) 真因：资产目录默认值相对 cwd（已修）**。把窗口放宽到 5 分钟之后 corridor 起栈仍然失败
（`app API on port 18189 silent for 5m0s`），而同一条命令在**仓库根目录**下手动跑就 EXIT=0。
差别是调用目录：`internal/orchestrator` 的 `defaultAssetsDir = "deploy/assets"` 是**相对路径**，
于是从别的目录调用时 `deploy/assets/worlds/corridor.sdf` 找不到 → `worldHandoverBind`
**静默返回空** → 车端容器拿不到环境这份世界文件 → 车端 launch 按自己镜像的 `worlds/` 校验世界名时
只有镜像里已有的世界能用：arena（镜像里有 `simple.sdf`）照常，corridor 直接起不来。
改成默认值**相对可执行文件所在目录**解析（资产目录存在才采用，否则回落原相对路径）。

修完实测（复现原失败条件：`cd /tmp` 再起栈）：
`simenv start --robots 2 --world corridor` → **EXIT=0**，两车 `init succeeded`，
上报 (-13703.6, 751.9) / (14279.3, -1459.9)，vs 声明值 (-13712, 746) / (14288, -1454)
→ **Δ≈8.4/5.9 mm**。这同时把"世界文件找不到就悄悄不挂载"这个静默失败也消掉了。

**下一步**：把 s1 的 WATCH 放宽到 240 s、按实际文案修 S1-5 的 grep，再跑一轮；然后重跑跨世界一键验收
（corridor 段上一轮被 `START_TIMEOUT=400` 掐掉，已放宽到 900；证据行里嵌引号的 bug 也已修）。

## 5.9 多车解锁场景矩阵：真车维度的定案与跨层归属（2026-10-13 实测）

把"仿真能构造的多车解锁场景"按维度补齐后逐条跑，结论分三类：**合法且过 / 不可合法构造 / 仿真能力边界**。

**(a) 合法且过的真车维度**（新增三个真车场景，全绿）：

- **定向让行**（`s3_corridor_yield_dir.sh`，5/0）——判据来自源码：方向由 Nav2 定
  （`nav2_bt_navigator/.../multi_amr_navigation_boundary.cpp`：阻挡 peer 在前→`DRIVE_ON_HEADING_BACKWARD`、
  在后→`DRIVE_ON_HEADING_FORWARD`），App 把它写进 `multi-amr-avoidance/src/lib.rs:2562` 的日志字段
  `chosen="..."`。构造用"同向贴身"让两车所需方向**相反**，才能分辨方向对错。实测后车只出
  `chosen="DRIVE_ON_HEADING_BACKWARD"`（12 次）、FORWARD 0 次。
- **对端撤任务/退出协商**（`s6_corridor_peer_withdrawn.sh`，5/0）——协商中给对端 `tasks/current/stop`；
  按 `classify_local_participation`（`multi-amr-avoidance/src/lib.rs:219`：ELIGIBLE 仅当相位 ∈
  {Starting,Running,Pausing,Paused,Resuming}；`Cancelled` 属 OBSTACLE_ONLY）对端退出资格。本车恢复、无残留协商。
- **对端消失**（`s5_corridor_peer_killed.sh`，4/0）——协商中 `docker kill` 对端容器（真 runtime 下线），
  本车恢复、无残留协商；收尾重新拉起并重定位。
- 另：**idle 对端不该进协商** 负例（`s4_corridor_idle_peer.sh`，4/0）——r1 侧 blocking/conflict/pause/动作全 0。

**(b) 不可合法构造的维度：「贴身·同向·同目标」**（原 #2；`s2_corridor_queue.sh` 作为见证用例保留）。

含对照实验，两步定案：

1. **子足印间距**（默认 664 mm < 车体足印 0.80 m，两车碰撞多边形从 t=0 互相穿透）：冲突**会**触发
   （blocking 29~37 次/侧、escalate 10~12 次），但**任何恢复动作都做不了**——`drive_on_heading`
   每 ~15 s `Exceeded time allowance` Abort（真值整场只动 4/11 mm），`collision_monitor` 反复拦停
   （`motion state` 每 ~1.3 s moving↔static 抖）。恒卡死，与 `WATCH` 无关。
2. **合法间距**（把间距放到 1238 mm > 足印）复跑：`blocking 0/0、conflict 0`——**根本不触发多车解锁**，
   两车各自 90 s 内跑完全程（r1 finished、r2 running）。

即：该几何要么合法但不触发解锁、要么触发解锁但处于"车体重叠"的非法态。**它不是多车解锁的有效维度**；
真车二元解锁的合法维度是**对头**（#1）。arena 同几何（`s1_two_real_mutual_wait`，599 mm）本轮同样卡死，
且 `nav2 候选可用 0 次`、整段无 `recovery plan selection`——两世界一致。

**(c) 收敛上界不稳**（#7）：corridor 对头构造三次全量跑给出不同结论（`8/0`、`7/2`、`7/1`）——
解消**在 180 s 墙钟窗内不稳定**（有时换位并转静，有时停在走廊中段 ~1.1 m 处不再冲突但也没换位）。
所以"T 秒内必收敛"暂不可断言。

**(d) 仿真能力边界**：`s8_estop_interrupts_recovery.sh` 退出码 3——仿真车端无急停，该分支不可构造（非缺陷）。

**(e) 供以后定位的 app 侧事实**（读源码，非待办结论）：冲突事件的协调者 = 事件成员里
`(robot_id, boot_id)` 字典序最小者（`multi-amr-avoidance/src/lib.rs:2195`），即 robot1 恒为协调者；
每轮 plan 只有**一个**成员拿到"动"的动作，其余 HOLD（`lib.rs:2560`）；升级判据是"上一轮本车拿到
`CONTINUE_TASK` 且冲突仍在"（`lib.rs:2551`）——§5.7 记的振荡即由此而来。

**(f) 一键验收最终态**：`bash run_worlds.sh corridor simple`（干净起栈、按 bundle 场景组顺序跑、汇总退出码）
→ `corridor rc=1`（s3/s4/s5/s6 绿；s1 7/1、s2 6/2 见上）、`simple rc=3`（s2~s7、s9 绿，s8 为 (d) 的 3）。
两次全量跑结论一致。

## 5.10 「最终消解」卡住的根因链：改过、验过，属跨仓设计缺口（2026-10-13）

§5.9(a) 说真车对头互等"机制全活、最终消解不稳"。这一节把"为什么消解不了"钉到可复现的证据，并交代
**试过哪些修法、结果如何**——结论是这不是本仓能修的一行 bug，需要 easygo 侧定收敛策略。

**(1) 确定性的方案循环（读源码 + 真车日志）**：协调车（`(robot_id,boot_id)` 字典序最小者，恒为 robot1）
的 `multi-AMR recovery plan selection ... chosen=` 呈 3 轮一循环：

```
chosen="CONTINUE_TASK" / "DRIVE_ON_HEADING_BACKWARD" / "DRIVE_ON_HEADING_BACKWARD" / 循环
```

成因在 `crates/multi-amr-avoidance/src/lib.rs` 的升级判据：`escalate = previous_local_action_kind ==
Some("CONTINUE_TASK") && <首选属于本车>`——一旦上一轮是 Nav2 动作，下一轮就退回首选，来回翻，
两个动作都执行不到底。

**(2) 试过修：把升级判据锁存（代码）**。改成"只要上一轮本车动过（非 HOLD）就不再退回首选"，
抽出 `recovery_escalation_latched()` 并补单测（5/5 过），编译、构建、部署候选二进制到两个容器都确认过。
**结果：s1 仍是 7/1——不足以消解。**

**(3) 为什么锁存也救不了**：`previous_local_action_kind` 来自 state-center 的 `action_kind`，而它在
"等 peer Ready / 等 ActionOptionSummary"的 tick 会被写成新一轮 `summarize_multi_amr_avoidance_state`
的值（`action_kind: None`）——跨 tick 真正存活的只有 `event_id / round_id / message`。
所以锁存仍被周期性 None 打断；而 `round_id` 只在"recovery succeeded"后推进，也无法当"已经试过"的判据。

**(4) 试过修：把恢复幅度调大（参数，非代码）**。幅度是 nav2 参数而非硬编码：
`multi_amr_recovery_heading_distance_m=0.3`、`_speed_mps=0.1`、`_time_allowance_s=5.0`
（`nav2_bt_navigator/src/multi_amr_navigation_boundary.cpp:550-554`）。改成 **1.5 m / 0.3 m/s / 20 s**
后：冲突触发由 200+ 次掉到 3~4 次，**s1 反而由 7/1 变 5/3**（两车更早停在走廊中间），arena s1 不变。
**已回滚**，harness 保持镜像默认。

**(5) 卡住的真实形态（同一栈计数）**：

```
242 conflict detected / 168 recovery plan selection（每 ~0.5 s 一次）
 96 waiting for peer Ready messages / 51 execute_recovery … Aborted / 51 tick failed(request rejected)
 58 recovery succeeded; waiting for conflict-clear
  4 participant action executed          4 次本车执行
  1 task resume requested after conflict cleared
```

方案在选、对端"成功"过 54 次，**冲突始终没消解**；两车最后停在走廊中间（852 mm / 511 mm）互不冲突、
也没有换位——即"多方视角下冲突消失"之后，多车层不再介入，而 nav2 在 1.2 m 窄道里也规划不过去。

**(6) 归属与结果（2026-10-13 傍晚更新：已解，且已在仿真验证）**：上面 (1)~(5) 是**当天下午的诊断快照**，
当时结论是"属跨仓收敛策略缺口"。当天 easygo 侧落了修复 `656d66ca feat(multi-amr): 升级粘滞 + 方案身份再收敛`，
**三处正好覆盖本节诊断出的三条**：

| 本节诊断 | 修复 |
|---|---|
| 升级判据不粘 → 方案每轮在 `CONTINUE_TASK ↔ DRIVE_ON_HEADING_*` 之间交替 | `已升级过(前一轮是 Nav2 动作)` 也触发升级 → **粘滞** |
| `action_kind` 被"等 peer 的 tick"清成 None，粘不住 | 状态投影改按**成员集合摘要**承接，不只认 `event_id` |
| 对端 Ready 的回声落不到当前方案的 digest 上 | **把 `actionRef` 从 `recovery_plan_digest` 里去掉**（fwd/bwd 摆动不再改身份） |

**根因坐实**：方案来回交替 ⇒ 0.3 m 的 nudge **走一步退一步、净位移 0**；升级一旦粘住，nudge 就能累积。
所以**不需要新增第五种策略**。

**验证方式（不碰主工作区）**：主工作区当时正被他人修改（HEAD 在动、有未提交改动、revision 缺 crate 致构建坏）
→ `git worktree add --detach <…>/.worktrees/easygo-app-verify-amr origin/main`，冷构建
（需 `EASYGO_MUSIC_MANIFEST` + `AMENT_PREFIX_PATH=<主树>/ros2_interfaces/install:/opt/sros:/opt/ros/jazzy`），
产物 md5 `7b3df14b20f6859aa3ddda5deb4717ea`；harness 的 `APP_SRC_ROOT` 指向该 worktree 部署其二进制。

**验证结果**：corridor+arena 全量 15 场景 → **13 绿**。关键一条：

```
== s1_corridor_mutual_wait.sh: PASS=8 FAIL=0 ==
S1-8 错身完成：r1 −13703→−1249，r2 14281→−1483；换位=1
```

（同一份 harness 修复前四次全量跑是 8/0、7/2、7/1、7/1——只有一次偶然过；现在稳定 8/0。）

**仍红的两条是同一个"构造越界"类别**（不是解锁缺陷）：corridor `s2`（664 mm）与 arena `s1`（600 mm）
都让两车**中心距小于车体足印 0.80 m**（车体互相穿透）。前者有候选但被 `collision_monitor` 永久拦停；
后者 Nav2 **根本不产恢复候选**（`nav2 候选可用 0 次`）。**建议**改成"合法间距（≥1.2 m）下的负例"——
语义改动待用户拍板。

**(7) 留档**：候选补丁 `~/.cache/simenv-scenarios/patches/easygo-escalate-latch.patch`（我下午那版锁存 + 单测，
单独效果中性，已被 `656d66ca` 的正式实现覆盖，仅作过程记录）。

**(8) 一个使用上的坑**：仿真镜像 `runtime-sim:latest` 是 **2026-09-12** 构建的，比该修复早一个月。
用镜像起栈拿到的是**修复前**行为；要有修复行为，得用候选二进制（`APP_SRC_ROOT`）或等镜像重建。

## 6. 初始坐标必然定位成功

约定与判据：

- 定位 = 按世界位姿下发车端 `POST /api/v6/vehicle/localization/initial-pose`（`loctype=2`，地图名来自 bundle）；
- 成功判据 = `system.initialization.result = succeeded` **且** 车端上报位姿与目标位姿的差在容差内（150 mm / 0.3 rad）；
  `confidence` **只作证据打印、不作判据**（三次反例：坐标系写错那次 conf=99 却偏 3.6 m；corridor conf 52~83 时位置只差 2 mm；
  起栈瞬间 conf=0 却只差 12 mm。而 conf 本身就是匹配分数×100，见 §5.5）；
- 车端明确拒绝（例如 `no_scan` / `900009 Localization failed`）要在窗口内重试，**超窗即失败**，不许静默跳过（多车解锁在未定位时会静默不参与，历史上因此假绿过）。

已知问题必须在 bundle 化之后消失（都进回归）：

- 摆位偶发不收敛 → 现在靠 `place_robot` 重试 3 次掩盖；
- 站点形状不可读（`{"fields":{...}}` vs 扁平）导致导航目标恒为 `(0,0)`；
- 起栈时车端 localizer 尚未就绪，`simenv start` 直接判死（`409 / 900009`）。

## 7. 场景组与一键脚本

- 场景组 = bundle 的 `scenarios[]`；一键脚本**先按 bundle 校验**（世界/地图/站点/坐标），再跑组内场景；
- **切换世界是显式一步**：每个世界组跑之前有可见的"起栈 + world init 校验"步骤，不藏在钩子里；
- 退出码口径沿用：`0` 全通过、`1` 有断言失败、`2` 构造无效未验证、`3` 超范围。

实现（都在仓库外的 harness 目录 `~/.cache/simenv-scenarios/`，不属于 simenv 仓）：

- `run_worlds.sh [world ...]`：**跨世界**一键验收。不给世界名就遍历 `deploy/assets/worlds/*.world.json`；
  每个世界：`simenv stop` + 清容器/车端缓存 → **显式起栈**（`START_TIMEOUT`，默认 900 s，
  冷缓存下 app 首次启动 + world init + 定位实测能到 5 分钟以上）→ 打印两车证据行
  （`map=... pos=(x,y,θ) conf=... localized=...`）→ 跑该 bundle 声明的场景组 → 汇总；
  退出码取最严重（3>2>1>0）。每世界的原始日志留在 `/tmp/run_worlds_<world>.startlog` 与
  `/tmp/run_worlds_<world>.scenariolog`。
- `run_all.sh`：只跑"当前栈里那个世界"的场景组（先 `assert_stack_world`，栈里世界与 bundle 不符即拒跑）。
- `lib.sh`：世界语汇全部来自 bundle（`WORLD_NAME`/`MAP_NAME`/`OFF_X`/`OFF_Y`/`SCENARIOS`、
  `bundle_env.py poses` 给的复位位姿）；定位判据是**位置**（`localized=true` 且上报与真值 ≤150 mm），
  conf 只记不判。
- 三类取证工具：`fit.py`/`check.py`/`verify_map.py`（地图↔世界几何校核、偏移拟合）、
  `connectivity.py`（世界连通性：按车半宽膨胀后 BFS，判定"车道是不是唯一通路"）、
  `measure_offset.py`（把车摆在已知世界位姿、下发地图位姿、读回上报值，得到 map↔world 偏移）。

## 8. 验收（本合同完成判据）

干净起栈下：

1. 两个世界各自带 bundle：现有 `simple`（多车解锁 7 个场景）+ 新建窄通道 `corridor`（真车互等 S1），**一键脚本顺序跑完**，跨世界切换是显式一步，不为 corridor 出车端镜像；
2. bundle 校验生效：故意把 manifest/地图/站点任一改坏，脚本**失败**而不是跑空；
3. 定位稳定性：给定初始坐标重复 N 次全部收敛（不靠脚本重试掩盖）；
4. 全绿或对每个未过项写明"非缺陷/由其它层负责"的理由；未放宽断言、未跳过用例、未改契约语义。

## 9. 落地顺序

1. manifest 类型与校验（`internal/contract`，非法即失败 + 单测）；
2. ~~world init 阶段化~~ ✅ —— 按 §3 的口径：编排侧 **bundle 校验**（读 manifest → 世界名一致 → 地图主名对齐）+ 每车一遍的 **world delivery**（灌地图 → 注站点 → 下发初始位姿并断言收敛；`SeedStations` 缺则建、已存在且一致则跳过、**坐标不一致直接失败**、建后回读校验）。函数名 `worldInit` 保留，读作"world delivery + bundle 校验"；⑥ 还差"定位收敛断言"；
3. 站点/坐标从 harness 常量迁到 bundle（harness 只保留"按 bundle 断言"）；
4. easygo 侧清单（世界路径入参）→ 出镜像后删过渡态；
5. `corridor` bundle：SDF + 地图集 + manifest 都已进仓（地图由 app 自己的建图流程产出：起建图 → 连续驾驶覆盖两端待避区 → **回到建图起点** → stop 200 → 导出）。
   世界重做过一次（原车道墙立在开阔场地里、可以被绕过，见 §5.6），图与偏移都重开重测，
   `simenv start --robots 2 --world corridor` 现在**退出 0**、两车定位差 5~8 mm（见 §5.5）；
   场景组的解锁断言仍红，原因在 app 侧（见 §5.7）。

## 10. 现状与剩余

| 判据 | 状态 | 证据 |
|---|---|---|
| (1) 两个世界"显式一步"切换 | ✅ | `simenv start --world simple` 与 `--world corridor` 都退出 0；切世界不改代码、不出新车端镜像（世界文件由环境镜像提供，车端经过渡态挂载读到同一份） |
| (2) 场景组与 bundle/地图绑定校验 | ✅ | manifest × 资产一致性测试 + `ValidateForWorld` + `ValidateForMap`；新增"声明坐标必须落在出货地图观测过的区域"（窗口信息占比 ≥60%，含负例验证） |
| (3) 给定初始坐标必然定位成功 | ✅（非偶发） | 两世界起栈后上报值 vs bundle 声明值：corridor 5~8 mm、arena 8~15 mm；判据是位置而不是 conf（conf 在 corridor 长期 0~80 波动） |
| (4) 一键脚本顺序跑完全部场景组 | ✅ | `run_worlds.sh corridor simple` 干净起栈跑完 15 个场景并汇总退出码；**修复后（候选二进制）13 绿**，剩两条为"构造越界"并写明理由，见 §5.9 / §5.10 |
| 真车二元"让开"能力（窄道对头） | ✅ | easygo `656d66ca`（升级粘滞 + 方案身份再收敛）落地后，corridor `s1` 由"四次里偶然过一次"变为稳定 **8/0（换位=1）**；全量 15 场景 13 绿 → §5.10 |
| 失效点归属 | ✅ | 定位偏 2.2 m（本仓两处 bug，已修）→ §5.5；地形可绕行（本仓世界定义，已重做）→ §5.6；两真车解锁不触发/振荡（app 侧，已由 `656d66ca` 修复并验证）→ §5.7 / §5.10；场景矩阵逐条归属（含"贴身同目标不可合法构造"、"构造越界 vs 收敛策略"）→ §5.9 |
