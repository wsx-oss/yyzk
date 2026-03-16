package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ReasoningStep 表示思维链中的一个推理步骤
type ReasoningStep struct {
	Step       int     `json:"step"`
	Type       string  `json:"type"` // "analysis", "decision", "evaluation", "optimization"
	Context    string  `json:"context"`
	Question   string  `json:"question"`
	Thought    string  `json:"thought"`
	Answer     string  `json:"answer"`
	Confidence float64 `json:"confidence"`
}

// ReasoningChain 完整的思维链
type ReasoningChain struct {
	ID                string          `json:"id"`
	TaskType          string          `json:"task_type"`
	TaskID            string          `json:"task_id"`
	CreatedAt         time.Time       `json:"created_at"`
	Steps             []ReasoningStep `json:"steps"`
	FinalDecision     string          `json:"final_decision"`
	OverallConfidence float64         `json:"overall_confidence"`
}

// GeneratePlanWithReasoning 生成带思维链的飞行规划
func (c *Client) GeneratePlanWithReasoning(req PlanRequest) (*PlanResult, *ReasoningChain, error) {
	// 使用增强的prompt来获取推理过程
	enhancedPrompt := `你是一个无人机飞行路径规划专家。请按照以下步骤进行推理：

推理步骤：
1. 分析任务需求和约束条件
2. 评估环境因素（天气、禁飞区等）
3. 考虑无人机性能和电量限制
4. 制定多个候选方案
5. 比较各方案的优缺点
6. 选择最优方案并说明理由

最终输出格式（必须严格遵循）：
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
  "explanation": "对规划路线的中文解释，说明推理过程和选择理由",
  "reasoning_steps": [
    {
      "step": 1,
      "type": "analysis",
      "context": "分析上下文",
      "question": "分析的问题",
      "thought": "思考过程",
      "answer": "分析结论",
      "confidence": 0.9
    }
  ]
}`

	userPrompt, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}

	// 附加地图上下文
	promptStr := string(userPrompt)
	if req.MapContextStr != "" {
		promptStr += "\n\n以下是真实地图数据（来自高德地图API），请参考这些信息进行更准确的规划：\n" + req.MapContextStr
	}

	raw, err := c.call(enhancedPrompt, promptStr)
	if err != nil {
		return nil, nil, err
	}

	// 处理markdown代码块
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) > 2 {
			lines = lines[1 : len(lines)-1]
		}
		raw = strings.Join(lines, "\n")
		raw = strings.TrimSpace(raw)
	}

	// 解析结果
	var result struct {
		Waypoints   []Waypoint      `json:"waypoints"`
		Actions     []Action        `json:"actions"`
		Estimates   Estimates       `json:"estimates"`
		Warnings    []Warning       `json:"warnings"`
		Explanation string          `json:"explanation"`
		Reasoning   []ReasoningStep `json:"reasoning_steps"`
	}

	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, nil, fmt.Errorf("LLM output is not valid JSON: %w\nRaw output: %s", err, raw)
	}

	// 构建规划结果
	planResult := &PlanResult{
		Waypoints:   result.Waypoints,
		Actions:     result.Actions,
		Estimates:   result.Estimates,
		Warnings:    result.Warnings,
		Explanation: result.Explanation,
	}

	// 构建思维链
	reasoningChain := &ReasoningChain{
		ID:                fmt.Sprintf("reasoning_%d", time.Now().UnixNano()),
		TaskType:          "flight_planning",
		TaskID:            fmt.Sprintf("drone_%d", req.DroneID),
		CreatedAt:         time.Now(),
		Steps:             result.Reasoning,
		FinalDecision:     result.Explanation,
		OverallConfidence: calculateOverallConfidence(result.Reasoning),
	}

	// 验证规划结果
	if warnings := Validate(&req, planResult); len(warnings) > 0 {
		planResult.Warnings = append(planResult.Warnings, warnings...)
	}

	return planResult, reasoningChain, nil
}

// calculateOverallConfidence 计算总体置信度
func calculateOverallConfidence(steps []ReasoningStep) float64 {
	if len(steps) == 0 {
		return 0.0
	}

	var total float64
	for _, step := range steps {
		total += step.Confidence
	}
	return total / float64(len(steps))
}

// FormatReasoningForDisplay 格式化思维链用于显示
func (chain *ReasoningChain) FormatForDisplay() string {
	var result string
	result += fmt.Sprintf("思维链ID: %s\n", chain.ID)
	result += fmt.Sprintf("任务类型: %s\n", chain.TaskType)
	result += fmt.Sprintf("关联任务: %s\n", chain.TaskID)
	result += fmt.Sprintf("创建时间: %s\n", chain.CreatedAt.Format("2006-01-02 15:04:05"))
	result += fmt.Sprintf("总体置信度: %.1f%%\n\n", chain.OverallConfidence*100)

	result += "推理步骤:\n"
	for _, step := range chain.Steps {
		result += fmt.Sprintf("步骤%d [%s]:\n", step.Step, step.Type)
		result += fmt.Sprintf("  上下文: %s\n", step.Context)
		result += fmt.Sprintf("  问题: %s\n", step.Question)
		result += fmt.Sprintf("  思考: %s\n", step.Thought)
		result += fmt.Sprintf("  结论: %s\n", step.Answer)
		result += fmt.Sprintf("  置信度: %.1f%%\n\n", step.Confidence*100)
	}

	result += fmt.Sprintf("最终决策: %s\n", chain.FinalDecision)
	return result
}

// SaveReasoningChain 保存思维链到数据库（简化实现）
func SaveReasoningChain(chain *ReasoningChain) error {
	// 在实际实现中，这里会将思维链保存到数据库
	// 目前先返回nil，表示保存成功
	return nil
}
