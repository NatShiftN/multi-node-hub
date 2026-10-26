package controlplane

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAgentVersionAPI(t *testing.T) {
	server, _ := newDeployTestServer(t)
	recorder := apiCall(t, server, http.MethodGet, "/api/agent/version", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var info struct {
		Version     string `json:"version"`
		OS          string `json:"os"`
		Arch        string `json:"arch"`
		DownloadURL string `json:"download_url"`
		SHA256      string `json:"sha256"`
		Size        int64  `json:"size"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Version != "0.9.0" || info.OS != "linux" || info.Arch != "amd64" {
		t.Fatalf("unexpected version info: %+v", info)
	}
	if info.SHA256 == "" || info.Size == 0 || info.DownloadURL == "" {
		t.Fatalf("missing release metadata: %+v", info)
	}
}
