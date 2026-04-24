package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"smartcontrol/internal/db"
	"smartcontrol/internal/llm"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AIAssistantAPI handles AI assistant chat endpoints
type AIAssistantAPI struct {
	db  *db.DB
	llm *llm.Client
}

// NewAIAssistantAPI creates a new AIAssistantAPI
func NewAIAssistantAPI(db *db.DB) *AIAssistantAPI {
	// Ensure the shared RAG engine is initialised
	InitSharedRAG(db)
	return &AIAssistantAPI{
		db:  db,
		llm: llm.NewClient(),
	}
}

// knowledgeBase returns the system prompt with contextual knowledge
func (a *AIAssistantAPI) knowledgeBase() string {
	return `你是 云翼智控 智能无人机管控平台的 AI 助手，名字叫"小云"。你的职责是帮助用户操作平台、查询数据、解答问题、分析告警和提供操作引导。

【平台功能模块】
— 无人机状态监控 —
1. 无人机管理 (drones) - 注册、编辑、删除无人机，查看在线/离线状态
2. GPS/位置信息 (gps) - 实时GPS追踪、地理围栏告警、历史轨迹
3. 视频监控 (video) - 无人机视频流查看
4. 远程桌面控制 (remote) - VNC/SSH/RDP远程连接无人机
5. 异常报警 (alerts) - 系统告警查看和处理
6. 电池监控 (battery) - 电池电量、温度、健康度监控
— 飞行任务管理 —
7. 禁飞区管理 (noflyzone) - 设置禁飞区域，支持多边形和圆形
8. 航线规划 (flight) - 航线规划、任务创建、AI智能规划航线
9. MAVLink 遥测 (mavlink) - 飞控遥测数据
10. MAVLink 调试 (mavlink-debug) - MAVLink协议调试
— 仿真与智能决策 —
11. 仿真模拟引擎 (simulation) - 批量模拟无人机创建、启停、异常注入、实时监控
12. CoT 智能决策 (cot) - AI决策推理过程展示
— 系统运维与日志 —
13. 并发任务监控 (concurrency) - 后台任务调度与监控
14. 备份与数据回滚 (backup) - 数据库备份与恢复
15. 数据同步状态 (sync) - 多节点数据同步
16. 系统状态监控 (monitor) - CPU/内存/磁盘实时监控
17. 性能分析报告 (performance) - 系统性能报告

【快捷指令】
- /status - 查看系统整体状态
- /drones - 查看无人机列表
- /alerts - 查看最近告警
- /battery - 查看电池状态
- /tasks - 查看飞行任务
- /help - 显示帮助信息

【页面跳转指令】
当用户需要跳转到某个页面时，请在回复中包含跳转指令标签：
- [NAV:drones] - 跳转到无人机管理页
- [NAV:gps] - 跳转到GPS页
- [NAV:video] - 跳转到视频监控页
- [NAV:remote] - 跳转到远程桌面页
- [NAV:alerts] - 跳转到告警页
- [NAV:battery] - 跳转到电池监控页
- [NAV:noflyzone] - 跳转到禁飞区页
- [NAV:flight] - 跳转到航线规划页
- [NAV:mavlink] - 跳转到MAVLink遥测页
- [NAV:simulation] - 跳转到仿真模拟页
- [NAV:cot] - 跳转到CoT决策页
- [NAV:concurrency] - 跳转到并发任务页
- [NAV:backup] - 跳转到备份页
- [NAV:sync] - 跳转到数据同步页
- [NAV:monitor] - 跳转到系统监控页
- [NAV:performance] - 跳转到性能分析页

【回复要求】
1. 使用中文回复，语言简洁友好
2. 当涉及具体数据查询时，使用提供的实时数据来回答
3. 当用户要求跳转时，在合适位置附加[NAV:xxx]标签
4. 对告警和异常要给出分析建议
5. 支持对电池状态、飞行规划、CoT决策结果的解读
6. 当提供了【知识库参考】内容时，优先参考知识库中的信息来回答用户问题，确保回答准确且有据可查
`
}

