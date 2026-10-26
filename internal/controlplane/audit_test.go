package controlplane

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestAuditLogsForWriteAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-a", "a.example.com", "a.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	recorder := apiCall(t, server, http.MethodPut, "/api/nodes/node-a/tags", `{"tags":["region:hk"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set tags status = %d", recorder.Code)
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/audit-logs?page=1&page_size=10", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Logs []struct {
			Username     string `json:"username"`
			Action       string `json:"action"`
			TargetNodeID string `json:"target_node_id"`
			IPAddress    string `json:"ip_address"`
			Details      string `json:"details"`
		} `json:"logs"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if response.Total != 1 || len(response.Logs) != 1 {
		t.Fatalf("audit response = %+v", response)
	}
	if response.Logs[0].Action != "set_tags" || response.Logs[0].TargetNodeID != "node-a" {
		t.Fatalf("audit entry = %+v", response.Logs[0])
	}
	if response.Logs[0].Username != "admin" || response.Logs[0].IPAddress == "" {
		t.Fatalf("audit actor = %+v", response.Logs[0])
	}
}

func TestDeleteNodeAPIWritesAudit(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-del", "del.example.com", "del.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	recorder := apiCall(t, server, http.MethodDelete, "/api/nodes/node-del", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if _, err := server.store.GetNode("node-del"); err == nil {
		t.Fatal("deleted node still exists")
	}
	recorder = apiCall(t, server, http.MethodGet, "/api/audit-logs", "")
	if !strings.Contains(recorder.Body.String(), "delete_node") {
		t.Fatalf("delete audit missing: %s", recorder.Body.String())
	}
}

func TestScheduledBackupCreatesSnapshot(t *testing.T) {
	server, _ := newDeployTestServer(t)
	server.cfg.BackupDir = t.TempDir()
	path, err := server.backupOnce()
	if err != nil {
		t.Fatalf("backupOnce: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if len(body) < 16 || string(body[:16]) != "SQLite format 3\x00" {
		t.Fatalf("invalid backup header")
	}
	entries, total, err := server.store.ListAuditLogs(1, 10)
	if err != nil || total == 0 || entries[0].Action != "backup" {
		t.Fatalf("backup audit entries=%+v total=%d err=%v", entries, total, err)
	}
}

func TestBackupDownloadAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	if err := server.store.CreateProvisioningNode("node-b", "b.example.com", "b.example.com"); err != nil {
		t.Fatalf("create node: %v", err)
	}
	recorder := apiCall(t, server, http.MethodGet, "/api/system/backup", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("backup status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.Bytes()
	if len(body) < 16 || string(body[:16]) != "SQLite format 3\x00" {
		t.Fatalf("backup is not SQLite (len=%d)", len(body))
	}
	if !strings.Contains(recorder.Header().Get("Content-Disposition"), "nodehub_") {
		t.Fatalf("missing attachment header: %q", recorder.Header().Get("Content-Disposition"))
	}
}
