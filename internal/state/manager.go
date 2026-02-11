package state

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
)

type ToneCompensation struct {
	TargetTone string  `json:"target_tone"`
	Warmth     float64 `json:"warmth"`
	Directness float64 `json:"directness"`
	Empathy    float64 `json:"empathy"`
	Stability  float64 `json:"stability"`
	Intensity  float64 `json:"intensity"`
}

type CognitiveState struct {
	SessionID          string           `json:"session_id"`
	TaskMode           string           `json:"task_mode"`
	Topic              string           `json:"topic"`
	TopicKeywords      []string         `json:"topic_keywords"`
	Sentiment          string           `json:"sentiment"`
	SentimentInstant   float64          `json:"sentiment_instant"`
	SentimentCarryover float64          `json:"sentiment_carryover"`
	Valence            float64          `json:"valence"`
	Arousal            float64          `json:"arousal"`
	Mood               float64          `json:"mood"`
	MoodSlope          float64          `json:"mood_slope"`
	EmotionalEnergy    float64          `json:"emotional_energy"`
	MoodShift          float64          `json:"mood_shift"`
	TopicDrift         float64          `json:"topic_drift"`
	Pacing             string           `json:"pacing"`
	MicroSwitches      []string         `json:"micro_switches"`
	RecencySeconds     float64          `json:"recency_seconds"`
	InteractionCount   int              `json:"interaction_count"`
	ToneCompensation   ToneCompensation `json:"tone_compensation"`
	UpdatedAt          time.Time        `json:"updated_at"`
	DecayLambdaPerHour float64          `json:"decay_lambda_per_hour"`
}

type ContextState = CognitiveState

type topicSignal struct {
	Weight  float64
	Updated time.Time
}

type sessionState struct {
	ID               string
	TaskMode         string
	LastInteraction  time.Time
	InteractionCount int

	SentimentInstant   float64
	SentimentCarryover float64
	Valence            float64
	Arousal            float64
	Mood               float64
	MoodSlope          float64
	EmotionalEnergy    float64
	MoodShift          float64
	TopicDrift         float64
	Pacing             string
	PrevTopicKeywords  []string

	TopicSignals map[string]topicSignal
}

type Manager struct {
	mu      sync.RWMutex
	window  int
	session map[string]*sessionState
}

const (
	defaultDecayLambdaPerHour = 0.45
	sentimentEMASmoothing     = 0.22
	arousalEMASmoothing       = 0.20
	moodRiseAlpha             = 0.16
	moodDecayAlpha            = 0.08
	topicDecayFloor           = 0.05
	topicPruneThreshold       = 0.02
	maxTopicKeywords          = 3
)

func NewManager(window int) *Manager {
	if window <= 0 {
		window = 20
	}
	return &Manager{window: window, session: map[string]*sessionState{}}
}

func (m *Manager) ResolveSessionID(reqSessionID, headerSessionID, fallback string) string {
	if s := strings.TrimSpace(reqSessionID); s != "" {
		return s
	}
	if s := strings.TrimSpace(headerSessionID); s != "" {
		return s
	}
	return strings.TrimSpace(fallback)
}

