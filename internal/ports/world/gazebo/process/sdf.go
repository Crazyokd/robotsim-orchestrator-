package process

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

var topicTags = map[string]bool{
	"topic": true, "odom_topic": true, "tf_topic": true,
}

// cameraUpdateRate replaces the model's camera rate when cameras are kept.
// The model ships them at 30 Hz, but camera rendering is serialized inside
// the world step, so four cameras per robot stall real time.
const cameraUpdateRate = "5"

// RewriteRobotSDF 重写机器人 SDF：改名、改话题前缀、可选丢相机。
//
// **不带出生位姿**：实体在 world 里的位姿由 `create` 请求给（见 GZ.CreateEntity），SDF 里
// 的 model pose 会被它整体覆盖，写在这里只会变成"看着像生效、其实被丢掉"的第二个来源。
func RewriteRobotSDF(source string, robotName, topicPrefix string, keepCameras bool) (string, error) {
	if robotName == "" {
		return "", fmt.Errorf("robot name must not be empty")
	}
	if topicPrefix == "" {
		return "", fmt.Errorf("topic prefix must not be empty")
	}
	dec := xml.NewDecoder(strings.NewReader(source))
	var out strings.Builder
	out.WriteString(xml.Header)
	skipDepth := 0
	var capture *strings.Builder
	var captureTag string
	poseInserted := false
	diffDrive := false
	inCamera := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			name := t.Name.Local
			if name == "sensor" && cameraType(t) {
				if !keepCameras {
					skipDepth = 1
					continue
				}
				inCamera = true
			}
			if name == "model" {
				t = setAttr(t, "name", robotName)
			}
			if name == "plugin" && attr(t, "name") == "gz::sim::systems::DiffDrive" {
				diffDrive = true
			}
			if topicTags[name] || (inCamera && name == "update_rate") {
				capture = &strings.Builder{}
				captureTag = name
			}
			writeStart(&out, t)
			if name == "model" && !poseInserted {
				out.WriteString("<pose>0 0 0 0 0 0</pose>")
				poseInserted = true
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if capture != nil && t.Name.Local == captureTag {
				text := capture.String()
				if inCamera && captureTag == "update_rate" {
					text = cameraUpdateRate
				} else {
					text = rewriteTopic(text, topicPrefix, captureTag, diffDrive)
				}
				out.WriteString(xmlEscape(text))
				capture = nil
				captureTag = ""
			}
			if t.Name.Local == "sensor" {
				inCamera = false
			}
			if t.Name.Local == "plugin" {
				diffDrive = false
			}
			if t.Name.Local == "model" {
				out.WriteString(truthOdomPlugin(topicPrefix))
			}
			fmt.Fprintf(&out, "</%s>", t.Name.Local)
		case xml.CharData:
			if skipDepth > 0 {
				continue
			}
			if capture != nil {
				capture.Write(t)
				continue
			}
			out.Write(t)
		case xml.Comment, xml.ProcInst, xml.Directive:
		}
	}
	return out.String(), nil
}

func cameraType(t xml.StartElement) bool {
	switch attr(t, "type") {
	case "camera", "depth_camera":
		return true
	default:
		return false
	}
}

func attr(t xml.StartElement, key string) string {
	for _, a := range t.Attr {
		if a.Name.Local == key {
			return a.Value
		}
	}
	return ""
}

func setAttr(t xml.StartElement, key, value string) xml.StartElement {
	found := false
	attrs := make([]xml.Attr, len(t.Attr))
	copy(attrs, t.Attr)
	for i, a := range attrs {
		if a.Name.Local == key {
			attrs[i].Value = value
			found = true
		}
	}
	if !found {
		attrs = append(attrs, xml.Attr{Name: xml.Name{Local: key}, Value: value})
	}
	t.Attr = attrs
	return t
}

func writeStart(out *strings.Builder, t xml.StartElement) {
	out.WriteByte('<')
	out.WriteString(t.Name.Local)
	for _, a := range t.Attr {
		fmt.Fprintf(out, ` %s="%s"`, a.Name.Local, xmlEscape(a.Value))
	}
	out.WriteByte('>')
}

func rewriteTopic(text, robotName, tag string, inDiffDrive bool) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "clock" || strings.TrimPrefix(trimmed, "/") == "clock" {
		return trimmed
	}
	if inDiffDrive && (tag == "odom_topic" && (trimmed == "odom" || trimmed == "/odom")) {
		return "/" + robotName + "/odom_wheel"
	}
	if inDiffDrive && tag == "tf_topic" && (trimmed == "tf" || trimmed == "/tf") {
		return "/" + robotName + "/tf_wheel"
	}
	return "/" + robotName + "/" + strings.TrimPrefix(trimmed, "/")
}

func truthOdomPlugin(robotName string) string {
	return fmt.Sprintf(
		`<plugin name="gz::sim::systems::OdometryPublisher" filename="gz-sim-odometry-publisher-system"><odom_topic>/%[1]s/odom</odom_topic><tf_topic>/%[1]s/tf</tf_topic><odom_frame>odom</odom_frame><robot_base_frame>base_link</robot_base_frame><dimensions>2</dimensions></plugin>`,
		robotName,
	)
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}
