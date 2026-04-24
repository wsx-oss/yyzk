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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
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
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
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
	Type      string       `json:"type,omitempty"`     // "polygon" or "circle"
	Center    *Coordinate  `json:"center,omitempty"`   // circle centre
	RadiusM   float64      `json:"radius_m,omitempty"` // circle radius in metres
	Polygon   []Coordinate `json:"polygon,omitempty"`  // polygon vertices (also used as circle approximation)
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

// ======================== RPM Metrics ========================

// rpmTracker records per-minute request counts and latency for monitoring.
type rpmTracker struct {
	totalRequests  int64 // lifetime total
	totalFailed    int64
	totalLatencyNs int64
	windowStart    int64 // unix second of current window start
	windowCount    int64
	mu             sync.Mutex
}

var globalRPM = &rpmTracker{windowStart: time.Now().Unix()}

func (r *rpmTracker) record(latency time.Duration, failed bool) {
	atomic.AddInt64(&r.totalRequests, 1)
	atomic.AddInt64(&r.totalLatencyNs, int64(latency))
	if failed {
		atomic.AddInt64(&r.totalFailed, 1)
	}
	now := time.Now().Unix()
	r.mu.Lock()
	if now-atomic.LoadInt64(&r.windowStart) >= 60 {
		atomic.StoreInt64(&r.windowCount, 1)
		atomic.StoreInt64(&r.windowStart, now)
	} else {
		atomic.AddInt64(&r.windowCount, 1)
	}
	r.mu.Unlock()
}

