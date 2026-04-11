package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"smartcontrol/internal/rag"
)

// SharedRAG holds a reference to the global RAG engine so all handlers can use it.
var SharedRAG *rag.Engine

// InitSharedRAG initialises the shared RAG engine by scanning the knowledge_base
// directory. It should be called once from main (or from the first handler that
// needs it). If the engine has already been initialised, this is a no-op.
func InitSharedRAG() {
	if SharedRAG != nil {
		return
	}
	SharedRAG = rag.New()

	candidates := []string{
		"knowledge_base",
		"project/knowledge_base",
		filepath.Join("..", "knowledge_base"),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "knowledge_base"))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			if err := SharedRAG.LoadDirectory(dir); err == nil && SharedRAG.ChunkCount() > 0 {
				log.Printf("[RAG] shared engine loaded %d chunks from %s", SharedRAG.ChunkCount(), dir)
				return
			}
		}
	}
	log.Println("[RAG] shared engine: no knowledge base documents found (non-fatal)")
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
type RLPolicyBridge struct {
	policyPath string
}

// NewRLPolicyBridge creates a bridge pointing at the saved RL policy.
func NewRLPolicyBridge() *RLPolicyBridge {
	candidates := []string{
		"data/rl_policy.json",
		"project/data/rl_policy.json",
		filepath.Join("..", "data", "rl_policy.json"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return &RLPolicyBridge{policyPath: p}
		}
	}
	return &RLPolicyBridge{}
}

// Available returns true if a trained policy file exists on disk.
func (b *RLPolicyBridge) Available() bool {
	if b.policyPath == "" {
		return false
	}
	_, err := os.Stat(b.policyPath)
	return err == nil
}

// GenerateFlightHints reads the Q-table and produces concise natural-language
// hints about what the RL agent prefers in various situations. These hints are
// appended to the LLM's flight-planning prompt so it benefits from simulated
// experience without requiring model fine-tuning.
func (b *RLPolicyBridge) GenerateFlightHints() string {
	if !b.Available() {
		return ""
	}

	raw, err := os.ReadFile(b.policyPath)
	if err != nil {
		return ""
	}

	var data struct {
		Table   map[string][]float64 `json:"table"`
		Epsilon float64              `json:"epsilon"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || len(data.Table) == 0 {
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
