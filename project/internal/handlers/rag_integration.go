package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"smartcontrol/internal/db"
	"smartcontrol/internal/rag"
)

// SharedRAG holds a reference to the global RAG engine so all handlers can use it.
var SharedRAG *rag.Engine

// sharedDB stores a reference to the database for data_store operations.
var sharedDB *db.DB

// InitSharedRAG initialises the shared RAG engine by loading knowledge documents
// from the database (knowledge_docs table). On first run, it seeds the table from
// the local knowledge_base/ directory, then loads from DB.
func InitSharedRAG(database *db.DB) {
	if SharedRAG != nil {
		return
	}
	sharedDB = database
	SharedRAG = rag.New()

	// Try loading from DB
	docs := loadKnowledgeDocsFromDB(database)
	if len(docs) == 0 {
		// First run: seed from filesystem
		docs = seedKnowledgeDocsToDB(database)
	}
	if len(docs) > 0 {
		SharedRAG.LoadTexts(docs)
		log.Printf("[RAG] shared engine loaded %d chunks from database (%d docs)", SharedRAG.ChunkCount(), len(docs))
		return
	}
	log.Println("[RAG] shared engine: no knowledge base documents found (non-fatal)")
}

// loadKnowledgeDocsFromDB reads all documents from the knowledge_docs table.
func loadKnowledgeDocsFromDB(database *db.DB) map[string]string {
	rows, err := database.Query(`SELECT name, content FROM knowledge_docs`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	docs := make(map[string]string)
	for rows.Next() {
		var name, content string
		if rows.Scan(&name, &content) == nil && content != "" {
			docs[name] = content
		}
	}
	return docs
}

// seedKnowledgeDocsToDB reads .md files from the knowledge_base/ directory and
// inserts them into the knowledge_docs table. Returns the loaded docs map.
func seedKnowledgeDocsToDB(database *db.DB) map[string]string {
	candidates := []string{
		"knowledge_base",
		"project/knowledge_base",
		filepath.Join("..", "knowledge_base"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "knowledge_base"))
	}
	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		docs := make(map[string]string)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil || len(data) < 10 {
				continue
			}
			name := entry.Name()
			content := string(data)
			database.Exec(`INSERT OR REPLACE INTO knowledge_docs(name, content, updated_at) VALUES(?,?,datetime('now'))`, name, content)
			docs[name] = content
		}
		if len(docs) > 0 {
			log.Printf("[RAG] seeded %d knowledge docs from %s into database", len(docs), dir)
			return docs
		}
	}
	return nil
}

// RAGRetrieveContext is a helper that queries the shared RAG engine and returns
// a formatted context string suitable for injection into LLM prompts.
// It returns "" if no relevant chunks are found or the engine is unavailable.
func RAGRetrieveContext(query string, topK int) string {
	if SharedRAG == nil || SharedRAG.ChunkCount() == 0 || query == "" {
		return ""
	}
	if topK <= 0 {
		topK = 3
	}
	chunks := SharedRAG.Retrieve(query, topK)
	return rag.FormatChunks(chunks)
}

// ---------- RL Policy Bridge ----------

// RLPolicyBridge reads the trained RL Q-table policy and extracts
// human-readable decision hints that can be injected into LLM prompts
// to bias planning toward RL-proven strategies.
type RLPolicyBridge struct{}

// NewRLPolicyBridge creates a bridge that reads RL policy from DB.
func NewRLPolicyBridge() *RLPolicyBridge {
	return &RLPolicyBridge{}
}

// Available returns true if RL policy data exists in the database.
func (b *RLPolicyBridge) Available() bool {
	if sharedDB == nil {
		return false
	}
	var content string
	err := sharedDB.QueryRow(`SELECT content FROM data_store WHERE store_key='rl_policy'`).Scan(&content)
	return err == nil && content != ""
}

