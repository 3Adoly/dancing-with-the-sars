package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "" || c.HTTPAddr != ":8080" || c.MinYear != 1900 || c.RequestTimeout != 10*time.Second {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if len(c.CORSOrigins) != 2 || c.ChatEnabled || c.ChatConfigured() {
		t.Errorf("cors/chat defaults wrong: %+v", c)
	}
}

func TestInvalidValues(t *testing.T) {
	_, err := FromEnv(env(map[string]string{"MIN_YEAR": "abc", "REQUEST_TIMEOUT": "10", "SAR_WINDOW_DAYS": "500"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"MIN_YEAR", "REQUEST_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %s", err, want)
		}
	}
	if _, err := FromEnv(env(map[string]string{"SAR_WINDOW_DAYS": "500"})); err == nil {
		t.Error("SAR_WINDOW_DAYS=500 should fail validation")
	}
}

func TestChatConfigured(t *testing.T) {
	c, _ := FromEnv(env(map[string]string{"CHAT_ENABLED": "true", "GROQ_API_KEY": "k", "GROQ_MODEL": "m"}))
	if !c.ChatConfigured() {
		t.Error("expected chat configured")
	}
	c, _ = FromEnv(env(map[string]string{"CHAT_ENABLED": "true"}))
	if c.ChatConfigured() {
		t.Error("no key → chat must not be configured")
	}
}

func TestSARMissions(t *testing.T) {
	ms := SARMissions()
	seen := map[string]bool{}
	var prev time.Time
	now := time.Now()
	for _, m := range ms {
		if seen[m.ID] {
			t.Errorf("duplicate id %s", m.ID)
		}
		seen[m.ID] = true
		from, to, err := MissionRange(m, now)
		if err != nil {
			t.Fatalf("%s: %v", m.ID, err)
		}
		if to.Before(from) {
			t.Errorf("%s: to before from", m.ID)
		}
		if from.Before(prev) {
			t.Errorf("%s: table not sorted by From", m.ID)
		}
		prev = from
		if m.Searchable != (len(m.ASFParams) > 0) {
			t.Errorf("%s: searchable must have ASF params and vice versa", m.ID)
		}
	}
	for _, id := range []string{"seasat", "alos", "sentinel1", "nisar", "saocom"} {
		if !seen[id] {
			t.Errorf("missing mission %s", id)
		}
	}
}
