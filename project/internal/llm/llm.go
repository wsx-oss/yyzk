package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// init automatically loads .env file from the working directory on startup
func init() {
	loadEnvFile(".env")
}

// loadEnvFile reads a .env file and sets environment variables (does NOT override existing ones)
func loadEnvFile(filename string) {
	// Try current directory first, then look relative to executable
	paths := []string{filename}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), filename))
	}

	var f *os.File
	var err error
	for _, p := range paths {
		f, err = os.Open(p)
		if err == nil {
			break
		}
	}
	if f == nil {
		return // no .env file found, silently skip
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Do not override existing environment variables
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
			count++
		}
	}
	if count > 0 {
		log.Printf("[LLM] Loaded %d config(s) from .env file", count)
	}
}

// ======================== Data Structures ========================

// Coordinate represents a GPS point
type Coordinate struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	AltM float64 `json:"alt_m"`
}

// Action represents a drone action in the mission
type Action struct {
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// NoFlyZone represents a no-fly area (polygon or circle)
type NoFlyZone struct {
	Name      string       `json:"name,omitempty"`
	Type      string       `json:"type,omitempty"`    // "polygon" or "circle"
	Center    *Coordinate  `json:"center,omitempty"` // circle centre
	RadiusM   float64      `json:"radius_m,omitempty"` // circle radius in metres
	Polygon   []Coordinate `json:"polygon,omitempty"` // polygon vertices (also used as circle approximation)
	AltLimitM float64      `json:"alt_limit_m,omitempty"`
}

// Constraints for the flight plan
type Constraints struct {
	MaxSpeedMPS                float64     `json:"max_speed_mps,omitempty"`
	MaxAltM                    float64     `json:"max_alt_m,omitempty"`
	BatteryReturnThreshPercent int         `json:"battery_return_threshold_percent,omitempty"`
	NoFlyZones                 []NoFlyZone `json:"no_fly_zones,omitempty"`
}

// PlanRequest is the input for LLM-based flight planning
type PlanRequest struct {
	Start         Coordinate  `json:"start"`
	Goal          Coordinate  `json:"goal"`
	Actions       []Action    `json:"actions"`
	Constraints   Constraints `json:"constraints,omitempty"`
	DroneID       int         `json:"drone_id,omitempty"`
	DroneName     string      `json:"drone_name,omitempty"`
	MapContextStr string      `json:"map_context,omitempty"`
}

// Waypoint is a single point in the planned route
type Waypoint struct {
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	AltM     float64 `json:"alt_m"`
	SpeedMPS float64 `json:"speed_mps,omitempty"`
	Action   string  `json:"action,omitempty"`
}

// Warning is a risk/info message from the planner
type Warning struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// PlanResult is the output of the flight planner
type PlanResult struct {
	Waypoints   []Waypoint `json:"waypoints"`
	Actions     []Action   `json:"actions"`
	Estimates   Estimates  `json:"estimates"`
	Warnings    []Warning  `json:"warnings"`
	Explanation string     `json:"explanation"`
}

// Estimates for the planned route
type Estimates struct {
	DistanceM           float64 `json:"distance_m"`
	TimeS               float64 `json:"time_s"`
	ExpectedBatteryDrop float64 `json:"expected_battery_drop_percent"`
}

// ======================== LLM Client ========================

// Client wraps calls to an OpenAI-compatible chat completions API
type Client struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
}

// NewClient creates an LLM client from environment variables or defaults
func NewClient() *Client {
	apiKey := os.Getenv("LLM_API_KEY")
	baseURL := os.Getenv("LLM_BASE_URL")
	model := os.Getenv("LLM_MODEL")

	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	if model == "" {
		model = "qwen-plus"
	}

	return &Client{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Timeout: 60 * time.Second,
	}
}

// Available returns true if the LLM client has a valid API key configured
func (c *Client) Available() bool {
	return c.APIKey != ""
}

// chatRequest / chatResponse mirror the OpenAI-compatible API schema
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// CallRaw sends a chat completion request and returns the raw assistant message content.
// This is the public version of call() for use by other packages.
func (c *Client) CallRaw(systemPrompt, userPrompt string) (string, error) {
	return c.call(systemPrompt, userPrompt)
}

