package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/dwts/backend/internal/domain"
)

func TestSARCache(t *testing.T) {
	c := NewSARCache()
	ctx := context.Background()
	if _, err := c.GetSAR(ctx, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miss: %v", err)
	}
	_ = c.PutSAR(ctx, domain.SARResult{IncidentID: 1, Coverage: "ok"})
	if r, err := c.GetSAR(ctx, 1); err != nil || r.Coverage != "ok" {
		t.Fatalf("hit: %+v %v", r, err)
	}
}
