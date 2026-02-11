package orchestrator

import (
	"errors"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/policy"
	"github.com/mike/cognitive-llm/internal/state"
)

var ErrNoAllowedAvailableModel = errors.New("no allowed available model for auto routing")

type Decision struct {
	RequestedModel string
	ChosenModel    string
	Reason         string
	TaskClass      string
	FallbackUsed   bool
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
}

func NewRouter(defaultModel string, aliases []string, fallbackDefault string) *Router {
	if len(aliases) == 0 {
		aliases = []string{defaultModel}
	}
	taskMap := map[TaskClass][]string{
		TaskGeneral:                  {defaultModel, fallbackDefault, "mistral:7b", "gemma2:2b"},
		TaskCodingReasoning:          {"mistral:7b", defaultModel, fallbackDefault},
		TaskLightQA:                  {"llama3.2:1b", "qwen2.5:3b-instruct", "gemma2:2b"},
		TaskExtractionClassification: {"qwen2.5:3b-instruct", "phi3:mini", "gemma2:2b"},
	}
	return &Router{defaultModel: defaultModel, defaultAliases: aliases, fallbackDefault: fallbackDefault, taskMap: taskMap}
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
	availableMap := map[string]string{}
	for _, m := range available {
		availableMap[strings.ToLower(m)] = m
	}
	if len(availableMap) == 0 {
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

	resolvedDefault, defaultResolved := r.resolveDefault(availableMap)
	candidates := append([]string{}, r.taskMap[class]...)
	if class != TaskGeneral {
		// Ensure default candidates still appear for non-general routes as backup.
		candidates = append(candidates, resolvedDefault, r.fallbackDefault, r.defaultModel)
	}

	chosen, reason, fallbackUsed, ok := r.firstUsable(candidates, pol, availableMap, resolvedDefault, defaultResolved, class)
	if ok {
		d.ChosenModel = chosen
		d.Reason = reason
		d.FallbackUsed = fallbackUsed
		return d, nil
	}

	// Final fallback: policy primary model.
	if cand, ok := r.lookupCanonical(pol.PrimaryModel, availableMap); ok && policy.Allowed(cand, pol) {
		d.ChosenModel = cand
		d.Reason = "auto.fallback.policy_primary"
		d.FallbackUsed = true
		return d, nil
	}

	// Final fallback: first allowed available model.
	for _, avail := range availableMap {
		if policy.Allowed(avail, pol) {
			d.ChosenModel = avail
			d.Reason = "auto.fallback.first_allowed_available"
			d.FallbackUsed = true
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

	availableMap := map[string]string{}
	for _, m := range available {
		availableMap[strings.ToLower(m)] = m
	}
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
