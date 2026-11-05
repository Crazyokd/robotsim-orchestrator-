// Package null 是零依赖的世界 Port：不拉容器、不起进程，会话状态就是
// 一个状态文件。它是框架的第二个世界实现——证明"加实现不动框架"，同时
// 兼作无容器运行时的开发模式（配合 vehicle/null 一起，裸机跑全链路）。
package null

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// Kind 是世界种类：bundle 的 worlds.null 节与会话标记按它路由。
const Kind = "null"

// dataRoot 是状态目录：固定 <用户缓存目录>/simenv/null。发现路径（status /
// stop / call）拿不到 start 的参数，所以 null 世界没有参数——状态位置
// 必须参数无关，测试用 XDG_CACHE_HOME 隔离。
func dataRoot() (string, error) {
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
