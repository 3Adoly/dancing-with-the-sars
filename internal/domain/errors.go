package domain

import "errors"

// Sentinel errors. Wrap them with fmt.Errorf("...: %w", ErrX); httpapi maps them to HTTP codes.
var (
	ErrNotFound        = errors.New("not found")
	ErrInvalid         = errors.New("invalid argument")
	ErrUpstream        = errors.New("upstream error")
	ErrUpstreamTimeout = errors.New("upstream timeout")
	ErrDisabled        = errors.New("feature disabled")
	ErrNotReady        = errors.New("not ready")
)