// call sends a chat completion request and returns the assistant message content
func (c *Client) call(systemPrompt, userPrompt string) (string, error) {
	if !c.Available() {
		return "", errors.New("LLM API key not configured (set LLM_API_KEY environment variable)")
	}

	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   4096,
	}

	body, _ := json.Marshal(reqBody)
	url := c.BaseURL + "/chat/completions"

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Prefer IPv4 to avoid IPv6-only resolution / unreachable IPv6 routes in some networks.
		if strings.HasPrefix(network, "tcp") {
			network = "tcp4"
		}
		d := &net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	client := &http.Client{Timeout: c.Timeout, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("LLM API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if chatResp.Error != nil {
		return "", fmt.Errorf("LLM error: %s", chatResp.Error.Message)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("LLM returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}

// ======================== Flight Planning ========================

const systemPrompt = `你是一个无人机飞行路径规划专家。用户会给你起点坐标、终点坐标、中间动作列表和约束条件。
你需要规划一条从起点到终点的最优飞行路线，在合适的位置插入用户要求的动作（如拍照、悬停、扫描等）。

你必须严格按照以下JSON格式输出，不要输出任何其他内容（不要输出markdown代码块标记）：
{
  "waypoints": [
    {"lat": 数字, "lon": 数字, "alt_m": 数字, "speed_mps": 数字, "action": "动作名称或空字符串"}
  ],
  "actions": [
    {"type": "动作类型", "params": {}}
  ],
  "estimates": {
    "distance_m": 总距离米,
    "time_s": 预计耗时秒,
    "expected_battery_drop_percent": 预计电量消耗百分比
  },
  "warnings": [
    {"level": "info/warning/critical", "message": "提示信息"}
  ],
  "explanation": "对规划路线的中文解释，说明为什么这样规划、有哪些风险点"
}

规划规则：
1. 第一个航点必须是起点（含TAKEOFF动作），最后一个航点必须是终点（含LAND动作）
2. 中间航点应合理分布，严格避开所有禁飞区
3. 在需要执行动作的位置插入对应航点
4. 默认飞行高度80m，默认速度8m/s，除非约束另有规定
5. 电量消耗按每公里2%估算
6. 【最重要、零容忍】禁飞区绕行规则（必须100%遵守，允许大幅度远距离绕路）：

   ■ polygon（多边形）禁飞区处理方法：
     - polygon字段中的坐标是多边形的顶点列表，这些顶点围成的区域是严禁飞行的
     - 判断方法：对于路径上每一段（航点A→航点B），必须确保该线段不穿越多边形内部
     - 绕行方法：找到多边形的外围顶点，选择绕行距离较短的一侧，在多边形顶点向外扩展100m处规划绕行航点
     - 具体操作：若直线路径穿越多边形，必须在多边形边界的左侧或右侧规划1-3个中间航点，使整条路径完全在多边形外部
     - 允许绕路增加50%以上的飞行距离，安全优先于效率

   ■ circle（圆形）禁飞区处理方法：
     - center字段为圆心坐标，radius_m为半径（米）
     - 所有航点与圆心的距离必须大于(radius_m + 100m)
     - 路径线段不得穿越圆形区域，须在圆形边界外100m处绕行

   ■ 通用规则：
     - 每个航点生成后，必须自我检查：该点是否在任何禁飞区内？若是，必须重新计算
     - 不得以任何理由（包括路线过长、效率低）穿越禁飞区
     - 规划完成后在explanation中详细说明绕行路线和绕行理由

7. 如果路线仍穿过禁飞区，在warnings中给出critical级别警告
8. 如果预计电量消耗超过电池返航阈值，必须在warnings中给出critical警告`

// cleanLLMJSON strips markdown code fences from LLM output
func cleanLLMJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
		}
		raw = strings.Join(lines, "\n")
		raw = strings.TrimSpace(raw)
	}
	return raw
}

// collectNFZViolations returns a description of waypoints inside NFZs (empty string = no violations)
func collectNFZViolations(req *PlanRequest, result *PlanResult) string {
	var msgs []string
	for _, nfz := range req.Constraints.NoFlyZones {
		name := nfz.Name
		if name == "" {
			name = "未命名禁飞区"
		}
		for i, wp := range result.Waypoints {
			inside := false
			if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
				inside = haversine(wp.Lat, wp.Lon, nfz.Center.Lat, nfz.Center.Lon) <= nfz.RadiusM
			} else if len(nfz.Polygon) >= 3 {
				inside = pointInPolygon(wp.Lat, wp.Lon, nfz.Polygon)
			}
			if inside {
				msgs = append(msgs, fmt.Sprintf("  - 航点%d(%.6f,%.6f)位于禁飞区[%s]内", i, wp.Lat, wp.Lon, name))
			}
		}
	}
	if len(msgs) == 0 {
		return ""
	}
	return "【严重错误】以下航点仍位于禁飞区内，必须规划绕行路线，所有航点坐标必须在禁飞区边界外部：\n" + strings.Join(msgs, "\n")
}