// gatherContext collects real-time platform data for AI context
func (a *AIAssistantAPI) gatherContext(query string) string {
	var parts []string
	lq := strings.ToLower(query)

	// Always provide basic stats
	var droneCnt, onlineCnt int
	a.db.QueryRow("SELECT COUNT(*) FROM drones").Scan(&droneCnt)
	a.db.QueryRow("SELECT COUNT(*) FROM drones WHERE status='online'").Scan(&onlineCnt)
	parts = append(parts, fmt.Sprintf("【无人机】共%d架，在线%d架", droneCnt, onlineCnt))

	// Alert stats
	var alertCnt, unresolved int
	a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&alertCnt)
	a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE acknowledged=0").Scan(&unresolved)
	parts = append(parts, fmt.Sprintf("【告警】共%d条，未处理%d条", alertCnt, unresolved))

	// Notification stats
	var notifUnread int
	a.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE is_read=0").Scan(&notifUnread)
	parts = append(parts, fmt.Sprintf("【通知】未读%d条", notifUnread))

	// Detailed data based on query topic
	if strings.Contains(lq, "电池") || strings.Contains(lq, "battery") || strings.Contains(lq, "充电") || strings.Contains(lq, "电量") {
		rows, err := a.db.Query("SELECT device_name, level, temperature, voltage, status FROM battery_records ORDER BY created_at DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var battInfo []string
			for rows.Next() {
				var name, status string
				var level int
				var temp, volt float64
				if rows.Scan(&name, &level, &temp, &volt, &status) == nil {
					battInfo = append(battInfo, fmt.Sprintf("%s: 电量%d%%, 温度%.1f°C, 电压%.2fV, 状态:%s", name, level, temp, volt, status))
				}
			}
			if len(battInfo) > 0 {
				parts = append(parts, "【电池详情】\n"+strings.Join(battInfo, "\n"))
			}
		}
	}

	if strings.Contains(lq, "告警") || strings.Contains(lq, "alert") || strings.Contains(lq, "异常") || strings.Contains(lq, "报警") {
		rows, err := a.db.Query("SELECT category, severity, message, created_at FROM alerts ORDER BY created_at DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var alertInfo []string
			for rows.Next() {
				var cat, sev, msg string
				var createdAt sql.NullString
				if rows.Scan(&cat, &sev, &msg, &createdAt) == nil {
					alertInfo = append(alertInfo, fmt.Sprintf("[%s/%s] %s (%s)", cat, sev, msg, createdAt.String))
				}
			}
			if len(alertInfo) > 0 {
				parts = append(parts, "【最近告警】\n"+strings.Join(alertInfo, "\n"))
			}
		}
	}

	if strings.Contains(lq, "任务") || strings.Contains(lq, "flight") || strings.Contains(lq, "飞行") || strings.Contains(lq, "航线") {
		rows, err := a.db.Query("SELECT name, status, current_phase, progress FROM flight_missions ORDER BY created_at DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var missionInfo []string
			for rows.Next() {
				var name, status, phase string
				var progress int
				if rows.Scan(&name, &status, &phase, &progress) == nil {
					missionInfo = append(missionInfo, fmt.Sprintf("%s: %s, 阶段:%s, 进度:%d%%", name, status, phase, progress))
				}
			}
			if len(missionInfo) > 0 {
				parts = append(parts, "【飞行任务】\n"+strings.Join(missionInfo, "\n"))
			}
		}
	}

	if strings.Contains(lq, "无人机") || strings.Contains(lq, "drone") || strings.Contains(lq, "飞机") {
		rows, err := a.db.Query("SELECT name, model, status, agent_id FROM drones ORDER BY created_at DESC LIMIT 10")
		if err == nil {
			defer rows.Close()
			var droneInfo []string
			for rows.Next() {
				var name, model, status, agentID string
				if rows.Scan(&name, &model, &status, &agentID) == nil {
					droneInfo = append(droneInfo, fmt.Sprintf("%s (型号:%s) - %s", name, model, status))
				}
			}
			if len(droneInfo) > 0 {
				parts = append(parts, "【无人机列表】\n"+strings.Join(droneInfo, "\n"))
			}
		}
	}

	if strings.Contains(lq, "日志") || strings.Contains(lq, "log") {
		rows, err := a.db.Query("SELECT level, message, created_at FROM logs ORDER BY created_at DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var logInfo []string
			for rows.Next() {
				var level, msg string
				var createdAt sql.NullString
				if rows.Scan(&level, &msg, &createdAt) == nil {
					logInfo = append(logInfo, fmt.Sprintf("[%s] %s (%s)", level, msg, createdAt.String))
				}
			}
			if len(logInfo) > 0 {
				parts = append(parts, "【最近日志】\n"+strings.Join(logInfo, "\n"))
			}
		}
	}

	if strings.Contains(lq, "硬件") || strings.Contains(lq, "hardware") || strings.Contains(lq, "服务器") {
		rows, err := a.db.Query("SELECT name, type, status, cpu_usage, mem_usage, temperature FROM hardware_items ORDER BY detected_at DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var hwInfo []string
			for rows.Next() {
				var name, typ, status string
				var cpu, mem, temp float64
				if rows.Scan(&name, &typ, &status, &cpu, &mem, &temp) == nil {
					hwInfo = append(hwInfo, fmt.Sprintf("%s(%s): %s, CPU:%.1f%%, 内存:%.1f%%, 温度:%.1f°C", name, typ, status, cpu, mem, temp))
				}
			}
			if len(hwInfo) > 0 {
				parts = append(parts, "【硬件状态】\n"+strings.Join(hwInfo, "\n"))
			}
		}
	}

	// Simulation data context
	if strings.Contains(lq, "仿真") || strings.Contains(lq, "模拟") || strings.Contains(lq, "sim") || strings.Contains(lq, "训练") || strings.Contains(lq, "rl") || strings.Contains(lq, "强化学习") {
		if SimEngineRef != nil {
			m := SimEngineRef.Metrics()
			parts = append(parts, fmt.Sprintf("【仿真引擎】总实例:%d, 运行中:%d, 已停止:%d, 故障:%d, 批次数:%d, 运行时间:%.0f秒",
				m.TotalInstances, m.RunningInstances, m.StoppedInstances, m.FailedInstances, m.TotalBatches, m.UptimeSec))

			// Recent sim events
			rows, err := a.db.Query("SELECT instance_id, event_type, level, message, created_at FROM sim_events ORDER BY datetime(created_at) DESC LIMIT 5")
			if err == nil {
				defer rows.Close()
				var evtInfo []string
				for rows.Next() {
					var instID, evtType, level, msg, created string
					if rows.Scan(&instID, &evtType, &level, &msg, &created) == nil {
						evtInfo = append(evtInfo, fmt.Sprintf("[%s] %s: %s (%s)", level, instID, msg, created))
					}
				}
				if len(evtInfo) > 0 {
					parts = append(parts, "【仿真事件】\n"+strings.Join(evtInfo, "\n"))
				}
			}
		}
		if SimTrainerRef != nil {
			metrics := SimTrainerRef.TrainingMetrics()
			parts = append(parts, fmt.Sprintf("【RL训练】训练中:%v, 训练轮次:%v, 平均奖励:%.3f, 最佳奖励:%.3f",
				metrics["training"], metrics["episodes"], metrics["avg_reward"], metrics["best_reward"]))
		}
	}

	if strings.Contains(lq, "gps") || strings.Contains(lq, "位置") || strings.Contains(lq, "定位") {
		rows, err := a.db.Query("SELECT name, latitude, longitude, altitude, speed, status FROM gps_devices ORDER BY last_update DESC LIMIT 5")
		if err == nil {
			defer rows.Close()
			var gpsInfo []string
			for rows.Next() {
				var name, status string
				var lat, lng, alt, speed float64
				if rows.Scan(&name, &lat, &lng, &alt, &speed, &status) == nil {
					gpsInfo = append(gpsInfo, fmt.Sprintf("%s: (%.6f,%.6f) 高度:%.1fm 速度:%.1fm/s %s", name, lat, lng, alt, speed, status))
				}
			}
			if len(gpsInfo) > 0 {
				parts = append(parts, "【GPS设备】\n"+strings.Join(gpsInfo, "\n"))
			}
		}
	}

	return strings.Join(parts, "\n\n")
}

