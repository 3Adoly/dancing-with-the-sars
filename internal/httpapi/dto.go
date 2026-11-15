package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/dwts/backend/internal/domain"
)

// writeJSON writes v as JSON with the given status. HTML escaping is off (URLs stay readable).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // the client may have gone away; nothing useful to do
}

// --- Response DTOs. JSON tags must match api/openapi.yaml and api/examples/*.json. ---

// HealthResponse is E1 GET /healthz.
type HealthResponse struct {
	Status      string `json:"status"` // ok | degraded
	DB          string `json:"db"`     // ok | unavailable | not_configured
	CubeVersion int64  `json:"cubeVersion"`
	Stories     int    `json:"stories"`
	UptimeSec   int64  `json:"uptimeSec"`
	Version     string `json:"version,omitempty"`
}

// IncidentTypesResponse is E3.
type IncidentTypesResponse struct {
	Types []domain.TypeInfo `json:"types"`
}

// GridResponse is E6.
type GridResponse struct {
	CellDeg float64           `json:"cellDeg"`
	Unit    string            `json:"unit"`
	Max     int64             `json:"max"`
	Cells   []domain.GridCell `json:"cells"`
}

// IncidentsResponse is E7.
type IncidentsResponse struct {
	Items      []domain.IncidentSummary `json:"items"`
	NextCursor string                   `json:"nextCursor"`
	Limit      int                      `json:"limit"`
}

// SearchResponse is E9.
type SearchResponse struct {
	Q     string                   `json:"q"`
	Items []domain.IncidentSummary `json:"items"`
}

// StoriesResponse is E12.
type StoriesResponse struct {
	Stories []domain.StorySummary `json:"stories"`
}

// --- E15 chat (ADDITIONAL — NOT REQUIRED) ---

// ChatMessage is one chat turn.
type ChatMessage struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// ChatContext is the UI state sent with a question.
type ChatContext struct {
	Types              []domain.IncidentType `json:"types,omitempty"`
	From               int                   `json:"from,omitempty"`
	To                 int                   `json:"to,omitempty"`
	SelectedIncidentID int64                 `json:"selectedIncidentId,omitempty"`
	StoryID            string                `json:"storyId,omitempty"`
}

// ChatRequest is the E15 body.
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
	Context  *ChatContext  `json:"context,omitempty"`
	Stream   bool          `json:"stream,omitempty"`
}

// ChatCitation links an answer to an incident or story.
type ChatCitation struct {
	Kind  string `json:"kind"` // incident | story
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ChatAction is a UI action suggested by the assistant.
type ChatAction struct {
	Action  string                `json:"action"` // set_filters | fly_to | open_incident | open_story
	Types   []domain.IncidentType `json:"types,omitempty"`
	From    int                   `json:"from,omitempty"`
	To      int                   `json:"to,omitempty"`
	Lon     float64               `json:"lon,omitempty"`
	Lat     float64               `json:"lat,omitempty"`
	Zoom    float64               `json:"zoom,omitempty"`
	ID      int64                 `json:"id,omitempty"`
	StoryID string                `json:"storyId,omitempty"`
}

// ChatToolTrace records one tool call made by the assistant.
type ChatToolTrace struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// ChatResponse is the E15 response.
type ChatResponse struct {
	Answer    string          `json:"answer"`
	Citations []ChatCitation  `json:"citations"`
	Actions   []ChatAction    `json:"actions"`
	ToolTrace []ChatToolTrace `json:"toolTrace"`
	Provider  string          `json:"provider"`
}
