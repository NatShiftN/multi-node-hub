package xraygen

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const testUUID = "11111111-1111-4111-8111-111111111111"

func realityInbound(t *testing.T) Inbound {
	t.Helper()
	keys, err := GenerateRealityKeys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	return Inbound{
		ID:       7,
		Remark:   "Edge Reality",
		Port:     443,
		Protocol: ProtocolVLESS,
		Network:  NetworkTCP,
		Security: SecurityReality,
		Settings: Settings{
			UUID:       testUUID,
			PrivateKey: keys.PrivateKey,
			PublicKey:  keys.PublicKey,
			ShortID:    keys.ShortID,
			Dest:       "www.microsoft.com",
		},
	}
}

func TestGenerateVLESSReality(t *testing.T) {
	raw, err := Generate([]Inbound{realityInbound(t)})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := config["stats"]; !ok {
		t.Fatal("stats missing")
	}
	policy := config["policy"].(map[string]any)
	if policy["levels"] == nil {
		t.Fatal("policy levels missing")
	}
	inbounds := config["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("inbounds = %d, want user + api", len(inbounds))
	}
	user := inbounds[0].(map[string]any)
	if user["protocol"] != "vless" || user["port"].(float64) != 443 {
		t.Fatalf("user inbound = %+v", user)
	}
	settings := user["settings"].(map[string]any)
	if settings["decryption"] != "none" {
		t.Fatalf("settings = %+v", settings)
	}
	clients := settings["clients"].([]any)
	client := clients[0].(map[string]any)
	if client["id"] != testUUID || client["flow"] != FlowVision {
		t.Fatalf("client = %+v", client)
	}
	stream := user["streamSettings"].(map[string]any)
	if stream["network"] != "tcp" || stream["security"] != "reality" {
		t.Fatalf("stream = %+v", stream)
	}
	reality := stream["realitySettings"].(map[string]any)
	if reality["dest"] != "www.microsoft.com:443" {
		t.Fatalf("reality = %+v", reality)
	}
	names := reality["serverNames"].([]any)
	if len(names) != 1 || names[0] != "www.microsoft.com" {
		t.Fatalf("serverNames = %+v", names)
	}
	if reality["privateKey"] == "" {
		t.Fatal("reality private key missing")
	}
	api := inbounds[1].(map[string]any)
	if api["protocol"] != "dokodemo-door" || api["port"].(float64) != StatsPort {
		t.Fatalf("api inbound = %+v", api)
	}
}