func (m *Manager) RecordUserInput(sessionID string, req model.ChatCompletionRequest) CognitiveState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreateLocked(sessionID)
	now := time.Now().UTC()
	delta := now.Sub(s.LastInteraction)
	if delta < 0 {
		delta = 0
	}
	m.applyDecayLocked(s, now)

	text := joinUserMessages(req.Messages)
	s.TaskMode = detectTaskMode(text)
	prevMood := s.Mood
	prevTopic := append([]string{}, s.PrevTopicKeywords...)

	instantSentiment := detectSentimentScore(text)
	s.SentimentInstant = instantSentiment
	if s.InteractionCount == 0 {
		s.SentimentCarryover = instantSentiment
	} else {
		s.SentimentCarryover = ema(s.SentimentCarryover, instantSentiment, sentimentEMASmoothing)
	}

	instantArousal := detectArousal(text)
	if s.InteractionCount == 0 {
		s.Arousal = instantArousal
		s.Valence = s.SentimentCarryover
	} else {
		s.Arousal = ema(s.Arousal, instantArousal, arousalEMASmoothing)
		s.Valence = ema(s.Valence, s.SentimentCarryover, sentimentEMASmoothing)
	}

	targetMood := clamp((0.72 * s.SentimentCarryover) + (0.28 * ((s.Arousal * 2.0) - 1.0)))
	alpha := moodDecayAlpha
	if targetMood > s.Mood {
		alpha = moodRiseAlpha
	}
	s.Mood = clamp((1.0-alpha)*s.Mood + alpha*targetMood)
	s.MoodSlope = s.Mood - prevMood
	s.EmotionalEnergy = clamp01((math.Abs(s.Mood)*0.55 + s.Arousal*0.45))

	m.updateTopicsLocked(s, text, now)
	newTopics, _ := topTopics(s.TopicSignals)
	s.TopicDrift = topicDriftScore(prevTopic, newTopics)
	s.PrevTopicKeywords = append([]string{}, newTopics...)
	s.MoodShift = math.Abs(s.Mood - prevMood)
	s.Pacing = detectPacing(delta, text)

	s.InteractionCount++
	s.LastInteraction = now
	return m.snapshotLocked(s, now)
}

func (m *Manager) RecordAssistantOutput(sessionID, content string) CognitiveState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.getOrCreateLocked(sessionID)
	now := time.Now().UTC()
	m.applyDecayLocked(s, now)
	if strings.TrimSpace(content) != "" {
		s.EmotionalEnergy = clamp01(ema(s.EmotionalEnergy, 0.45, 0.08))
	}
	s.LastInteraction = now
	return m.snapshotLocked(s, now)
}

func (m *Manager) Snapshot(sessionID string) (CognitiveState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.session[strings.TrimSpace(sessionID)]
	if !ok {
		return CognitiveState{}, false
	}
	return m.snapshotLocked(s, time.Now().UTC()), true
}

func (m *Manager) ToneMetadata(sessionID string) (ToneCompensation, bool) {
	snap, ok := m.Snapshot(sessionID)
	if !ok {
		return ToneCompensation{}, false
	}
	return snap.ToneCompensation, true
}

func (m *Manager) getOrCreateLocked(sessionID string) *sessionState {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	if s, ok := m.session[sessionID]; ok {
		return s
	}
	now := time.Now().UTC()
	s := &sessionState{
		ID:              sessionID,
		TaskMode:        "general",
		TopicSignals:    map[string]topicSignal{},
		LastInteraction: now,
	}
	m.session[sessionID] = s
	return s
}

func (m *Manager) applyDecayLocked(s *sessionState, now time.Time) {
	if s.LastInteraction.IsZero() {
		s.LastInteraction = now
		return
	}
	dt := now.Sub(s.LastInteraction)
	if dt <= 0 {
		return
	}
	hours := dt.Hours()
	decay := math.Exp(-defaultDecayLambdaPerHour * hours)

	s.SentimentCarryover = clamp(s.SentimentCarryover * decay)
	s.Valence = clamp(s.Valence * decay)
	s.Arousal = clamp01(s.Arousal * decay)
	s.Mood = clamp(s.Mood * decay)
	s.MoodSlope = s.MoodSlope * decay
	s.EmotionalEnergy = clamp01(s.EmotionalEnergy * decay)

	for k, sig := range s.TopicSignals {
		lagHours := now.Sub(sig.Updated).Hours()
		if lagHours < 0 {
			lagHours = 0
		}
		w := sig.Weight * math.Exp(-defaultDecayLambdaPerHour*0.6*lagHours)
		if w < topicPruneThreshold {
			delete(s.TopicSignals, k)
			continue
		}
		s.TopicSignals[k] = topicSignal{Weight: w, Updated: now}
	}
}