// handleBuiltinCommand handles quick commands like /status, /drones, etc.
func (a *AIAssistantAPI) handleBuiltinCommand(cmd string) (string, bool) {
	switch cmd {
	case "/help":
		return `🤖 我是小云，云翼智控 平台 AI 助手，可以帮你：

**快捷指令：**
- /status - 查看系统整体状态
- /drones - 查看无人机列表
- /alerts - 查看最近告警
- /battery - 查看电池状态
- /tasks - 查看飞行任务
- /help - 显示本帮助

**我能做的：**
- 📊 查询平台各模块数据
- 🔍 分析告警和异常
- 🔋 解读电池状态
- ✈️ 解释飞行规划
- 🧠 解读 CoT 决策结果
- 🗺️ 操作引导与页面跳转
- ❓ 回答任何平台相关问题

直接用自然语言问我就好！`, true

	case "/status":
		return a.getSystemStatus(), true

	case "/drones":
		return a.getDronesSummary(), true

	case "/alerts":
		return a.getAlertsSummary(), true

	case "/battery":
		return a.getBatterySummary(), true

	case "/tasks":
		return a.getTasksSummary(), true
	}
	return "", false
}

func (a *AIAssistantAPI) getSystemStatus() string {
	var droneCnt, droneOnline int
	a.db.QueryRow("SELECT COUNT(*) FROM drones").Scan(&droneCnt)
	a.db.QueryRow("SELECT COUNT(*) FROM drones WHERE status='online'").Scan(&droneOnline)

	var alertCnt, alertUnack int
	a.db.QueryRow("SELECT COUNT(*) FROM alerts").Scan(&alertCnt)
	a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE acknowledged=0").Scan(&alertUnack)

	var missionCnt, missionActive int
	a.db.QueryRow("SELECT COUNT(*) FROM flight_missions").Scan(&missionCnt)
	a.db.QueryRow("SELECT COUNT(*) FROM flight_missions WHERE status NOT IN ('已完成','已取消')").Scan(&missionActive)

	var notifUnread int
	a.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE is_read=0").Scan(&notifUnread)

	var hwOnline, hwTotal int
	a.db.QueryRow("SELECT COUNT(*) FROM hardware_items").Scan(&hwTotal)
	a.db.QueryRow("SELECT COUNT(*) FROM hardware_items WHERE status='在线'").Scan(&hwOnline)

	return fmt.Sprintf(`📊 **系统状态总览**

🚁 无人机：共 %d 架，在线 %d 架
⚠️ 告警：共 %d 条，未处理 %d 条
✈️ 飞行任务：共 %d 个，进行中 %d 个
🔔 通知：未读 %d 条
🖥️ 硬件设备：共 %d 台，在线 %d 台

当前时间：%s`, droneCnt, droneOnline, alertCnt, alertUnack, missionCnt, missionActive, notifUnread, hwTotal, hwOnline, time.Now().Format("2006-01-02 15:04:05"))
}

