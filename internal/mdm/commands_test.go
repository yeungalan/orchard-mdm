package mdm

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSyncTokensIncludesRequiredTimestamp(t *testing.T) {
	const token = "test-declarations-token"

	var payload struct {
		SyncTokens struct {
			DeclarationsToken string `json:"DeclarationsToken"`
			Timestamp         string `json:"Timestamp"`
		} `json:"SyncTokens"`
	}

	if err := json.Unmarshal(SyncTokens(token), &payload); err != nil {
		t.Fatalf("decode SyncTokens: %v", err)
	}
	if got := payload.SyncTokens.DeclarationsToken; got != token {
		t.Errorf("DeclarationsToken = %q, want %q", got, token)
	}
	parsed, err := time.Parse(time.RFC3339, payload.SyncTokens.Timestamp)
	if err != nil {
		t.Fatalf("Timestamp must be RFC3339: %v", err)
	}
	if parsed.IsZero() {
		t.Fatal("Timestamp must not be zero")
	}
}