// GenerateFlightHints reads the Q-table from DB and produces concise
// natural-language hints about what the RL agent prefers in various situations.
func (b *RLPolicyBridge) GenerateFlightHints() string {
	if sharedDB == nil {
		return ""
	}

	var raw string
	err := sharedDB.QueryRow(`SELECT content FROM data_store WHERE store_key='rl_policy'`).Scan(&raw)
	if err != nil || raw == "" {
		return ""
	}

	var data struct {
		Table   map[string][]float64 `json:"table"`
		Epsilon float64              `json:"epsilon"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil || len(data.Table) == 0 {
		return ""
	}

	actionNames := []string{"调整航向", "调整速度", "调整高度", "前往航点", "返航", "悬停", "紧急降落"}

	// Aggregate: for each discretised battery bin + anomaly combination,
	// count which action is preferred across all state keys.
	type scenarioStats struct {
		actionCounts [7]int
		total        int
	}
	scenarios := map[string]*scenarioStats{
		"low_battery":   {},
		"critical_bat":  {},
		"anomaly":       {},
		"collision":     {},
		"normal_cruise": {},
	}

	for key, qValues := range data.Table {
		parts := strings.Split(key, "_")
		if len(parts) < 10 {
			continue
		}
		// parts: latBin_lngBin_altBin_spdBin_hdgBin_batBin_phaseBin_anomBin_nearBin_sepBin
		batBin := parts[5]
		anomBin := parts[7]
		sepBin := parts[9]

		bestAction := 0
		bestQ := qValues[0]
		for i := 1; i < len(qValues) && i < 7; i++ {
			if qValues[i] > bestQ {
				bestQ = qValues[i]
				bestAction = i
			}
		}

		// Classify scenario
		switch {
		case batBin == "0" || batBin == "1": // 0-19%
			scenarios["critical_bat"].actionCounts[bestAction]++
			scenarios["critical_bat"].total++
		case batBin == "2": // 20-29%
			scenarios["low_battery"].actionCounts[bestAction]++
			scenarios["low_battery"].total++
		case anomBin == "1":
			scenarios["anomaly"].actionCounts[bestAction]++
			scenarios["anomaly"].total++
		case sepBin == "0" || sepBin == "1": // collision danger or close
			scenarios["collision"].actionCounts[bestAction]++
			scenarios["collision"].total++
		default:
			scenarios["normal_cruise"].actionCounts[bestAction]++
			scenarios["normal_cruise"].total++
		}
	}

	var hints []string
	hints = append(hints, fmt.Sprintf("\n【RL强化学习策略参考（基于 %d 个训练状态）】", len(data.Table)))

	labels := map[string]string{
		"critical_bat":  "电量危急 (<20%)",
		"low_battery":   "电量偏低 (20-30%)",
		"anomaly":       "异常状态",
		"collision":     "碰撞风险 (间距<30m)",
		"normal_cruise": "正常巡航",
	}
	order := []string{"critical_bat", "low_battery", "anomaly", "collision", "normal_cruise"}

	for _, k := range order {
		s := scenarios[k]
		if s.total < 3 {
			continue
		}
		top := 0
		for i := 1; i < 7; i++ {
			if s.actionCounts[i] > s.actionCounts[top] {
				top = i
			}
		}
		pct := float64(s.actionCounts[top]) * 100.0 / float64(s.total)
		// Find second-best
		second := -1
		for i := 0; i < 7; i++ {
			if i != top && (second == -1 || s.actionCounts[i] > s.actionCounts[second]) {
				second = i
			}
		}
		hint := fmt.Sprintf("- %s → 推荐「%s」(%.0f%%)", labels[k], actionNames[top], pct)
		if second >= 0 && s.actionCounts[second] > 0 {
			pct2 := float64(s.actionCounts[second]) * 100.0 / float64(s.total)
			hint += fmt.Sprintf("，次选「%s」(%.0f%%)", actionNames[second], pct2)
		}
		hints = append(hints, hint)
	}

	if len(hints) <= 1 {
		return ""
	}
	hints = append(hints, "请在航线规划中参考以上RL策略经验，尤其在低电量和异常场景下优先采用RL推荐的动作策略。")
	return strings.Join(hints, "\n")
}