func (a *AIAssistantAPI) getDronesSummary() string {
	rows, err := a.db.Query("SELECT name, model, status FROM drones ORDER BY created_at DESC LIMIT 10")
	if err != nil {
		return "❌ 查询无人机数据失败"
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, model, status string
		if rows.Scan(&name, &model, &status) == nil {
			icon := "🔴"
			if status == "online" {
				icon = "🟢"
			}
			lines = append(lines, fmt.Sprintf("%s %s (%s) - %s", icon, name, model, status))
		}
	}
	if len(lines) == 0 {
		return "📭 暂无注册的无人机\n\n前往无人机管理页添加 [NAV:drones]"
	}
	return "🚁 **无人机列表**\n\n" + strings.Join(lines, "\n") + "\n\n查看详情请前往无人机管理页 [NAV:drones]"
}

func (a *AIAssistantAPI) getAlertsSummary() string {
	rows, err := a.db.Query("SELECT category, severity, message, created_at FROM alerts WHERE acknowledged=0 ORDER BY created_at DESC LIMIT 10")
	if err != nil {
		return "❌ 查询告警数据失败"
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var cat, sev, msg string
		var createdAt sql.NullString
		if rows.Scan(&cat, &sev, &msg, &createdAt) == nil {
			icon := "⚠️"
			if sev == "critical" {
				icon = "🔴"
			}
			lines = append(lines, fmt.Sprintf("%s [%s] %s - %s", icon, cat, msg, createdAt.String))
		}
	}
	if len(lines) == 0 {
		return "✅ 当前没有未处理的告警"
	}
	return "⚠️ **未处理告警**\n\n" + strings.Join(lines, "\n") + "\n\n查看详情请前往告警页 [NAV:alerts]"
}

