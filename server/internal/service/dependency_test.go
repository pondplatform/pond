package service

import (
	"encoding/json"
	"testing"

	"github.com/pondplatform/pond/server/internal/model/db"
)

// --- buildContexts ---

func TestBuildContexts_UnmarshalsOutputsCorrectly(t *testing.T) {
	rawOutputs := map[string]json.RawMessage{
		"db":    json.RawMessage(`{"host":"db.internal","port":5432}`),
		"cache": json.RawMessage(`{"url":"redis://cache:6379"}`),
	}

	contexts, err := buildContexts(rawOutputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected 2 contexts, got %d", len(contexts))
	}
	if contexts["db"]["host"] != "db.internal" {
		t.Errorf("expected db.host='db.internal', got %v", contexts["db"]["host"])
	}
	if contexts["cache"]["url"] != "redis://cache:6379" {
		t.Errorf("expected cache.url, got %v", contexts["cache"]["url"])
	}
}

func TestBuildContexts_InvalidJSONReturnsError(t *testing.T) {
	_, err := buildContexts(map[string]json.RawMessage{"bad": json.RawMessage(`{not valid json}`)})
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestBuildContexts_EmptyInputReturnsEmptyMap(t *testing.T) {
	contexts, err := buildContexts(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contexts) != 0 {
		t.Errorf("expected empty contexts, got %d entries", len(contexts))
	}
}

// --- environmentProviderInput ---

func TestEnvironmentProviderInput_IncludesExpectedFields(t *testing.T) {
	env := &db.Environment{
		Name:                   "staging",
		Namespace:              "staging-ns",
		DefaultIngressBaseHost: "example.com",
	}
	result := environmentProviderInput(env)
	if result["name"] != "staging" {
		t.Errorf("expected name=staging, got %v", result["name"])
	}
	if result["namespace"] != "staging-ns" {
		t.Errorf("expected namespace, got %v", result["namespace"])
	}
	if result["defaultIngressBaseHost"] != "example.com" {
		t.Errorf("expected defaultIngressBaseHost, got %v", result["defaultIngressBaseHost"])
	}
}
