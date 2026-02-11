package audit

import (
	"context"
	"time"

	"github.com/mike/cognitive-llm/internal/model"
	"github.com/mike/cognitive-llm/internal/store"
)

type Service struct {
	store store.Store
}

func NewService(st store.Store) *Service {
	return &Service{store: st}
}

func (s *Service) Record(ctx context.Context, ev model.AuditEvent) {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = model.FlexTime{Time: time.Now().UTC()}
	}
	_, _ = s.store.CreateAuditEvent(ctx, ev)
}
