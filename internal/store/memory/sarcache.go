// Package memory holds in-process store implementations for running without a database.
// Like store/postgres it imports only domain + store and is imported only by cmd/* (rule R3).
package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/dwts/backend/internal/domain"
	"github.com/dwts/backend/internal/store"
)

// SARCache is a process-local store.SARCache (no-DB mode). Lost on restart; the Postgres
// sar_cache table (A2) replaces it in production.
type SARCache struct {
	mu sync.RWMutex
	m  map[int64]domain.SARResult
}

var _ store.SARCache = (*SARCache)(nil)

// NewSARCache creates an empty cache.
func NewSARCache() *SARCache { return &SARCache{m: map[int64]domain.SARResult{}} }

// GetSAR returns the cached result or domain.ErrNotFound.
func (c *SARCache) GetSAR(_ context.Context, id int64) (domain.SARResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.m[id]
	if !ok {
		return domain.SARResult{}, fmt.Errorf("sar cache %d: %w", id, domain.ErrNotFound)
	}
	return r, nil
}

// PutSAR stores (upserts) a result.
func (c *SARCache) PutSAR(_ context.Context, r domain.SARResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[r.IncidentID] = r
	return nil
}