func TestGenerateVLESSWebSocketTLS(t *testing.T) {
	in := Inbound{
		ID: 1, Remark: "WS TLS", Port: 8443,
		Protocol: ProtocolVLESS, Network: NetworkWS, Security: SecurityTLS,
		Settings: Settings{
			UUID: testUUID, Path: "/edge", Host: "cdn.example.com",
			CertificateFile: "/etc/nodehub/fullchain.pem",
			KeyFile:         "/etc/nodehub/key.pem",
			ServerName:      "cdn.example.com",
		},
	}
	raw, err := Generate([]Inbound{in})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var config struct {
		Inbounds []struct {
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	stream := config.Inbounds[0].StreamSettings
	if stream["network"] != "ws" || stream["security"] != "tls" {
		t.Fatalf("stream = %+v", stream)
	}
	ws := stream["wsSettings"].(map[string]any)
	if ws["path"] != "/edge" {
		t.Fatalf("ws = %+v", ws)
	}
	tls := stream["tlsSettings"].(map[string]any)
	certs := tls["certificates"].([]any)
	cert := certs[0].(map[string]any)
	if cert["certificateFile"] != "/etc/nodehub/fullchain.pem" || cert["keyFile"] != "/etc/nodehub/key.pem" {
		t.Fatalf("tls = %+v", tls)
	}
}

func TestGenerateTrojanGRPC(t *testing.T) {
	in := Inbound{
		ID: 2, Remark: "Trojan gRPC", Port: 9443,
		Protocol: ProtocolTrojan, Network: NetworkGRPC, Security: SecurityTLS,
		Settings: Settings{
			Password: "strong-password", ServiceName: "edge-grpc",
			CertificateFile: "/tmp/cert.pem", KeyFile: "/tmp/key.pem",
		},
	}
	raw, err := Generate([]Inbound{in})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var config struct {
		Inbounds []struct {
			Settings       map[string]any `json:"settings"`
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Inbounds[0].StreamSettings["grpcSettings"].(map[string]any)["serviceName"] != "edge-grpc" {
		t.Fatalf("stream = %+v", config.Inbounds[0].StreamSettings)
	}
	clients := config.Inbounds[0].Settings["clients"].([]any)
	if clients[0].(map[string]any)["password"] != "strong-password" {
		t.Fatalf("settings = %+v", config.Inbounds[0].Settings)
	}
}

func TestGenerateShadowsocks(t *testing.T) {
	password := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	in := Inbound{
		ID: 3, Remark: "SS", Port: 8388,
		Protocol: ProtocolShadowsocks, Network: NetworkTCP, Security: SecurityNone,
		Settings: Settings{Method: "2022-blake3-aes-128-gcm", Password: password},
	}
	raw, err := Generate([]Inbound{in})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var config struct {
		Inbounds []struct {
			Settings map[string]any `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if config.Inbounds[0].Settings["method"] != "2022-blake3-aes-128-gcm" {
		t.Fatalf("settings = %+v", config.Inbounds[0].Settings)
	}
}

func TestValidationRejectsUnsupportedCombinations(t *testing.T) {
	keys, _ := GenerateRealityKeys()
	cases := []Inbound{
		{Protocol: ProtocolVLESS, Network: NetworkWS, Security: SecurityReality, Port: 443,
			Settings: Settings{UUID: testUUID, PrivateKey: keys.PrivateKey, PublicKey: keys.PublicKey, Dest: "example.com"}},
		{Protocol: ProtocolTrojan, Network: NetworkTCP, Security: SecurityReality, Port: 443,
			Settings: Settings{Password: "x", PrivateKey: keys.PrivateKey, PublicKey: keys.PublicKey, Dest: "example.com"}},
		{Protocol: ProtocolShadowsocks, Network: NetworkWS, Security: SecurityNone, Port: 8388,
			Settings: Settings{Method: "aes-256-gcm", Password: "password"}},
		{Protocol: ProtocolVLESS, Network: NetworkTCP, Security: SecurityNone, Port: StatsPort,
			Settings: Settings{UUID: testUUID}},
	}
	for _, in := range cases {
		if _, err := Normalize(in); err == nil {
			t.Fatalf("expected validation error for %+v", in)
		}
	}
}

func TestShareLinks(t *testing.T) {
	in := realityInbound(t)
	link, err := ShareLink(in, "edge.example.com")
	if err != nil {
		t.Fatalf("ShareLink: %v", err)
	}
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Scheme != "vless" || parsed.User.Username() != testUUID {
		t.Fatalf("link = %s", link)
	}
	query := parsed.Query()
	if query.Get("security") != "reality" || query.Get("pbk") == "" || query.Get("sni") != "www.microsoft.com" {
		t.Fatalf("query = %+v", query)
	}
	if query.Get("flow") != FlowVision || query.Get("sid") != in.Settings.ShortID {
		t.Fatalf("query = %+v", query)
	}
	if parsed.Fragment != "Edge Reality" {
		t.Fatalf("fragment = %q", parsed.Fragment)
	}

	trojan := Inbound{
		ID: 1, Remark: "Trojan", Port: 443, Protocol: ProtocolTrojan,
		Network: NetworkWS, Security: SecurityTLS,
		Settings: Settings{Password: "p@ss", Path: "/edge", ServerName: "edge.example.com",
			CertificateFile: "/tmp/cert", KeyFile: "/tmp/key"},
	}
	link, err = ShareLink(trojan, "edge.example.com")
	if err != nil {
		t.Fatalf("trojan ShareLink: %v", err)
	}
	parsed, _ = url.Parse(link)
	if parsed.Scheme != "trojan" || parsed.User.Username() != "p@ss" || parsed.Query().Get("path") != "/edge" {
		t.Fatalf("trojan link = %s", link)
	}

	password := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	ss := Inbound{
		ID: 2, Remark: "SS", Port: 8388, Protocol: ProtocolShadowsocks,
		Network: NetworkTCP, Security: SecurityNone,
		Settings: Settings{Method: "2022-blake3-aes-128-gcm", Password: password},
	}
	link, err = ShareLink(ss, "edge.example.com")
	if err != nil {
		t.Fatalf("ss ShareLink: %v", err)
	}
	parsed, _ = url.Parse(link)
	if parsed.Scheme != "ss" {
		t.Fatalf("ss link = %s", link)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parsed.User.Username())
	if err != nil {
		t.Fatalf("ss userinfo: %v", err)
	}
	if !strings.HasPrefix(string(decoded), "2022-blake3-aes-128-gcm:") {
		t.Fatalf("ss userinfo = %q", decoded)
	}
}
