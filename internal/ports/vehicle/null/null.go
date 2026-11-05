// Package null 是零依赖的车端 Port：不起容器、不开端口，一台车的状态就是
// 一个状态文件。它是框架的第二个车端实现——与世界侧的 null 配对，证明
// "加实现 = 新 Port 包 + 组合根一行注册"，兼作无容器运行时的开发模式。
package null

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// Kind 是车端种类：bundle 的 vehicles.null 节与会话标记按它路由。
const Kind = "null"

// dataRoot 是状态目录：固定 <用户缓存目录>/simenv/null-vehicles。发现路径
// （status / stop / call）拿不到 start 的参数，所以 null 车端没有参数——
// 状态位置必须参数无关，测试用 XDG_CACHE_HOME 隔离。
func dataRoot() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "simenv", "null-vehicles"), nil
}

// Section 是 bundle vehicles.null 节严格解析后的样子。
type Section struct {
	// Note 是任意注释：车端把它记进状态文件，证明节确实到达了车端 Port。
	Note string `json:"note"`
}

// ParseSection 严格解析车端节：字段多余即失败（本仓未发版，脏输入不迁就）。
func ParseSection(raw assets.Section) (Section, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var section Section
	if err := dec.Decode(&section); err != nil {
		return Section{}, fmt.Errorf("parse vehicles.%s section: %w", Kind, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Section{}, fmt.Errorf("parse vehicles.%s section: trailing content", Kind)
	}
	return section, nil
}
