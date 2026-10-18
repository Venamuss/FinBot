package conversation

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Venamuss/FinBot/internal/expense"
)

func TestConversationDiagnosticsContainOnlySafeMetadata(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})
	orchestrator, model, _ := setup(t)
	now := time.Now()
	orchestrator.now = func() time.Time {
		return now
	}
	handle(t, orchestrator, model, 1, 101, "private-user-message", "clarify", `{"field":"amount","category":"еда","comment":"private-comment"}`)
	handle(t, orchestrator, model, 2, 101, "private-amount", "answer_clarification", `{"value":"87654321.001"}`)
	handle(t, orchestrator, model, 3, 101, "13", "choose_expense", `{"choice":"cancel"}`)
	handle(t, orchestrator, model, 4, 101, "отмена", "cancel_request", `{}`)
	handle(t, orchestrator, model, 5, 101, "private-currency-message$", "create_expense", `{"amount":"87654321","spent_at":"today","comment":"private-expense"}`)
	now = now.Add(30 * time.Minute)
	handle(t, orchestrator, model, 6, 101, "private-reply", "answer_clarification", `{"value":"RUB"}`)
	handle(t, orchestrator, model, 7, 101, "private-invalid-category", "create_expense", `{"amount":"5","category":"private-category","spent_at":"today"}`)
	handle(t, orchestrator, model, 8, 101, "private-invalid-field", "clarify", `{"field":"private-field"}`)
	logs := output.String()
	for _, secret := range []string{"private-", "87654321", "You interpret", "pending_values", "pending_request"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("conversation logs leaked %s", secret)
		}
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["component"] != "conversation" || record["update_id"] == nil {
			t.Fatal("missing safe correlation metadata")
		}
		for _, key := range []string{"state_type", "from_state", "to_state", "tool", "clarification_type", "validation_category", "error_category", "reset_reason"} {
			if record[key] != nil {
				seen[key] = true
			}
		}
		if record["reset_reason"] == "expired" {
			seen["expiration"] = true
		}
		if record["reset_reason"] == "canceled" {
			seen["cancellation"] = true
		}
	}
	for _, field := range []string{"state_type", "from_state", "to_state", "tool", "clarification_type", "validation_category", "error_category", "reset_reason", "expiration", "cancellation"} {
		if !seen[field] {
			t.Fatalf("missing diagnostic metadata %s", field)
		}
	}
}

func TestModelToolsMatchConversationStateAndCategoryDomain(t *testing.T) {
	orchestrator, model, _ := setup(t)
	request := orchestrator.request("synthetic", time.Now(), state{})
	for _, definition := range request.Tools {
		if contains([]string{"choose_expense", "answer_clarification", "cancel_request"}, definition.Function.Name) {
			t.Fatal("idle conversation exposed a pending-state tool")
		}
	}
	handle(t, orchestrator, model, 1, 101, "кофе", "clarify", `{"field":"amount","comment":"кофе"}`)
	request = orchestrator.request("350", time.Now(), orchestrator.states[101])
	if strings.Contains(request.Messages[0].Content, `"choices"`) || !strings.Contains(request.Messages[0].Content, `"state_type":"awaiting_amount"`) {
		t.Fatal("field clarification advertised record choices")
	}
	available := make(map[string]bool)
	for _, definition := range request.Tools {
		available[definition.Function.Name] = true
		if definition.Function.Name != "create_expense" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(definition.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		categories := schema.Properties["category"].Enum
		if len(categories) != 7 {
			t.Fatal("incorrect canonical category count")
		}
		for _, category := range categories {
			if _, err := expense.ParseCategory(category); err != nil {
				t.Fatal("tool category is rejected by the domain")
			}
		}
		if !contains(categories, "другое") {
			t.Fatal("category fallback missing from tool schema")
		}
	}
	if available["choose_expense"] || !available["answer_clarification"] || !available["cancel_request"] {
		t.Fatal("clarification tools do not match the actual pending state")
	}
}
