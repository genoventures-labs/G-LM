package orchestrator

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/policy"
	"github.com/mike/cognitive-llm/internal/state"
)

var ErrNoAllowedAvailableModel = errors.New("no allowed available model for auto routing")

type HostStats struct {
	DiskUsedBytes  uint64
	DiskTotalBytes uint64
	Available      bool
}

type InventoryControlClient interface {
	ListModels(ctx context.Context) ([]string, error)
	PullModel(ctx context.Context, model string) error
	DeleteModel(ctx context.Context, model string) error
	HostStats(ctx context.Context) (diskUsedBytes uint64, diskTotalBytes uint64, available bool, err error)
}

type RouterConfig struct {
	JITInventoryEnabled    bool
	ReconcileInterval      time.Duration
	ReconcileJitter        time.Duration
	MaxModels              int
	StorageHighWatermark   float64
	StorageTargetWatermark float64
	PullTimeout            time.Duration
	PruneEnabled           bool
	IdealCoding            string
	IdealExtraction        string
	IdealLightQA           string
	IdealGeneral           string
}

type inventorySnapshot struct {
	Available map[string]string
	UpdatedAt time.Time
	Stale     bool
}

type Decision struct {
	RequestedModel   string
	ChosenModel      string
	Reason           string
	TaskClass        string
	FallbackUsed     bool
	IdealModel       string
	IdealAvailable   bool
	JITPullTriggered bool
	InventoryStale   bool
}

type CandidateDecision struct {
	Input      string `json:"input"`
	Canonical  string `json:"canonical,omitempty"`
	Available  bool   `json:"available"`
	Allowed    bool   `json:"allowed"`
	Selected   bool   `json:"selected"`
	SkipReason string `json:"skip_reason,omitempty"`
}

type DebugInfo struct {
	Triggered       bool                `json:"triggered"`
	TaskClass       string              `json:"task_class"`
	DefaultResolved string              `json:"default_resolved,omitempty"`
	Candidates      []CandidateDecision `json:"candidates"`
	Decision        Decision            `json:"decision"`
	AvailableModels []string            `json:"available_models"`
	AllowedModels   []string            `json:"allowed_models"`
	PolicyPrimary   string              `json:"policy_primary"`
}

type Router struct {
	defaultModel    string
	defaultAliases  []string
	fallbackDefault string
	taskMap         map[TaskClass][]string

	cfg         RouterConfig
	control     InventoryControlClient
	idealModels map[TaskClass]string

	inventory atomic.Value // inventorySnapshot

	pullInFlight sync.Map
	pullRecent   sync.Map // model -> time.Time
	lastUsedMu   sync.Mutex
	lastUsed     map[string]time.Time
	startOnce    sync.Once
}

func DefaultRouterConfig() RouterConfig {
	return RouterConfig{
		JITInventoryEnabled:    false,
		ReconcileInterval:      30 * time.Second,
		ReconcileJitter:        5 * time.Second,
		MaxModels:              20,
		StorageHighWatermark:   0.85,
		StorageTargetWatermark: 0.75,
		PullTimeout:            15 * time.Minute,
		PruneEnabled:           true,
		IdealCoding:            "deepseek-coder:6.7b",
		IdealExtraction:        "phi3:medium",
		IdealLightQA:           "llama3.2:1b",
		IdealGeneral:           "qwen3-8b-instruct-Q4_K_M",
	}
}

func NewRouter(defaultModel string, aliases []string, fallbackDefault string) *Router {
	return NewRouterWithControl(defaultModel, aliases, fallbackDefault, DefaultRouterConfig(), nil)
}

