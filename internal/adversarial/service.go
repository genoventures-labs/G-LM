package adversarial

import (
	"context"
	"strconv"
	"strings"

	"github.com/mike/cognitive-llm/internal/model"
)

type Upstream interface {
	ChatCompletions(ctx context.Context, req model.ChatCompletionRequest) (model.ChatCompletionResponse, error)
}

type Config struct {
	Enabled                   bool
	DefaultRounds             int
	MaxRounds                 int
	ConstraintBreakingEnabled bool
	ConstraintBreakingLevel   string
}

type Service struct {
	cfg Config
}

type Result struct {
	Applied bool
	Rounds  int
	Output  model.ChatCompletionResponse
}

func New(cfg Config) *Service {
	if cfg.DefaultRounds <= 0 {
		cfg.DefaultRounds = 2
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 4
	}
	cfg.ConstraintBreakingLevel = normalizeLevel(cfg.ConstraintBreakingLevel)
	return &Service{cfg: cfg}
}

func (s *Service) Enabled(req model.ChatCompletionRequest) bool {
	if req.Reasoning != nil && req.Reasoning.AdversarialSelfPlayEnabled {
		return true
	}
	return s.cfg.Enabled
}

func (s *Service) Execute(
	ctx context.Context,
	up Upstream,
	req model.ChatCompletionRequest,
	baseResp model.ChatCompletionResponse,
	modelID string,
) (Result, error) {
	rounds := s.cfg.DefaultRounds
	if req.Reasoning != nil && req.Reasoning.AdversarialRounds > 0 {
		rounds = req.Reasoning.AdversarialRounds
	}
	if rounds > s.cfg.MaxRounds {
		rounds = s.cfg.MaxRounds
	}
	if rounds < 1 {
		rounds = 1
	}
	cur := baseResp
	for i := 1; i <= rounds; i++ {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		revisionReq := req
		revisionReq.Model = modelID
		revisionReq.Messages = buildRoundMessages(req, cur, i)
		resp, err := up.ChatCompletions(ctx, revisionReq)
		if err != nil {
			return Result{}, err
		}
		cur = resp
	}
	return Result{Applied: true, Rounds: rounds, Output: cur}, nil
}

func buildRoundMessages(req model.ChatCompletionRequest, current model.ChatCompletionResponse, round int) []model.Message {
	base := []model.Message{
		{
			Role:    "system",
			Content: "Adversarial self-play loop. Simulate attacker critique, defender revision, then auditor tighten pass. Return one final improved answer.",
		},
		{
			Role: "user",
			Content: "Round " + strconvI(round) + ": current answer:\n" +
				firstContent(current) +
				"\n\nOriginal user request:\n" +
				joinUser(req.Messages),
		},
	}
	return base
}

func firstContent(resp model.ChatCompletionResponse) string {
	if len(resp.Choices) == 0 {
		return ""
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content)
}

func joinUser(msgs []model.Message) string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if strings.EqualFold(m.Role, "user") {
			out = append(out, strings.TrimSpace(m.Content))
		}
	}
	return strings.Join(out, "\n")
}

func normalizeLevel(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "low"
	}
}

func strconvI(v int) string {
	return strconv.Itoa(v)
}
