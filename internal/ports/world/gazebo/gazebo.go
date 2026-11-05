// Package gazebo 是 Gazebo 世界 Port：在容器里跑 gz sim server，按会话 spawn 实体，
// 等 clock 与实体就绪，并把挂接信息（Attachment）交给车端。
//
// 本包的约定只属于本包：实体与 GZ topic 前缀 robotN、GZ_IP、GZ_PARTITION、
// 环境容器的 ROS_DOMAIN_ID。它们不进 contract，框架与车端经 Attachment 拿到它们。
package gazebo

import (
	"git.standard-robots.com/yicao/sim/internal/contract"
)

const (
	// Kind 是 Attachment 的种类：车端 Port 按它认出"这是一个 Gazebo 世界"。
	Kind = "gazebo"

	// Partition / GZIP 是 GZ transport 接线：一个 simenv 会话一个 partition，
	// 世界与车端都回环到宿主。
	Partition = "easygo_sim"
	GZIP      = "127.0.0.1"
	// EnvROSDomainID 是环境容器自己的 ROS domain（与车端的 11+(N-1) 错开）。
	EnvROSDomainID = 1

	// WorldContainer 是世界容器的名字。
	WorldContainer = "simenv-world"
)

// Attachment 是 Gazebo 世界给每台车的挂接信息：车端按它接上本世界的
// GZ transport。不认识 Gazebo 的车端在 Factory.New 就失败。
type Attachment struct {
	// Cameras 表示本世界为这台车 spawn 了相机传感器：车端要不要桥接
	// 相机话题由它决定——相机选择单一来源是世界的 --cameras。
	Cameras     bool
	IP          string
	Partition   string
	TopicPrefix string
}

func (Attachment) Kind() string { return Kind }

// GZEndpoint 把三要素摊平给不想 import 本包的调用方（结构化匹配）。
func (a Attachment) GZEndpoint() (ip, partition, topicPrefix string) {
	return a.IP, a.Partition, a.TopicPrefix
}

// attachmentFor 派生一台车的挂接信息。TopicPrefix 与实体名同形（robotN）：
// GZ topic 前缀即实体名，这是本 Port 的接线约定。
func attachmentFor(robot contract.Robot, cameras bool) Attachment {
	return Attachment{Cameras: cameras, IP: GZIP, Partition: Partition, TopicPrefix: robot.Name}
}
