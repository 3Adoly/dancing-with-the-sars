// Package config loads and validates environment variables (Plan_Detailed §2).
// Shared file: Ahmed owns the API block, Mohammed the ingest block.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/dwts/backend/internal/domain"
)

// LLMProviderConfig configures one OpenAI-compatible LLM endpoint (ADDITIONAL — NOT REQUIRED).
type LLMProviderConfig struct{ BaseURL, APIKey, Model string }

// Config is the whole runtime configuration.
type Config struct {
	// --- Shared ---
	DatabaseURL string // empty = no database (API runs degraded)
	LogLevel    string
	LogFormat   string
	MinYear     int
	StoriesDir  string

	// --- API fields (Ahmed) ---
	HTTPAddr           string
	CORSOrigins        []string
	RequestTimeout     time.Duration
	DBMaxConns         int32
	CubeReloadInterval time.Duration
	StaticDir          string
	StatsFixture       string // DEV-ONLY: JSON fixture for E2–E5 when there is no DB (until A2)
	IncidentsFixture   string // DEV-ONLY: JSON incidents for E10 when there is no DB (until A2)
	ASFALOSLevel       string // processingLevel used for ALOS searches (L1.5 verified 2026-11-15)
	ASFBaseURL         string
	ASFTimeout         time.Duration
	SARWindowDays      int
	SARCacheTTL        time.Duration
	SARDBFresh         time.Duration
	ChatEnabled        bool // ADDITIONAL — NOT REQUIRED
	LLMProviders       []string
	LLM                map[string]LLMProviderConfig
	ChatRatePerMin     int

	// --- Ingest fields (Mohammed) ---
	// RawDir, BoundariesPath, USGSMinMag, IngestWorkers, FIRMSMapKey, FIRMSProduct …
	// are added by Mohammed in M1.
}

// Load reads the environment (and ./.env if present; real env vars win) and validates it.
func Load() (Config, error) {
	_ = godotenv.Load() // optional .env; missing file is fine
	return FromEnv(os.Getenv)
}

