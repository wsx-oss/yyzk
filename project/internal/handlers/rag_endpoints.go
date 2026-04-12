package handlers

import (
	"fmt"
	"strings"

	"smartcontrol/internal/db"
	"smartcontrol/internal/llm"

	"github.com/gin-gonic/gin"
)

// ==================== RAG-Enhanced Alert Analysis (Integration Point 3) ====================

// AlertAnalyze provides RAG-enhanced intelligent analysis for a given alert.
// POST /api/alerts/analyze
func AlertAnalyzeHandler(database *db.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			AlertID     int    `json:"alert_id"`
			Category    string `json:"category"`
			Severity    string `json:"severity"`
			Message     string `json:"message"`
			Description string `json:"description"`
		}
		if err := c.BindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求"})
			return
		}

		// If alert_id is provided, load alert from DB
		if req.AlertID > 0 && req.Message == "" {
			database.QueryRow(
				`SELECT category, severity, message FROM alerts WHERE id = ?`, req.AlertID,
			).Scan(&req.Category, &req.Severity, &req.Message)
		}
		if req.Message == "" {
			c.JSON(400, gin.H{"error": "缺少告警信息"})
			return
		}

		// RAG: retrieve relevant knowledge for this alert type
		ragQuery := fmt.Sprintf("%s %s %s 告警 处置 应急", req.Category, req.Severity, req.Message)
		ragContext := RAGRetrieveContext(ragQuery, 3)

		// RL: get policy hints for anomaly handling
		bridge := NewRLPolicyBridge()
		rlHints := bridge.GenerateFlightHints()

		// Build analysis prompt
		var promptParts []string
		promptParts = append(promptParts, fmt.Sprintf("告警类别: %s", req.Category))
		promptParts = append(promptParts, fmt.Sprintf("严重程度: %s", req.Severity))
		promptParts = append(promptParts, fmt.Sprintf("告警内容: %s", req.Message))
		if req.Description != "" {
			promptParts = append(promptParts, fmt.Sprintf("补充描述: %s", req.Description))
		}
		if ragContext != "" {
			promptParts = append(promptParts, ragContext)
		}
		if rlHints != "" {
			promptParts = append(promptParts, rlHints)
		}

		// Gather quick system stats
		var onlineDrones, activeAlerts int
		database.QueryRow(`SELECT COUNT(*) FROM drones WHERE status='online'`).Scan(&onlineDrones)
		database.QueryRow(`SELECT COUNT(*) FROM alerts WHERE acknowledged=0`).Scan(&activeAlerts)
		promptParts = append(promptParts, fmt.Sprintf("当前在线无人机: %d, 未处理告警: %d", onlineDrones, activeAlerts))

		userPrompt := strings.Join(promptParts, "\n\n")
		systemPrompt := `你是无人机智能管控系统的告警分析专家。请对以下告警进行专业分析，输出包含：
1. 告警原因分析
2. 风险等级评估（低/中/高/极高）
3. 建议处置措施（立即执行 + 后续跟踪）
4. 预防建议
请参考提供的知识库和RL策略建议来给出准确的分析。使用中文回复，简洁专业。`

		client := llm.NewClient()
		var analysis string
		if client.Available() {
			resp, err := client.CallRaw(systemPrompt, userPrompt)
			if err == nil {
				analysis = resp
			} else {
				analysis = buildFallbackAlertAnalysis(req.Category, req.Severity, req.Message)
			}
		} else {
			analysis = buildFallbackAlertAnalysis(req.Category, req.Severity, req.Message)
		}

		c.JSON(200, gin.H{
			"ok":           true,
			"analysis":     analysis,
			"rag_enhanced": ragContext != "",
			"rl_enhanced":  rlHints != "",
		})
	}
}

func buildFallbackAlertAnalysis(category, severity, message string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 告警分析 [%s/%s]\n\n", category, severity))
	sb.WriteString(fmt.Sprintf("**告警内容**: %s\n\n", message))
	sb.WriteString("**处置建议**: ")
	switch severity {
	case "critical":
		sb.WriteString("⚠️ 严重告警，建议立即检查相关无人机状态，必要时执行紧急降落。\n")
	case "warning":
		sb.WriteString("⚡ 警告级别，建议密切关注并准备备选方案。\n")
	default:
		sb.WriteString("ℹ️ 提示级别，建议记录并在下次维护时处理。\n")
	}
	sb.WriteString("\n（LLM未配置，使用基础分析。配置LLM API后可获得更详细的智能分析。）")
	return sb.String()
}

