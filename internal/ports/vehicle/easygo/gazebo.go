package easygo

import (
	"os"
	"path/filepath"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// appWorldsDir 是车端镜像里 easygo_sim_bringup 的世界目录：车端 launch 按它校验世界名
// （easygo_sim_bringup/launch/sim_nav2.launch.py 的 `_world_path`），而世界本身由环境提供。
// 这是**过渡态**：easygo 侧接受显式世界路径后（合同 §5 第 1 条），本常量与 worldHandoverBind 一起删。
const appWorldsDir = "/opt/sros/share/easygo_sim_bringup/worlds"

// worldHandoverBind 把环境这份世界 SDF 只读挂进车端容器，让"同一份文件既喂 gz server
// 又喂车端校验"——否则世界在两边各存一份，内容会悄悄漂移。世界文件不在环境里时不挂
// （裸世界只看场景，车端用它自己那份）。
func worldHandoverBind(assetsDir, world string) string {
	path := filepath.Join(assets.WorldDir(assetsDir, world), "gazebo", "world.sdf")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	// 挂载源必须是绝对路径：默认资产目录是相对的（`deploy/assets`），Docker 会把它
	// 当成 volume 名而拒绝创建容器。
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return absolute + ":" + filepath.Join(appWorldsDir, world+".sdf") + ":ro"
}