func (a *AIAssistantAPI) getBatterySummary() string {
	rows, err := a.db.Query(`SELECT br.device_name, br.level, br.temperature, br.voltage, br.status
		FROM battery_records br
		INNER JOIN (SELECT device_id, MAX(created_at) as max_time FROM battery_records GROUP BY device_id) latest
		ON br.device_id = latest.device_id AND br.created_at = latest.max_time
		ORDER BY br.level ASC LIMIT 10`)
	if err != nil {
		return "❌ 查询电池数据失败"
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, status string
		var level int
		var temp, volt float64
		if rows.Scan(&name, &level, &temp, &volt, &status) == nil {
			icon := "🟢"
			if level < 20 {
				icon = "🔴"
			} else if level < 50 {
				icon = "🟡"
			}
			lines = append(lines, fmt.Sprintf("%s %s：电量 %d%%，温度 %.1f°C，电压 %.2fV，%s", icon, name, level, temp, volt, status))
		}
	}
	if len(lines) == 0 {
		return "📭 暂无电池数据\n\n前往电池监控页查看 [NAV:battery]"
	}
	return "🔋 **电池状态**\n\n" + strings.Join(lines, "\n") + "\n\n查看详情请前往电池监控页 [NAV:battery]"
}

func (a *AIAssistantAPI) getTasksSummary() string {
	rows, err := a.db.Query("SELECT name, status, current_phase, progress FROM flight_missions ORDER BY created_at DESC LIMIT 10")
	if err != nil {
		return "❌ 查询任务数据失败"
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var name, status, phase string
		var progress int
		if rows.Scan(&name, &status, &phase, &progress) == nil {
			icon := "📋"
			if status == "飞行中" {
				icon = "✈️"
			} else if status == "已完成" {
				icon = "✅"
			}
			lines = append(lines, fmt.Sprintf("%s %s：%s / %s / 进度 %d%%", icon, name, status, phase, progress))
		}
	}
	if len(lines) == 0 {
		return "📭 暂无飞行任务\n\n前往航线规划页创建 [NAV:flight]"
	}
	return "✈️ **飞行任务**\n\n" + strings.Join(lines, "\n") + "\n\n查看详情请前往航线规划页 [NAV:flight]"
}