func NewRouterWithControl(defaultModel string, aliases []string, fallbackDefault string, cfg RouterConfig, control InventoryControlClient) *Router {
	if len(aliases) == 0 {
		aliases = []string{defaultModel}
	}
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = 30 * time.Second
	}
	if cfg.ReconcileJitter < 0 {
		cfg.ReconcileJitter = 0
	}
	if cfg.MaxModels < 1 {
		cfg.MaxModels = 20
	}
	if cfg.StorageHighWatermark <= 0 || cfg.StorageHighWatermark > 1 {
		cfg.StorageHighWatermark = 0.85
	}
	if cfg.StorageTargetWatermark <= 0 || cfg.StorageTargetWatermark > cfg.StorageHighWatermark {
		cfg.StorageTargetWatermark = 0.75
	}
	if cfg.PullTimeout <= 0 {
		cfg.PullTimeout = 15 * time.Minute
	}
	if strings.TrimSpace(cfg.IdealGeneral) == "" {
		cfg.IdealGeneral = defaultModel
	}
	if strings.TrimSpace(cfg.IdealCoding) == "" {
		cfg.IdealCoding = "deepseek-coder:6.7b"
	}
	if strings.TrimSpace(cfg.IdealExtraction) == "" {
		cfg.IdealExtraction = "phi3:medium"
	}
	if strings.TrimSpace(cfg.IdealLightQA) == "" {
		cfg.IdealLightQA = "llama3.2:1b"
	}

	taskMap := map[TaskClass][]string{
		TaskGeneral:                  {defaultModel, fallbackDefault, "mistral:7b", "gemma2:2b"},
		TaskCodingReasoning:          {"mistral:7b", defaultModel, fallbackDefault},
		TaskLightQA:                  {"llama3.2:1b", "qwen2.5:3b-instruct", "gemma2:2b"},
		TaskExtractionClassification: {"qwen2.5:3b-instruct", "phi3:mini", "gemma2:2b"},
	}
	r := &Router{
		defaultModel:    defaultModel,
		defaultAliases:  aliases,
		fallbackDefault: fallbackDefault,
		taskMap:         taskMap,
		cfg:             cfg,
		control:         control,
		idealModels: map[TaskClass]string{
			TaskCodingReasoning:          cfg.IdealCoding,
			TaskExtractionClassification: cfg.IdealExtraction,
			TaskLightQA:                  cfg.IdealLightQA,
			TaskGeneral:                  cfg.IdealGeneral,
		},
		lastUsed: map[string]time.Time{},
	}
	r.inventory.Store(inventorySnapshot{Available: map[string]string{}, UpdatedAt: time.Time{}, Stale: true})
	return r
}

func (r *Router) Start(ctx context.Context) {
	if !r.cfg.JITInventoryEnabled || r.control == nil {
		return
	}
	r.startOnce.Do(func() {
		go r.reconcileLoop(ctx)
	})
}

func ShouldAutoRoute(requestedModel string) bool {
	requestedModel = strings.TrimSpace(strings.ToLower(requestedModel))
	return requestedModel == "" || requestedModel == "auto"
}

func (r *Router) Choose(req model.ChatCompletionRequest, available []string, pol model.ModelPolicy) (Decision, error) {
	return r.ChooseWithState(req, available, pol, nil)
}

func (r *Router) ChooseWithState(req model.ChatCompletionRequest, available []string, pol model.ModelPolicy, st *state.ContextState) (Decision, error) {
	d := Decision{RequestedModel: req.Model}
	requestAvailable := toAvailableMap(available)
	snap := r.currentSnapshot()
	effective := requestAvailable
	if len(effective) == 0 {
		effective = snap.Available
		d.InventoryStale = snap.Stale
	}
	if len(effective) == 0 {
		return d, ErrNoAllowedAvailableModel
	}

	userTexts := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		if strings.EqualFold(m.Role, "user") {
			userTexts = append(userTexts, m.Content)
		}
	}
	class := ClassifyMessages(userTexts)
	if st != nil {
		if mapped, ok := taskClassFromStateMode(st.TaskMode); ok {
			class = mapped
		}
	}
	d.TaskClass = string(class)

	ideal := strings.TrimSpace(r.idealModels[class])
	d.IdealModel = ideal
	if ideal != "" {
		if canon, ok := r.lookupCanonical(ideal, effective); ok && policy.Allowed(canon, pol) {
			d.ChosenModel = canon
			d.Reason = "auto.ideal." + string(class)
			d.IdealAvailable = true
			r.markModelUsed(canon)
			return d, nil
		}
		d.IdealAvailable = false
		d.JITPullTriggered = r.triggerBackgroundPull(ideal)
	}

	resolvedDefault, defaultResolved := r.resolveDefault(effective)
	candidates := append([]string{}, r.taskMap[class]...)
	if class != TaskGeneral {
		// Ensure default candidates still appear for non-general routes as backup.
		candidates = append(candidates, resolvedDefault, r.fallbackDefault, r.defaultModel)
	}

	chosen, reason, fallbackUsed, ok := r.firstUsable(candidates, pol, effective, resolvedDefault, defaultResolved, class)
	if ok {
		d.ChosenModel = chosen
		d.Reason = reason
		d.FallbackUsed = fallbackUsed
		r.markModelUsed(chosen)
		return d, nil
	}

	// Final fallback: policy primary model.
	if cand, ok := r.lookupCanonical(pol.PrimaryModel, effective); ok && policy.Allowed(cand, pol) {
		d.ChosenModel = cand
		d.Reason = "auto.fallback.policy_primary"
		d.FallbackUsed = true
		r.markModelUsed(cand)
		return d, nil
	}

	// Final fallback: first allowed available model.
	for _, avail := range effective {
		if policy.Allowed(avail, pol) {
			d.ChosenModel = avail
			d.Reason = "auto.fallback.first_allowed_available"
			d.FallbackUsed = true
			r.markModelUsed(avail)
			return d, nil
		}
	}

	return d, ErrNoAllowedAvailableModel
}

