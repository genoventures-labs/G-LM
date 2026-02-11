package document

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

type Upstream interface {
	ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error)
}

type Config struct {
	Enabled           bool
	DefaultChunkSize  int
	MaxDocuments      int
	MaxChunksPerDoc   int
	MaxLinkedSections int
}

type Result struct {
	Applied       bool     `json:"applied"`
	DocumentCount int      `json:"document_count"`
	ChunkCount    int      `json:"chunk_count"`
	LinksCount    int      `json:"links_count"`
	Flags         []string `json:"flags,omitempty"`
}

type Orchestrator struct {
	cfg Config
}

type docSummary struct {
	DocID   string
	Title   string
	Summary string
	Topics  []string
}

type linkEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Score int    `json:"score"`
}

func New(cfg Config) *Orchestrator {
	if cfg.DefaultChunkSize <= 0 {
		cfg.DefaultChunkSize = 1200
	}
	if cfg.MaxDocuments <= 0 {
		cfg.MaxDocuments = 8
	}
	if cfg.MaxChunksPerDoc <= 0 {
		cfg.MaxChunksPerDoc = 8
	}
	if cfg.MaxLinkedSections <= 0 {
		cfg.MaxLinkedSections = 12
	}
	return &Orchestrator{cfg: cfg}
}

func (o *Orchestrator) ShouldApply(req model.ChatCompletionRequest) bool {
	if !o.cfg.Enabled || len(req.Documents) == 0 {
		return false
	}
	if req.DocumentFlow == nil {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(req.DocumentFlow.Mode))
	return mode == "" || mode == "auto" || mode == "hierarchical"
}

func (o *Orchestrator) Prepare(ctx context.Context, up Upstream, req model.ChatCompletionRequest) (model.ChatCompletionRequest, Result, error) {
	if !o.ShouldApply(req) {
		return req, Result{}, nil
	}
	processed := trimDocuments(req.Documents, o.resolveMaxDocuments(req))
	if len(processed) == 0 {
		return req, Result{Applied: false}, nil
	}

	chunkSize := o.resolveChunkSize(req)
	summaries := make([]docSummary, 0, len(processed))
	totalChunks := 0
	flags := []string{}

	for _, d := range processed {
		chunks := splitChunks(d.Text, chunkSize)
		if len(chunks) > o.cfg.MaxChunksPerDoc {
			chunks = chunks[:o.cfg.MaxChunksPerDoc]
			flags = append(flags, "chunks_truncated")
		}
		totalChunks += len(chunks)
		chunkSummaries := make([]string, 0, len(chunks))
		for i, ch := range chunks {
			sum, err := o.summarizeChunk(ctx, up, req.Model, req.MaxTokens, d, i+1, len(chunks), ch)
			if err != nil {
				return req, Result{}, err
			}
			chunkSummaries = append(chunkSummaries, sum)
		}
		docSum, err := o.summarizeDocument(ctx, up, req.Model, req.MaxTokens, d, chunkSummaries)
		if err != nil {
			return req, Result{}, err
		}
		summaries = append(summaries, docSummary{
			DocID:   chooseDocID(d),
			Title:   d.Title,
			Summary: docSum,
			Topics:  topTokens(docSum, 6),
		})
	}

	links := buildLinks(summaries, o.cfg.MaxLinkedSections)
	contextBlob := buildContextBlob(summaries, links)
	req.Messages = prependDocumentContext(req.Messages, contextBlob)

	return req, Result{
		Applied:       true,
		DocumentCount: len(processed),
		ChunkCount:    totalChunks,
		LinksCount:    len(links),
		Flags:         dedupe(flags),
	}, nil
}

func (o *Orchestrator) summarizeChunk(ctx context.Context, up Upstream, modelID string, maxTokens *int, d model.DocumentInput, idx, total int, chunk string) (string, error) {
	prompt := fmt.Sprintf("Summarize chunk %d/%d from document '%s'. Keep factual bullets only and include key entities.", idx, total, d.Title)
	subMax := capSubcallTokens(maxTokens, 96)
	resp, err := up.ChatCompletions(ctx, model.ChatCompletionRequest{
		Model:     modelID,
		MaxTokens: subMax,
		Messages: []model.Message{
			{Role: "system", Content: "You summarize text deterministically. Do not add facts."},
			{Role: "user", Content: prompt + "\n\n" + chunk},
		},
	})
	if err != nil {
		return "", fmt.Errorf("chunk summary failed: %w", err)
	}
	return firstContent(resp), nil
}

func (o *Orchestrator) summarizeDocument(ctx context.Context, up Upstream, modelID string, maxTokens *int, d model.DocumentInput, chunkSummaries []string) (string, error) {
	payload := strings.Join(chunkSummaries, "\n- ")
	subMax := capSubcallTokens(maxTokens, 96)
	resp, err := up.ChatCompletions(ctx, model.ChatCompletionRequest{
		Model:     modelID,
		MaxTokens: subMax,
		Messages: []model.Message{
			{Role: "system", Content: "Combine chunk summaries into a concise document summary with major claims and risks. No speculation."},
			{Role: "user", Content: "Document: " + d.Title + "\nChunk summaries:\n- " + payload},
		},
	})
	if err != nil {
		return "", fmt.Errorf("document summary failed: %w", err)
	}
	return firstContent(resp), nil
}