// Chat handles AI chat requests
func (a *AIAssistantAPI) Chat(c *gin.Context) {
	var req struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		c.JSON(400, gin.H{"error": "message is required"})
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}

	msg := strings.TrimSpace(req.Message)

	// Save user message
	a.db.Exec("INSERT INTO ai_chat_messages(session_id, role, content) VALUES(?,?,?)", req.SessionID, "user", msg)

	// Check for quick commands
	if resp, ok := a.handleBuiltinCommand(msg); ok {
		a.db.Exec("INSERT INTO ai_chat_messages(session_id, role, content) VALUES(?,?,?)", req.SessionID, "assistant", resp)
		c.JSON(200, gin.H{"reply": resp, "type": "command"})
		return
	}

	// Gather context
	context := a.gatherContext(msg)

	// RAG: retrieve relevant knowledge base chunks
	ragContext := RAGRetrieveContext(msg, 3)

	// Build the user prompt with context + RAG knowledge
	var promptParts []string
	promptParts = append(promptParts, fmt.Sprintf("【平台实时数据】\n%s", context))
	if ragContext != "" {
		promptParts = append(promptParts, ragContext)
	}
	promptParts = append(promptParts, fmt.Sprintf("【用户问题】\n%s", msg))
	userPrompt := strings.Join(promptParts, "\n\n")

	// Get recent conversation history for continuity (last 6 messages)
	var history []map[string]string
	rows, err := a.db.Query(
		"SELECT role, content FROM ai_chat_messages WHERE session_id=? ORDER BY created_at DESC LIMIT 6",
		req.SessionID,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var role, content string
			if rows.Scan(&role, &content) == nil {
				history = append([]map[string]string{{"role": role, "content": content}}, history...)
			}
		}
	}

	// If LLM is available, use it; otherwise fallback to local logic
	var reply string
	if a.llm.Available() {
		llmReply, err := a.llm.CallRaw(a.knowledgeBase(), userPrompt)
		if err != nil {
			log.Printf("[AI Assistant] LLM call failed: %v", err)
			reply = a.fallbackReply(msg, context)
		} else {
			reply = llmReply
		}
	} else {
		reply = a.fallbackReply(msg, context)
	}

	// Save assistant reply
	a.db.Exec("INSERT INTO ai_chat_messages(session_id, role, content) VALUES(?,?,?)", req.SessionID, "assistant", reply)

	c.JSON(200, gin.H{"reply": reply, "type": "ai"})
}

// fallbackReply generates a response without LLM
func (a *AIAssistantAPI) fallbackReply(query, context string) string {
	lq := strings.ToLower(query)

	if strings.Contains(lq, "状态") || strings.Contains(lq, "status") {
		return a.getSystemStatus()
	}
	if strings.Contains(lq, "无人机") || strings.Contains(lq, "drone") {
		return a.getDronesSummary()
	}
	if strings.Contains(lq, "告警") || strings.Contains(lq, "alert") || strings.Contains(lq, "异常") {
		return a.getAlertsSummary()
	}
	if strings.Contains(lq, "电池") || strings.Contains(lq, "battery") {
		return a.getBatterySummary()
	}
	if strings.Contains(lq, "任务") || strings.Contains(lq, "flight") || strings.Contains(lq, "飞行") {
		return a.getTasksSummary()
	}
	if strings.Contains(lq, "帮助") || strings.Contains(lq, "help") {
		r, _ := a.handleBuiltinCommand("/help")
		return r
	}

	// Navigation hints
	navMap := map[string]string{
		"监控": "monitor",
		"同步": "sync",
		"视频": "video", "远程": "remote",
		"gps": "gps", "位置": "gps",
		"禁飞": "noflyzone",
		"性能": "performance", "cot": "cot", "决策": "cot",
		"备份": "backup", "并发": "concurrency",
		"mavlink": "mavlink", "遥测": "mavlink",
	}
	for keyword, page := range navMap {
		if strings.Contains(lq, keyword) {
			return fmt.Sprintf("🔍 已为您找到相关模块，点击跳转查看 [NAV:%s]\n\n当前数据概况：\n%s", page, context)
		}
	}

	return fmt.Sprintf("🤖 收到！根据当前系统数据：\n\n%s\n\n如需更详细的分析，请告诉我具体想了解的内容。输入 /help 查看所有可用指令。", context)
}