func (r *Router) Explain(req model.ChatCompletionRequest, available []string, pol model.ModelPolicy) (DebugInfo, error) {
	return r.ExplainWithState(req, available, pol, nil)
}

func (r *Router) ExplainWithState(req model.ChatCompletionRequest, available []string, pol model.ModelPolicy, st *state.ContextState) (DebugInfo, error) {
	info := DebugInfo{
		Triggered:       ShouldAutoRoute(req.Model),
		AvailableModels: append([]string{}, available...),
		AllowedModels:   append([]string{}, pol.AllowedModels...),
		PolicyPrimary:   pol.PrimaryModel,
	}
	if !info.Triggered {
		info.Decision = Decision{
			RequestedModel: req.Model,
			ChosenModel:    req.Model,
			Reason:         "explicit_model_passthrough",
			TaskClass:      "",
			FallbackUsed:   false,
		}
		return info, nil
	}

	d, err := r.ChooseWithState(req, available, pol, st)
	info.Decision = d
	if d.TaskClass != "" {
		info.TaskClass = d.TaskClass
	}

	availableMap := toAvailableMap(available)
	resolvedDefault, defaultResolved := r.resolveDefault(availableMap)
	if defaultResolved {
		info.DefaultResolved = resolvedDefault
	}

	userTexts := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		if strings.EqualFold(m.Role, "user") {
			userTexts = append(userTexts, m.Content)
		}
	}
	class := ClassifyMessages(userTexts)
	if st != nil {
		if mapped, ok := taskClassFromStateMode(st.TaskMode); ok {
			class = mapped
		}
	}
	candidates := append([]string{}, r.taskMap[class]...)
	if class != TaskGeneral {
		candidates = append(candidates, resolvedDefault, r.fallbackDefault, r.defaultModel)
	}
	if ideal := strings.TrimSpace(r.idealModels[class]); ideal != "" {
		candidates = append([]string{ideal}, candidates...)
	}

	seen := map[string]struct{}{}
	for _, c := range candidates {
		cd := CandidateDecision{Input: c}
		if strings.TrimSpace(c) == "" {
			cd.SkipReason = "empty_candidate"
			info.Candidates = append(info.Candidates, cd)
			continue
		}
		cand := c
		if strings.EqualFold(c, r.defaultModel) {
			if defaultResolved {
				cand = resolvedDefault
			} else {
				cd.SkipReason = "default_unresolved"
				info.Candidates = append(info.Candidates, cd)
				continue
			}
		}
		canon, ok := r.lookupCanonical(cand, availableMap)
		if !ok {
			cd.SkipReason = "not_available"
			info.Candidates = append(info.Candidates, cd)
			continue
		}
		cd.Canonical = canon
		cd.Available = true
		if _, ok := seen[strings.ToLower(canon)]; ok {
			cd.SkipReason = "duplicate_candidate"
			info.Candidates = append(info.Candidates, cd)
			continue
		}
		seen[strings.ToLower(canon)] = struct{}{}
		if !policy.Allowed(canon, pol) {
			cd.SkipReason = "blocked_by_allowlist"
			info.Candidates = append(info.Candidates, cd)
			continue
		}
		cd.Allowed = true
		if strings.EqualFold(canon, d.ChosenModel) {
			cd.Selected = true
		}
		info.Candidates = append(info.Candidates, cd)
	}

	return info, err
}

func taskClassFromStateMode(mode string) (TaskClass, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "coding":
		return TaskCodingReasoning, true
	case "qa_light":
		return TaskLightQA, true
	case "extraction":
		return TaskExtractionClassification, true
	case "general":
		return TaskGeneral, true
	default:
		return "", false
	}
}