// segmentCircleBypass inserts a bypass waypoint when segment a→b passes through a circle NFZ.
// The bypass point is perpendicular to the segment at bufM beyond the circle radius.
func segmentCircleBypass(a, b Waypoint, nfz NoFlyZone, bufM float64) []Waypoint {
	if nfz.Center == nil || nfz.RadiusM <= 0 {
		return nil
	}
	cx, cy := nfz.Center.Lon, nfz.Center.Lat
	cosLat := math.Cos(cy * math.Pi / 180)
	mLat, mLon := 111320.0, 111320.0*cosLat

	// Convert to local metres relative to circle centre
	ax := (a.Lon - cx) * mLon
	ay := (a.Lat - cy) * mLat
	bx := (b.Lon - cx) * mLon
	by := (b.Lat - cy) * mLat

	dx, dy := bx-ax, by-ay
	lenSq := dx*dx + dy*dy
	if lenSq < 1 {
		return nil
	}
	// t = parameter of closest point on AB to centre (0,0)
	t := math.Max(0, math.Min(1, -(ax*dx+ay*dy)/lenSq))
	px, py := ax+t*dx, ay+t*dy
	dist := math.Sqrt(px*px + py*py)
	bufR := nfz.RadiusM + bufM
	if dist >= bufR {
		return nil // segment doesn't cross the buffer zone
	}

	// Bypass direction: from centre toward the closest point on segment (shortest detour side)
	var bpX, bpY float64
	if dist < 1 {
		// Centre is nearly on the segment — go perpendicular to segment
		pLen := math.Sqrt(lenSq)
		bpX = (-dy / pLen) * bufR
		bpY = (dx / pLen) * bufR
	} else {
		bpX = (px / dist) * bufR
		bpY = (py / dist) * bufR
	}

	alt := (a.AltM + b.AltM) / 2
	if alt < 80 {
		alt = 80
	}
	return []Waypoint{{
		Lat:      math.Round((cy+bpY/mLat)*1e6) / 1e6,
		Lon:      math.Round((cx+bpX/mLon)*1e6) / 1e6,
		AltM:     alt,
		SpeedMPS: a.SpeedMPS,
	}}
}

// CorrectNFZViolations geometrically fixes waypoints that fall inside circle NFZs and
// inserts bypass waypoints for path segments that cross circle NFZ buffers.
func CorrectNFZViolations(req *PlanRequest, result *PlanResult) {
	const bufM = 100.0 // 100 m safety buffer beyond NFZ radius

	var circles []NoFlyZone
	for _, nfz := range req.Constraints.NoFlyZones {
		if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
			circles = append(circles, nfz)
		}
	}
	if len(circles) == 0 {
		return
	}

	// Pass 1: push any waypoint inside a circle outside to radius+buffer
	for idx := range result.Waypoints {
		wp := &result.Waypoints[idx]
		for _, nfz := range circles {
			dist := haversine(wp.Lat, wp.Lon, nfz.Center.Lat, nfz.Center.Lon)
			target := nfz.RadiusM + bufM
			if dist < target {
				if dist < 1 {
					dist = 1
				}
				scale := target / dist
				wp.Lat = math.Round((nfz.Center.Lat+(wp.Lat-nfz.Center.Lat)*scale)*1e6) / 1e6
				wp.Lon = math.Round((nfz.Center.Lon+(wp.Lon-nfz.Center.Lon)*scale)*1e6) / 1e6
			}
		}
	}

	// Pass 2: insert bypass waypoints for segments that still cross circles
	newWPs := make([]Waypoint, 0, len(result.Waypoints)+6)
	newWPs = append(newWPs, result.Waypoints[0])
	for i := 1; i < len(result.Waypoints); i++ {
		a := newWPs[len(newWPs)-1]
		b := result.Waypoints[i]
		for _, nfz := range circles {
			if bypass := segmentCircleBypass(a, b, nfz, bufM); len(bypass) > 0 {
				newWPs = append(newWPs, bypass...)
				break
			}
		}
		newWPs = append(newWPs, b)
	}
	result.Waypoints = newWPs
}

