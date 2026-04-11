package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"smartcontrol/internal/cot"
	"smartcontrol/internal/db"
	"smartcontrol/internal/llm"

	"github.com/gin-gonic/gin"
)

// CoTAPI 思维链API处理器
type CoTAPI struct {
	db     *db.DB
	cotMgr *cot.CoTManager
	llm    *llm.Client
}

// NewCoTAPI 创建新的CoT API处理器
func NewCoTAPI(db *db.DB) *CoTAPI {
	return &CoTAPI{
		db:     db,
		cotMgr: cot.NewCoTManager(),
		llm:    llm.NewClient(),
	}
}

// CoTChainRequest 创建思维链的请求
type CoTChainRequest struct {
	TaskType      string                 `json:"task_type"`
	TaskID        string                 `json:"task_id"`
	Context       map[string]interface{} `json:"context"`
	ReasoningType string                 `json:"reasoning_type"` // "auto" 或指定类型
}

// CoTChainResponse 思维链响应
type CoTChainResponse struct {
	ChainID       string              `json:"chain_id"`
	TaskType      string              `json:"task_type"`
	TaskID        string              `json:"task_id"`
	CreatedAt     time.Time           `json:"created_at"`
	Steps         []cot.ReasoningStep `json:"steps"`
	FinalDecision string              `json:"final_decision"`
	Confidence    float64             `json:"confidence"`
}

// CreateCoTChain 创建新的思维链
func (a *CoTAPI) CreateCoTChain(c *gin.Context) {
	var req CoTChainRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	// 验证任务类型
	validTaskTypes := map[string]bool{
		"scheduling":        true,
		"fault_diagnosis":   true,
		"path_optimization": true,
		"mission_planning":  true,
	}
	if !validTaskTypes[req.TaskType] {
		c.JSON(400, gin.H{"error": "不支持的任务类型"})
		return
	}

	var chain *cot.ReasoningChain

	switch req.TaskType {
	case "scheduling":
		// 无人机调度决策
		chain = a.generateSchedulingCoT(req)
	case "fault_diagnosis":
		// 故障诊断
		chain = a.generateFaultDiagnosisCoT(req)
	case "path_optimization":
		// 路径优化
		chain = a.generatePathOptimizationCoT(req)
	case "mission_planning":
		// 任务规划
		chain = a.generateMissionPlanningCoT(req)
	default:
		c.JSON(400, gin.H{"error": "未知的任务类型"})
		return
	}

	// 保存到数据库
	if err := a.saveCoTChain(chain); err != nil {
		c.JSON(500, gin.H{"error": "保存思维链失败: " + err.Error()})
		return
	}

	response := CoTChainResponse{
		ChainID:       chain.ID,
		TaskType:      chain.TaskType,
		TaskID:        chain.TaskID,
		CreatedAt:     chain.CreatedAt,
		Steps:         chain.Steps,
		FinalDecision: chain.FinalDecision,
		Confidence:    chain.OverallConfidence,
	}

	c.JSON(200, gin.H{
		"ok":           true,
		"chain":        response,
		"display_text": chain.FormatForDisplay(),
	})
}

// GetCoTChain 获取思维链详情
func (a *CoTAPI) GetCoTChain(c *gin.Context) {
	chainID := c.Param("id")

	// 先从内存中查找
	if chain, exists := a.cotMgr.GetChain(chainID); exists {
		response := CoTChainResponse{
			ChainID:       chain.ID,
			TaskType:      chain.TaskType,
			TaskID:        chain.TaskID,
			CreatedAt:     chain.CreatedAt,
			Steps:         chain.Steps,
			FinalDecision: chain.FinalDecision,
			Confidence:    chain.OverallConfidence,
		}
		c.JSON(200, gin.H{
			"ok":           true,
			"chain":        response,
			"display_text": chain.FormatForDisplay(),
		})
		return
	}

	// 从数据库加载
	chain, err := a.loadCoTChain(chainID)
	if err != nil {
		c.JSON(404, gin.H{"error": "思维链不存在"})
		return
	}

	response := CoTChainResponse{
		ChainID:       chain.ID,
		TaskType:      chain.TaskType,
		TaskID:        chain.TaskID,
		CreatedAt:     chain.CreatedAt,
		Steps:         chain.Steps,
		FinalDecision: chain.FinalDecision,
		Confidence:    chain.OverallConfidence,
	}

	c.JSON(200, gin.H{
		"ok":           true,
		"chain":        response,
		"display_text": chain.FormatForDisplay(),
	})
}