func (r *Router) firstUsable(candidates []string, pol model.ModelPolicy, available map[string]string, resolvedDefault string, defaultResolved bool, class TaskClass) (string, string, bool, bool) {
	seen := map[string]struct{}{}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		cand := c
		if strings.EqualFold(c, r.defaultModel) {
			if defaultResolved {
				cand = resolvedDefault
			} else {
				continue
			}
		}
		canon, ok := r.lookupCanonical(cand, available)
		if !ok {
			continue
		}
		if _, ok := seen[strings.ToLower(canon)]; ok {
			continue
		}
		seen[strings.ToLower(canon)] = struct{}{}
		if !policy.Allowed(canon, pol) {
			continue
		}
		reason, fallback := decisionReason(class, canon, resolvedDefault, defaultResolved, r.fallbackDefault)
		return canon, reason, fallback, true
	}
	return "", "", false, false
}

func decisionReason(class TaskClass, chosen, resolvedDefault string, defaultResolved bool, fallbackDefault string) (string, bool) {
	if class == TaskGeneral && strings.EqualFold(chosen, resolvedDefault) {
		return "auto.default", false
	}
	if !defaultResolved && strings.EqualFold(chosen, fallbackDefault) {
		return "auto.fallback.default_unavailable", true
	}
	switch class {
	case TaskCodingReasoning:
		return "auto.classification.coding", !strings.EqualFold(chosen, "mistral:7b")
	case TaskLightQA:
		return "auto.classification.qa_light", !strings.EqualFold(chosen, "llama3.2:1b")
	case TaskExtractionClassification:
		return "auto.classification.extraction", !strings.EqualFold(chosen, "qwen2.5:3b-instruct")
	default:
		if strings.EqualFold(chosen, fallbackDefault) {
			return "auto.fallback.default_unavailable", true
		}
		return "auto.classification.general", true
	}
}

func (r *Router) resolveDefault(available map[string]string) (string, bool) {
	allAliases := append([]string{}, r.defaultAliases...)
	allAliases = append(allAliases, r.defaultModel)
	for _, a := range allAliases {
		if canon, ok := r.lookupCanonical(a, available); ok {
			return canon, true
		}
	}
	return "", false
}

func (r *Router) lookupCanonical(candidate string, available map[string]string) (string, bool) {
	if candidate == "" {
		return "", false
	}
	if v, ok := available[strings.ToLower(candidate)]; ok {
		return v, true
	}
	nc := normalizeModelID(candidate)
	for k, v := range available {
		if normalizeModelID(k) == nc || normalizeModelID(v) == nc {
			return v, true
		}
	}
	return "", false
}

func normalizeModelID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(":", "", "-", "", "_", "", ".", "")
	return repl.Replace(s)
}

func toAvailableMap(available []string) map[string]string {
	m := map[string]string{}
	for _, v := range available {
		if strings.TrimSpace(v) == "" {
			continue
		}
		m[strings.ToLower(v)] = v
	}
	return m
}

func (r *Router) currentSnapshot() inventorySnapshot {
	raw := r.inventory.Load()
	if raw == nil {
		return inventorySnapshot{Available: map[string]string{}, UpdatedAt: time.Time{}, Stale: true}
	}
	snap, ok := raw.(inventorySnapshot)
	if !ok {
		return inventorySnapshot{Available: map[string]string{}, UpdatedAt: time.Time{}, Stale: true}
	}
	if snap.Available == nil {
		snap.Available = map[string]string{}
	}
	return snap
}

func (r *Router) setSnapshot(models []string, stale bool) {
	r.inventory.Store(inventorySnapshot{
		Available: toAvailableMap(models),
		UpdatedAt: time.Now().UTC(),
		Stale:     stale,
	})
}

func (r *Router) reconcileLoop(ctx context.Context) {
	r.reconcileOnce(ctx)
	ticker := time.NewTicker(r.cfg.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx)
			if r.cfg.ReconcileJitter > 0 {
				jitter := time.Duration(rand.Int63n(int64(r.cfg.ReconcileJitter)))
				select {
				case <-ctx.Done():
					return
				case <-time.After(jitter):
				}
			}
		}
	}
}

func (r *Router) reconcileOnce(ctx context.Context) {
	if r.control == nil {
		return
	}
	models, err := r.control.ListModels(ctx)
	if err != nil {
		s := r.currentSnapshot()
		s.Stale = true
		r.inventory.Store(s)
		log.Printf("jit inventory reconcile failed: %v", err)
		return
	}
	r.setSnapshot(models, false)
	r.clearResolvedInFlight(models)
	r.cleanInventoryIfNeeded(ctx)
}

