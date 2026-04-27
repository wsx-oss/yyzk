// AI辅助生成：DeepSeek v3.2, API调用, 2025-11-27 15:00-16:40
// 环节：RAG知识库系统设计
// 关键提示词：构建无人机领域AI知识库
// AI回复：BM25检索+文档增强生成（RAG）
// 人工修改说明：改为20篇本地知识文档轻量索引
package rag

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Chunk represents a piece of knowledge from the knowledge base.
type Chunk struct {
	ID      int     `json:"id"`
	Source  string  `json:"source"`  // filename
	Section string  `json:"section"` // heading / section title
	Content string  `json:"content"` // raw text
	Score   float64 `json:"score"`   // retrieval relevance score
	tokens  []string
}

// Engine is a lightweight keyword-based RAG retrieval engine using BM25 scoring.
// It loads markdown documents, splits them into heading-delimited chunks, builds
// an inverted index, and retrieves the most relevant chunks for a given query.
type Engine struct {
	mu     sync.RWMutex
	chunks []Chunk
	// Inverted index: token -> list of chunk IDs containing the token
	index map[string][]int
	// Document frequency for each token
	df map[string]int
	// Average document length (in tokens)
	avgDL float64
}

// New creates and initialises an empty RAG engine.
func New() *Engine {
	return &Engine{
		index: make(map[string][]int),
		df:    make(map[string]int),
	}
}

// LoadDirectory scans a directory for .md files and indexes their contents.
// It can be called multiple times to add more documents.
func (e *Engine) LoadDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		e.addDocument(entry.Name(), string(data))
	}
	e.buildIndex()
	return nil
}

// LoadText adds a single named text document to the engine (useful for dynamic docs).
func (e *Engine) LoadText(name, content string) {
	e.mu.Lock()
	e.addDocument(name, content)
	e.buildIndex()
	e.mu.Unlock()
}

// LoadTexts adds multiple named documents in a single batch (for DB-backed loading).
func (e *Engine) LoadTexts(docs map[string]string) {
	e.mu.Lock()
	for name, content := range docs {
		e.addDocument(name, content)
	}
	e.buildIndex()
	e.mu.Unlock()
}

// ChunkCount returns the total number of indexed chunks.
func (e *Engine) ChunkCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.chunks)
}

// Retrieve returns the top-K most relevant chunks for the given query.
func (e *Engine) Retrieve(query string, topK int) []Chunk {
	if topK <= 0 {
		topK = 3
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.chunks) == 0 {
		return nil
	}

	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return nil
	}

	// BM25 parameters
	const k1 = 1.2
	const b = 0.75
	n := float64(len(e.chunks))

	type scored struct {
		idx   int
		score float64
	}
	results := make([]scored, 0, len(e.chunks))

	for i, chunk := range e.chunks {
		score := 0.0
		dl := float64(len(chunk.tokens))
		for _, qt := range qTokens {
			dfVal := float64(e.df[qt])
			if dfVal == 0 {
				continue
			}
			// IDF component
			idf := math.Log((n - dfVal + 0.5) / (dfVal + 0.5))
			if idf < 0 {
				idf = 0
			}
			// Term frequency in this chunk
			tf := 0.0
			for _, ct := range chunk.tokens {
				if ct == qt {
					tf++
				}
			}
			// BM25 term score
			score += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*dl/e.avgDL))
		}
		if score > 0 {
			results = append(results, scored{i, score})
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].score > results[j].score })

	if topK > len(results) {
		topK = len(results)
	}
	out := make([]Chunk, topK)
	for i := 0; i < topK; i++ {
		c := e.chunks[results[i].idx]
		c.Score = results[i].score
		c.tokens = nil // don't expose internal tokens
		out[i] = c
	}
	return out
}

