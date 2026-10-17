package config

import (
	"strings"
	"testing"
	"time"
)

func resetEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://synthetic-db")
	for _, name := range []string{"HTTP_ADDR", "LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "LLM_TIMEOUT"} {
		t.Setenv(name, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	resetEnvironment(t)
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.DatabaseURL != "postgres://synthetic-db" || configuration.HTTPAddr != "127.0.0.1:8080" {
		t.Fatal("existing configuration defaults changed")
	}
	if configuration.LLM.BaseURL != "https://openrouter.ai/api/v1" || configuration.LLM.Model != "google/gemma-4-31b-it:free" {
		t.Fatal("incorrect initial endpoint/model defaults")
	}
	if configuration.LLM.Timeout != 30*time.Second || configuration.LLM.APIKey != "" {
		t.Fatal("incorrect timeout/key defaults")
	}
}

func TestLoadOverrides(t *testing.T) {
	resetEnvironment(t)
	t.Setenv("HTTP_ADDR", ":8090")
	t.Setenv("LLM_BASE_URL", "http://localhost:11434/v1/")
	t.Setenv("LLM_MODEL", "local-model")
	t.Setenv("LLM_API_KEY", "synthetic-key")
	t.Setenv("LLM_TIMEOUT", "1m30s")
	configuration, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HTTPAddr != ":8090" || configuration.LLM.BaseURL != "http://localhost:11434/v1/" || configuration.LLM.Model != "local-model" {
		t.Fatal("endpoint/model override ignored")
	}
	if configuration.LLM.Timeout != 90*time.Second || configuration.LLM.APIKey != "synthetic-key" {
		t.Fatal("timeout/key override ignored")
	}
}

func TestLoadInvalidSettings(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "DATABASE_URL", value: ""},
		{name: "HTTP_ADDR", value: "invalid"},
		{name: "LLM_BASE_URL", value: "relative/v1"},
		{name: "LLM_BASE_URL", value: "ftp://localhost/v1"},
		{name: "LLM_BASE_URL", value: "https:///v1"},
		{name: "LLM_BASE_URL", value: "http://localhost:bad/v1"},
		{name: "LLM_BASE_URL", value: "https://user:synthetic-key@localhost/v1"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1?key=synthetic-key"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1?"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1#synthetic-key"},
		{name: "LLM_BASE_URL", value: "https://localhost/v1#"},
		{name: "LLM_MODEL", value: " \t"},
		{name: "LLM_API_KEY", value: "synthetic-key\nheader"},
		{name: "LLM_TIMEOUT", value: "synthetic-key"},
		{name: "LLM_TIMEOUT", value: "30"},
		{name: "LLM_TIMEOUT", value: "0s"},
		{name: "LLM_TIMEOUT", value: "-1s"},
		{name: "LLM_TIMEOUT", value: "999999999999h"},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+test.value, func(t *testing.T) {
			resetEnvironment(t)
			t.Setenv(test.name, test.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), test.name) {
				t.Fatalf("want safe setting error for %s, got %v", test.name, err)
			}
			if strings.Contains(err.Error(), "synthetic-key") {
				t.Fatal("configuration error leaked input")
			}
		})
	}
}
