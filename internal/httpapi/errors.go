package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dwts/backend/internal/domain"
)

// Error codes (Plan_Detailed §8.1). not_implemented is used only while endpoints are being built.
const (
	CodeInvalidParam     = "invalid_param"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal"
	CodeNotImplemented   = "not_implemented"
	CodeUpstreamError    = "upstream_error"
	CodeNotReady         = "not_ready"
	CodeChatDisabled     = "chat_disabled"
	CodeChatUnavailable  = "chat_unavailable"
	CodeUpstreamTimeout  = "upstream_timeout"
)

// APIError is an error that already knows its HTTP status and public message.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// BadParam builds a 400 invalid_param error naming the offending parameter.
func BadParam(param, msg string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParam, Message: msg,
		Details: map[string]any{"param": param}}
}

// ErrorBody is the single error JSON shape used by every 4xx/5xx response.
type ErrorBody struct {
	Error     ErrorDetail `json:"error"`
	RequestID string      `json:"requestId"`
}

// ErrorDetail is the inner object of ErrorBody.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// toAPIError maps any error to an APIError. Unknown errors become 500 internal
// with a generic message (never leak SQL or stack traces).
func toAPIError(err error) *APIError {
	var ae *APIError
	switch {
	case errors.As(err, &ae):
		return ae
	case errors.Is(err, domain.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "resource not found"}
	case errors.Is(err, domain.ErrInvalid):
		return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidParam, Message: err.Error()}
	case errors.Is(err, domain.ErrUpstreamTimeout), errors.Is(err, context.DeadlineExceeded):
		return &APIError{Status: http.StatusGatewayTimeout, Code: CodeUpstreamTimeout, Message: "the request timed out"}
	case errors.Is(err, domain.ErrUpstream):
		return &APIError{Status: http.StatusBadGateway, Code: CodeUpstreamError, Message: "an upstream service failed"}
	case errors.Is(err, domain.ErrDisabled):
		return &APIError{Status: http.StatusServiceUnavailable, Code: CodeChatDisabled, Message: "this feature is disabled"}
	case errors.Is(err, domain.ErrNotReady):
		return &APIError{Status: http.StatusServiceUnavailable, Code: CodeNotReady, Message: "the service is starting, try again shortly"}
	default:
		return &APIError{Status: http.StatusInternalServerError, Code: CodeInternal, Message: "internal error"}
	}
}

// writeError writes the standard error JSON. 5xx errors are logged with the original cause.
// A canceled request (client went away) is logged at debug level and nothing is written.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		loggerFrom(r.Context()).Debug("request canceled by client", "path", r.URL.Path)
		return
	}
	ae := toAPIError(err)
	if ae.Status >= 500 {
		loggerFrom(r.Context()).Error("request failed", "path", r.URL.Path, "status", ae.Status, "code", ae.Code, "err", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, ae.Status, ErrorBody{
		Error:     ErrorDetail{Code: ae.Code, Message: ae.Message, Details: ae.Details},
		RequestID: RequestIDFrom(r.Context()),
	})
}

// notImplemented answers 501 with the standard error shape so the frontend sees the final format.
func notImplemented(task string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, &APIError{Status: http.StatusNotImplemented, Code: CodeNotImplemented,
			Message: "endpoint not implemented yet (" + task + "); see api/examples for the response shape",
			Details: map[string]any{"task": task}})
	}
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: "route not found"})
}

func handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, &APIError{Status: http.StatusMethodNotAllowed, Code: CodeMethodNotAllowed, Message: "method not allowed"})
}

// loggerFrom returns the request-scoped logger (set by the logging middleware) or the default.
func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