// GeneratePlan calls the LLM to produce a flight plan, retries once on NFZ violations,
// then applies geometric correction and validates.
func (c *Client) GeneratePlan(req PlanRequest) (*PlanResult, error) {
	userPrompt, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	promptStr := string(userPrompt)
	if req.MapContextStr != "" {
		promptStr += "\n\n以下是真实地图数据（来自高德地图API），请参考这些信息进行更准确的规划：\n" + req.MapContextStr
	}

	raw, err := c.call(systemPrompt, promptStr)
	if err != nil {
		return nil, err
	}
	raw = cleanLLMJSON(raw)

	var result PlanResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("LLM output is not valid JSON: %w\nRaw output: %s", err, raw)
	}

	// If NFZ violations exist, retry with explicit error feedback
	if feedback := collectNFZViolations(&req, &result); feedback != "" && len(req.Constraints.NoFlyZones) > 0 {
		retryPrompt := promptStr + "\n\n" + feedback
		if raw2, err2 := c.call(systemPrompt, retryPrompt); err2 == nil {
			raw2 = cleanLLMJSON(raw2)
			var result2 PlanResult
			if json.Unmarshal([]byte(raw2), &result2) == nil {
				result = result2
			}
		}
	}

	// Geometric post-correction for circle NFZs
	CorrectNFZViolations(&req, &result)

	// Final validation
	if warnings := Validate(&req, &result); len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
	}
	return &result, nil
}

// GenerateFallbackPlan creates a simple direct-line plan without LLM
func GenerateFallbackPlan(req PlanRequest) *PlanResult {
	dist := haversine(req.Start.Lat, req.Start.Lon, req.Goal.Lat, req.Goal.Lon)
	speed := 8.0
	if req.Constraints.MaxSpeedMPS > 0 {
		speed = req.Constraints.MaxSpeedMPS
	}
	alt := 80.0
	if req.Start.AltM > 0 {
		alt = req.Start.AltM
	}

	waypoints := []Waypoint{
		{Lat: req.Start.Lat, Lon: req.Start.Lon, AltM: alt, SpeedMPS: 0, Action: "TAKEOFF"},
	}

	// Add intermediate waypoints for each action
	totalActions := len(req.Actions)
	for i, act := range req.Actions {
		if act.Type == "TAKEOFF" || act.Type == "LAND" {
			continue
		}
		// Interpolate position between start and goal
		frac := float64(i+1) / float64(totalActions+1)
		lat := req.Start.Lat + (req.Goal.Lat-req.Start.Lat)*frac
		lon := req.Start.Lon + (req.Goal.Lon-req.Start.Lon)*frac
		waypoints = append(waypoints, Waypoint{
			Lat: math.Round(lat*1e6) / 1e6, Lon: math.Round(lon*1e6) / 1e6,
			AltM: alt, SpeedMPS: speed, Action: act.Type,
		})
	}

	waypoints = append(waypoints, Waypoint{
		Lat: req.Goal.Lat, Lon: req.Goal.Lon, AltM: alt, SpeedMPS: speed, Action: "LAND",
	})

	timeS := dist / speed
	batteryDrop := dist / 1000 * 2

	result := &PlanResult{
		Waypoints: waypoints,
		Actions:   req.Actions,
		Estimates: Estimates{
			DistanceM:           math.Round(dist*10) / 10,
			TimeS:               math.Round(timeS*10) / 10,
			ExpectedBatteryDrop: math.Round(batteryDrop*10) / 10,
		},
		Warnings: []Warning{
			{Level: "info", Message: "此为直线降级规划（未使用LLM），仅供参考"},
		},
		Explanation: fmt.Sprintf("直线飞行路线：从起点(%.6f,%.6f)直飞至终点(%.6f,%.6f)，总距离%.0fm，预计耗时%.0fs。中间动作按等距插入。",
			req.Start.Lat, req.Start.Lon, req.Goal.Lat, req.Goal.Lon, dist, timeS),
	}

	if warnings := Validate(&req, result); len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
	}

	return result
}

// ======================== Validation ========================