// ListCoTChains 列出思维链
func (a *CoTAPI) ListCoTChains(c *gin.Context) {
	taskType := c.Query("task_type")
	taskID := c.Query("task_id")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	where := []string{"1=1"}
	args := []interface{}{}

	if taskType != "" {
		where = append(where, "task_type = ?")
		args = append(args, taskType)
	}
	if taskID != "" {
		where = append(where, "task_id = ?")
		args = append(args, taskID)
	}

	whereClause := strings.Join(where, " AND ")

	// 查询总数
	var total int
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM cot_chains WHERE %s", whereClause)
	err := a.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败: " + err.Error()})
		return
	}

	// 查询列表（包含 steps JSON 以计算步骤数）
	query := fmt.Sprintf("SELECT id, task_type, task_id, created_at, steps, final_decision, overall_confidence FROM cot_chains WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?", whereClause)
	args = append(args, pageSize, offset)

	rows, err := a.db.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": "查询失败: " + err.Error()})
		return
	}
	defer rows.Close()

	chains := []gin.H{}
	for rows.Next() {
		var id, taskType, taskID, finalDecision, createdAtStr, stepsJSON string
		var confidence float64

		if err := rows.Scan(&id, &taskType, &taskID, &createdAtStr, &stepsJSON, &finalDecision, &confidence); err == nil {
			// 计算步骤数
			var steps []json.RawMessage
			stepCount := 0
			if json.Unmarshal([]byte(stepsJSON), &steps) == nil {
				stepCount = len(steps)
			}
			chains = append(chains, gin.H{
				"id":             id,
				"task_type":      taskType,
				"task_id":        taskID,
				"created_at":     createdAtStr,
				"final_decision": finalDecision,
				"confidence":     confidence,
				"step_count":     stepCount,
			})
		}
	}

	c.JSON(200, gin.H{
		"items":     chains,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// DeleteCoTChain 删除思维链
func (a *CoTAPI) DeleteCoTChain(c *gin.Context) {
	chainID := c.Param("id")

	_, err := a.db.Exec("DELETE FROM cot_chains WHERE id = ?", chainID)
	if err != nil {
		c.JSON(500, gin.H{"error": "删除失败: " + err.Error()})
		return
	}

	c.JSON(200, gin.H{"ok": true})
}

// 生成调度决策思维链
func (a *CoTAPI) generateSchedulingCoT(req CoTChainRequest) *cot.ReasoningChain {
	// 从上下文获取无人机和任务数据
	drones := a.getAvailableDrones()
	tasks := a.getPendingTasks()

	schedulingCtx := cot.SchedulingContext{
		Drones: drones,
		Tasks:  tasks,
		Constraints: cot.SchedulingConstraints{
			MaxConcurrentTasks: 5,
			BatteryThreshold:   20,
			MaxDistance:        50.0,
			TimeWindow: struct {
				Start time.Time `json:"start"`
				End   time.Time `json:"end"`
			}{
				Start: time.Now(),
				End:   time.Now().Add(2 * time.Hour),
			},
		},
		CurrentState: cot.SystemState{
			Time:              time.Now(),
			WeatherConditions: "晴朗",
			SystemLoad:        0.3,
		},
	}

	return a.cotMgr.GenerateSchedulingCoT(schedulingCtx)
}

// 生成故障诊断思维链
func (a *CoTAPI) generateFaultDiagnosisCoT(req CoTChainRequest) *cot.ReasoningChain {
	droneID := req.TaskID
	if droneID == "" {
		droneID = "unknown"
	}

	symptoms := req.Context
	if symptoms == nil {
		symptoms = make(map[string]interface{})
	}

	return a.cotMgr.GenerateFaultDiagnosisCoT(droneID, symptoms)
}

// 生成路径优化思维链
func (a *CoTAPI) generatePathOptimizationCoT(req CoTChainRequest) *cot.ReasoningChain {
	missionID := req.TaskID
	if missionID == "" {
		missionID = "unknown"
	}

	constraints := req.Context
	if constraints == nil {
		constraints = make(map[string]interface{})
	}

	return a.cotMgr.GeneratePathOptimizationCoT(missionID, constraints)
}

// 生成任务规划思维链
func (a *CoTAPI) generateMissionPlanningCoT(req CoTChainRequest) *cot.ReasoningChain {
	chain := a.cotMgr.StartReasoning("mission_planning", req.TaskID)

	// 步骤1: 分析任务需求
	chain.AddStep("analysis",
		"分析任务目标和约束",
		"任务的主要目标和约束条件是什么？",
		"检查任务类型、时间要求、资源需求等",
		"明确任务目标和关键约束",
		[]string{"任务类型分析", "时间窗口约束", "资源需求评估"},
		0.95,
	)

	// 步骤2: 评估可用资源
	chain.AddStep("evaluation",
		"评估可用无人机资源",
		"有哪些无人机可以执行这个任务？",
		"检查无人机状态、能力、位置和电量",
		"确定可用无人机列表",
		[]string{"无人机状态检查", "能力匹配分析", "位置和电量评估"},
		0.90,
	)

	// 步骤3: 制定执行策略
	chain.AddStep("decision",
		"制定任务执行策略",
		"如何最优地执行这个任务？",
		"考虑任务优先级、资源分配、风险控制等因素",
		"制定详细的任务执行计划",
		[]string{"优先级排序", "资源分配方案", "风险控制策略"},
		0.85,
	)

	// 步骤4: 风险评估
	chain.AddStep("evaluation",
		"评估任务风险",
		"任务执行存在哪些风险？",
		"分析天气、设备、环境等风险因素",
		"识别关键风险并制定应对措施",
		[]string{"天气风险评估", "设备可靠性分析", "应急预案制定"},
		0.80,
	)

	chain.SetFinalDecision("任务规划完成，建议按计划执行", 0.85)
	return chain
}

// 保存思维链到数据库
func (a *CoTAPI) saveCoTChain(chain *cot.ReasoningChain) error {
	stepsJSON, err := json.Marshal(chain.Steps)
	if err != nil {
		return err
	}

	metadataJSON, err := json.Marshal(chain.Metadata)
	if err != nil {
		return err
	}

	_, err = a.db.Exec(
		`INSERT INTO cot_chains (id, task_type, task_id, created_at, steps, final_decision, overall_confidence, metadata) 
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		chain.ID, chain.TaskType, chain.TaskID, chain.CreatedAt.Format("2006-01-02 15:04:05"), string(stepsJSON),
		chain.FinalDecision, chain.OverallConfidence, string(metadataJSON),
	)

	return err
}

// 从数据库加载思维链
func (a *CoTAPI) loadCoTChain(chainID string) (*cot.ReasoningChain, error) {
	var id, taskType, taskID, stepsJSON, finalDecision, metadataJSON, createdAtStr string
	var confidence float64

	err := a.db.QueryRow(
		`SELECT id, task_type, task_id, created_at, steps, final_decision, overall_confidence, metadata 
		 FROM cot_chains WHERE id = ?`, chainID,
	).Scan(&id, &taskType, &taskID, &createdAtStr, &stepsJSON, &finalDecision, &confidence, &metadataJSON)

	if err != nil {
		return nil, err
	}

	var steps []cot.ReasoningStep
	if err := json.Unmarshal([]byte(stepsJSON), &steps); err != nil {
		return nil, err
	}

	var metadata map[string]interface{}
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		metadata = make(map[string]interface{})
	}

	createdAt, _ := time.Parse("2006-01-02 15:04:05", createdAtStr)

	chain := &cot.ReasoningChain{
		ID:                id,
		TaskType:          taskType,
		TaskID:            taskID,
		CreatedAt:         createdAt,
		Steps:             steps,
		FinalDecision:     finalDecision,
		OverallConfidence: confidence,
		Metadata:          metadata,
	}

	// 也保存到内存管理器（通过公共方法）
	// 注意：这里我们无法直接保存到内存管理器，因为chains字段未导出
	// 在实际使用中，我们会通过其他方式管理

	return chain, nil
}

// 获取可用无人机列表
func (a *CoTAPI) getAvailableDrones() []cot.DroneInfo {
	var drones []cot.DroneInfo

	rows, err := a.db.Query(`
		SELECT d.id, d.name, d.status, 
		       COALESCE(b.level, 0) as battery_level,
		       COALESCE(g.latitude, 0) as lat,
		       COALESCE(g.longitude, 0) as lon
		FROM drones d
		LEFT JOIN battery_records b ON d.id = b.device_id AND b.id = (
			SELECT MAX(id) FROM battery_records WHERE device_id = d.id
		)
		LEFT JOIN gps_devices g ON d.linked_gps_device_id = g.id
		WHERE d.status = 'online'
		LIMIT 10
	`)
	if err != nil {
		return drones
	}
	defer rows.Close()

	for rows.Next() {
		var drone cot.DroneInfo
		var lat, lon float64
		var battery int

		if err := rows.Scan(&drone.ID, &drone.Name, &drone.Status, &battery, &lat, &lon); err == nil {
			drone.BatteryLevel = battery
			drone.Location = cot.Location{Lat: lat, Lon: lon}
			drone.Capabilities = []string{"拍照", "录像", "测量"}
			drones = append(drones, drone)
		}
	}

	return drones
}

// 获取待处理任务列表
func (a *CoTAPI) getPendingTasks() []cot.TaskInfo {
	var tasks []cot.TaskInfo

	rows, err := a.db.Query(`
		SELECT id, name, target, estimated_duration 
		FROM flight_missions 
		WHERE status IN ('待起飞', '飞行中')
		LIMIT 10
	`)
	if err != nil {
		return tasks
	}
	defer rows.Close()

	for rows.Next() {
		var task cot.TaskInfo
		var name, target, duration string

		if err := rows.Scan(&task.ID, &name, &target, &duration); err == nil {
			task.Type = "飞行任务"
			task.Priority = 2                            // 中等优先级
			task.Location = cot.Location{Lat: 0, Lon: 0} // 简化处理
			task.Duration = 30                           // 默认30分钟
			task.Requirements = []string{"无人机", "GPS定位"}
			tasks = append(tasks, task)
		}
	}

	return tasks
}

// AnalyzeRequest 通用AI分析请求
type AnalyzeRequest struct {
	Scenario    string                 `json:"scenario"`    // flight_planning, emergency_analysis, battery_analysis, fault_diagnosis
	TaskID      string                 `json:"task_id"`     // 关联任务ID
	Context     map[string]interface{} `json:"context"`     // 场景上下文数据
	Description string                 `json:"description"` // 问题描述
}

// CoTAnalyze 通用AI思维链分析接口 - 调用真实LLM进行推理
func (a *CoTAPI) CoTAnalyze(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "无效的请求数据"})
		return
	}

	if req.Scenario == "" {
		c.JSON(400, gin.H{"error": "场景类型不能为空"})
		return
	}

	// 构建LLM提示词
	systemPrompt := a.buildCoTSystemPrompt(req.Scenario)
	userPrompt := a.buildCoTUserPrompt(req)

	chain := a.cotMgr.StartReasoning(req.Scenario, req.TaskID)
	chain.Metadata["request"] = req

	if a.llm.Available() {
		// 调用真实LLM进行思维链推理
		raw, err := a.llm.CallRaw(systemPrompt, userPrompt)
		if err != nil {
			log.Printf("[CoT] LLM call failed: %v, using fallback", err)
			a.generateFallbackAnalysis(chain, req)
			chain.Metadata["source"] = "fallback"
			chain.Metadata["llm_error"] = err.Error()
		} else {
			// 解析LLM返回的结构化推理步骤
			if err := a.parseLLMReasoning(chain, raw); err != nil {
				log.Printf("[CoT] Parse LLM reasoning failed: %v, raw=%s", err, raw[:min(len(raw), 200)])
				// 将原始回复作为单步推理
				chain.AddStep("analysis", req.Description, "分析当前场景", raw, "已完成分析", nil, 0.85)
				chain.SetFinalDecision(raw, 0.85)
			}
			chain.Metadata["source"] = "llm"
		}
	} else {
		a.generateFallbackAnalysis(chain, req)
		chain.Metadata["source"] = "fallback_no_key"
	}

	// 保存到数据库
	if err := a.saveCoTChain(chain); err != nil {
		log.Printf("[CoT] Save chain failed: %v", err)
	}

	response := CoTChainResponse{
		ChainID:       chain.ID,
		TaskType:      chain.TaskType,
		TaskID:        chain.TaskID,
		CreatedAt:     chain.CreatedAt,
		Steps:         chain.Steps,
		FinalDecision: chain.FinalDecision,
		Confidence:    chain.OverallConfidence,
	}

	c.JSON(200, gin.H{
		"ok":           true,
		"chain":        response,
		"display_text": chain.FormatForDisplay(),
		"source":       chain.Metadata["source"],
	})
}

// buildCoTSystemPrompt 根据场景构建LLM系统提示词
func (a *CoTAPI) buildCoTSystemPrompt(scenario string) string {
	base := `你是一个无人机智能管控系统的AI决策专家。请使用思维链(Chain-of-Thought)方式进行严谨的多步推理分析。

推理框架（请严格按以下8步展开，可根据场景合并或省略不适用的步骤）：
1. 态势感知：收集当前设备状态、环境条件、任务状态、告警信息
2. 问题识别：明确需要决策的核心问题和关键矛盾
3. 约束分析：列出所有硬约束（不可违反）和软约束（可权衡）
4. 方案生成：基于约束和目标，生成2-3个可行方案
5. 风险评估：对每个方案评估成功概率、失败后果、资源消耗
6. 方案对比：综合安全性、可行性、任务完成度、资源效率进行排序
7. 决策输出：给出推荐方案、执行步骤、监控重点、降级条件
8. 应急预案：推荐方案失败时的备选处置

输出必须严格按照以下JSON格式，不要输出任何其他内容（不要输出markdown代码块标记）：
{
  "steps": [
    {
      "step": 1,
      "type": "analysis|evaluation|decision|optimization",
      "context": "当前步骤的上下文",
      "question": "这一步要解决的问题",
      "thought": "详细的思考过程（引用具体数据和规则依据）",
      "answer": "这一步的结论",
      "confidence": 0.0到1.0之间的置信度
    }
  ],
  "final_decision": "最终决策（格式：当前建议 + 主要依据 + 备选方案 + 风险提示）",
  "overall_confidence": 0.0到1.0之间的总体置信度
}

要求：
- 每个步骤的思考过程要引用具体数据、阈值、规则，体现专业性
- 置信度标准：>0.9 数据充分且结论确定；0.7-0.9 有合理依据但存在不确定性；<0.7 信息不足需人工确认
- 最终决策必须包含：推荐操作、依据来源、备选方案、风险提示
- 告警优先级遵循：P0紧急(失控/碰撞/起火)>P1严重(低电/失联/GNSS丢失)>P2警告(压差/风速/温度)>P3提示(载荷/轻微偏航)
- 安全决策原则：安全优先、任务次之、资源最优
- 所有输出使用中文
`

	switch scenario {
	case "flight_planning":
		return base + `
你正在分析一个无人机飞行规划任务。请按思维链框架推理，重点关注：
1. 态势感知：起终点地理环境、距离、海拔差、沿途地形（城市/山地/水域/开阔地）
2. 设备评估：所选无人机的机型适配性、电量是否满足往返+20%安全裕量、载荷匹配度
3. 约束分析：
   - 空域约束：航线是否穿越法定禁飞区、动态限制区、企业敏感区
   - 动力约束：电池容量 vs 预估能耗（考虑风速修正）
   - 平台约束：最大速度、最小转弯半径、最大爬升率
   - 环境约束：风场（逆风增加30-50%能耗）、GNSS可用性、通信链路可达性
   - 通信约束：全程图传/遥控链路是否可达
4. 路径策略：生成高效/均衡/保守三种方案，说明各方案的航程、耗时、能耗、风险
5. 返航安全：验证任意航点的返航电量裕量；标注最近备降点
6. 动态重规划预案：定义偏离门限和重规划触发条件
`
	case "emergency_analysis":
		return base + `
你正在分析一个紧急报警情况。请按思维链框架推理，重点关注：
1. 告警识别：确认告警类型、严重级别（P0-P3）、影响范围
2. 因果分析：告警之间是否存在因果链（如：强风→高功耗→低电→链路降级）
3. 影响评估：
   - 对飞行安全的影响（是否需要立即返航/悬停/迫降）
   - 对当前任务的影响（能否继续/降级执行/必须终止）
   - 对其他在飞无人机的影响（是否需要全局响应）
4. 立即处置：按告警优先级给出可执行的应急措施和操作步骤
5. 跟踪恢复：告警解除条件、恢复飞行的前置检查项、任务恢复策略
6. 经验沉淀：根因分析、预防建议、是否需要修改告警阈值或SOP
`
	case "battery_analysis":
		return base + `
你正在分析无人机电池异常情况。请按思维链框架推理，重点关注：
1. 电池状态全景：电量SOC、总电压、单体压差、放电电流、温度、循环次数、SOH健康度
2. 趋势分析：电压下降速率是否异常、温升趋势、与历史同条件飞行的对比
3. 安全边界判断：
   - 剩余电量 vs 返航所需电量（含风速修正和爬升修正）
   - 单体压差是否>0.1V（不均衡风险）
   - 温度是否在安全范围（-10°C~45°C）
   - 是否存在低温电压塌陷风险
4. 决策建议：
   - >40% 且趋势正常：可继续任务，持续监控
   - 25%-40%：评估是否缩短任务或提前返航
   - <25%：建议立即返航
   - <15% 或电压异常下降：紧急返航或就近降落
5. 电池管理建议：充放电窗口、存储电量、循环次数与降级使用策略
6. 联动调整：是否需要调低后续任务的距离限制或提高返航阈值
`
	case "fault_diagnosis":
		return base + `
你正在对无人机故障进行诊断。请按思维链框架推理，重点关注：
1. 症状收集：具体故障表现、发生时间、触发条件、是否可复现
2. 多源排查：
   - 硬件：IMU漂移/罗盘干扰/电机温升/桨叶损伤/云台卡顿/天线异常
   - 导航：GNSS信号弱/RTK失锁/位置跳变/航向偏差
   - 能源：电池压差/温度异常/电压塌陷/充电异常
   - 链路：遥控信号弱/图传中断/频段干扰/时延异常
   - 软件：飞控异常/参数配置错误/固件兼容性问题
3. 故障定位：通过排除法和关联分析确定最可能的故障源，给出各原因的概率
4. 严重度评估：对飞行安全的影响等级、是否可继续飞行
5. 处置方案：
   - 立即措施（如悬停、返航、人工接管）
   - 地面维修方案（检查项、更换件、标定步骤）
6. 预防措施：飞前检查增补项、维护周期调整、参数阈值修改
`
	default:
		return base + `请按照思维链推理框架，结合提供的上下文信息进行全面分析和决策。`
	}
}

// buildCoTUserPrompt 构建用户提示词
func (a *CoTAPI) buildCoTUserPrompt(req AnalyzeRequest) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("场景类型: %s", req.Scenario))
	if req.Description != "" {
		parts = append(parts, fmt.Sprintf("问题描述: %s", req.Description))
	}
	if req.TaskID != "" {
		parts = append(parts, fmt.Sprintf("关联任务ID: %s", req.TaskID))
	}
	if req.Context != nil {
		ctxJSON, _ := json.MarshalIndent(req.Context, "", "  ")
		parts = append(parts, fmt.Sprintf("上下文数据:\n%s", string(ctxJSON)))
	}

	// 添加系统实时数据
	systemData := a.gatherSystemContext(req.Scenario)
	if systemData != "" {
		parts = append(parts, fmt.Sprintf("系统实时数据:\n%s", systemData))
	}

	// RAG: 注入相关知识库内容
	ragQuery := req.Scenario
	if req.Description != "" {
		ragQuery = req.Description
	}
	if ragCtx := RAGRetrieveContext(ragQuery, 3); ragCtx != "" {
		parts = append(parts, ragCtx)
	}

	// RL: 对飞行规划和故障诊断场景注入RL策略建议
	if req.Scenario == "flight_planning" || req.Scenario == "emergency_analysis" {
		bridge := NewRLPolicyBridge()
		if hints := bridge.GenerateFlightHints(); hints != "" {
			parts = append(parts, hints)
		}
	}

	return strings.Join(parts, "\n\n")
}

// gatherSystemContext 收集系统实时上下文
func (a *CoTAPI) gatherSystemContext(scenario string) string {
	var info []string

	// 获取在线无人机数量
	var onlineCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM drones WHERE status='online'`).Scan(&onlineCount)
	info = append(info, fmt.Sprintf("当前在线无人机: %d台", onlineCount))

	// 获取活跃任务数
	var activeTaskCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM flight_missions WHERE status IN ('待起飞','飞行中','返航中')`).Scan(&activeTaskCount)
	info = append(info, fmt.Sprintf("当前活跃飞行任务: %d个", activeTaskCount))

	// 获取未解决报警数
	var alertCount int
	a.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE COALESCE(status,'未解决') != '已解决'`).Scan(&alertCount)
	info = append(info, fmt.Sprintf("未解决报警: %d条", alertCount))

	info = append(info, fmt.Sprintf("当前时间: %s", time.Now().Format("2006-01-02 15:04:05")))

	return strings.Join(info, "\n")
}

// parseLLMReasoning 解析LLM返回的思维链推理结果
func (a *CoTAPI) parseLLMReasoning(chain *cot.ReasoningChain, raw string) error {
	// 清理markdown代码块标记
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
		}
		raw = strings.Join(lines, "\n")
		raw = strings.TrimSpace(raw)
	}

	var result struct {
		Steps []struct {
			Step       int     `json:"step"`
			Type       string  `json:"type"`
			Context    string  `json:"context"`
			Question   string  `json:"question"`
			Thought    string  `json:"thought"`
			Answer     string  `json:"answer"`
			Confidence float64 `json:"confidence"`
		} `json:"steps"`
		FinalDecision     string  `json:"final_decision"`
		OverallConfidence float64 `json:"overall_confidence"`
	}

	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return fmt.Errorf("parse LLM reasoning JSON: %w", err)
	}

	for _, s := range result.Steps {
		chain.AddStep(s.Type, s.Context, s.Question, s.Thought, s.Answer, nil, s.Confidence)
	}

	if result.FinalDecision != "" {
		chain.SetFinalDecision(result.FinalDecision, result.OverallConfidence)
	}

	return nil
}