// FromEnv builds a Config from a getenv function (testable without touching os env).
func FromEnv(getenv func(string) string) (Config, error) {
	p := parser{get: getenv}
	c := Config{
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
		LogLevel:    p.getStr("LOG_LEVEL", "info"),
		LogFormat:   p.getStr("LOG_FORMAT", "text"),
		MinYear:     p.getInt("MIN_YEAR", 1900),
		StoriesDir:  p.getStr("STORIES_DIR", "./data/stories"),

		HTTPAddr:           p.getStr("HTTP_ADDR", ":8080"),
		CORSOrigins:        p.getList("CORS_ORIGINS", "http://localhost:5173,http://localhost:3000"),
		RequestTimeout:     p.getDur("REQUEST_TIMEOUT", 10*time.Second),
		DBMaxConns:         int32(p.getInt("DB_MAX_CONNS", 10)),
		CubeReloadInterval: p.getDur("CUBE_RELOAD_INTERVAL", 60*time.Second),
		StaticDir:          p.getStr("STATIC_DIR", ""),
		StatsFixture:       p.getStr("STATS_FIXTURE", ""),
		IncidentsFixture:   p.getStr("INCIDENTS_FIXTURE", ""),
		ASFALOSLevel:       p.getStr("ASF_ALOS_LEVEL", "L1.5"),
		ASFBaseURL:         p.getStr("ASF_BASE_URL", "https://api.daac.asf.alaska.edu/services/search/param"),
		ASFTimeout:         p.getDur("ASF_TIMEOUT", 8*time.Second),
		SARWindowDays:      p.getInt("SAR_WINDOW_DAYS", 45),
		SARCacheTTL:        p.getDur("SAR_CACHE_TTL", 24*time.Hour),
		SARDBFresh:         p.getDur("SAR_DB_FRESH", 168*time.Hour),
		ChatEnabled:        p.getBool("CHAT_ENABLED", false),
		LLMProviders:       p.getList("LLM_PROVIDERS", "groq,gemini,openrouter"),
		ChatRatePerMin:     p.getInt("CHAT_RATE_PER_MIN", 10),
	}
	c.LLM = map[string]LLMProviderConfig{
		"groq":       {BaseURL: "https://api.groq.com/openai/v1", APIKey: getenv("GROQ_API_KEY"), Model: getenv("GROQ_MODEL")},
		"gemini":     {BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai/", APIKey: getenv("GEMINI_API_KEY"), Model: getenv("GEMINI_MODEL")},
		"openrouter": {BaseURL: "https://openrouter.ai/api/v1", APIKey: getenv("OPENROUTER_API_KEY"), Model: getenv("OPENROUTER_MODEL")},
		"ollama":     {BaseURL: getenv("OLLAMA_BASE_URL"), Model: getenv("OLLAMA_MODEL")},
	}
	if len(p.errs) > 0 {
		return c, errors.Join(p.errs...)
	}
	return c, c.Validate()
}

// Validate checks ranges. It never logs secrets.
func (c Config) Validate() error {
	var errs []error
	if c.HTTPAddr == "" {
		errs = append(errs, errors.New("HTTP_ADDR must not be empty"))
	}
	if c.MinYear < 1800 || c.MinYear > 2100 {
		errs = append(errs, fmt.Errorf("MIN_YEAR=%d out of range 1800..2100", c.MinYear))
	}
	if c.SARWindowDays < 1 || c.SARWindowDays > 180 {
		errs = append(errs, fmt.Errorf("SAR_WINDOW_DAYS=%d out of range 1..180", c.SARWindowDays))
	}
	if c.DBMaxConns < 1 || c.DBMaxConns > 100 {
		errs = append(errs, fmt.Errorf("DB_MAX_CONNS=%d out of range 1..100", c.DBMaxConns))
	}
	if c.RequestTimeout <= 0 || c.ASFTimeout <= 0 || c.CubeReloadInterval <= 0 {
		errs = append(errs, errors.New("REQUEST_TIMEOUT, ASF_TIMEOUT and CUBE_RELOAD_INTERVAL must be > 0"))
	}
	if c.ChatRatePerMin < 1 {
		errs = append(errs, fmt.Errorf("CHAT_RATE_PER_MIN=%d must be >= 1", c.ChatRatePerMin))
	}
	return errors.Join(errs...)
}

// SARMissionTable returns SARMissions() with config overrides applied (ALOS processing level).
func (c Config) SARMissionTable() []domain.SARMission {
	ms := SARMissions()
	for i := range ms {
		if ms[i].ID == "alos" && c.ASFALOSLevel != "" {
			ms[i].ASFParams = map[string]string{"platform": "ALOS", "processingLevel": c.ASFALOSLevel}
		}
	}
	return ms
}

// ChatConfigured reports whether chat is enabled AND at least one provider has a key (or Ollama a URL).
func (c Config) ChatConfigured() bool {
	if !c.ChatEnabled {
		return false
	}
	for _, name := range c.LLMProviders {
		p := c.LLM[name]
		if p.Model != "" && (p.APIKey != "" || (name == "ollama" && p.BaseURL != "")) {
			return true
		}
	}
	return false
}

type parser struct {
	get  func(string) string
	errs []error
}

func (p *parser) raw(k string) (string, bool) {
	v := strings.TrimSpace(p.get(k))
	return v, v != ""
}

func (p *parser) getStr(k, def string) string {
	if v, ok := p.raw(k); ok {
		return v
	}
	return def
}

func (p *parser) getInt(k string, def int) int {
	v, ok := p.raw(k)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s=%q is not an integer", k, v))
		return def
	}
	return n
}

func (p *parser) getBool(k string, def bool) bool {
	v, ok := p.raw(k)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s=%q is not a boolean", k, v))
		return def
	}
	return b
}

func (p *parser) getDur(k string, def time.Duration) time.Duration {
	v, ok := p.raw(k)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s=%q is not a duration (e.g. 10s, 24h)", k, v))
		return def
	}
	return d
}

func (p *parser) getList(k, def string) []string {
	v := p.getStr(k, def)
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
