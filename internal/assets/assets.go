// Package assets 是 simenv 的资产层：世界 bundle 的目录布局、manifest 解析与
// 资产路径解析。布局：
//
//	deploy/assets/worlds/<world>/
//	  bundle.json
//	  <world-kind>/…    世界 Port 的文件（路径写进 worlds.<kind> 节）
//	  <vehicle-kind>/…  车端 Port 的文件（路径写进 vehicles.<kind> 节）
//
// bundle 的 worlds / vehicles 两节框架只读成原始 JSON：按选中 kind 取一节交给
// Port 严格解析（缺节即失败）。站点与初始位姿是世界系 m / rad——坐标系换算
// 是消费方（车端 Port）的事。
package assets

import (
	"os"
	"path/filepath"
)

const defaultRoot = "deploy/assets"

// Path 解析资产根目录。默认值 `deploy/assets` 是**相对可执行文件所在目录**解析的，
// 不是相对 cwd：`simenv` 是仓库内的开发工具，从任意目录调用都该找到同一份资产。
//
// 这里踩过一次：原来直接返回相对路径，于是从别的目录调用时世界文件找不到，
// 交接 bind 静默为空，车端只认镜像内已有的世界——arena 正常、corridor 起不来，
// 而同样的命令在仓库根目录下手动跑就成功。
func Path(override string) string {
	if override != "" {
		return override
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), defaultRoot)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return defaultRoot
}

// WorldsRoot 是世界 bundle 的根目录。
func WorldsRoot(override string) string {
	return filepath.Join(Path(override), "worlds")
}

// WorldDir 是一个世界的 bundle 目录。
func WorldDir(override, world string) string {
	return filepath.Join(WorldsRoot(override), world)
}