func (m *Manager) updateTopicsLocked(s *sessionState, text string, now time.Time) {
	tokens := extractTopicTokens(text)
	if len(tokens) == 0 {
		return
	}
	for _, tok := range tokens {
		sig := s.TopicSignals[tok]
		sig.Weight += 1.0
		if sig.Weight < topicDecayFloor {
			sig.Weight = topicDecayFloor
		}
		sig.Updated = now
		s.TopicSignals[tok] = sig
	}
}

func (m *Manager) snapshotLocked(s *sessionState, now time.Time) CognitiveState {
	kws, topic := topTopics(s.TopicSignals)
	sentLabel := sentimentLabel(s.SentimentCarryover)
	recency := 0.0
	if !s.LastInteraction.IsZero() {
		recency = now.Sub(s.LastInteraction).Seconds()
		if recency < 0 {
			recency = 0
		}
	}

	return CognitiveState{
		SessionID:          s.ID,
		TaskMode:           s.TaskMode,
		Topic:              topic,
		TopicKeywords:      kws,
		Sentiment:          sentLabel,
		SentimentInstant:   round3(s.SentimentInstant),
		SentimentCarryover: round3(s.SentimentCarryover),
		Valence:            round3(s.Valence),
		Arousal:            round3(s.Arousal),
		Mood:               round3(s.Mood),
		MoodSlope:          round3(s.MoodSlope),
		EmotionalEnergy:    round3(s.EmotionalEnergy),
		MoodShift:          round3(s.MoodShift),
		TopicDrift:         round3(s.TopicDrift),
		Pacing:             s.Pacing,
		MicroSwitches:      detectMicroSwitches(s),
		RecencySeconds:     round3(recency),
		InteractionCount:   s.InteractionCount,
		ToneCompensation:   deriveToneCompensation(s),
		UpdatedAt:          s.LastInteraction,
		DecayLambdaPerHour: defaultDecayLambdaPerHour,
	}
}

func deriveToneCompensation(s *sessionState) ToneCompensation {
	warmth := clamp01(0.5 + (s.SentimentCarryover * 0.35))
	directness := clamp01(0.55 + (s.Arousal * 0.25) - (math.Abs(s.Mood) * 0.08))
	empathy := clamp01(0.45 + (math.Max(0, -s.SentimentCarryover) * 0.45))
	stability := clamp01(1.0 - math.Min(1.0, math.Abs(s.MoodSlope)*3.0))
	intensity := clamp01((s.Arousal * 0.6) + (math.Abs(s.Mood) * 0.4))

	tone := "neutral-professional"
	switch {
	case empathy >= 0.65:
		tone = "supportive-calm"
	case directness >= 0.7 && stability >= 0.5:
		tone = "concise-direct"
	case warmth >= 0.65:
		tone = "warm-collaborative"
	}

	return ToneCompensation{
		TargetTone: tone,
		Warmth:     round3(warmth),
		Directness: round3(directness),
		Empathy:    round3(empathy),
		Stability:  round3(stability),
		Intensity:  round3(intensity),
	}
}

func joinUserMessages(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if strings.EqualFold(m.Role, "user") {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}

func detectTaskMode(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return "general"
	}
	coding := []string{"implement", "debug", "refactor", "stack trace", "panic", "exception", "compile", "function", "code"}
	for _, m := range coding {
		if strings.Contains(t, m) {
			return "coding"
		}
	}
	extraction := []string{"extract", "classify", "label", "return json", "fields", "categorize"}
	for _, m := range extraction {
		if strings.Contains(t, m) {
			return "extraction"
		}
	}
	if len(t) <= 120 && strings.Contains(t, "?") {
		return "qa_light"
	}
	return "general"
}

func detectSentimentScore(text string) float64 {
	t := strings.ToLower(text)
	if strings.TrimSpace(t) == "" {
		return 0
	}
	pos := []string{"good", "great", "excellent", "happy", "love", "awesome", "thanks", "perfect", "nice", "solid"}
	neg := []string{"bad", "terrible", "hate", "broken", "angry", "sad", "issue", "problem", "fail", "frustrated"}
	score := 0.0
	for _, p := range pos {
		if strings.Contains(t, p) {
			score += 1.0
		}
	}
	for _, n := range neg {
		if strings.Contains(t, n) {
			score -= 1.0
		}
	}
	if score == 0 {
		return 0
	}
	norm := score / 3.0
	return clamp(norm)
}

