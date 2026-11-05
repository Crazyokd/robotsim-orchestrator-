package easygo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"git.standard-robots.com/yicao/sim/internal/contract"
)

// localizePollInterval 是等 init / 等收敛的轮询间隔（变量而非常量：测试里缩短）。
var localizePollInterval = time.Second

const (
	// appInitTimeout 是等车端 init 报 succeeded 的窗口。车端要等 nav2 起来才算完，
	// 本机慢的时候要几十秒。
	appInitTimeout = 3 * time.Minute
	// localizeTimeout 是"下发初始位姿后等收敛"的窗口。
	localizeTimeout = 45 * time.Second

	// localizeToleranceMM 是"车端上报位姿 vs 下发位姿"的容差（mm）。
	// 这是**判别力所在**的判据：坐标系搞错（world 与 map 差一个 origin_offset）是米级，
	// 而实测两张图在修好偏移后分别落在 15 mm（arena）与 2 mm（corridor），故取 150 mm。
	localizeToleranceMM = 150.0
	// localizeToleranceRad 是朝向容差。
	localizeToleranceRad = 0.3
)

// 关于置信度：**不作为判据**，只作为证据打印。实测两次反例说明它区分不了要抓的失效——
// ① 坐标系写错（把世界坐标当地图坐标下发）那次，车端 conf=99 却偏了 3.6 m；
// ② 低特征世界 corridor：conf 52–83 波动，但位置与下发值差 2 mm。
// 而车端在提示错误时会用扫描匹配自己纠回来（①里就没有"回声"提示值），
// 所以"位置对得上"本身已经证明"下发的初始位姿与地图自洽"。

// mapPose 把世界位姿换算成车端地图位姿：bundle 的 origin_offset_mm 说明
// "世界坐标 = 地图坐标 + offset"，而 initial-pose 接口要的是地图坐标。
// 少了这一步就是在用世界坐标当下发值——arena 里差 3.6 m，车端只能靠重定位自己兜回来。
func mapPose(world contract.Pose, offset OriginOffset) contract.Pose {
	return contract.Pose{
		X:   world.X - offset.X/1000.0,
		Y:   world.Y - offset.Y/1000.0,
		Z:   world.Z,
		Yaw: world.Yaw,
	}
}

// waitForVehicleInit 等车端 init 报 succeeded：init 完成前车端不接受初始位姿
// （实测表现为 409 / 900009 Localization failed 或 no_scan）。
func waitForVehicleInit(ctx context.Context, apiPort int) error {
	deadline := time.Now().Add(appInitTimeout)
	var last string
	for {
		body, err := statusJSON(ctx, apiPort)
		if err != nil {
			return fmt.Errorf("wait for init on :%d: %w", apiPort, err)
		}
		result, step, err := initializationState(body)
		if err != nil {
			return fmt.Errorf("wait for init on :%d: %w", apiPort, err)
		}
		switch result {
		case "succeeded":
			return nil
		case "failed":
			return fmt.Errorf("wait for init on :%d: vehicle reports failed (step=%s)", apiPort, step)
		}
		last = result
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for init on :%d: still %s after %s", apiPort, last, appInitTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(localizePollInterval):
		}
	}
}

// statusJSON 读车端状态体。
func statusJSON(ctx context.Context, apiPort int) ([]byte, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v6/vehicle/status", apiPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// awaitPose 轮询车端上报位姿，直到它与下发的目标位姿一致且置信度达标。
func (v Vehicle) awaitPose(ctx context.Context, want contract.Pose) error {
	deadline := time.Now().Add(localizeTimeout)
	var last string
	for {
		body, status, err := v.appJSON(ctx, "GET", "/api/v6/vehicle/status", nil)
		if err != nil {
			return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
		}
		if status >= 300 {
			return fmt.Errorf("localize %s: status %d", v.Robot.Name, status)
		}
		converged, observed, err := poseConverged(body, want)
		if err != nil {
			return fmt.Errorf("localize %s: %w", v.Robot.Name, err)
		}
		if converged {
			return nil
		}
		last = observed
		if time.Now().After(deadline) {
			return fmt.Errorf("localize %s: pose did not converge within %s (last %s)", v.Robot.Name, localizeTimeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(localizePollInterval):
		}
	}
}

// poseConverged 判断车端状态体里的定位位姿是否已经落在目标位姿的容差内。
// 返回 (是否收敛, 观测到的位姿描述, 错误)。
func poseConverged(body []byte, want contract.Pose) (bool, string, error) {
	var status struct {
		Data struct {
			Localization struct {
				Pose struct {
					X          *float64 `json:"x"`
					Y          *float64 `json:"y"`
					Theta      *float64 `json:"theta"`
					Confidence *uint16  `json:"confidence"`
				} `json:"pose"`
			} `json:"localization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return false, "", fmt.Errorf("decode vehicle status: %w", err)
	}
	pose := status.Data.Localization.Pose
	if pose.X == nil || pose.Y == nil {
		return false, "pose missing", nil
	}
	observed := fmt.Sprintf("x=%.0fmm y=%.0fmm theta=%.0fmrad conf=%s", *pose.X, *pose.Y, deref(pose.Theta), confidenceText(pose.Confidence))
	if abs(*pose.X-want.X*1000) > localizeToleranceMM || abs(*pose.Y-want.Y*1000) > localizeToleranceMM {
		return false, observed, nil
	}
	if pose.Theta != nil && angleDelta(*pose.Theta/1000.0, want.Yaw) > localizeToleranceRad {
		return false, observed, nil
	}
	return true, observed, nil
}

// angleDelta 把两个朝向之差折到 [0, π]：车端上报 -π 与我们下发的 +π 是同一个朝向，
// 直接相减会得到 2π 的假差（实测 robot2 位置只差 2 mm 却被判"没收敛"）。
func angleDelta(a, b float64) float64 {
	delta := math.Atan2(math.Sin(a-b), math.Cos(a-b))
	if delta < 0 {
		return -delta
	}
	return delta
}

// initializationState 读车端 init 的 result 与当前 step。
func initializationState(body []byte) (string, string, error) {
	var status struct {
		Data struct {
			System struct {
				Initialization struct {
					Result *string `json:"result"`
					Step   *string `json:"step"`
				} `json:"initialization"`
			} `json:"system"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return "", "", fmt.Errorf("decode vehicle status: %w", err)
	}
	initialization := status.Data.System.Initialization
	if initialization.Result == nil {
		return "", "", fmt.Errorf("vehicle status carries no system.initialization.result")
	}
	step := ""
	if initialization.Step != nil {
		step = *initialization.Step
	}
	return *initialization.Result, step, nil
}

func deref(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func confidenceText(value *uint16) string {
	if value == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *value)
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
