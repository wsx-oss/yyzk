package cot

import (
	"fmt"
	"sync"
	"time"
)

type ReasoningStep struct {
	Step       int     `json:"step"`
	Type       string  `json:"type"`
	Context    string  `json:"context"`
	Question   string  `json:"question"`
	Thought    string  `json:"thought"`
	Answer     string  `json:"answer"`
	Confidence float64 `json:"confidence"`
}

type ReasoningChain struct {
	ID                string                 `json:"id"`
	TaskType          string                 `json:"task_type"`
	TaskID            string                 `json:"task_id"`
	CreatedAt         time.Time              `json:"created_at"`
	Steps             []ReasoningStep        `json:"steps"`
	FinalDecision     string                 `json:"final_decision"`
	OverallConfidence float64                `json:"overall_confidence"`
	Metadata          map[string]interface{} `json:"metadata"`
}

func (c *ReasoningChain) AddStep(stepType, context, question, thought, answer string, options []string, confidence float64) {
	next := len(c.Steps) + 1
	_ = options
	c.Steps = append(c.Steps, ReasoningStep{
		Step:       next,
		Type:       stepType,
		Context:    context,
		Question:   question,
		Thought:    thought,
		Answer:     answer,
		Confidence: confidence,
	})
	c.OverallConfidence = calculateOverallConfidence(c.Steps)
}

func (c *ReasoningChain) SetFinalDecision(decision string, confidence float64) {
	c.FinalDecision = decision
	c.OverallConfidence = confidence
}

func (c *ReasoningChain) FormatForDisplay() string {
	res := ""
	res += fmt.Sprintf("思维链ID: %s\n", c.ID)
	res += fmt.Sprintf("任务类型: %s\n", c.TaskType)
	res += fmt.Sprintf("关联任务: %s\n", c.TaskID)
	res += fmt.Sprintf("创建时间: %s\n", c.CreatedAt.Format("2006-01-02 15:04:05"))
	res += fmt.Sprintf("总体置信度: %.1f%%\n\n", c.OverallConfidence*100)
	res += "推理步骤:\n"
	for _, s := range c.Steps {
		res += fmt.Sprintf("步骤%d [%s]:\n", s.Step, s.Type)
		res += fmt.Sprintf("  上下文: %s\n", s.Context)
		res += fmt.Sprintf("  问题: %s\n", s.Question)
		res += fmt.Sprintf("  思考: %s\n", s.Thought)
		res += fmt.Sprintf("  结论: %s\n", s.Answer)
		res += fmt.Sprintf("  置信度: %.1f%%\n\n", s.Confidence*100)
	}
	res += fmt.Sprintf("最终决策: %s\n", c.FinalDecision)
	return res
}

func calculateOverallConfidence(steps []ReasoningStep) float64 {
	if len(steps) == 0 {
		return 0
	}
	var total float64
	for _, s := range steps {
		total += s.Confidence
	}
	return total / float64(len(steps))
}

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type DroneInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	BatteryLevel int      `json:"battery_level"`
	Location     Location `json:"location"`
	Capabilities []string `json:"capabilities"`
}

type TaskInfo struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Priority     int      `json:"priority"`
	Location     Location `json:"location"`
	Duration     int      `json:"duration"`
	Requirements []string `json:"requirements"`
}

type SchedulingConstraints struct {
	MaxConcurrentTasks int     `json:"max_concurrent_tasks"`
	BatteryThreshold   int     `json:"battery_threshold"`
	MaxDistance        float64 `json:"max_distance"`
	TimeWindow         struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"time_window"`
}

type SystemState struct {
	Time              time.Time `json:"time"`
	WeatherConditions string    `json:"weather_conditions"`
	SystemLoad        float64   `json:"system_load"`
}

type SchedulingContext struct {
	Drones       []DroneInfo             `json:"drones"`
	Tasks        []TaskInfo              `json:"tasks"`
	Constraints  SchedulingConstraints   `json:"constraints"`
	CurrentState SystemState             `json:"current_state"`
	Extra        map[string]interface{}  `json:"extra"`
}

type CoTManager struct {
	mu     sync.RWMutex
	chains map[string]*ReasoningChain
}

func NewCoTManager() *CoTManager {
	return &CoTManager{chains: make(map[string]*ReasoningChain)}
}

func (m *CoTManager) GetChain(id string) (*ReasoningChain, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.chains[id]
	return ch, ok
}

func (m *CoTManager) StartReasoning(taskType, taskID string) *ReasoningChain {
	chain := &ReasoningChain{
		ID:        fmt.Sprintf("cot_%d", time.Now().UnixNano()),
		TaskType:  taskType,
		TaskID:    taskID,
		CreatedAt: time.Now(),
		Metadata:  make(map[string]interface{}),
	}
	m.mu.Lock()
	m.chains[chain.ID] = chain
	m.mu.Unlock()
	return chain
}

func (m *CoTManager) GenerateSchedulingCoT(ctx SchedulingContext) *ReasoningChain {
	chain := m.StartReasoning("scheduling", "")
	chain.Metadata["input"] = ctx
	chain.AddStep("analysis", "调度任务", "当前有哪些无人机和任务？", "统计资源与需求", fmt.Sprintf("无人机数=%d，任务数=%d", len(ctx.Drones), len(ctx.Tasks)), nil, 0.85)
	chain.SetFinalDecision("已生成调度思维链（简化版）", 0.85)
	return chain
}

func (m *CoTManager) GenerateFaultDiagnosisCoT(droneID string, symptoms map[string]interface{}) *ReasoningChain {
	chain := m.StartReasoning("fault_diagnosis", droneID)
	chain.Metadata["symptoms"] = symptoms
	chain.AddStep("analysis", "故障诊断", "当前症状是什么？", "基于上报症状进行初步归类", "完成症状采集与归类（简化版）", nil, 0.8)
	chain.SetFinalDecision("建议进一步检查传感器/电源/通信链路（简化版）", 0.8)
	return chain
}

func (m *CoTManager) GeneratePathOptimizationCoT(missionID string, constraints map[string]interface{}) *ReasoningChain {
	chain := m.StartReasoning("path_optimization", missionID)
	chain.Metadata["constraints"] = constraints
	chain.AddStep("analysis", "路径优化", "有哪些约束条件？", "读取约束并构建优化目标", "已读取约束并生成优化思路（简化版）", nil, 0.82)
	chain.SetFinalDecision("建议使用最短路径 + 电量约束的综合目标进行优化（简化版）", 0.82)
	return chain
}