// RPMSnapshot holds point-in-time RPM metrics.
type RPMSnapshot struct {
	CurrentMinute int64   `json:"current_minute_requests"`
	RPMLimit      int     `json:"rpm_limit"`
	TotalRequests int64   `json:"total_requests"`
	TotalFailed   int64   `json:"total_failed"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	QueuedCount   int     `json:"queued"`
}

// RPMMetrics returns a snapshot of LLM request rate metrics.
func RPMMetrics() RPMSnapshot {
	total := atomic.LoadInt64(&globalRPM.totalRequests)
	failed := atomic.LoadInt64(&globalRPM.totalFailed)
	latNs := atomic.LoadInt64(&globalRPM.totalLatencyNs)
	now := time.Now().Unix()

	globalRPM.mu.Lock()
	curMin := atomic.LoadInt64(&globalRPM.windowCount)
	if now-atomic.LoadInt64(&globalRPM.windowStart) >= 60 {
		curMin = 0
	}
	globalRPM.mu.Unlock()

	var avgMs float64
	if total > 0 {
		avgMs = float64(latNs) / float64(total) / 1e6
	}

	limitVal := 30
	if v := os.Getenv("LLM_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limitVal = n
		}
	}

	// Queued = waiting on rate limiter (estimated from Tokens)
	queued := 0
	if globalLimiter != nil {
		if toks := globalLimiter.Tokens(); toks < 1 {
			queued = 1 // at least one request is waiting
		}
	}

	return RPMSnapshot{
		CurrentMinute: curMin,
		RPMLimit:      limitVal,
		TotalRequests: total,
		TotalFailed:   failed,
		AvgLatencyMs:  avgMs,
		QueuedCount:   queued,
	}
}

// ======================== Rate Limiter ========================

var globalLimiter *rate.Limiter

func init() {
	rpm := 30
	if v := os.Getenv("LLM_RPM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rpm = n
		}
	}
	// rate.Limit = events per second, burst = allow 2 concurrent
	globalLimiter = rate.NewLimiter(rate.Limit(float64(rpm)/60.0), 2)
	log.Printf("[LLM] RPM limiter initialized: %d RPM (%.2f req/s, burst 2)", rpm, float64(rpm)/60.0)
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

	timeout := 60 * time.Second
	if v := os.Getenv("LLM_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}

	return &Client{
		APIKey:  apiKey,
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Timeout: timeout,
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

// callWithTemp sends a chat completion request with a configurable sampling temperature.
func (c *Client) callWithTemp(systemPrompt, userPrompt string, temperature float64) (string, error) {
	if !c.Available() {
		return "", errors.New("LLM API key not configured (set LLM_API_KEY environment variable)")
	}

	// Wait for RPM rate limiter
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()
	if err := globalLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("RPM rate limit exceeded: %w", err)
	}

	start := time.Now()
	result, err := c.doCallWithTemp(systemPrompt, userPrompt, temperature)
	globalRPM.record(time.Since(start), err != nil)
	return result, err
}

// doCallWithTemp is the actual HTTP call implementation.
func (c *Client) doCallWithTemp(systemPrompt, userPrompt string, temperature float64) (string, error) {
	reqBody := chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: temperature,
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

// call sends a chat completion request with the default temperature (0.3).
func (c *Client) call(systemPrompt, userPrompt string) (string, error) {
	return c.callWithTemp(systemPrompt, userPrompt, 0.3)
}

// ======================== Flight Planning ========================

const systemPrompt = `你是一个无人机飞行路径规划专家。接收起终点坐标、任务动作列表和飞行约束，输出结构化飞行计划。

【输出格式】仅输出纯JSON，绝对不允许添加任何说明文字或代码块标记（禁止输出` + "```" + `等符号）：
{
  "waypoints": [
    {"lat": 数字, "lon": 数字, "alt_m": 数字, "speed_mps": 数字, "action": "动作名或空字符串"}
  ],
  "actions": [{"type": "动作类型", "params": {}}],
  "estimates": {
    "distance_m": 总距离米,
    "time_s": 预计耗时秒,
    "expected_battery_drop_percent": 电量消耗百分比
  },
  "warnings": [{"level": "info/warning/critical", "message": "提示信息"}],
  "explanation": "中文路线说明，重点描述绕行决策和路径逻辑"
}

【规划规则】
1. 第一个航点动作=TAKEOFF，最后一个航点动作=LAND
2. 默认高度80m、速度8m/s；constraints字段另有规定时以constraints为准
3. 电量估算：总距离(km) × 2%
4. 航点精简原则（重要）：总航点数控制在5~20个；禁止生成两两相距不足50m的相邻重复航点；
   用最少航点表达完整路径语义，不要冗余插点
5. 任务动作插入：将actions中的每个动作插入到合理地理位置对应的航点中

【禁飞区绕行 — 零容忍，允许大幅绕路】
注：后端几何引擎会对你的输出做精确校正，但你必须在语义层面主动避开禁飞区。

■ polygon（多边形禁飞区）：
  - polygon数组中的坐标围成严禁飞入的封闭区域
  - 要求：每段航线(A→B)不得与多边形任意边相交，且航点不得落入多边形内部
  - 绕行：路径与多边形相交时，选取相交侧的1~2个多边形顶点，在其外扩150m处插入绕行航点，
    选择总绕行距离较短的一侧（左绕或右绕）

■ circle（圆形禁飞区）：
  - 所有航点到center距离必须 > (radius_m + 150m)
  - 每段航线亦不得穿越圆形区域；须在圆外150m处规划切线绕行航点

■ 三步自检（每个航点输出后必须执行）：
  ① 该航点是否落入任意多边形内部？→ 是则推移至多边形外
  ② 该航点到任意圆心距离是否 ≤ (radius_m+150)？→ 是则推移至圆外
  ③ 该航点与前一航点的连线是否穿越任意禁飞区？→ 是则插入中间绕行航点

6. 路线仍有违规时，在warnings中给出critical级别警告
7. 电量消耗超过返航阈值时，在warnings中给出critical级别警告`

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

// collectNFZViolations returns a description of waypoints/segments inside or crossing NFZs (empty = no violations)
func collectNFZViolations(req *PlanRequest, result *PlanResult) string {
	var msgs []string
	for _, nfz := range req.Constraints.NoFlyZones {
		name := nfz.Name
		if name == "" {
			name = "未命名禁飞区"
		}
		// Check each waypoint is not inside NFZ
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
		// Check each path segment does not cross the polygon boundary
		if len(nfz.Polygon) >= 3 {
			for i := 1; i < len(result.Waypoints); i++ {
				a, b := result.Waypoints[i-1], result.Waypoints[i]
				if segmentCrossesPolygon(a, b, nfz.Polygon) {
					msgs = append(msgs, fmt.Sprintf("  - 路径段%d→%d(%.6f,%.6f)→(%.6f,%.6f)穿越禁飞区[%s]边界，必须绕行", i-1, i, a.Lat, a.Lon, b.Lat, b.Lon, name))
				}
			}
		}
	}
	if len(msgs) == 0 {
		return ""
	}
	return "【严重错误】以下航点或路径段仍位于或穿越禁飞区，必须规划绕行路线，所有路径段必须完全在禁飞区边界外部：\n" + strings.Join(msgs, "\n")
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

// CorrectNFZViolations geometrically fixes the plan in two passes:
//  1. Push any waypoint that landed inside an NFZ to just outside the boundary.
//  2. For every segment that still crosses an NFZ, find the optimal detour using
//     a visibility-graph + Dijkstra search among the NFZ boundary vertices.
func CorrectNFZViolations(req *PlanRequest, result *PlanResult) {
	const bufM = 150.0
	if len(req.Constraints.NoFlyZones) == 0 {
		return
	}

	// ── Pass 1: push waypoints that are inside any NFZ to just outside ─────────────
	for idx := range result.Waypoints {
		wp := &result.Waypoints[idx]
		for _, nfz := range req.Constraints.NoFlyZones {
			if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
				d := haversine(wp.Lat, wp.Lon, nfz.Center.Lat, nfz.Center.Lon)
				if target := nfz.RadiusM + bufM; d < target {
					if d < 1 {
						d = 1
					}
					scale := target / d
					wp.Lat = math.Round((nfz.Center.Lat+(wp.Lat-nfz.Center.Lat)*scale)*1e6) / 1e6
					wp.Lon = math.Round((nfz.Center.Lon+(wp.Lon-nfz.Center.Lon)*scale)*1e6) / 1e6
				}
			}
			if len(nfz.Polygon) >= 3 && pointInPolygon(wp.Lat, wp.Lon, nfz.Polygon) {
				var cLat, cLon float64
				for _, p := range nfz.Polygon {
					cLat += p.Lat
					cLon += p.Lon
				}
				cLat /= float64(len(nfz.Polygon))
				cLon /= float64(len(nfz.Polygon))
				cosLat := math.Cos(cLat * math.Pi / 180)
				mLonP, mLatP := 111320.0*cosLat, 111320.0
				vx := (wp.Lon - cLon) * mLonP
				vy := (wp.Lat - cLat) * mLatP
				vLen := math.Sqrt(vx*vx + vy*vy)
				if vLen < 1 {
					vx, vy, vLen = 0, 1, 1
				}
				var maxR float64
				for _, p := range nfz.Polygon {
					if d := haversine(cLat, cLon, p.Lat, p.Lon); d > maxR {
						maxR = d
					}
				}
				if target := maxR + bufM; vLen < target {
					scale := target / vLen
					wp.Lon = math.Round((cLon+vx*scale/mLonP)*1e6) / 1e6
					wp.Lat = math.Round((cLat+vy*scale/mLatP)*1e6) / 1e6
				}
			}
		}
	}

	// ── Pass 2: visibility-graph detour for segments still crossing NFZs ────────
	newWPs := make([]Waypoint, 0, len(result.Waypoints)+20)
	newWPs = append(newWPs, result.Waypoints[0])
	for i := 1; i < len(result.Waypoints); i++ {
		a := newWPs[len(newWPs)-1]
		b := result.Waypoints[i]
		if bypass := visibilityGraphBypass(a, b, req.Constraints.NoFlyZones, bufM); len(bypass) > 0 {
			newWPs = append(newWPs, bypass...)
		}
		newWPs = append(newWPs, b)
	}
	result.Waypoints = newWPs

	// ── Post-process: greedy removal of redundant waypoints ─────────────────────
	result.Waypoints = simplifyPath(result.Waypoints, req.Constraints.NoFlyZones)
}

// generateWithTemp is the core LLM planning function with a configurable sampling temperature.
func (c *Client) generateWithTemp(req PlanRequest, temperature float64) (*PlanResult, error) {
	userPrompt, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	promptStr := string(userPrompt)
	if req.MapContextStr != "" {
		promptStr += "\n\n以下是真实地图数据（来自高德地图API），请参考这些信息进行更准确的规划：\n" + req.MapContextStr
	}

	raw, err := c.callWithTemp(systemPrompt, promptStr, temperature)
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
		if raw2, err2 := c.callWithTemp(systemPrompt, retryPrompt, temperature); err2 == nil {
			raw2 = cleanLLMJSON(raw2)
			var result2 PlanResult
			if json.Unmarshal([]byte(raw2), &result2) == nil {
				result = result2
			}
		}
	}

	// Geometric post-correction
	CorrectNFZViolations(&req, &result)

	// Final validation
	if warnings := Validate(&req, &result); len(warnings) > 0 {
		result.Warnings = append(result.Warnings, warnings...)
	}
	return &result, nil
}

// GeneratePlan calls the LLM to produce a flight plan with balanced parameters.
func (c *Client) GeneratePlan(req PlanRequest) (*PlanResult, error) {
	return c.generateWithTemp(req, 0.3)
}

// MultiPlanResult holds multiple candidate flight plans generated with different styles.
type MultiPlanResult struct {
	Plans  []*PlanResult `json:"plans"`
	Labels []string      `json:"labels"`
}

// GenerateMultiplePlans generates three candidate flight plans in parallel using different
// temperature settings:
//   - 高效方案 (temp=0.10): focuses on shortest path
//   - 均衡方案 (temp=0.35): balanced route
//   - 保守方案 (temp=0.65): safety-first with creative detours
func (c *Client) GenerateMultiplePlans(req PlanRequest) *MultiPlanResult {
	labels := []string{"高效方案", "均衡方案", "保守方案"}
	temps := []float64{0.10, 0.35, 0.65}
	plans := make([]*PlanResult, 3)
	for i, temp := range temps {
		result, err := c.generateWithTemp(req, temp)
		if err != nil {
			result = GenerateFallbackPlan(req)
			result.Warnings = append(result.Warnings, Warning{
				Level:   "warning",
				Message: fmt.Sprintf("LLM调用失败(%s)，已降级为直线规划: %s", labels[i], err.Error()),
			})
		}
		plans[i] = result
	}
	return &MultiPlanResult{Plans: plans, Labels: labels}
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

	// Apply geometric NFZ correction even on fallback straight-line plans
	CorrectNFZViolations(&req, result)

	// Recalculate estimates after correction (path may now be longer)
	correctedDist := 0.0
	for i := 1; i < len(result.Waypoints); i++ {
		correctedDist += haversine(
			result.Waypoints[i-1].Lat, result.Waypoints[i-1].Lon,
			result.Waypoints[i].Lat, result.Waypoints[i].Lon,
		)
	}
	if correctedDist > 0 {
		correctedTimeS := correctedDist / speed
		result.Estimates.DistanceM = math.Round(correctedDist*10) / 10
		result.Estimates.TimeS = math.Round(correctedTimeS*10) / 10
		result.Estimates.ExpectedBatteryDrop = math.Round(correctedDist/1000*2*10) / 10
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

// segmentClearOfAllNFZs returns true if segment a→b does not enter any NFZ.
func segmentClearOfAllNFZs(a, b Waypoint, nfzs []NoFlyZone) bool {
	for _, nfz := range nfzs {
		if len(nfz.Polygon) >= 3 && segmentCrossesPolygon(a, b, nfz.Polygon) {
			return false
		}
		if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 && segmentCrossesCircle(a, b, nfz) {
			return false
		}
	}
	return true
}

// visibilityGraphBypass finds the globally shortest NFZ-free detour between waypoints a and b
// using a visibility graph built from NFZ boundary vertices and Dijkstra's algorithm.
// Returns nil when a→b is already clear; returns intermediate waypoints otherwise.
func visibilityGraphBypass(a, b Waypoint, nfzs []NoFlyZone, bufM float64) []Waypoint {
	if segmentClearOfAllNFZs(a, b, nfzs) {
		return nil
	}

	alt := (a.AltM + b.AltM) / 2
	if alt < 80 {
		alt = 80
	}

	// ─ Build node list: start + NFZ boundary offset-vertices + goal ─────────────
	type vgNode struct{ lat, lon float64 }
	nodes := []vgNode{{a.Lat, a.Lon}}

	for _, nfz := range nfzs {
		if len(nfz.Polygon) >= 3 {
			var cLat, cLon float64
			for _, p := range nfz.Polygon {
				cLat += p.Lat
				cLon += p.Lon
			}
			cLat /= float64(len(nfz.Polygon))
			cLon /= float64(len(nfz.Polygon))
			cosLat := math.Cos(cLat * math.Pi / 180)
			mLonN, mLatN := 111320.0*cosLat, 111320.0
			for _, p := range nfz.Polygon {
				vx := (p.Lon - cLon) * mLonN
				vy := (p.Lat - cLat) * mLatN
				vLen := math.Sqrt(vx*vx + vy*vy)
				if vLen < 1 {
					continue
				}
				scale := (vLen + bufM) / vLen
				nodes = append(nodes, vgNode{
					lat: math.Round((cLat+vy*scale/mLatN)*1e6) / 1e6,
					lon: math.Round((cLon+vx*scale/mLonN)*1e6) / 1e6,
				})
			}
		}
		if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
			r := nfz.RadiusM + bufM
			cosLat := math.Cos(nfz.Center.Lat * math.Pi / 180)
			mLonN, mLatN := 111320.0*cosLat, 111320.0
			for i := 0; i < 8; i++ {
				angle := float64(i) * math.Pi / 4
				nodes = append(nodes, vgNode{
					lat: math.Round((nfz.Center.Lat+r/mLatN*math.Sin(angle))*1e6) / 1e6,
					lon: math.Round((nfz.Center.Lon+r/mLonN*math.Cos(angle))*1e6) / 1e6,
				})
			}
		}
	}

	goalIdx := len(nodes)
	nodes = append(nodes, vgNode{b.Lat, b.Lon})
	n := len(nodes)

	toWP := func(nd vgNode) Waypoint {
		return Waypoint{Lat: nd.lat, Lon: nd.lon, AltM: alt, SpeedMPS: a.SpeedMPS}
	}

	// ─ Build visibility matrix (O(n²) segment checks) ─────────────────────────
	vis := make([][]bool, n)
	edgeDist := make([][]float64, n)
	for i := range vis {
		vis[i] = make([]bool, n)
		edgeDist[i] = make([]float64, n)
		for j := range edgeDist[i] {
			edgeDist[i][j] = math.Inf(1)
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			wi, wj := toWP(nodes[i]), toWP(nodes[j])
			if segmentClearOfAllNFZs(wi, wj, nfzs) {
				d := haversine(wi.Lat, wi.Lon, wj.Lat, wj.Lon)
				vis[i][j], vis[j][i] = true, true
				edgeDist[i][j], edgeDist[j][i] = d, d
			}
		}
	}

	// ─ Dijkstra from node 0 (start) to goalIdx ────────────────────────────
	distArr := make([]float64, n)
	prev := make([]int, n)
	for i := range distArr {
		distArr[i] = math.Inf(1)
		prev[i] = -1
	}
	distArr[0] = 0
	visited := make([]bool, n)
	for range nodes {
		u := -1
		for i := 0; i < n; i++ {
			if !visited[i] && (u == -1 || distArr[i] < distArr[u]) {
				u = i
			}
		}
		if u == -1 || math.IsInf(distArr[u], 1) {
			break
		}
		visited[u] = true
		if u == goalIdx {
			break
		}
		for v := 0; v < n; v++ {
			if vis[u][v] && !visited[v] {
				if nd := distArr[u] + edgeDist[u][v]; nd < distArr[v] {
					distArr[v] = nd
					prev[v] = u
				}
			}
		}
	}

	if math.IsInf(distArr[goalIdx], 1) {
		// Visibility graph found no path – fall back to single extreme-vertex bypass
		for _, nfz := range nfzs {
			if len(nfz.Polygon) >= 3 && segmentCrossesPolygon(a, b, nfz.Polygon) {
				return polygonBypassWaypoints(a, b, nfz.Polygon, bufM)
			}
		}
		return nil
	}

	// ─ Reconstruct path ─────────────────────────────────────────────────────
	path := []int{}
	for cur := goalIdx; cur != -1; cur = prev[cur] {
		path = append(path, cur)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	var bypass []Waypoint
	for k := 1; k < len(path)-1; k++ {
		bypass = append(bypass, toWP(nodes[path[k]]))
	}
	return bypass
}

// segmentsCross2D reports whether segment [ax,ay]→[bx,by] and [cx,cy]→[dx,dy] share an interior point
func segmentsCross2D(ax, ay, bx, by, cx, cy, dx, dy float64) bool {
	d1x, d1y := bx-ax, by-ay
	d2x, d2y := dx-cx, dy-cy
	denom := d1x*d2y - d1y*d2x
	if math.Abs(denom) < 1e-12 {
		return false
	}
	t := ((cx-ax)*d2y - (cy-ay)*d2x) / denom
	u := ((cx-ax)*d1y - (cy-ay)*d1x) / denom
	return t > 1e-9 && t < 1-1e-9 && u > 1e-9 && u < 1-1e-9
}

// segmentCrossesPolygon reports whether segment a→b crosses any polygon edge,
// or whether the midpoint of the segment lies inside the polygon.
func segmentCrossesPolygon(a, b Waypoint, poly []Coordinate) bool {
	n := len(poly)
	if n < 3 {
		return false
	}
	// Check if midpoint is inside polygon (handles case where A and B are both outside
	// but the path passes entirely through the polygon interior)
	midLat := (a.Lat + b.Lat) / 2
	midLon := (a.Lon + b.Lon) / 2
	if pointInPolygon(midLat, midLon, poly) {
		return true
	}
	// Check if either endpoint is inside
	if pointInPolygon(a.Lat, a.Lon, poly) || pointInPolygon(b.Lat, b.Lon, poly) {
		return true
	}
	// Check edge crossings
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		if segmentsCross2D(a.Lon, a.Lat, b.Lon, b.Lat,
			poly[i].Lon, poly[i].Lat, poly[j].Lon, poly[j].Lat) {
			return true
		}
	}
	return false
}

// polygonBypassWaypoints returns the minimal set of bypass waypoints to route around
// a polygon NFZ for segment a→b.
// Strategy: first try a single extreme-vertex bypass (cheapest); if that single point
// still clips the polygon, fall back to boundary traversal.
func polygonBypassWaypoints(a, b Waypoint, poly []Coordinate, bufM float64) []Waypoint {
	if !segmentCrossesPolygon(a, b, poly) {
		return nil
	}
	n := len(poly)

	// Compute centroid
	var cLat, cLon float64
	for _, p := range poly {
		cLat += p.Lat
		cLon += p.Lon
	}
	cLat /= float64(n)
	cLon /= float64(n)
	cosLat := math.Cos(cLat * math.Pi / 180)
	mLon := 111320.0 * cosLat
	mLat := 111320.0

	// Build offset vertices (each vertex pushed outward from centroid by bufM)
	alt := (a.AltM + b.AltM) / 2
	if alt < 80 {
		alt = 80
	}
	offsetVerts := make([]Waypoint, n)
	for i, p := range poly {
		vx := (p.Lon - cLon) * mLon
		vy := (p.Lat - cLat) * mLat
		vLen := math.Sqrt(vx*vx + vy*vy)
		if vLen < 1 {
			offsetVerts[i] = Waypoint{Lat: p.Lat, Lon: p.Lon, AltM: alt, SpeedMPS: a.SpeedMPS}
			continue
		}
		scale := (vLen + bufM) / vLen
		offsetVerts[i] = Waypoint{
			Lat:      math.Round((cLat+vy*scale/mLat)*1e6) / 1e6,
			Lon:      math.Round((cLon+vx*scale/mLon)*1e6) / 1e6,
			AltM:     alt,
			SpeedMPS: a.SpeedMPS,
		}
	}

	// Segment direction in metres
	abLonM := (b.Lon - a.Lon) * mLon
	abLatM := (b.Lat - a.Lat) * mLat
	segLen := math.Sqrt(abLonM*abLonM + abLatM*abLatM)
	if segLen < 1 {
		return nil
	}

	// Find the extreme polygon vertex on each side (left/right) of A→B
	var maxLeft, maxRight float64
	leftIdx, rightIdx := 0, 0
	maxLeft, maxRight = math.Inf(-1), math.Inf(-1)
	for i, p := range poly {
		px := (p.Lon - a.Lon) * mLon
		py := (p.Lat - a.Lat) * mLat
		if lp := (-abLatM*px + abLonM*py) / segLen; lp > maxLeft {
			maxLeft = lp
			leftIdx = i
		}
		if rp := (abLatM*px - abLonM*py) / segLen; rp > maxRight {
			maxRight = rp
			rightIdx = i
		}
	}

	lbp := offsetVerts[leftIdx]
	rbp := offsetVerts[rightIdx]

	pathDist1 := func(bp Waypoint) float64 {
		return haversine(a.Lat, a.Lon, bp.Lat, bp.Lon) + haversine(bp.Lat, bp.Lon, b.Lat, b.Lon)
	}

	// ── Step 1: try single-vertex bypass – preferred because it produces the fewest waypoints
	leftClear := !segmentCrossesPolygon(a, lbp, poly) && !segmentCrossesPolygon(lbp, b, poly)
	rightClear := !segmentCrossesPolygon(a, rbp, poly) && !segmentCrossesPolygon(rbp, b, poly)

	if leftClear && rightClear {
		if pathDist1(lbp) <= pathDist1(rbp) {
			return []Waypoint{lbp}
		}
		return []Waypoint{rbp}
	}
	if leftClear {
		return []Waypoint{lbp}
	}
	if rightClear {
		return []Waypoint{rbp}
	}

	// ── Step 2: boundary traversal fallback (for very concave polygons)
	type crossInfo struct {
		edgeIdx int
		t       float64
	}
	var crosses []crossInfo
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		edgeLonM := (poly[j].Lon - poly[i].Lon) * mLon
		edgeLatM := (poly[j].Lat - poly[i].Lat) * mLat
		denom := abLonM*edgeLatM - abLatM*edgeLonM
		if math.Abs(denom) < 1e-9 {
			continue
		}
		diffLonM := (poly[i].Lon - a.Lon) * mLon
		diffLatM := (poly[i].Lat - a.Lat) * mLat
		t := (diffLonM*edgeLatM - diffLatM*edgeLonM) / denom
		u := (diffLonM*abLatM - diffLatM*abLonM) / denom
		if t > 1e-9 && t < 1-1e-9 && u > 1e-9 && u < 1-1e-9 {
			crosses = append(crosses, crossInfo{i, t})
		}
	}
	sort.Slice(crosses, func(i, j int) bool { return crosses[i].t < crosses[j].t })

	pathDistWPs := func(wps []Waypoint) float64 {
		if len(wps) == 0 {
			return math.Inf(1)
		}
		d := haversine(a.Lat, a.Lon, wps[0].Lat, wps[0].Lon)
		for k := 1; k < len(wps); k++ {
			d += haversine(wps[k-1].Lat, wps[k-1].Lon, wps[k].Lat, wps[k].Lon)
		}
		d += haversine(wps[len(wps)-1].Lat, wps[len(wps)-1].Lon, b.Lat, b.Lon)
		return d
	}

	if len(crosses) >= 2 {
		entryEdge := crosses[0].edgeIdx
		exitEdge := crosses[len(crosses)-1].edgeIdx
		cwPath := []Waypoint{}
		for idx := (entryEdge + 1) % n; ; idx = (idx + 1) % n {
			cwPath = append(cwPath, offsetVerts[idx])
			if idx == (exitEdge+1)%n || len(cwPath) > n {
				break
			}
		}
		ccwPath := []Waypoint{}
		for idx := entryEdge; ; idx = (idx - 1 + n) % n {
			ccwPath = append(ccwPath, offsetVerts[idx])
			if idx == exitEdge || len(ccwPath) > n {
				break
			}
		}
		if pathDistWPs(cwPath) <= pathDistWPs(ccwPath) {
			return cwPath
		}
		return ccwPath
	}

	// Last resort: use the extreme vertex even if it may still clip
	if pathDist1(lbp) <= pathDist1(rbp) {
		return []Waypoint{lbp}
	}
	return []Waypoint{rbp}
}

// simplifyPath removes redundant intermediate waypoints while preserving NFZ avoidance.
// A waypoint is removed when the direct segment skipping it clears all NFZs.
// Waypoints with mission-critical actions (TAKEOFF, LAND, PHOTO, HOVER, RTH) are always kept.
func simplifyPath(wps []Waypoint, nfzs []NoFlyZone) []Waypoint {
	if len(wps) <= 2 {
		return wps
	}

	isCritical := func(wp Waypoint) bool {
		a := wp.Action
		return a == "TAKEOFF" || a == "LAND" || a == "PHOTO" || a == "HOVER" || a == "RTH"
	}

	segClear := func(a, b Waypoint) bool {
		for _, nfz := range nfzs {
			if len(nfz.Polygon) >= 3 && segmentCrossesPolygon(a, b, nfz.Polygon) {
				return false
			}
			if nfz.Type == "circle" && nfz.Center != nil && nfz.RadiusM > 0 {
				if segmentCrossesCircle(a, b, nfz) {
					return false
				}
			}
		}
		return true
	}

	// Greedy: from each position, jump as far forward as possible without crossing an NFZ
	// or skipping a critical waypoint.
	result := []Waypoint{wps[0]}
	i := 0
	for i < len(wps)-1 {
		j := len(wps) - 1
		for j > i+1 {
			// Don't skip any critical waypoints between i and j
			skippable := true
			for k := i + 1; k < j; k++ {
				if isCritical(wps[k]) {
					skippable = false
					break
				}
			}
			if skippable && segClear(wps[i], wps[j]) {
				break
			}
			j--
		}
		result = append(result, wps[j])
		i = j
	}
	return result
}

// segmentCrossesCircle reports whether the closest point on segment a→b to the
// circle centre is within the circle's radius.
func segmentCrossesCircle(a, b Waypoint, nfz NoFlyZone) bool {
	if nfz.Center == nil || nfz.RadiusM <= 0 {
		return false
	}
	cx, cy := nfz.Center.Lon, nfz.Center.Lat
	cosLat := math.Cos(cy * math.Pi / 180)
	mLon := 111320.0 * cosLat
	mLat := 111320.0
	ax := (a.Lon - cx) * mLon
	ay := (a.Lat - cy) * mLat
	bx := (b.Lon - cx) * mLon
	by := (b.Lat - cy) * mLat
	dx, dy := bx-ax, by-ay
	lenSq := dx*dx + dy*dy
	if lenSq < 1 {
		return math.Sqrt(ax*ax+ay*ay) <= nfz.RadiusM
	}
	t := math.Max(0, math.Min(1, -(ax*dx+ay*dy)/lenSq))
	px, py := ax+t*dx, ay+t*dy
	return math.Sqrt(px*px+py*py) <= nfz.RadiusM
}

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
