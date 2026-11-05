// Package null 是零依赖的车端 Port：不起容器、不开端口，一台车的状态就是
// 一个状态文件。它是框架的第二个车端实现——与世界侧的 null 配对，证明
// "加实现 = 新 Port 包 + 组合根一行注册"，兼作无容器运行时的开发模式。
package null

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// Kind 是车端种类：bundle 的 vehicles.null 节与会话标记按它路由。
const Kind = "null"

// Options 是 null 车端的实现参数。
type Options struct {
	// DataRoot 是状态文件目录；空走 <用户缓存目录>/simenv/null-vehicles。
	DataRoot string
}

// RegisterFlags 把实现参数挂到 start 的 FlagSet 上。
func RegisterFlags(fs *flag.FlagSet) *Options {
	options := &Options{}
	fs.StringVar(&options.DataRoot, "null-vehicle-data-root", "", "")
	return options
}

func (o Options) dataRoot() (string, error) {
	if o.DataRoot != "" {
		return o.DataRoot, nil
	}
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
