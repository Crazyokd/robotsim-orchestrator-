package world

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

var topicTags = map[string]bool{
	"topic": true, "odom_topic": true, "tf_topic": true,
}

func RewriteRobotSDF(source string, robotName, topicPrefix string, yaw float64) (string, error) {
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
				skipDepth = 1
				continue
			}
			if name == "model" {
				t = setAttr(t, "name", robotName)
			}
			if name == "plugin" && attr(t, "name") == "gz::sim::systems::DiffDrive" {
				diffDrive = true
			}
			if topicTags[name] {
				capture = &strings.Builder{}
				captureTag = name
			}
			writeStart(&out, t)
			if name == "model" && !poseInserted {
				fmt.Fprintf(&out, "<pose>0 0 0 0 0 %g</pose>", yaw)
				poseInserted = true
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if capture != nil && t.Name.Local == captureTag {
				text := rewriteTopic(capture.String(), topicPrefix, captureTag, diffDrive)
				out.WriteString(xmlEscape(text))
				capture = nil
				captureTag = ""
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
