package easygo

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Options 是 EasyGo 车端 Port 的启动参数；组合根把它挂到 start 的 FlagSet。
// gz-gui 跑在车端容器内，所以 viewer 参数归车端而不是世界。
type Options struct {
	// AppImage 是车端镜像（必填）。
	AppImage string
	// DataRoot 是宿主数据根目录（空则用用户缓存目录），按会话名 / 车名分目录。
	DataRoot string
	// DataVolumePrefix 非空时改走 daemon 自管 named volume（每车
	// <prefix>-<session>-<robot>），与 daemon 不共享文件系统的环境（CI docker
	// executor 里 simenv 跑在 runner 容器）必须用它：bind 源由 daemon 在
	// 宿主机解析，simenv 侧的路径在宿主机上根本不存在。
	DataVolumePrefix string
	// RvizRobots / GzGuiRobots 是要开对应 viewer 的车序号。
	RvizRobots  []int
	GzGuiRobots []int
}

// RegisterFlags 把本 Port 的 flag 挂到给定 FlagSet，返回 parse 后填充的 Options。
func RegisterFlags(fs *flag.FlagSet) *Options {
	options := &Options{}
	fs.StringVar(&options.AppImage, "app-image", "", "")
	fs.StringVar(&options.DataRoot, "data-root", "", "")
	fs.StringVar(&options.DataVolumePrefix, "data-volume-prefix", os.Getenv("SIMENV_DATA_VOLUME_PREFIX"), "")
	fs.Var((*robotIndices)(&options.RvizRobots), "rviz", "")
	fs.Var((*robotIndices)(&options.GzGuiRobots), "gz-gui", "")
	return options
}

// Validate 校验 Options 自身（与车数相关的校验在 Vehicle.Start 里做）。
func (o Options) Validate() error {
	if strings.TrimSpace(o.AppImage) == "" {
		return fmt.Errorf("app-image must not be empty")
	}
	return nil
}

func (o Options) wantsRviz(index int) bool  { return wantsViewer(o.RvizRobots, index) }
func (o Options) wantsGzGui(index int) bool { return wantsViewer(o.GzGuiRobots, index) }

func wantsViewer(indices []int, index int) bool {
	for _, candidate := range indices {
		if candidate == index {
			return true
		}
	}
	return false
}

// robotIndices 解析逗号分隔的车序号（"1,3"）。
type robotIndices []int

func (r *robotIndices) String() string {
	parts := make([]string, 0, len(*r))
	for _, index := range *r {
		parts = append(parts, strconv.Itoa(index))
	}
	return strings.Join(parts, ",")
}

func (r *robotIndices) Set(text string) error {
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		index, err := strconv.Atoi(part)
		if err != nil || index < 1 {
			return fmt.Errorf("robot %q is not a positive index", part)
		}
		*r = append(*r, index)
	}
	return nil
}
