package gazebo

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// Options 是 Gazebo 世界 Port 的启动参数；组合根把它挂到 start 的 FlagSet。
type Options struct {
	// EnvImage 是世界容器镜像（必填）。
	EnvImage string
	// CameraRobots 是要保相机传感器的车序号。
	CameraRobots []int
}

// RegisterFlags 把本 Port 的 flag 挂到给定 FlagSet，返回 parse 后填充的 Options。
func RegisterFlags(fs *flag.FlagSet) *Options {
	options := &Options{}
	fs.StringVar(&options.EnvImage, "env-image", "", "")
	fs.Var((*robotIndices)(&options.CameraRobots), "cameras", "")
	return options
}

// Validate 校验 Options 自身（与车数相关的校验在 DockerWorld.Start 里做）。
func (o Options) Validate() error {
	if strings.TrimSpace(o.EnvImage) == "" {
		return fmt.Errorf("env-image must not be empty")
	}
	return nil
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
