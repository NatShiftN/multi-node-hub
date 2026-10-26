package plugins

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeXrayConfig(t *testing.T) {
	object := []byte(`{"inbounds":[],"outbounds":[{"protocol":"freedom"}]}`)
	config, err := normalizeXrayConfig(object)
	if err != nil {
		t.Fatalf("normalize object: %v", err)
	}
	if !strings.Contains(string(config), `"freedom"`) {
		t.Fatalf("unexpected config: %s", config)
	}

	encoded := []byte(`"{\"inbounds\":[],\"outbounds\":[]}"`)
	if _, err := normalizeXrayConfig(encoded); err != nil {
		t.Fatalf("normalize string config: %v", err)
	}
	for _, invalid := range [][]byte{
		nil,
		[]byte(`null`),
		[]byte(`[]`),
		[]byte(`{bad`),
		[]byte(`"not-json"`),
	} {
		if _, err := normalizeXrayConfig(invalid); err == nil {
			t.Fatalf("normalizeXrayConfig(%q) should fail", invalid)
		}
	}
}

func TestExtractXrayFromArchive(t *testing.T) {
	binaryName := "xray"
	if runtime.GOOS == "windows" {
		binaryName = "xray.exe"
	}
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "xray.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create(binaryName)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := entry.Write([]byte("fake-xray-binary")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	destination := filepath.Join(dir, binaryName)
	if err := extractXrayFromArchive(archivePath, destination); err != nil {
		t.Fatalf("extractXrayFromArchive: %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read extracted binary: %v", err)
	}
	if string(data) != "fake-xray-binary" {
		t.Fatalf("extracted data = %q", data)
	}
}

func TestParseXrayTrafficStats(t *testing.T) {
	output := []byte(`{
		"stat": [
			{"name":"user>>>alice@example.com>>>traffic>>>uplink","value":79},
			{"name":"user>>>alice@example.com>>>traffic>>>downlink","value":"990"},
			{"name":"user>>>bob@example.com>>>traffic>>>uplink","value":5},
			{"name":"inbound>>>x>>>traffic>>>uplink","value":999}
		]
	}`)
	users, err := parseXrayTrafficStats(output)
	if err != nil {
		t.Fatalf("parseXrayTrafficStats: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %+v", users)
	}
	if users[0].Email != "alice@example.com" || users[0].UploadBytes != 79 || users[0].DownloadBytes != 990 {
		t.Fatalf("alice stats = %+v", users[0])
	}
	if users[1].Email != "bob@example.com" || users[1].UploadBytes != 5 || users[1].DownloadBytes != 0 {
		t.Fatalf("bob stats = %+v", users[1])
	}

	empty, err := parseXrayTrafficStats([]byte(`{}`))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty stats = %+v, err = %v", empty, err)
	}
	if _, err := parseXrayTrafficStats([]byte("not json")); err == nil {
		t.Fatal("invalid stats output should fail")
	}
}

func TestXrayDownloadURLOverride(t *testing.T) {
	asset, err := xrayAssetName()
	if err != nil {
		t.Skipf("current platform has no mapped Xray asset: %v", err)
	}
	url, err := xrayDownloadURL("https://mirror.example.com/xray/")
	if err != nil {
		t.Fatalf("xrayDownloadURL: %v", err)
	}
	if url != "https://mirror.example.com/xray/"+asset {
		t.Fatalf("url = %q", url)
	}
	exact, _ := xrayDownloadURL("https://mirror.example.com/custom.zip")
	if exact != "https://mirror.example.com/custom.zip" {
		t.Fatalf("exact url = %q", exact)
	}
}