// ==================== RAG-Enhanced Simulation Explanation (Integration Point 4) ====================

// SimExplainHandler provides RAG-enhanced explanation for RL training results.
// POST /api/sim/rl/explain
func SimExplainHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			StateKey string `json:"state_key"` // optional: specific state to explain
			Question string `json:"question"`  // optional: user question about RL
			Scenario string `json:"scenario"`  // optional: low_battery, collision, etc.
		}
		if err := c.BindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求"})
			return
		}

		// RAG: retrieve relevant RL/simulation knowledge
		ragQuery := "强化学习 策略 无人机 训练"
		if req.Question != "" {
			ragQuery = req.Question
		} else if req.Scenario != "" {
			ragQuery = fmt.Sprintf("强化学习 %s 策略 动作", req.Scenario)
		}
		ragContext := RAGRetrieveContext(ragQuery, 3)

		// RL: get policy statistics
		bridge := NewRLPolicyBridge()
		rlHints := bridge.GenerateFlightHints()

		// Build prompt
		var promptParts []string
		promptParts = append(promptParts, "请解释本平台的强化学习训练策略和结果。")
		if req.Question != "" {
			promptParts = append(promptParts, fmt.Sprintf("用户问题: %s", req.Question))
		}
		if req.Scenario != "" {
			promptParts = append(promptParts, fmt.Sprintf("关注场景: %s", req.Scenario))
		}
		if req.StateKey != "" {
			promptParts = append(promptParts, fmt.Sprintf("状态键: %s", req.StateKey))
		}
		if ragContext != "" {
			promptParts = append(promptParts, ragContext)
		}
		if rlHints != "" {
			promptParts = append(promptParts, rlHints)
		}

		userPrompt := strings.Join(promptParts, "\n\n")
		systemPrompt := `你是无人机强化学习系统的专家。请根据提供的RL策略数据和知识库信息，用通俗易懂的中文解释：
1. 当前RL策略学到了什么行为模式
2. 在特定场景下智能体偏好什么动作，为什么
3. 这些策略如何应用到真实无人机决策中
4. 策略的优势和需要改进的地方
请结合知识库中的专业知识给出解释。`

		client := llm.NewClient()
		var explanation string
		if client.Available() {
			resp, err := client.CallRaw(systemPrompt, userPrompt)
			if err == nil {
				explanation = resp
			} else {
				explanation = buildFallbackRLExplanation(rlHints)
			}
		} else {
			explanation = buildFallbackRLExplanation(rlHints)
		}

		c.JSON(200, gin.H{
			"ok":               true,
			"explanation":      explanation,
			"rag_enhanced":     ragContext != "",
			"rl_enhanced":      rlHints != "",
			"policy_available": bridge.Available(),
		})
	}
}

func buildFallbackRLExplanation(rlHints string) string {
	var sb strings.Builder
	sb.WriteString("## RL 策略解释\n\n")
	if rlHints != "" {
		sb.WriteString("基于当前训练好的Q-Table策略，各场景下的推荐动作如下：\n\n")
		sb.WriteString(rlHints)
	} else {
		sb.WriteString("当前暂无训练好的RL策略数据。请先启动仿真训练以生成策略。\n")
	}
	sb.WriteString("\n\n（LLM未配置，使用基础解释。配置LLM API后可获得更详细的策略解读。）")
	return sb.String()
}

// RegisterRAGEndpoints registers all RAG-enhanced endpoints across modules.
func RegisterRAGEndpoints(r *gin.Engine, database *db.DB) {
	// Ensure shared RAG is initialised
	InitSharedRAG(database)

	// Alert analysis with RAG+RL
	r.POST("/api/alerts/analyze", AlertAnalyzeHandler(database))

	// Simulation RL explanation with RAG
	r.POST("/api/sim/rl/explain", SimExplainHandler())
}