// ChatHistory returns chat history for a session
func (a *AIAssistantAPI) ChatHistory(c *gin.Context) {
	sessionID := c.DefaultQuery("session_id", "default")
	limitStr := c.DefaultQuery("limit", "50")
	limit := 50
	if n, err := fmt.Sscanf(limitStr, "%d", &limit); n == 0 || err != nil {
		limit = 50
	}

	rows, err := a.db.Query(
		"SELECT id, role, content, created_at FROM ai_chat_messages WHERE session_id=? ORDER BY created_at ASC LIMIT ?",
		sessionID, limit,
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var items []gin.H
	for rows.Next() {
		var id int
		var role, content string
		var createdAt sql.NullString
		if rows.Scan(&id, &role, &content, &createdAt) == nil {
			items = append(items, gin.H{
				"id":         id,
				"role":       role,
				"content":    content,
				"created_at": createdAt.String,
			})
		}
	}
	if items == nil {
		items = []gin.H{}
	}
	c.JSON(200, gin.H{"items": items})
}

// ChatClear clears chat history for a session
func (a *AIAssistantAPI) ChatClear(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := c.BindJSON(&req); err != nil || req.SessionID == "" {
		req.SessionID = "default"
	}
	_, err := a.db.Exec("DELETE FROM ai_chat_messages WHERE session_id=?", req.SessionID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// DataQuery handles structured data queries from the AI assistant
func (a *AIAssistantAPI) DataQuery(c *gin.Context) {
	var req struct {
		QueryType string `json:"query_type"`
		Params    gin.H  `json:"params"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}

	var result interface{}
	var err error

	switch req.QueryType {
	case "drone_count":
		var cnt int
		err = a.db.QueryRow("SELECT COUNT(*) FROM drones").Scan(&cnt)
		result = gin.H{"count": cnt}
	case "online_drones":
		var cnt int
		err = a.db.QueryRow("SELECT COUNT(*) FROM drones WHERE status='online'").Scan(&cnt)
		result = gin.H{"count": cnt}
	case "alert_count":
		var cnt int
		err = a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE acknowledged=0").Scan(&cnt)
		result = gin.H{"count": cnt}
	case "battery_low":
		var cnt int
		err = a.db.QueryRow("SELECT COUNT(DISTINCT device_id) FROM battery_records WHERE level < 20").Scan(&cnt)
		result = gin.H{"count": cnt}
	case "active_missions":
		var cnt int
		err = a.db.QueryRow("SELECT COUNT(*) FROM flight_missions WHERE status NOT IN ('已完成','已取消')").Scan(&cnt)
		result = gin.H{"count": cnt}
	default:
		c.JSON(400, gin.H{"error": "unknown query type"})
		return
	}

	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": result})
}

// Suggest returns quick action suggestions based on current system state
func (a *AIAssistantAPI) Suggest(c *gin.Context) {
	suggestions := []gin.H{}

	// Check unread alerts
	var alertCnt int
	a.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE acknowledged=0").Scan(&alertCnt)
	if alertCnt > 0 {
		suggestions = append(suggestions, gin.H{
			"text":   fmt.Sprintf("⚠️ 有 %d 条未处理告警", alertCnt),
			"action": "查看告警详情",
			"nav":    "alerts",
		})
	}

	// Check low battery
	var lowBatt int
	a.db.QueryRow(`SELECT COUNT(DISTINCT device_id) FROM battery_records 
		WHERE level < 20 AND created_at > datetime('now', '-10 minutes')`).Scan(&lowBatt)
	if lowBatt > 0 {
		suggestions = append(suggestions, gin.H{
			"text":   fmt.Sprintf("🔋 %d 台设备电量低于20%%", lowBatt),
			"action": "查看电池详情",
			"nav":    "battery",
		})
	}

	// Check offline drones
	var offlineDrones int
	a.db.QueryRow("SELECT COUNT(*) FROM drones WHERE status='offline'").Scan(&offlineDrones)
	if offlineDrones > 0 {
		suggestions = append(suggestions, gin.H{
			"text":   fmt.Sprintf("🔴 %d 架无人机离线", offlineDrones),
			"action": "查看无人机状态",
			"nav":    "drones",
		})
	}

	// Check active missions
	var activeMissions int
	a.db.QueryRow("SELECT COUNT(*) FROM flight_missions WHERE status='飞行中'").Scan(&activeMissions)
	if activeMissions > 0 {
		suggestions = append(suggestions, gin.H{
			"text":   fmt.Sprintf("✈️ %d 个任务正在执行", activeMissions),
			"action": "查看任务进度",
			"nav":    "flight",
		})
	}

	// Unread notifications
	var unreadNotif int
	a.db.QueryRow("SELECT COUNT(*) FROM notifications WHERE is_read=0").Scan(&unreadNotif)
	if unreadNotif > 0 {
		suggestions = append(suggestions, gin.H{
			"text":   fmt.Sprintf("🔔 有 %d 条未读通知", unreadNotif),
			"action": "查看通知中心",
			"nav":    "",
		})
	}

	c.JSON(200, gin.H{"suggestions": suggestions})
}

// CoTExplain calls the LLM to explain a CoT reasoning chain in plain language
func (a *AIAssistantAPI) CoTExplain(c *gin.Context) {
	chainID := c.Param("id")
	var stepsJSON, finalDecision string
	var taskType, taskID string
	err := a.db.QueryRow(
		"SELECT task_type, task_id, steps, final_decision FROM cot_chains WHERE id=?", chainID,
	).Scan(&taskType, &taskID, &stepsJSON, &finalDecision)
	if err != nil {
		c.JSON(404, gin.H{"error": "CoT chain not found"})
		return
	}

	var steps []json.RawMessage
	json.Unmarshal([]byte(stepsJSON), &steps)

	prompt := fmt.Sprintf(`请用简明的中文解释以下 CoT (思维链) 决策过程：
任务类型: %s
任务ID: %s
推理步骤: %s
最终决策: %s

请逐步解释每个推理步骤的含义，以及为什么做出最终决策。`, taskType, taskID, stepsJSON, finalDecision)

	if a.llm.Available() {
		reply, err := a.llm.CallRaw("你是一个AI决策解释专家，用简明中文解释CoT推理过程。", prompt)
		if err != nil {
			c.JSON(200, gin.H{"explanation": fmt.Sprintf("CoT决策 [%s]\n任务: %s\n最终决策: %s\n\n（LLM暂不可用，无法生成详细解释）", taskType, taskID, finalDecision)})
			return
		}
		c.JSON(200, gin.H{"explanation": reply})
		return
	}

	c.JSON(200, gin.H{"explanation": fmt.Sprintf("CoT决策 [%s]\n任务: %s\n最终决策: %s\n\n包含 %d 个推理步骤。配置 LLM_API_KEY 后可获得详细解释。", taskType, taskID, finalDecision, len(steps))})
}

// RAGSearch exposes the RAG knowledge base retrieval as an API endpoint.
// Users or the frontend can query the knowledge base directly.
func (a *AIAssistantAPI) RAGSearch(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(400, gin.H{"error": "query parameter 'q' is required"})
		return
	}
	topK := 5
	if SharedRAG == nil || SharedRAG.ChunkCount() == 0 {
		c.JSON(200, gin.H{"chunks": []gin.H{}, "total_chunks": 0})
		return
	}
	chunks := SharedRAG.Retrieve(query, topK)
	items := make([]gin.H, 0, len(chunks))
	for _, ch := range chunks {
		items = append(items, gin.H{
			"source":  ch.Source,
			"section": ch.Section,
			"content": ch.Content,
			"score":   ch.Score,
		})
	}
	c.JSON(200, gin.H{"chunks": items, "total_chunks": SharedRAG.ChunkCount()})
}

// RAGStats returns basic statistics about the RAG knowledge base.
func (a *AIAssistantAPI) RAGStats(c *gin.Context) {
	count := 0
	if SharedRAG != nil {
		count = SharedRAG.ChunkCount()
	}
	c.JSON(200, gin.H{
		"chunk_count": count,
		"enabled":     count > 0,
	})
}

// RegisterAIAssistantRoutes registers all AI assistant routes
func RegisterAIAssistantRoutes(r *gin.Engine, db *db.DB) {
	api := NewAIAssistantAPI(db)
	g := r.Group("/api/ai")
	{
		g.POST("/chat", api.Chat)
		g.GET("/history", api.ChatHistory)
		g.POST("/clear", api.ChatClear)
		g.POST("/query", api.DataQuery)
		g.GET("/suggest", api.Suggest)
		g.GET("/cot-explain/:id", api.CoTExplain)
		g.GET("/rag/search", api.RAGSearch)
		g.GET("/rag/stats", api.RAGStats)
	}
}