func detectArousal(text string) float64 {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0.3
	}
	lower := strings.ToLower(t)
	arousal := 0.35
	if strings.Contains(t, "!") {
		arousal += 0.18
	}
	if strings.Count(t, "?") > 1 {
		arousal += 0.1
	}
	high := []string{"urgent", "asap", "critical", "now", "immediately", "panic", "help"}
	for _, h := range high {
		if strings.Contains(lower, h) {
			arousal += 0.1
		}
	}
	return clamp01(arousal)
}

func sentimentLabel(v float64) string {
	switch {
	case v > 0.2:
		return "positive"
	case v < -0.2:
		return "negative"
	default:
		return "neutral"
	}
}

var wordRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9_-]{2,}`)

func extractTopicTokens(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	stopwords := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "you": {}, "your": {}, "are": {}, "from": {}, "have": {}, "what": {}, "when": {}, "where": {}, "which": {}, "will": {}, "about": {}, "please": {}, "need": {},
	}
	freq := map[string]int{}
	for _, token := range wordRe.FindAllString(strings.ToLower(text), -1) {
		if _, blocked := stopwords[token]; blocked {
			continue
		}
		freq[token]++
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
	if len(items) > maxTopicKeywords {
		items = items[:maxTopicKeywords]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.K)
	}
	return out
}

func topTopics(signals map[string]topicSignal) ([]string, string) {
	if len(signals) == 0 {
		return nil, ""
	}
	type kv struct {
		K string
		W float64
	}
	items := make([]kv, 0, len(signals))
	for k, v := range signals {
		items = append(items, kv{K: k, W: v.Weight})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].W == items[j].W {
			return items[i].K < items[j].K
		}
		return items[i].W > items[j].W
	})
	if len(items) > maxTopicKeywords {
		items = items[:maxTopicKeywords]
	}
	kws := make([]string, 0, len(items))
	for _, it := range items {
		kws = append(kws, it.K)
	}
	return kws, strings.Join(kws, ",")
}

func topicDriftScore(prev, current []string) float64 {
	if len(prev) == 0 || len(current) == 0 {
		return 0
	}
	prevSet := map[string]struct{}{}
	for _, p := range prev {
		prevSet[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	curSet := map[string]struct{}{}
	for _, c := range current {
		curSet[strings.ToLower(strings.TrimSpace(c))] = struct{}{}
	}
	inter := 0
	union := map[string]struct{}{}
	for k := range prevSet {
		union[k] = struct{}{}
	}
	for k := range curSet {
		union[k] = struct{}{}
		if _, ok := prevSet[k]; ok {
			inter++
		}
	}
	if len(union) == 0 {
		return 0
	}
	jaccard := float64(inter) / float64(len(union))
	return clamp01(1 - jaccard)
}

func detectPacing(delta time.Duration, text string) string {
	words := len(strings.Fields(strings.TrimSpace(text)))
	seconds := delta.Seconds()
	switch {
	case words > 120 || seconds < 20:
		return "fast"
	case seconds > 240:
		return "slow"
	default:
		return "steady"
	}
}

func detectMicroSwitches(s *sessionState) []string {
	out := make([]string, 0, 3)
	if s.MoodShift >= 0.12 {
		out = append(out, "mood_shift")
	}
	if s.TopicDrift >= 0.45 {
		out = append(out, "topic_drift")
	}
	if s.Pacing == "fast" || s.Pacing == "slow" {
		out = append(out, "pacing_shift")
	}
	return out
}

func ema(prev, value, alpha float64) float64 {
	if alpha <= 0 {
		return value
	}
	if alpha >= 1 {
		return value
	}
	return ((1.0 - alpha) * prev) + (alpha * value)
}

func clamp(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func clamp01(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
