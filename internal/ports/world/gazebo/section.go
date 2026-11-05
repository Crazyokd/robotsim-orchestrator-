package gazebo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"git.standard-robots.com/yicao/sim/internal/assets"
)

// Section 是 bundle worlds.gazebo 节（节名就是 Attachment 的 Kind）严格解析后
// 的样子：本世界 Port 要的世界描述文件，相对世界 bundle 目录。
type Section struct {
	SDF string `json:"sdf"`
}

// ParseSection 严格解析世界节：字段多余、缺失、sdf 为空都是错误。
func ParseSection(raw assets.Section) (Section, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var section Section
	if err := dec.Decode(&section); err != nil {
		return Section{}, fmt.Errorf("parse worlds.%s section: %w", Kind, err)
	}
	if strings.TrimSpace(section.SDF) == "" {
		return Section{}, fmt.Errorf("worlds.%s section sdf must not be empty", Kind)
	}
	return section, nil
}
