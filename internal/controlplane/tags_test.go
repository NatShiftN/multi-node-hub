package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"nodehub/internal/protocol"
)

func TestTagsAPIAndBatchMatching(t *testing.T) {
	server, _ := newDeployTestServer(t)
	now := time.Now().UTC()
	for _, id := range []string{"node-a", "node-b"} {
		if err := server.store.CreateProvisioningNode(id, id, id+".example.com"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := server.store.MarkNodeOnline(protocol.AgentInfo{ID: id, Hostname: id}, "10.0.0.8", now); err != nil {
			t.Fatalf("online %s: %v", id, err)
		}
	}

	recorder := apiCall(t, server, http.MethodPut, "/api/nodes/node-a/tags",
		`{"tags":["region:hk","type:relay"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set tags status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	recorder = apiCall(t, server, http.MethodPut, "/api/nodes/node-b/tags", `{"tags":["region:hk"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("set tags b status = %d", recorder.Code)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/nodes/node-a", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("get node status = %d", recorder.Code)
	}
	var detail struct {
		Node NodeResponse `json:"node"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if len(detail.Node.Tags) != 2 {
		t.Fatalf("node tags = %+v", detail.Node.Tags)
	}

	recorder = apiCall(t, server, http.MethodGet, "/api/tags", "")
	if recorder.Code != http.StatusOK || recorder.Body.String() == "" {
		t.Fatalf("list tags status = %d", recorder.Code)
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/batch/command",
		`{"tags":["region:hk","type:relay"],"plugin":"system_info","payload":{}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("batch status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var batch struct {
		Matched int               `json:"matched"`
		Failed  []batchNodeResult `json:"failed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &batch); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if batch.Matched != 1 || len(batch.Failed) != 1 || batch.Failed[0].NodeID != "node-a" {
		t.Fatalf("batch = %+v", batch)
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/batch/command",
		`{"all_online":true,"plugin":"system_info","payload":{}}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("all online status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var allBatch struct {
		Matched int `json:"matched"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &allBatch)
	if allBatch.Matched != 2 {
		t.Fatalf("all_online matched = %d, body=%s", allBatch.Matched, recorder.Body.String())
	}

	recorder = apiCall(t, server, http.MethodPost, "/api/batch/command",
		`{"tags":[],"plugin":"system_info","payload":{}}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("empty tags status = %d", recorder.Code)
	}
}
