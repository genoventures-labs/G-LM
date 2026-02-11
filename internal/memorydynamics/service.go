package memorydynamics

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store"
)

type Config struct {
	Enabled               bool
	HalfLifeHours         float64
	ReplayThreshold       float64
	FreshnessWindowHours  float64
	ContextNodeLimit      int
	UpdateConceptsPerTurn int
}

type Service struct {
	store store.Store
	cfg   Config
}

type ContextResult struct {
	Applied         bool     `json:"applied"`
	NodeCount       int      `json:"node_count"`
	ReplayTriggered bool     `json:"replay_triggered"`
	Keys            []string `json:"keys,omitempty"`
}

type scoredNode struct {
	Node      model.MemoryNode
	Score     float64
	Freshness float64
}

func New(st store.Store, cfg Config) *Service {
	if cfg.HalfLifeHours <= 0 {
		cfg.HalfLifeHours = 24 * 7
	}
	if cfg.ReplayThreshold <= 0 {
		cfg.ReplayThreshold = 0.68
	}
	if cfg.FreshnessWindowHours <= 0 {
		cfg.FreshnessWindowHours = 24 * 3
	}
	if cfg.ContextNodeLimit <= 0 {
		cfg.ContextNodeLimit = 5
	}
	if cfg.UpdateConceptsPerTurn <= 0 {
		cfg.UpdateConceptsPerTurn = 6
	}
	return &Service{store: st, cfg: cfg}
}

func (s *Service) BuildContext(ctx context.Context, tenantID, sessionID string, req model.ChatCompletionRequest) (model.ChatCompletionRequest, ContextResult, error) {
	if !s.cfg.Enabled {
		return req, ContextResult{}, nil
	}
	nodes, err := s.store.ListMemoryNodes(ctx, tenantID, sessionID, 100)
	if err != nil {
		return req, ContextResult{}, err
	}
	scored := s.scoreNodes(nodes, time.Now().UTC())
	if len(scored) == 0 {
		return req, ContextResult{Applied: true}, nil
	}
	limit := s.cfg.ContextNodeLimit
	if len(scored) < limit {
		limit = len(scored)
	}
	selected := scored[:limit]
	replay := false
	keys := make([]string, 0, len(selected))
	lines := make([]string, 0, len(selected))
	for _, sn := range selected {
		keys = append(keys, sn.Node.Key)
		if sn.Score >= s.cfg.ReplayThreshold {
			replay = true
		}
		lines = append(lines, "- key="+sn.Node.Key+" label="+sn.Node.Label+" score="+f3(sn.Score)+" freshness="+f3(sn.Freshness))
	}
	ctxMsg := model.Message{
		Role: "system",
		Content: "memory_dynamics context (deterministic):\n" + strings.Join(lines, "\n") +
			"\nUse only as prioritization hints. Do not fabricate details.",
	}
	out := make([]model.Message, 0, len(req.Messages)+1)
	out = append(out, ctxMsg)
	out = append(out, req.Messages...)
	req.Messages = out
	return req, ContextResult{
		Applied:         true,
		NodeCount:       len(selected),
		ReplayTriggered: replay,
		Keys:            keys,
	}, nil
}

func (s *Service) UpdateFromTurn(ctx context.Context, tenantID, sessionID string, userText, assistantText string) error {
	if !s.cfg.Enabled {
		return nil
	}
	combined := strings.TrimSpace(userText + "\n" + assistantText)
	if combined == "" {
		return nil
	}
	concepts := topConcepts(combined, s.cfg.UpdateConceptsPerTurn)
	if len(concepts) == 0 {
		return nil
	}
	importance := importanceScore(combined)
	existing, err := s.store.ListMemoryNodes(ctx, tenantID, sessionID, 200)
	if err != nil {
		return err
	}
	lookup := map[string]model.MemoryNode{}
	for _, n := range existing {
		lookup[n.Key] = n
	}
	now := model.FlexTime{Time: time.Now().UTC()}
	for _, c := range concepts {
		n := lookup[c]
		if n.Key == "" {
			n = model.MemoryNode{
				TenantID:    tenantID,
				SessionID:   sessionID,
				Key:         c,
				Label:       c,
				Metadata:    map[string]any{"session_id": sessionID},
				Weight:      0.45,
				Importance:  importance,
				AccessCount: 0,
			}
		}
		if n.Metadata == nil {
			n.Metadata = map[string]any{}
		}
		n.Metadata["session_id"] = sessionID
		n.Weight = clamp01((n.Weight * 0.72) + (importance * 0.28) + 0.08)
		n.Importance = clamp01((n.Importance * 0.65) + (importance * 0.35))
		n.AccessCount = n.AccessCount + 1
		n.LastSeenAt = now
		if _, err := s.store.UpsertMemoryNode(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) scoreNodes(nodes []model.MemoryNode, now time.Time) []scoredNode {
	if len(nodes) == 0 {
		return nil
	}
	lambda := math.Ln2 / s.cfg.HalfLifeHours
	scored := make([]scoredNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Key == "" {
			continue
		}
		last := n.LastSeenAt.Time
		if last.IsZero() {
			last = n.UpdatedAt
		}
		if last.IsZero() {
			last = n.CreatedAt
		}
		ageHours := now.Sub(last).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		forget := math.Exp(-lambda * ageHours)
		fresh := math.Exp(-ageHours / s.cfg.FreshnessWindowHours)
		score := clamp01((n.Weight * forget * 0.55) + (n.Importance * 0.30) + (fresh * 0.15))
		scored = append(scored, scoredNode{Node: n, Score: score, Freshness: fresh})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Node.Key < scored[j].Node.Key
		}
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func topConcepts(text string, limit int) []string {
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '(' || r == ')' || r == '"' || r == '\''
	})
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {}, "have": {}, "your": {}, "you": {}, "into": {}, "will": {}, "could": {}, "should": {}, "would": {}, "there": {}, "their": {},
	}
	freq := map[string]int{}
	for _, t := range tokens {
		if len(t) < 4 {
			continue
		}
		if _, blocked := stop[t]; blocked {
			continue
		}
		freq[t]++
	}
	type kv struct {
		K string
		V int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{K: stableKey(k), V: v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].V == items[j].V {
			return items[i].K < items[j].K
		}
		return items[i].V > items[j].V
	})
	if len(items) > limit {
		items = items[:limit]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.K)
	}
	return out
}

func stableKey(token string) string {
	t := strings.TrimSpace(strings.ToLower(token))
	if len(t) <= 24 {
		return t
	}
	h := sha1.Sum([]byte(t))
	return t[:24] + "-" + hex.EncodeToString(h[:4])
}

func importanceScore(text string) float64 {
	t := strings.ToLower(text)
	base := 0.45
	if strings.Contains(t, "critical") || strings.Contains(t, "urgent") || strings.Contains(t, "incident") {
		base += 0.25
	}
	if strings.Contains(t, "decision") || strings.Contains(t, "must") || strings.Contains(t, "required") {
		base += 0.2
	}
	if strings.Contains(t, "todo") || strings.Contains(t, "action") {
		base += 0.1
	}
	return clamp01(base)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func f3(v float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 3, 64), "0"), ".")
}