func (r *Router) triggerBackgroundPull(model string) bool {
	model = strings.TrimSpace(model)
	if !r.cfg.JITInventoryEnabled || r.control == nil || model == "" {
		return false
	}
	key := strings.ToLower(model)
	if raw, ok := r.pullRecent.Load(key); ok {
		if ts, ok := raw.(time.Time); ok && time.Since(ts) < 30*time.Second {
			return false
		}
	}
	if _, loaded := r.pullInFlight.LoadOrStore(key, true); loaded {
		return false
	}
	r.pullRecent.Store(key, time.Now().UTC())
	go func() {
		defer r.pullInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), r.cfg.PullTimeout)
		defer cancel()
		if err := r.control.PullModel(ctx, model); err != nil {
			log.Printf("jit pull failed for %s: %v", model, err)
			return
		}
		r.reconcileOnce(context.Background())
	}()
	return true
}

func (r *Router) clearResolvedInFlight(models []string) {
	if len(models) == 0 {
		return
	}
	current := map[string]struct{}{}
	for _, m := range models {
		current[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
	}
	r.pullInFlight.Range(func(key, value any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		if _, exists := current[k]; exists {
			r.pullInFlight.Delete(k)
		}
		return true
	})
}

func (r *Router) cleanInventoryIfNeeded(ctx context.Context) {
	if !r.cfg.JITInventoryEnabled || !r.cfg.PruneEnabled || r.control == nil {
		return
	}
	snap := r.currentSnapshot()
	if len(snap.Available) == 0 {
		return
	}

	usedBytes, totalBytes, available, statsErr := r.control.HostStats(ctx)
	statsAvailable := statsErr == nil && available && totalBytes > 0
	if statsAvailable {
		usage := float64(usedBytes) / float64(totalBytes)
		if usage <= r.cfg.StorageHighWatermark {
			return
		}
	} else if len(snap.Available) <= r.cfg.MaxModels {
		return
	}

	candidates := r.prunableModels(snap.Available)
	for _, modelID := range candidates {
		if statsAvailable {
			curUsed, curTotal, curAvail, err := r.control.HostStats(ctx)
			if err == nil && curAvail && curTotal > 0 {
				usage := float64(curUsed) / float64(curTotal)
				if usage <= r.cfg.StorageTargetWatermark {
					break
				}
			}
		} else {
			if len(snap.Available) <= r.cfg.MaxModels {
				break
			}
		}
		if err := r.control.DeleteModel(ctx, modelID); err != nil {
			log.Printf("jit prune failed for %s: %v", modelID, err)
			continue
		}
		delete(snap.Available, strings.ToLower(modelID))
		r.lastUsedMu.Lock()
		delete(r.lastUsed, strings.ToLower(modelID))
		r.lastUsedMu.Unlock()
	}
	// keep internal snapshot in sync after prune decisions
	models := make([]string, 0, len(snap.Available))
	for _, m := range snap.Available {
		models = append(models, m)
	}
	r.setSnapshot(models, false)
}

func (r *Router) prunableModels(available map[string]string) []string {
	protected := r.protectedModels()
	type pair struct {
		id       string
		lastUsed time.Time
	}
	items := make([]pair, 0, len(available))

	r.lastUsedMu.Lock()
	defer r.lastUsedMu.Unlock()
	for _, id := range available {
		if _, keep := protected[strings.ToLower(id)]; keep {
			continue
		}
		items = append(items, pair{id: id, lastUsed: r.lastUsed[strings.ToLower(id)]})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].lastUsed.Before(items[j].lastUsed)
	})
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}

func (r *Router) protectedModels() map[string]struct{} {
	out := map[string]struct{}{}
	if strings.TrimSpace(r.defaultModel) != "" {
		out[strings.ToLower(strings.TrimSpace(r.defaultModel))] = struct{}{}
	}
	if strings.TrimSpace(r.fallbackDefault) != "" {
		out[strings.ToLower(strings.TrimSpace(r.fallbackDefault))] = struct{}{}
	}
	for _, m := range r.idealModels {
		if strings.TrimSpace(m) != "" {
			out[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
		}
	}
	return out
}

func (r *Router) markModelUsed(model string) {
	if strings.TrimSpace(model) == "" {
		return
	}
	r.lastUsedMu.Lock()
	r.lastUsed[strings.ToLower(model)] = time.Now().UTC()
	r.lastUsedMu.Unlock()
}
