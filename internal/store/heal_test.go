package store

import (
	"testing"
	"time"
)

func TestReplacementCircuitBreakerPersistsAcrossRestart(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateProvisioningNode("node-heal", "node-heal.example.com", "node-heal.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := store.db.Create(&NodeHealEvent{
			NodeID: "node-heal", Action: "auto_heal", Reason: "test", CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		}).Error; err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	claimed, err := store.TryClaimNodeReplacement("node-heal", "manual_replace", "test", now, 0, 2)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed {
		t.Fatal("third replacement within 24h must be blocked")
	}
	if count, _ := store.CountNodeHealEvents("node-heal", now.Add(-24*time.Hour)); count != 2 {
		t.Fatalf("event count = %d, want 2", count)
	}

	// A new calendar window (events older than 24h) must allow one new attempt.
	later := now.Add(25 * time.Hour)
	claimed, err = store.TryClaimNodeReplacement("node-heal", "manual_replace", "test", later, 0, 2)
	if err != nil {
		t.Fatalf("claim later: %v", err)
	}
	if !claimed {
		t.Fatal("replacement should be allowed after the 24h window")
	}
}