func capSubcallTokens(src *int, capN int) *int {
	if capN <= 0 {
		return src
	}
	if src == nil {
		v := capN
		return &v
	}
	v := *src
	if v <= 0 || v > capN {
		v = capN
	}
	return &v
}

func buildContextBlob(summaries []docSummary, links []linkEdge) string {
	type summaryOut struct {
		DocID   string   `json:"doc_id"`
		Title   string   `json:"title"`
		Summary string   `json:"summary"`
		Topics  []string `json:"topics"`
	}
	payload := map[string]any{
		"document_orchestration": map[string]any{
			"summaries": toSummaryOut(summaries),
			"links":     links,
		},
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func toSummaryOut(in []docSummary) []summaryOut {
	out := make([]summaryOut, 0, len(in))
	for _, s := range in {
		out = append(out, summaryOut{DocID: s.DocID, Title: s.Title, Summary: s.Summary, Topics: s.Topics})
	}
	return out
}

type summaryOut struct {
	DocID   string   `json:"doc_id"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Topics  []string `json:"topics"`
}

func prependDocumentContext(messages []model.Message, blob string) []model.Message {
	ctx := model.Message{
		Role:    "system",
		Content: "Use this precomputed document orchestration context for grounded answering: " + blob,
	}
	out := make([]model.Message, 0, len(messages)+1)
	out = append(out, ctx)
	out = append(out, messages...)
	return out
}

func buildLinks(summaries []docSummary, maxLinks int) []linkEdge {
	edges := []linkEdge{}
	for i := 0; i < len(summaries); i++ {
		for j := i + 1; j < len(summaries); j++ {
			score := overlapScore(summaries[i].Topics, summaries[j].Topics)
			if score <= 0 {
				continue
			}
			edges = append(edges, linkEdge{From: summaries[i].DocID, To: summaries[j].DocID, Score: score})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Score == edges[j].Score {
			if edges[i].From == edges[j].From {
				return edges[i].To < edges[j].To
			}
			return edges[i].From < edges[j].From
		}
		return edges[i].Score > edges[j].Score
	})
	if len(edges) > maxLinks {
		edges = edges[:maxLinks]
	}
	return edges
}

func overlapScore(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]struct{}{}
	for _, x := range a {
		set[strings.ToLower(x)] = struct{}{}
	}
	score := 0
	for _, y := range b {
		if _, ok := set[strings.ToLower(y)]; ok {
			score++
		}
	}
	return score
}

func splitChunks(text string, chunkSize int) []string {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if len(t) <= chunkSize {
		return []string{t}
	}
	chunks := []string{}
	start := 0
	for start < len(t) {
		end := start + chunkSize
		if end >= len(t) {
			chunks = append(chunks, strings.TrimSpace(t[start:]))
			break
		}
		split := strings.LastIndexAny(t[start:end], ".\n ")
		if split <= 0 {
			split = chunkSize
		}
		end = start + split
		chunks = append(chunks, strings.TrimSpace(t[start:end]))
		start = end
	}
	return chunks
}

func topTokens(text string, max int) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '(' || r == ')' || r == '"' || r == '\''
	})
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {}, "from": {}, "into": {}, "using": {}, "document": {}, "summary": {}, "risk": {},
	}
	freq := map[string]int{}
	for _, w := range words {
		if len(w) < 4 {
			continue
		}
		if _, blocked := stop[w]; blocked {
			continue
		}
		freq[w]++
	}
	type kv struct {
		K string
		V int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{K: k, V: v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].V == items[j].V {
			return items[i].K < items[j].K
		}
		return items[i].V > items[j].V
	})
	if len(items) > max {
		items = items[:max]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.K)
	}
	return out
}

func trimDocuments(in []model.DocumentInput, max int) []model.DocumentInput {
	out := make([]model.DocumentInput, 0, len(in))
	for i, d := range in {
		if i >= max {
			break
		}
		if strings.TrimSpace(d.Text) == "" {
			continue
		}
		if strings.TrimSpace(d.ID) == "" {
			d.ID = fmt.Sprintf("doc-%d", i+1)
		}
		if strings.TrimSpace(d.Title) == "" {
			d.Title = d.ID
		}
		out = append(out, d)
	}
	return out
}

func (o *Orchestrator) resolveChunkSize(req model.ChatCompletionRequest) int {
	if req.DocumentFlow != nil && req.DocumentFlow.ChunkSize > 0 {
		return req.DocumentFlow.ChunkSize
	}
	return o.cfg.DefaultChunkSize
}

func (o *Orchestrator) resolveMaxDocuments(req model.ChatCompletionRequest) int {
	if req.DocumentFlow != nil && req.DocumentFlow.MaxDocuments > 0 {
		if req.DocumentFlow.MaxDocuments < o.cfg.MaxDocuments {
			return req.DocumentFlow.MaxDocuments
		}
	}
	return o.cfg.MaxDocuments
}

func chooseDocID(d model.DocumentInput) string {
	if strings.TrimSpace(d.ID) != "" {
		return d.ID
	}
	if strings.TrimSpace(d.Title) != "" {
		return d.Title
	}
	return "doc"
}

func firstContent(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
