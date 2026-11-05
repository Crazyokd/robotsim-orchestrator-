package process

import (
	"strings"
	"testing"
)

const sourceSDF = `<?xml version="1.0"?>
<sdf version="1.7">
  <model name="easygo_bj_bkn_description">
    <link name="base_link">
      <sensor name="front_rgb" type="camera"><update_rate>30</update_rate><topic>front_rgb</topic></sensor>
      <sensor name="front_depth" type="depth_camera"><update_rate>30</update_rate><topic>front_depth</topic></sensor>
      <sensor name="lidar" type="gpu_lidar"><update_rate>10</update_rate><topic>scan</topic></sensor>
    </link>
    <static>0</static>
    <plugin name="gz::sim::systems::DiffDrive" filename="gz-sim-diff-drive-system">
      <topic>cmd_vel</topic>
      <odom_topic>odom</odom_topic>
      <tf_topic>tf</tf_topic>
      <frame_id>odom</frame_id>
      <child_frame_id>base_link</child_frame_id>
      <publish_tf>1</publish_tf>
    </plugin>
  </model>
</sdf>
`

func TestRewriteNamespacesTopicsKeepsFrames(t *testing.T) {
	out, err := RewriteRobotSDF(sourceSDF, "robot1", "robot1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `name="robot1"`) {
		t.Fatalf("model name not rewritten: %s", out)
	}
	if strings.Contains(out, `name="easygo_bj_bkn_description"`) {
		t.Fatal("source model name remains")
	}
	if !strings.Contains(out, "<topic>/robot1/cmd_vel</topic>") {
		t.Fatal("cmd_vel not prefixed")
	}
	if !strings.Contains(out, "<topic>/robot1/scan</topic>") {
		t.Fatal("scan not prefixed")
	}
	if !strings.Contains(out, "<frame_id>odom</frame_id>") {
		t.Fatal("frames must stay unprefixed")
	}
	if strings.Contains(out, `type="camera"`) || strings.Contains(out, `type="depth_camera"`) {
		t.Fatal("cameras must be removed")
	}
	if !strings.Contains(out, "<pose>0 0 0 0 0 0</pose>") {
		t.Fatalf("model pose must stay the identity: %s", out)
	}
}

func TestRewriteKeepsCamerasWhenAsked(t *testing.T) {
	out, err := RewriteRobotSDF(sourceSDF, "robot1", "robot1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `type="camera"`) {
		t.Fatalf("color camera must be kept: %s", out)
	}
	if !strings.Contains(out, `type="depth_camera"`) {
		t.Fatalf("depth camera must be kept: %s", out)
	}
	// Camera topics are namespaced like every other sensor topic.
	if !strings.Contains(out, "<topic>/robot1/front_rgb</topic>") {
		t.Fatalf("camera topic not prefixed: %s", out)
	}
	if !strings.Contains(out, "<topic>/robot1/front_depth</topic>") {
		t.Fatalf("depth topic not prefixed: %s", out)
	}
	// Camera rendering is serialized in the world step, so kept cameras are
	// throttled off their 30 Hz model default. The lidar keeps its own rate.
	if strings.Contains(out, "<update_rate>30</update_rate>") {
		t.Fatalf("camera update rate not throttled: %s", out)
	}
	if !strings.Contains(out, "<update_rate>5</update_rate>") {
		t.Fatalf("throttled camera rate missing: %s", out)
	}
	if !strings.Contains(out, "<update_rate>10</update_rate>") {
		t.Fatalf("lidar rate must be untouched: %s", out)
	}
}

func TestRewriteTruthOdomRenamesWheelTopics(t *testing.T) {
	out, err := RewriteRobotSDF(sourceSDF, "robot1", "robot1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<odom_topic>/robot1/odom_wheel</odom_topic>") {
		t.Fatal("diff-drive odom not moved to odom_wheel")
	}
	if !strings.Contains(out, "gz::sim::systems::OdometryPublisher") {
		t.Fatal("truth odom plugin missing")
	}
	if !strings.Contains(out, "<odom_topic>/robot1/odom</odom_topic>") {
		t.Fatal("truth odom topic missing")
	}
}

func TestRewriteUsesTopicPrefixSeparatelyFromModelName(t *testing.T) {
	out, err := RewriteRobotSDF(sourceSDF, "robot1", "fleet/robot1", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `name="robot1"`) {
		t.Fatalf("model name not rewritten: %s", out)
	}
	if !strings.Contains(out, "<topic>/fleet/robot1/cmd_vel</topic>") {
		t.Fatalf("topic prefix not used: %s", out)
	}
}
