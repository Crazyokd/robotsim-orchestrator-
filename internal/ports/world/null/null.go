// Package null 是零依赖的世界 Port：不拉容器、不起进程，会话状态就是
// 一个状态文件。它是框架的第二个世界实现——证明"加实现不动框架"，同时
// 兼作无容器运行时的开发模式（配合 vehicle/null 一起，裸机跑全链路）。
package null

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// Kind 是世界种类：bundle 的 worlds.null 节与会话标记按它路由。
const Kind = "null"

// Options 是 null 世界的实现参数。
type Options struct {
	// DataRoot 是状态文件目录；空走 <用户缓存目录>/simenv/null。
	DataRoot string
}

// RegisterFlags 把实现参数挂到 start 的 FlagSet 上。
func RegisterFlags(fs *flag.FlagSet) *Options {
	options := &Options{}
	fs.StringVar(&options.DataRoot, "null-data-root", "", "")
	return options
}

// dataRoot 解析状态目录；缓存目录都解析不出来是环境坏了，直接失败。
func (o Options) dataRoot() (string, error) {
	if o.DataRoot != "" {
		return o.DataRoot, nil
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "simenv", "null"), nil
}

// Section 是 bundle worlds.null 节严格解析后的样子。
type Section struct {
	// Note 是任意注释：null 世界把它记进状态文件，证明节确实到达了世界 Port。
	Note string `json:"note"`
}

// ParseSection 严格解析世界节：字段多余即失败（本仓未发版，脏输入不迁就）。
func ParseSection(raw assets.Section) (Section, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var section Section
	if err := dec.Decode(&section); err != nil {
		return Section{}, fmt.Errorf("parse worlds.%s section: %w", Kind, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Section{}, fmt.Errorf("parse worlds.%s section: trailing content", Kind)
	}
	return section, nil
}

// Attachment 是 null 世界给每台车的挂接信息：车端按它认出"这是一个 null
// 世界"。不认识 null 的车端在 Factory.New 就失败。
type Attachment struct {
	World string
	Robot int
}

func (Attachment) Kind() string { return Kind }

func validateSessionName(world string) error {
	if strings.TrimSpace(world) == "" {
		return fmt.Errorf("session world must not be empty")
	}
	return nil
}
