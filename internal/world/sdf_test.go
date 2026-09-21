package world

import (
	"strings"
	"testing"
)

const sourceSDF = `<?xml version="1.0"?>
<sdf version="1.7">
  <model name="easygo_bj_bkn_description">
    <link name="base_link">
      <sensor name="front_rgb" type="camera"><topic>front_rgb</topic></sensor>
      <sensor name="front_depth" type="depth_camera"><topic>front_depth</topic></sensor>
      <sensor name="lidar" type="gpu_lidar"><topic>scan</topic></sensor>
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
	out, err := RewriteRobotSDF(sourceSDF, "robot1", 0.5)
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
	if !strings.Contains(out, "0 0 0 0 0 0.5") {
		t.Fatal("yaw pose missing")
	}
}

func TestRewriteTruthOdomRenamesWheelTopics(t *testing.T) {
	out, err := RewriteRobotSDF(sourceSDF, "robot1", 0)
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