// Validate checks a plan result against the request constraints
func Validate(req *PlanRequest, result *PlanResult) []Warning {
	var warnings []Warning

	// Check waypoint count
	if len(result.Waypoints) < 2 {
		warnings = append(warnings, Warning{Level: "critical", Message: "航点数量不足，至少需要起点和终点"})
	}
	if len(result.Waypoints) > 500 {
		warnings = append(warnings, Warning{Level: "critical", Message: "航点数量过多（>500），请简化路线"})
	}

	// Check coordinate validity
	for i, wp := range result.Waypoints {
		if wp.Lat < -90 || wp.Lat > 90 || wp.Lon < -180 || wp.Lon > 180 {
			warnings = append(warnings, Warning{Level: "critical", Message: fmt.Sprintf("航点%d坐标无效: lat=%.6f lon=%.6f", i, wp.Lat, wp.Lon)})
		}
		if wp.AltM < 0 {
			warnings = append(warnings, Warning{Level: "warning", Message: fmt.Sprintf("航点%d高度为负值: %.1fm", i, wp.AltM)})
		}
	}

	// Check altitude constraint
	if req.Constraints.MaxAltM > 0 {
		for i, wp := range result.Waypoints {
			if wp.AltM > req.Constraints.MaxAltM {
				warnings = append(warnings, Warning{Level: "warning", Message: fmt.Sprintf("航点%d高度%.1fm超过限制%.1fm", i, wp.AltM, req.Constraints.MaxAltM)})
			}
		}
	}

	// Check speed constraint
	if req.Constraints.MaxSpeedMPS > 0 {
		for i, wp := range result.Waypoints {
			if wp.SpeedMPS > req.Constraints.MaxSpeedMPS {
				warnings = append(warnings, Warning{Level: "warning", Message: fmt.Sprintf("航点%d速度%.1fm/s超过限制%.1fm/s", i, wp.SpeedMPS, req.Constraints.MaxSpeedMPS)})
			}
		}
	}

	// Recalculate total distance from waypoints
	totalDist := 0.0
	for i := 1; i < len(result.Waypoints); i++ {
		totalDist += haversine(
			result.Waypoints[i-1].Lat, result.Waypoints[i-1].Lon,
			result.Waypoints[i].Lat, result.Waypoints[i].Lon,
		)
	}
	result.Estimates.DistanceM = math.Round(totalDist*10) / 10

	// Battery check
	batteryDrop := totalDist / 1000 * 2
	if req.Constraints.BatteryReturnThreshPercent > 0 {
		if batteryDrop > float64(100-req.Constraints.BatteryReturnThreshPercent) {
			warnings = append(warnings, Warning{Level: "critical",
				Message: fmt.Sprintf("预计电量消耗%.1f%%超过安全阈值（返航阈值%d%%），建议缩短路线或中途返航充电",
					batteryDrop, req.Constraints.BatteryReturnThreshPercent)})
		}
	}

	// No-fly zone check (polygon ray-casting + circle radius)
	for _, nfz := range req.Constraints.NoFlyZones {
		name := nfz.Name
		if name == "" {
			name = "未命名禁飞区"
		}
		for i, wp := range result.Waypoints {
			inZone := false
			if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
				dist := haversine(wp.Lat, wp.Lon, nfz.Center.Lat, nfz.Center.Lon)
				inZone = dist <= nfz.RadiusM
			} else if len(nfz.Polygon) >= 3 {
				inZone = pointInPolygon(wp.Lat, wp.Lon, nfz.Polygon)
			}
			if inZone {
				warnings = append(warnings, Warning{Level: "critical",
					Message: fmt.Sprintf("⚠ 航点%d(%.6f,%.6f)位于禁飞区[%s]内，路线规划违规！必须重新规划绕行路线", i, wp.Lat, wp.Lon, name)})
			}
		}
	}

	// Check first/last action consistency
	if len(result.Waypoints) > 0 {
		first := result.Waypoints[0]
		if first.Action != "TAKEOFF" && first.Action != "" {
			warnings = append(warnings, Warning{Level: "warning", Message: "第一个航点建议设置TAKEOFF动作"})
		}
		last := result.Waypoints[len(result.Waypoints)-1]
		if last.Action != "LAND" && last.Action != "" {
			warnings = append(warnings, Warning{Level: "warning", Message: "最后一个航点建议设置LAND动作"})
		}
	}

	return warnings
}

// ======================== Geometry Helpers ========================

// haversine returns the distance in meters between two lat/lon points
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000 // Earth radius in meters
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

// pointInPolygon checks if a point is inside a polygon using ray casting
func pointInPolygon(lat, lon float64, polygon []Coordinate) bool {
	n := len(polygon)
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		if (polygon[i].Lon > lon) != (polygon[j].Lon > lon) &&
			lat < (polygon[j].Lat-polygon[i].Lat)*(lon-polygon[i].Lon)/(polygon[j].Lon-polygon[i].Lon)+polygon[i].Lat {
			inside = !inside
		}
		j = i
	}
	return inside
}
