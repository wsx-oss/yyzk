package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"smartcontrol/internal/amap"
	"smartcontrol/internal/llm"

	"github.com/gin-gonic/gin"
)

// ==================== LLM Flight Plan API ====================

// FlightPlanCreate accepts a planning request, calls the LLM, stores the draft, and returns the plan.
// POST /api/flight/missions/plan
func (a *API) FlightPlanCreate(c *gin.Context) {
	var request struct {
		llm.PlanRequest
		WithReasoning bool `json:"with_reasoning"` // 是否包含思维链分析
	}
	if err := c.BindJSON(&request); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误: " + err.Error()})
		return
	}

	// Basic validation
	if request.Start.Lat == 0 && request.Start.Lon == 0 {
		c.JSON(400, gin.H{"error": "起点坐标不能为空"})
		return
	}
	if request.Goal.Lat == 0 && request.Goal.Lon == 0 {
		c.JSON(400, gin.H{"error": "终点坐标不能为空"})
		return
	}

	// Gather real map data from AMap if available
	amapClient := amap.NewClient()
	var mapCtx *amap.MapContext
	if amapClient.Available() {
		mapCtx = amapClient.BuildContext(request.Start.Lat, request.Start.Lon, request.Goal.Lat, request.Goal.Lon)
		if mapCtx != nil && mapCtx.Summary != "" {
			request.MapContextStr = mapCtx.Summary
			log.Printf("[FlightPlan] Map context: %s", mapCtx.Summary)
		}
	}

	client := llm.NewClient()

	var result *llm.PlanResult
	var reasoningChain *llm.ReasoningChain
	var planSource string
	var reasoningError string

	if client.Available() {
		var err error

		if request.WithReasoning {
			// 使用带思维链的规划
			result, reasoningChain, err = client.GeneratePlanWithReasoning(request.PlanRequest)
			if err != nil {
				reasoningError = err.Error()
				// 降级为普通规划
				result, err = client.GeneratePlan(request.PlanRequest)
				if err != nil {
					result = llm.GenerateFallbackPlan(request.PlanRequest)
					result.Warnings = append(result.Warnings, llm.Warning{
						Level:   "warning",
						Message: "LLM调用失败，已降级为直线规划: " + err.Error(),
					})
					planSource = "fallback"
				} else {
					planSource = "llm"
				}
			} else {
				planSource = "llm_with_reasoning"
			}
		} else {
			// 普通规划
			result, err = client.GeneratePlan(request.PlanRequest)
			if err != nil {
				result = llm.GenerateFallbackPlan(request.PlanRequest)
				result.Warnings = append(result.Warnings, llm.Warning{
					Level:   "warning",
					Message: "LLM调用失败，已降级为直线规划: " + err.Error(),
				})
				planSource = "fallback"
			} else {
				planSource = "llm"
			}
		}
	} else {
		result = llm.GenerateFallbackPlan(request.PlanRequest)
		planSource = "fallback"
	}

	// Serialize for storage
	reqJSON, _ := json.Marshal(request.PlanRequest)
	resultJSON, _ := json.Marshal(result)

	// Store in database
	res, err := a.db.Exec(
		`INSERT INTO flight_plans(drone_id, request_json, result_json, source, status) VALUES(?,?,?,?,?)`,
		request.DroneID, string(reqJSON), string(resultJSON), planSource, "draft",
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "保存规划失败: " + err.Error()})
		return
	}
	planID, _ := res.LastInsertId()

	respData := gin.H{
		"ok":      true,
		"plan_id": planID,
		"source":  planSource,
		"plan":    result,
	}

	if mapCtx != nil {
		respData["map_context"] = mapCtx
	}

	if reasoningChain != nil {
		respData["reasoning_chain"] = reasoningChain
		respData["reasoning_display"] = reasoningChain.FormatForDisplay()

		// 同步保存到 cot_chains 表，使 AI决策记录 页面可以展示
		go func() {
			stepsJSON, err := json.Marshal(reasoningChain.Steps)
			if err != nil {
				log.Printf("[FlightPlan] marshal reasoning steps: %v", err)
				return
			}
			metadata := map[string]interface{}{
				"source":  planSource,
				"plan_id": planID,
			}
			metaJSON, _ := json.Marshal(metadata)
			_, err = a.db.Exec(
				`INSERT INTO cot_chains (id, task_type, task_id, created_at, steps, final_decision, overall_confidence, metadata) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				reasoningChain.ID, reasoningChain.TaskType, reasoningChain.TaskID,
				reasoningChain.CreatedAt.Format("2006-01-02 15:04:05"),
				string(stepsJSON), reasoningChain.FinalDecision,
				reasoningChain.OverallConfidence, string(metaJSON),
			)
			if err != nil {
				log.Printf("[FlightPlan] save reasoning chain to cot_chains: %v", err)
			} else {
				log.Printf("[FlightPlan] reasoning chain %s saved to cot_chains", reasoningChain.ID)
			}
		}()
	}

	if reasoningError != "" {
		respData["reasoning_error"] = reasoningError
	}

	c.JSON(200, respData)
}

// FlightPlanGet retrieves a stored flight plan by ID.
// GET /api/flight/missions/plan/:id
func (a *API) FlightPlanGet(c *gin.Context) {
	id := c.Param("id")

	var planID, droneID int
	var reqJSON, resultJSON, source, status string
	var createdAt sql.NullString

	err := a.db.QueryRow(
		`SELECT id, drone_id, request_json, result_json, source, status, created_at FROM flight_plans WHERE id=?`, id,
	).Scan(&planID, &droneID, &reqJSON, &resultJSON, &source, &status, &createdAt)
	if err != nil {
		c.JSON(404, gin.H{"error": "规划不存在"})
		return
	}

	var req llm.PlanRequest
	var result llm.PlanResult
	json.Unmarshal([]byte(reqJSON), &req)
	json.Unmarshal([]byte(resultJSON), &result)

	c.JSON(200, gin.H{
		"id":         planID,
		"drone_id":   droneID,
		"request":    req,
		"plan":       result,
		"source":     source,
		"status":     status,
		"created_at": createdAt.String,
	})
}

// FlightPlanAdopt converts a draft plan into a real flight mission.
// POST /api/flight/missions/plan/:id/adopt
func (a *API) FlightPlanAdopt(c *gin.Context) {
	id := c.Param("id")

	var planID, droneID int
	var reqJSON, resultJSON, status string

	err := a.db.QueryRow(
		`SELECT id, drone_id, request_json, result_json, status FROM flight_plans WHERE id=?`, id,
	).Scan(&planID, &droneID, &reqJSON, &resultJSON, &status)
	if err != nil {
		c.JSON(404, gin.H{"error": "规划不存在"})
		return
	}
	if status != "draft" {
		c.JSON(400, gin.H{"error": "该规划已被处理，状态: " + status})
		return
	}

	var req llm.PlanRequest
	var result llm.PlanResult
	json.Unmarshal([]byte(reqJSON), &req)
	json.Unmarshal([]byte(resultJSON), &result)

	// Build route description from waypoints
	var routeParts []string
	for _, wp := range result.Waypoints {
		part := "(" + strconv.FormatFloat(wp.Lat, 'f', 6, 64) + "," + strconv.FormatFloat(wp.Lon, 'f', 6, 64) + ")"
		if wp.Action != "" {
			part += "[" + wp.Action + "]"
		}
		routeParts = append(routeParts, part)
	}
	route := strings.Join(routeParts, " → ")

	// Build target description from actions
	var actionNames []string
	for _, act := range req.Actions {
		actionNames = append(actionNames, act.Type)
	}
	target := strings.Join(actionNames, ", ")

	// Estimate duration
	estDur := ""
	if result.Estimates.TimeS > 0 {
		mins := int(result.Estimates.TimeS) / 60
		secs := int(result.Estimates.TimeS) % 60
		estDur = strconv.Itoa(mins) + "分" + strconv.Itoa(secs) + "秒"
	}

	// Create the flight mission
	now := time.Now().Format("2006-01-02 15:04:05")
	missionName := "LLM规划任务_" + now
	if req.DroneName != "" {
		missionName = req.DroneName + "_LLM规划_" + time.Now().Format("0102_1504")
	}

	// Find device_id from drone_id if available
	deviceID := 0
	if droneID > 0 {
		a.db.QueryRow(`SELECT linked_gps_device_id FROM drones WHERE id=?`, droneID).Scan(&deviceID)
	}

	description := result.Explanation
	if description == "" {
		description = "由LLM智能规划生成的飞行任务"
	}

	res, err := a.db.Exec(
		`INSERT INTO flight_missions(name, route, target, estimated_duration, description, status, current_phase, progress, device_id) VALUES(?,?,?,?,?,?,?,?,?)`,
		missionName, route, target, estDur, description, "待起飞", "待命", 0, deviceID,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "创建任务失败: " + err.Error()})
		return
	}
	missionID, _ := res.LastInsertId()

	// Update plan status
	a.db.Exec(`UPDATE flight_plans SET status='adopted', mission_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, missionID, planID)

	// Add mission log
	a.db.Exec(`INSERT INTO mission_logs(mission_id, phase, message) VALUES(?,?,?)`,
		missionID, "创建", "[LLM规划] 任务已从智能规划方案#"+strconv.Itoa(planID)+"创建")

	hub.Broadcast("flight", WSEvent{Type: "flight_created", Data: gin.H{"id": missionID, "from_plan": planID}})

	c.JSON(200, gin.H{
		"ok":         true,
		"mission_id": missionID,
		"plan_id":    planID,
	})
}

// FlightPlanDiscard marks a draft plan as discarded.
// POST /api/flight/missions/plan/:id/discard
func (a *API) FlightPlanDiscard(c *gin.Context) {
	id := c.Param("id")

	result, err := a.db.Exec(`UPDATE flight_plans SET status='discarded', updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='draft'`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		c.JSON(404, gin.H{"error": "规划不存在或已被处理"})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// FlightPlanList returns all flight plans with pagination.
// GET /api/flight/missions/plans
func (a *API) FlightPlanList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_plans`).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := a.db.Query(
		`SELECT id, drone_id, source, status, mission_id, created_at FROM flight_plans ORDER BY datetime(created_at) DESC LIMIT ? OFFSET ?`,
		pageSize, offset,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	items := []gin.H{}
	for rows.Next() {
		var id, droneID, missionID int
		var source, status string
		var createdAt sql.NullString
		if err := rows.Scan(&id, &droneID, &source, &status, &missionID, &createdAt); err == nil {
			items = append(items, gin.H{
				"id": id, "drone_id": droneID, "source": source,
				"status": status, "mission_id": missionID, "created_at": createdAt.String,
			})
		}
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "page_size": pageSize})
}

// FlightPlanStatus returns whether LLM is available.
// GET /api/flight/missions/plan/status
func (a *API) FlightPlanStatus(c *gin.Context) {
	client := llm.NewClient()
	amapClient := amap.NewClient()
	c.JSON(200, gin.H{
		"llm_available":  client.Available(),
		"amap_available": amapClient.Available(),
		"model":          client.Model,
		"base_url":       client.BaseURL,
	})
}

// AmapGeocode converts an address string to coordinate candidates.
// POST /api/amap/geocode
func (a *API) AmapGeocode(c *gin.Context) {
	var req struct {
		Address string `json:"address"`
		City    string `json:"city"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		c.JSON(400, gin.H{"error": "地址不能为空"})
		return
	}

	amapClient := amap.NewClient()
	if !amapClient.Available() {
		c.JSON(500, gin.H{"error": "高德地图 API 未配置（AMAP_KEY）"})
		return
	}

	candidates, err := amapClient.Geocode(strings.TrimSpace(req.Address), strings.TrimSpace(req.City))
	if err != nil {
		c.JSON(500, gin.H{"error": "地理编码失败: " + err.Error()})
		return
	}
	if len(candidates) == 0 {
		c.JSON(200, gin.H{"items": []interface{}{}, "message": "未找到匹配的地点"})
		return
	}
	c.JSON(200, gin.H{"items": candidates})
}

// AmapRegeocode converts coordinates to a human-readable address (reverse geocoding).
// POST /api/amap/regeocode
func (a *API) AmapRegeocode(c *gin.Context) {
	var req struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "请求格式错误"})
		return
	}
	if req.Latitude == 0 && req.Longitude == 0 {
		c.JSON(400, gin.H{"error": "坐标不能为空"})
		return
	}

	amapClient := amap.NewClient()
	if !amapClient.Available() {
		c.JSON(500, gin.H{"error": "高德地图 API 未配置（AMAP_KEY）"})
		return
	}

	result, err := amapClient.ReverseGeocode(req.Latitude, req.Longitude)
	if err != nil {
		c.JSON(500, gin.H{"error": "逆地理编码失败: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{
		"address":   result.Address,
		"province":  result.Province,
		"city":      result.City,
		"district":  result.District,
		"township":  result.Township,
		"formatted": result.Formatted,
	})
}