// FormatChunks formats retrieved chunks into a string suitable for LLM context.
func FormatChunks(chunks []Chunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("【知识库参考】\n")
	for i, c := range chunks {
		sb.WriteString(strings.Repeat("─", 40))
		sb.WriteByte('\n')
		if c.Section != "" {
			sb.WriteString("[" + c.Source + " > " + c.Section + "]\n")
		} else {
			sb.WriteString("[" + c.Source + "]\n")
		}
		// Truncate very long chunks
		content := c.Content
		if len(content) > 600 {
			content = content[:600] + "..."
		}
		sb.WriteString(content)
		sb.WriteByte('\n')
		if i == len(chunks)-1 {
			sb.WriteString(strings.Repeat("─", 40))
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// ── internal helpers ────────────────────────────────────────────────────────

// addDocument splits a markdown document by headings and appends chunks.
// Must be called with mu held (or before engine is used concurrently).
func (e *Engine) addDocument(name, content string) {
	sections := splitMarkdownSections(content)
	for _, sec := range sections {
		text := strings.TrimSpace(sec.body)
		if len(text) < 10 {
			continue
		}
		// Further split large sections into ~500-char sub-chunks
		subChunks := splitLarge(text, 500)
		for _, sc := range subChunks {
			chunk := Chunk{
				ID:      len(e.chunks),
				Source:  name,
				Section: sec.heading,
				Content: sc,
				tokens:  tokenize(sc + " " + sec.heading),
			}
			e.chunks = append(e.chunks, chunk)
		}
	}
}

// buildIndex rebuilds the inverted index from all chunks.
func (e *Engine) buildIndex() {
	e.index = make(map[string][]int)
	e.df = make(map[string]int)
	totalLen := 0
	for i, chunk := range e.chunks {
		totalLen += len(chunk.tokens)
		seen := map[string]bool{}
		for _, t := range chunk.tokens {
			if !seen[t] {
				seen[t] = true
				e.df[t]++
				e.index[t] = append(e.index[t], i)
			}
		}
	}
	if len(e.chunks) > 0 {
		e.avgDL = float64(totalLen) / float64(len(e.chunks))
	}
}

// section holds a heading + body pair from markdown splitting.
type section struct {
	heading string
	body    string
}

var headingRe = regexp.MustCompile(`(?m)^(#{1,4})\s+(.+)$`)

// splitMarkdownSections splits markdown text by headings.
func splitMarkdownSections(content string) []section {
	locs := headingRe.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return []section{{heading: "", body: content}}
	}

	var sections []section
	// Text before first heading
	if locs[0][0] > 0 {
		pre := strings.TrimSpace(content[:locs[0][0]])
		if len(pre) > 10 {
			sections = append(sections, section{heading: "", body: pre})
		}
	}

	for i, loc := range locs {
		heading := content[loc[4]:loc[5]] // capture group 2: heading text
		var bodyEnd int
		if i+1 < len(locs) {
			bodyEnd = locs[i+1][0]
		} else {
			bodyEnd = len(content)
		}
		body := strings.TrimSpace(content[loc[1]:bodyEnd])
		sections = append(sections, section{heading: heading, body: body})
	}
	return sections
}

// splitLarge breaks a text into sub-chunks of roughly maxLen characters,
// preferring to split on paragraph boundaries.
func splitLarge(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}
	paragraphs := strings.Split(text, "\n\n")
	var chunks []string
	var buf strings.Builder
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if buf.Len() > 0 && buf.Len()+len(p)+2 > maxLen {
			chunks = append(chunks, buf.String())
			buf.Reset()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p)
	}
	if buf.Len() > 0 {
		chunks = append(chunks, buf.String())
	}
	if len(chunks) == 0 {
		chunks = []string{text}
	}
	return chunks
}

// tokenize splits text into lowercase tokens, handling both CJK and Latin text.
func tokenize(text string) []string {
	text = strings.ToLower(text)
	var tokens []string

	// Split on whitespace and punctuation first
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || r == '|' || r == '─' || r == '>' || r == '【' || r == '】'
	})

	for _, field := range fields {
		// For CJK-heavy text, do character bigrams
		runes := []rune(field)
		hasCJK := false
		for _, r := range runes {
			if unicode.Is(unicode.Han, r) {
				hasCJK = true
				break
			}
		}
		if hasCJK && len(runes) > 1 {
			// Character bigrams for CJK
			for i := 0; i < len(runes)-1; i++ {
				tokens = append(tokens, string(runes[i:i+2]))
			}
			// Also add unigrams for single-char matches
			for _, r := range runes {
				if unicode.Is(unicode.Han, r) {
					tokens = append(tokens, string(r))
				}
			}
		} else if len(field) > 0 {
			tokens = append(tokens, field)
		}
	}
	return tokens
}