// generateFallbackAnalysis 生成降级分析（LLM不可用时）
func (a *CoTAPI) generateFallbackAnalysis(chain *cot.ReasoningChain, req AnalyzeRequest) {
	switch req.Scenario {
	case "flight_planning":
		chain.AddStep("analysis", "飞行规划分析", "当前飞行条件如何？", "检查起终点坐标、距离、无人机状态", "已完成基本参数检查", nil, 0.80)
		chain.AddStep("evaluation", "风险评估", "飞行存在哪些风险？", "评估电量、天气、禁飞区等因素", "建议保持安全边界并确保电量充足", nil, 0.75)
		chain.AddStep("decision", "路径决策", "应采用何种路径策略？", "综合考虑距离、安全和效率", "建议采用直线飞行并保持适当高度", nil, 0.78)
		chain.SetFinalDecision("基础飞行规划已生成（LLM未配置，使用基础分析）。建议配置LLM API获取更详尽的智能分析。", 0.78)
	case "emergency_analysis":
		desc := req.Description
		if desc == "" {
			desc = "未知报警"
		}
		chain.AddStep("analysis", "报警分析", "报警的严重程度如何？", "检查报警类型和影响范围", fmt.Sprintf("已识别报警: %s", desc), nil, 0.80)
		chain.AddStep("evaluation", "风险评估", "对系统和任务有何影响？", "评估对在飞无人机和任务的影响", "建议立即检查相关设备状态", nil, 0.75)
		chain.AddStep("decision", "应急措施", "应采取什么措施？", "制定紧急应对方案", "建议检查设备状态并考虑暂停非必要任务", nil, 0.72)
		chain.SetFinalDecision(fmt.Sprintf("针对报警[%s]的基础分析已完成。建议配置LLM API获取更详细的智能分析和具体应对方案。", desc), 0.75)
	case "battery_analysis":
		chain.AddStep("analysis", "电池状态分析", "电池当前状态如何？", "检查电量、电压、温度、健康度", "已读取电池基本参数", nil, 0.80)
		chain.AddStep("evaluation", "安全评估", "是否需要返航？", "评估剩余电量能否支撑安全返航", "建议当电量低于20%时立即返航", nil, 0.85)
		chain.AddStep("decision", "电池管理建议", "如何优化电池使用？", "制定电池管理策略", "建议降低飞行速度以节约电量，并设置自动返航阈值", nil, 0.82)
		chain.SetFinalDecision("电池基础分析已完成（LLM未配置）。建议配置LLM API获取更精准的电池寿命预测和管理建议。", 0.82)
	default:
		chain.AddStep("analysis", "通用分析", "当前情况如何？", "收集和分析可用信息", "已完成基本信息收集", nil, 0.75)
		chain.SetFinalDecision("基础分析已完成。建议配置LLM API获取更详细的智能分析。", 0.75)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// RegisterCoTRoutes 注册CoT相关路由
func RegisterCoTRoutes(r *gin.Engine, db *db.DB) {
	cotAPI := NewCoTAPI(db)

	cotGroup := r.Group("/api/cot")
	{
		cotGroup.POST("/chains", cotAPI.CreateCoTChain)
		cotGroup.GET("/chains/:id", cotAPI.GetCoTChain)
		cotGroup.GET("/chains", cotAPI.ListCoTChains)
		cotGroup.DELETE("/chains/:id", cotAPI.DeleteCoTChain)
		cotGroup.POST("/analyze", cotAPI.CoTAnalyze)
	}
}
