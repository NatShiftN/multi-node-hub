package store

import (
	"path/filepath"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nodehub.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestNodeLifecyclePersistence(t *testing.T) {
	store := openTestStore(t)
	if err := store.CreateProvisioningNode("node-a", "node-a.example.com", "node-a.example.com"); err != nil {
		t.Fatalf("CreateProvisioningNode: %v", err)
	}
	now := time.Now().UTC()
	info := protocol.AgentInfo{
		ID:       "node-a",
		Hostname: "edge-a",
		OS:       "linux",
		Arch:     "amd64",
		Version:  "0.3.0",
		Plugins:  []string{"system_info", "port_forward"},
	}
	if err := store.MarkNodeOnline(info, "203.0.113.10", now); err != nil {
		t.Fatalf("MarkNodeOnline: %v", err)
	}
	node, err := store.GetNode("node-a")
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if node.Status != NodeStatusOnline || node.Name != "edge-a" || node.Address != "node-a.example.com" {
		t.Fatalf("unexpected node: %+v", node)
	}
	if node.Plugins != `["system_info","port_forward"]` {
		t.Fatalf("plugins = %s", node.Plugins)
	}

	if err := store.MarkNodeOffline("node-a", now.Add(time.Minute)); err != nil {
		t.Fatalf("MarkNodeOffline: %v", err)
	}
	node, _ = store.GetNode("node-a")
	if node.Status != NodeStatusOffline || node.LastSeen == nil {
		t.Fatalf("unexpected offline node: %+v", node)
	}

	if err := store.MarkAllNodesOffline(); err != nil {
		t.Fatalf("MarkAllNodesOffline: %v", err)
	}
}

func TestTokenAndRulePersistenceAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodehub.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := first.ReplaceToken("node-a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC()); err != nil {
		t.Fatalf("ReplaceToken: %v", err)
	}
	now := time.Now().UTC()
	rule := &PortForwardRule{
		RuleID:     "pf-test",
		NodeID:     "node-a",
		ListenPort: 22022,
		TargetHost: "10.0.0.5",
		TargetPort: 22,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := first.CreatePortForwardRule(rule); err != nil {
		t.Fatalf("CreatePortForwardRule: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	tokens, err := second.ListTokens()
	if err != nil || len(tokens) != 1 || tokens[0].NodeID != "node-a" {
		t.Fatalf("tokens = %+v, err = %v", tokens, err)
	}
	rules, err := second.ListPortForwardRules("node-a")
	if err != nil || len(rules) != 1 || rules[0].RuleID != "pf-test" {
		t.Fatalf("rules = %+v, err = %v", rules, err)
	}
	if err := second.DeletePortForwardRule("node-a", "pf-test"); err != nil {
		t.Fatalf("DeletePortForwardRule: %v", err)
	}
	rules, _ = second.ListPortForwardRules("node-a")
	if len(rules) != 0 {
		t.Fatalf("rules after delete = %+v", rules)
	}

	if err := second.SaveXrayConfig("node-a", `{"inbounds":[],"outbounds":[]}`); err != nil {
		t.Fatalf("SaveXrayConfig: %v", err)
	}
	xrayConfig, err := second.GetXrayConfig("node-a")
	if err != nil || xrayConfig.Config == "" {
		t.Fatalf("GetXrayConfig = %+v, err = %v", xrayConfig, err)
	}
	if err := second.DeleteXrayConfig("node-a"); err != nil {
		t.Fatalf("DeleteXrayConfig: %v", err)
	}
	if _, err := second.GetXrayConfig("node-a"); !IsNotFound(err) {
		t.Fatalf("GetXrayConfig after delete err = %v, want not found", err)
	}
}

func TestApplyTrafficUpdateAccumulatesDeltas(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	apply := func(session string, upload, download uint64) {
		t.Helper()
		err := store.ApplyTrafficUpdate("node-a", protocol.TrafficUpdate{
			SessionID: session,
			Timestamp: now,
			Users: []protocol.TrafficUserStat{
				{Email: "alice@example.com", UploadBytes: upload, DownloadBytes: download},
			},
		}, now)
		if err != nil {
			t.Fatalf("ApplyTrafficUpdate: %v", err)
		}
	}

	apply("session-1", 100, 200)
	apply("session-1", 150, 260)
	rows, err := store.ListUserTraffic("node-a")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
	if rows[0].UploadBytes != 150 || rows[0].DownloadBytes != 260 {
		t.Fatalf("unexpected totals: %+v", rows[0])
	}

	// Counter reset within the same session.
	apply("session-1", 10, 20)
	rows, _ = store.ListUserTraffic("node-a")
	if rows[0].UploadBytes != 160 || rows[0].DownloadBytes != 280 {
		t.Fatalf("unexpected reset totals: %+v", rows[0])
	}

	// New Xray session counts its cumulative counters in full.
	apply("session-2", 7, 9)
	rows, _ = store.ListUserTraffic("node-a")
	if rows[0].UploadBytes != 167 || rows[0].DownloadBytes != 289 {
		t.Fatalf("unexpected new-session totals: %+v", rows[0])
	}
}

func TestProbeFailureThresholdAndRecovery(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	if err := store.CreateProvisioningNode("node-p", "p.example.com", "p.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	if err := store.MarkNodeOnline(protocol.AgentInfo{ID: "node-p", Hostname: "p"}, "10.0.0.8", now); err != nil {
		t.Fatalf("MarkNodeOnline: %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		status, marked, err := store.RecordProbeFailure("node-p", 3, now)
		if err != nil || status != NodeStatusOnline || marked {
			t.Fatalf("attempt %d status=%s marked=%v err=%v", attempt, status, marked, err)
		}
	}
	status, marked, err := store.RecordProbeFailure("node-p", 3, now)
	if err != nil || status != NodeStatusNetworkError || !marked {
		t.Fatalf("threshold status=%s marked=%v err=%v", status, marked, err)
	}
	node, err := store.GetNode("node-p")
	if err != nil || node.NetworkErrorAt == nil || node.ProbeFailures != 3 {
		t.Fatalf("node after failure = %+v, err=%v", node, err)
	}
	if err := store.RecordProbeSuccess("node-p", true); err != nil {
		t.Fatalf("RecordProbeSuccess: %v", err)
	}
	node, _ = store.GetNode("node-p")
	if node.Status != NodeStatusOnline || node.ProbeFailures != 0 {
		t.Fatalf("node after recovery = %+v", node)
	}
}

func TestNodeTagsAndAllTagMatching(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	for _, id := range []string{"node-a", "node-b"} {
		if err := st.CreateProvisioningNode(id, id, id+".example.com"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := st.MarkNodeOnline(protocol.AgentInfo{ID: id, Hostname: id}, "10.0.0.8", now); err != nil {
			t.Fatalf("online %s: %v", id, err)
		}
	}
	if err := st.SetNodeTags("node-a", []string{"region:hk", "type:relay", "region:hk"}); err != nil {
		t.Fatalf("SetNodeTags a: %v", err)
	}
	if err := st.SetNodeTags("node-b", []string{"region:hk"}); err != nil {
		t.Fatalf("SetNodeTags b: %v", err)
	}
	matched, err := st.ListOnlineNodesWithAllTags([]string{"region:hk", "type:relay"})
	if err != nil || len(matched) != 1 || matched[0].NodeID != "node-a" {
		t.Fatalf("matched = %+v, err = %v", matched, err)
	}
	tags, err := st.ListNodeTags("node-a")
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags = %+v, err = %v", tags, err)
	}
	counts, err := st.ListTagCounts()
	if err != nil || len(counts) != 2 {
		t.Fatalf("counts = %+v, err = %v", counts, err)
	}
	if _, err := NormalizeTags([]string{"bad tag"}); err == nil {
		t.Fatal("invalid tag should fail")
	}
}

func TestAutoHealCooldownClaim(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	if err := st.CreateProvisioningNode("node-h", "h.example.com", "h.example.com"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.UpdateNodeCloudSettings("node-h", "mock", "instance-h", "", "", "", true); err != nil {
		t.Fatalf("settings: %v", err)
	}
	claimed, err := st.TryClaimAutoHeal("node-h", now, 30*time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	claimed, err = st.TryClaimAutoHeal("node-h", now.Add(time.Minute), 30*time.Minute)
	if err != nil || claimed {
		t.Fatalf("cooling claim = %v, %v", claimed, err)
	}
	claimed, err = st.TryClaimAutoHeal("node-h", now.Add(31*time.Minute), 30*time.Minute)
	if err != nil || !claimed {
		t.Fatalf("expired claim = %v, %v", claimed, err)
	}
	if err := st.UpdateNodeCloudSettings("node-h", "mock", "instance-h", "", "", "", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	claimed, err = st.TryClaimAutoHeal("node-h", now.Add(2*time.Hour), 30*time.Minute)
	if err != nil || claimed {
		t.Fatalf("disabled claim = %v, %v", claimed, err)
	}
}

func TestNodeDecommissionBlocksReuseUntilProvisioned(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateProvisioningNode("node-del", "del", "del.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.DecommissionNode("node-del", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	blocked, err := st.IsNodeDecommissioned("node-del")
	if err != nil || !blocked {
		t.Fatalf("blocked=%v err=%v", blocked, err)
	}
	if err := st.DeleteNode("node-del"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateProvisioningNode("node-del", "del", "del.example.com"); err != nil {
		t.Fatal(err)
	}
	blocked, err = st.IsNodeDecommissioned("node-del")
	if err != nil || blocked {
		t.Fatalf("after reprovision blocked=%v err=%v", blocked, err)
	}
}

func TestRecordGFWBlockedThresholdAndRecovery(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	if err := st.MarkNodeOnline(protocol.AgentInfo{ID: "node-gfw", Hostname: "gfw", Version: "0.9.8"}, "198.51.100.10", now); err != nil {
		t.Fatalf("mark online: %v", err)
	}
	status, newly, err := st.RecordGFWBlocked("node-gfw", 2, now)
	if err != nil || status != NodeStatusOnline || newly {
		t.Fatalf("first block = %q, newly=%v, err=%v", status, newly, err)
	}
	status, newly, err = st.RecordGFWBlocked("node-gfw", 2, now.Add(time.Minute))
	if err != nil || status != NodeStatusGFWBlocked || !newly {
		t.Fatalf("second block = %q, newly=%v, err=%v", status, newly, err)
	}
	node, err := st.GetNode("node-gfw")
	if err != nil || node.GFWBlockedAt == nil {
		t.Fatalf("node = %+v, err=%v", node, err)
	}
	status, newly, err = st.RecordGFWBlocked("node-gfw", 2, now.Add(2*time.Minute))
	if err != nil || status != NodeStatusGFWBlocked || newly {
		t.Fatalf("repeat block = %q, newly=%v, err=%v", status, newly, err)
	}
	if err := st.RecordProbeSuccess("node-gfw", true); err != nil {
		t.Fatalf("probe success: %v", err)
	}
	node, _ = st.GetNode("node-gfw")
	if node.Status != NodeStatusOnline || node.GFWBlockedAt != nil || node.GFWFailures != 0 {
		t.Fatalf("recovered node = %+v", node)
	}
}

func TestBackupToCreatesUsableSnapshot(t *testing.T) {
	st := openTestStore(t)
	if err := st.CreateProvisioningNode("node-b", "backup.example.com", "backup.example.com"); err != nil {
		t.Fatalf("create: %v", err)
	}
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := st.BackupTo(path); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer restored.Close()
	node, err := restored.GetNode("node-b")
	if err != nil || node.NodeID != "node-b" {
		t.Fatalf("restored node = %+v, err=%v", node, err)
	}
}

func TestQuotaBanExceededUsers(t *testing.T) {
	store := openTestStore(t)
	now := time.Now().UTC()
	if err := store.ApplyTrafficUpdate("node-a", protocol.TrafficUpdate{
		SessionID: "s1",
		Users: []protocol.TrafficUserStat{
			{Email: "alice@example.com", UploadBytes: 100, DownloadBytes: 200},
			{Email: "bob@example.com", UploadBytes: 999, DownloadBytes: 999},
		},
	}, now); err != nil {
		t.Fatalf("ApplyTrafficUpdate: %v", err)
	}
	if err := store.SetUserQuota("alice@example.com", 250); err != nil {
		t.Fatalf("SetUserQuota: %v", err)
	}
	banned, err := store.BanExceededUsers(now)
	if err != nil {
		t.Fatalf("BanExceededUsers: %v", err)
	}
	if len(banned) != 1 || banned[0].UserEmail != "alice@example.com" {
		t.Fatalf("banned = %+v", banned)
	}
	if len(banned[0].NodeIDs) != 1 || banned[0].NodeIDs[0] != "node-a" {
		t.Fatalf("node IDs = %+v", banned[0].NodeIDs)
	}
	banned, err = store.BanExceededUsers(now)
	if err != nil || len(banned) != 0 {
		t.Fatalf("second ban pass = %+v, err = %v", banned, err)
	}
	summaries, err := store.ListTrafficUserSummaries()
	if err != nil {
		t.Fatalf("ListTrafficUserSummaries: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	var alice *TrafficUserSummary
	for index := range summaries {
		if summaries[index].UserEmail == "alice@example.com" {
			alice = &summaries[index]
		}
	}
	if alice == nil || !alice.IsBanned || alice.QuotaBytes != 250 {
		t.Fatalf("summaries = %+v", summaries)
	}
}
