// Package xraygen turns structured proxy inbounds into a complete Xray-core
// configuration. It deliberately owns the server-side Xray shape: callers
// persist only the structured rows, and this package is the single place that
// knows about clients, streamSettings, Reality, stats/API and routing.
package xraygen

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	StatsPort = 10085
	// Xray's traffic accounting is exposed through this API inbound.
	apiTag = "api"

	ProtocolVLESS       = "vless"
	ProtocolTrojan      = "trojan"
	ProtocolShadowsocks = "shadowsocks"

	NetworkTCP  = "tcp"
	NetworkWS   = "ws"
	NetworkGRPC = "grpc"

	SecurityNone    = "none"
	SecurityTLS     = "tls"
	SecurityReality = "reality"

	FlowVision = "xtls-rprx-vision"
)

type Settings struct {
	UUID            string   `json:"uuid,omitempty"`
	Flow            string   `json:"flow,omitempty"`
	Password        string   `json:"password,omitempty"`
	Method          string   `json:"method,omitempty"`
	Path            string   `json:"path,omitempty"`
	Host            string   `json:"host,omitempty"`
	ServiceName     string   `json:"service_name,omitempty"`
	PrivateKey      string   `json:"private_key,omitempty"`
	PublicKey       string   `json:"public_key,omitempty"`
	ShortID         string   `json:"short_id,omitempty"`
	Dest            string   `json:"dest,omitempty"`
	ServerNames     []string `json:"server_names,omitempty"`
	CertificateFile string   `json:"certificate_file,omitempty"`
	KeyFile         string   `json:"key_file,omitempty"`
	ServerName      string   `json:"server_name,omitempty"`
}

type Inbound struct {
	ID       uint
	Remark   string
	Port     int
	Protocol string
	Network  string
	Security string
	Settings Settings
}

type RealityKeys struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

var (
	uuidPattern     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	shortIDPattern  = regexp.MustCompile(`(?i)^[0-9a-f]{0,16}$`)
	grpcNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	hostPattern     = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
)

// Normalize validates one inbound and applies safe defaults. It is also used
// by the API before persisting, so invalid rows never reach the generator.
func Normalize(in Inbound) (Inbound, error) {
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	in.Network = strings.ToLower(strings.TrimSpace(in.Network))
	in.Security = strings.ToLower(strings.TrimSpace(in.Security))
	in.Remark = strings.TrimSpace(in.Remark)
	if in.Network == "" {
		in.Network = NetworkTCP
	}
	if in.Security == "" {
		in.Security = SecurityNone
	}
	if in.Port < 1 || in.Port > 65535 {
		return Inbound{}, errors.New("port must be between 1 and 65535")
	}
	if in.Port == StatsPort {
		return Inbound{}, fmt.Errorf("port %d is reserved for the Xray Stats API", StatsPort)
	}
	if len(in.Remark) > 128 {
		return Inbound{}, errors.New("remark must be at most 128 characters")
	}
	if in.Remark == "" {
		in.Remark = fmt.Sprintf("%s-%d", in.Protocol, in.Port)
	}

	switch in.Protocol {
	case ProtocolVLESS:
		if err := normalizeVLESS(&in); err != nil {
			return Inbound{}, err
		}
	case ProtocolTrojan:
		if err := normalizeTrojan(&in); err != nil {
			return Inbound{}, err
		}
	case ProtocolShadowsocks:
		if err := normalizeShadowsocks(&in); err != nil {
			return Inbound{}, err
		}
	default:
		return Inbound{}, fmt.Errorf("unsupported protocol %q", in.Protocol)
	}

	switch in.Network {
	case NetworkTCP:
	case NetworkWS:
		in.Settings.Path = strings.TrimSpace(in.Settings.Path)
		if in.Settings.Path == "" {
			in.Settings.Path = "/"
		}
		if !strings.HasPrefix(in.Settings.Path, "/") || len(in.Settings.Path) > 256 {
			return Inbound{}, errors.New("WebSocket path must start with / and be at most 256 characters")
		}
	case NetworkGRPC:
		in.Settings.ServiceName = strings.TrimSpace(in.Settings.ServiceName)
		if in.Settings.ServiceName == "" {
			in.Settings.ServiceName = "nodehub"
		}
		if !grpcNamePattern.MatchString(in.Settings.ServiceName) {
			return Inbound{}, errors.New("gRPC service name must match [A-Za-z0-9._-]{1,64}")
		}
	default:
		return Inbound{}, fmt.Errorf("unsupported network %q", in.Network)
	}

	if in.Security == SecurityTLS {
		in.Settings.CertificateFile = strings.TrimSpace(in.Settings.CertificateFile)
		in.Settings.KeyFile = strings.TrimSpace(in.Settings.KeyFile)
		if in.Settings.CertificateFile == "" || in.Settings.KeyFile == "" {
			return Inbound{}, errors.New("TLS requires certificate_file and key_file")
		}
	}
	in.Settings.ServerName = strings.TrimSpace(in.Settings.ServerName)
	in.Settings.Host = strings.TrimSpace(in.Settings.Host)
	return in, nil
}

func normalizeVLESS(in *Inbound) error {
	in.Settings.UUID = strings.TrimSpace(in.Settings.UUID)
	if !uuidPattern.MatchString(in.Settings.UUID) {
		return errors.New("VLESS requires a valid UUID")
	}
	in.Settings.Flow = strings.TrimSpace(in.Settings.Flow)
	switch in.Security {
	case SecurityNone:
		if in.Settings.Flow != "" {
			return errors.New("VLESS flow requires TLS or Reality")
		}
	case SecurityTLS:
		if in.Settings.Flow != "" && in.Settings.Flow != FlowVision {
			return fmt.Errorf("unsupported VLESS flow %q", in.Settings.Flow)
		}
		if in.Settings.Flow != "" && in.Network != NetworkTCP {
			return errors.New("xtls-rprx-vision requires TCP")
		}
	case SecurityReality:
		if in.Network != NetworkTCP {
			return errors.New("Reality requires TCP")
		}
		if in.Settings.Flow != "" && in.Settings.Flow != FlowVision {
			return fmt.Errorf("unsupported Reality flow %q", in.Settings.Flow)
		}
		if in.Settings.Flow == "" {
			in.Settings.Flow = FlowVision
		}
		if err := normalizeReality(&in.Settings); err != nil {
			return err
		}
	default:
		return fmt.Errorf("VLESS does not support security %q", in.Security)
	}
	return nil
}

func normalizeTrojan(in *Inbound) error {
	in.Settings.Password = strings.TrimSpace(in.Settings.Password)
	if in.Settings.Password == "" || len(in.Settings.Password) > 128 {
		return errors.New("Trojan requires a password of 1-128 characters")
	}
	if in.Settings.Flow != "" {
		return errors.New("Trojan does not support flow")
	}
	switch in.Security {
	case SecurityNone, SecurityTLS:
	default:
		return fmt.Errorf("Trojan does not support security %q", in.Security)
	}
	return nil
}

func normalizeShadowsocks(in *Inbound) error {
	if in.Network != NetworkTCP || in.Security != SecurityNone {
		return errors.New("Shadowsocks supports only TCP with security none")
	}
	in.Settings.Method = strings.ToLower(strings.TrimSpace(in.Settings.Method))
	if in.Settings.Method == "" {
		in.Settings.Method = "2022-blake3-aes-128-gcm"
	}
	keyBytes, ok := shadowsocksKeyBytes[in.Settings.Method]
	if !ok {
		return fmt.Errorf("unsupported Shadowsocks method %q", in.Settings.Method)
	}
	in.Settings.Password = strings.TrimSpace(in.Settings.Password)
	if in.Settings.Password == "" {
		return errors.New("Shadowsocks requires a password")
	}
	if keyBytes > 0 {
		decoded, err := decodeBase64Key(in.Settings.Password)
		if err != nil || len(decoded) != keyBytes {
			return fmt.Errorf("Shadowsocks %s requires a base64 %d-byte key", in.Settings.Method, keyBytes)
		}
	}
	if in.Settings.Flow != "" || in.Settings.UUID != "" {
		return errors.New("Shadowsocks does not support flow or UUID")
	}
	return nil
}

var shadowsocksKeyBytes = map[string]int{
	"aes-128-gcm":                   0,
	"aes-256-gcm":                   0,
	"chacha20-poly1305":             0,
	"2022-blake3-aes-128-gcm":       16,
	"2022-blake3-aes-256-gcm":       32,
	"2022-blake3-chacha20-poly1305": 32,
}

func normalizeReality(settings *Settings) error {
	settings.PrivateKey = strings.TrimSpace(settings.PrivateKey)
	settings.PublicKey = strings.TrimSpace(settings.PublicKey)
	if err := validateX25519Key(settings.PrivateKey); err != nil {
		return fmt.Errorf("invalid Reality private_key: %w", err)
	}
	if err := validateX25519Key(settings.PublicKey); err != nil {
		return fmt.Errorf("invalid Reality public_key: %w", err)
	}
	privateBytes, _ := decodeBase64Key(settings.PrivateKey)
	publicBytes, _ := decodeBase64Key(settings.PublicKey)
	private, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return errors.New("invalid Reality private_key")
	}
	if subtle.ConstantTimeCompare(private.PublicKey().Bytes(), publicBytes) != 1 {
		return errors.New("Reality public_key does not match private_key")
	}
	settings.Dest = strings.TrimSpace(settings.Dest)
	if settings.Dest == "" {
		return errors.New("Reality requires dest")
	}
	dest, err := normalizeDest(settings.Dest)
	if err != nil {
		return err
	}
	settings.Dest = dest
	settings.ShortID = strings.TrimSpace(settings.ShortID)
	if !shortIDPattern.MatchString(settings.ShortID) || len(settings.ShortID)%2 != 0 {
		return errors.New("Reality short_id must be 0-16 even-length hex characters")
	}
	names := make([]string, 0, len(settings.ServerNames))
	for _, name := range settings.ServerNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !validServerName(name) {
			return fmt.Errorf("invalid Reality server name %q", name)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		names = []string{destHost(settings.Dest)}
	}
	settings.ServerNames = names
	return nil
}

func normalizeDest(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Reality dest is required")
	}
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return "", errors.New("Reality dest must be host:port")
		}
		raw = parsed.Host
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		host = strings.Trim(raw, "[]")
		port = "443"
	}
	if !validServerName(host) {
		return "", fmt.Errorf("invalid Reality dest host %q", host)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("Reality dest port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

func destHost(dest string) string {
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		return strings.Trim(dest, "[]")
	}
	return host
}

func validServerName(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return hostPattern.MatchString(host) && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".")
}

func validateX25519Key(value string) error {
	decoded, err := decodeBase64Key(value)
	if err != nil {
		return errors.New("must be base64")
	}
	if len(decoded) != 32 {
		return errors.New("must decode to 32 bytes")
	}
	return nil
}

func decodeBase64Key(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.URLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

// GenerateRealityKeys produces an X25519 keypair in the format expected by
// Xray's Reality server and client settings.
func GenerateRealityKeys() (RealityKeys, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return RealityKeys{}, err
	}
	short := make([]byte, 8)
	if _, err := rand.Read(short); err != nil {
		return RealityKeys{}, err
	}
	return RealityKeys{
		PrivateKey: base64.RawURLEncoding.EncodeToString(private.Bytes()),
		PublicKey:  base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
		ShortID:    hex.EncodeToString(short),
	}, nil
}

// Generate assembles user inbounds plus the Stats API inbound, routing and
// default outbounds into a complete Xray-core configuration.
func Generate(inbounds []Inbound) ([]byte, error) {
	if len(inbounds) == 0 {
		return nil, errors.New("at least one inbound is required")
	}
	normalized := make([]Inbound, 0, len(inbounds))
	ports := make(map[int]string, len(inbounds))
	for _, in := range inbounds {
		item, err := Normalize(in)
		if err != nil {
			return nil, fmt.Errorf("inbound %q: %w", in.Remark, err)
		}
		if other, exists := ports[item.Port]; exists {
			return nil, fmt.Errorf("port %d is already used by %q", item.Port, other)
		}
		ports[item.Port] = item.Remark
		normalized = append(normalized, item)
	}

	configInbounds := make([]any, 0, len(normalized)+1)
	for _, in := range normalized {
		configInbounds = append(configInbounds, buildInbound(in))
	}
	configInbounds = append(configInbounds, buildAPIInbound())

	config := map[string]any{
		"log": map[string]any{
			"loglevel": "warning",
			"access":   "none",
			"error":    "",
		},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{
				"0": map[string]any{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
				},
			},
		},
		"api": map[string]any{
			"tag":      apiTag,
			"services": []string{"StatsService"},
		},
		"inbounds": configInbounds,
		"outbounds": []any{
			map[string]any{
				"tag":      "direct",
				"protocol": "freedom",
				"settings": map[string]any{"domainStrategy": "UseIP"},
			},
			map[string]any{
				"tag":      "block",
				"protocol": "blackhole",
				"settings": map[string]any{},
			},
		},
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			"rules": []any{
				map[string]any{
					"type":        "field",
					"inboundTag":  []string{apiTag},
					"outboundTag": apiTag,
				},
				map[string]any{
					"type":        "field",
					"ip":          []string{"geoip:private"},
					"outboundTag": "block",
				},
				map[string]any{
					"type":        "field",
					"domain":      []string{"geosite:category-ads-all"},
					"outboundTag": "block",
				},
			},
		},
	}
	return json.Marshal(config)
}

func buildInbound(in Inbound) map[string]any {
	tag := fmt.Sprintf("inbound-%d", in.ID)
	if in.ID == 0 {
		tag = fmt.Sprintf("inbound-%d", in.Port)
	}
	result := map[string]any{
		"tag":            tag,
		"listen":         "0.0.0.0",
		"port":           in.Port,
		"protocol":       in.Protocol,
		"settings":       inboundSettings(in, tag),
		"streamSettings": streamSettings(in),
		"sniffing": map[string]any{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
			"routeOnly":    true,
		},
	}
	return result
}

func inboundSettings(in Inbound, tag string) map[string]any {
	email := tag + "@nodehub"
	switch in.Protocol {
	case ProtocolVLESS:
		client := map[string]any{"id": in.Settings.UUID, "email": email}
		if in.Settings.Flow != "" {
			client["flow"] = in.Settings.Flow
		}
		return map[string]any{
			"clients":    []any{client},
			"decryption": "none",
		}
	case ProtocolTrojan:
		return map[string]any{
			"clients": []any{
				map[string]any{"password": in.Settings.Password, "email": email},
			},
		}
	case ProtocolShadowsocks:
		return map[string]any{
			"method":   in.Settings.Method,
			"password": in.Settings.Password,
			"network":  "tcp,udp",
		}
	default:
		return map[string]any{}
	}
}

func streamSettings(in Inbound) map[string]any {
	stream := map[string]any{
		"network":  in.Network,
		"security": in.Security,
	}
	switch in.Network {
	case NetworkWS:
		ws := map[string]any{"path": in.Settings.Path}
		if in.Settings.Host != "" {
			ws["headers"] = map[string]any{"Host": in.Settings.Host}
		}
		stream["wsSettings"] = ws
	case NetworkGRPC:
		stream["grpcSettings"] = map[string]any{"serviceName": in.Settings.ServiceName}
	}

	switch in.Security {
	case SecurityTLS:
		tls := map[string]any{
			"certificates": []any{
				map[string]any{
					"certificateFile": in.Settings.CertificateFile,
					"keyFile":         in.Settings.KeyFile,
				},
			},
		}
		if in.Settings.ServerName != "" {
			tls["serverName"] = in.Settings.ServerName
		}
		if in.Network == NetworkGRPC {
			// gRPC over TLS uses HTTP/2.
			tls["alpn"] = []string{"h2"}
		}
		stream["tlsSettings"] = tls
	case SecurityReality:
		stream["realitySettings"] = map[string]any{
			"show":        false,
			"dest":        in.Settings.Dest,
			"xver":        0,
			"serverNames": in.Settings.ServerNames,
			"privateKey":  in.Settings.PrivateKey,
			"shortIds":    []string{in.Settings.ShortID},
		}
	}
	return stream
}

func buildAPIInbound() map[string]any {
	return map[string]any{
		"tag":      apiTag,
		"listen":   "127.0.0.1",
		"port":     StatsPort,
		"protocol": "dokodemo-door",
		"settings": map[string]any{"address": "127.0.0.1"},
	}
}

// ShareLink builds a v2rayN/Shadowrocket-compatible link for one inbound.
func ShareLink(in Inbound, host string) (string, error) {
	in, err := Normalize(in)
	if err != nil {
		return "", err
	}
	host = normalizeHost(host)
	if host == "" {
		return "", errors.New("node public host is empty")
	}
	target := net.JoinHostPort(host, strconv.Itoa(in.Port))
	remark := in.Remark
	switch in.Protocol {
	case ProtocolVLESS:
		link := &url.URL{Scheme: "vless", User: url.User(in.Settings.UUID), Host: target, Fragment: remark}
		values := link.Query()
		values.Set("encryption", "none")
		values.Set("type", in.Network)
		values.Set("security", in.Security)
		if in.Settings.Flow != "" {
			values.Set("flow", in.Settings.Flow)
		}
		setTransportQuery(values, in)
		if in.Security == SecurityTLS {
			values.Set("sni", tlsServerName(in, host))
			values.Set("fp", "chrome")
		}
		if in.Security == SecurityReality {
			values.Set("sni", realityServerName(in))
			values.Set("fp", "chrome")
			values.Set("pbk", in.Settings.PublicKey)
			if in.Settings.ShortID != "" {
				values.Set("sid", in.Settings.ShortID)
			}
			values.Set("spx", "/")
		}
		link.RawQuery = values.Encode()
		return link.String(), nil
	case ProtocolTrojan:
		link := &url.URL{Scheme: "trojan", User: url.User(in.Settings.Password), Host: target, Fragment: remark}
		values := link.Query()
		values.Set("type", in.Network)
		values.Set("security", in.Security)
		setTransportQuery(values, in)
		if in.Security == SecurityTLS {
			values.Set("sni", tlsServerName(in, host))
			values.Set("fp", "chrome")
		}
		link.RawQuery = values.Encode()
		return link.String(), nil
	case ProtocolShadowsocks:
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(in.Settings.Method + ":" + in.Settings.Password))
		link := &url.URL{Scheme: "ss", User: url.User(userinfo), Host: target, Fragment: remark}
		return link.String(), nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", in.Protocol)
	}
}

func setTransportQuery(values url.Values, in Inbound) {
	switch in.Network {
	case NetworkWS:
		values.Set("path", in.Settings.Path)
		host := in.Settings.Host
		if host != "" {
			values.Set("host", host)
		}
	case NetworkGRPC:
		values.Set("serviceName", in.Settings.ServiceName)
	}
}

func tlsServerName(in Inbound, fallback string) string {
	if in.Settings.ServerName != "" {
		return in.Settings.ServerName
	}
	return fallback
}

func realityServerName(in Inbound) string {
	if len(in.Settings.ServerNames) > 0 {
		return in.Settings.ServerNames[0]
	}
	return destHost(in.Settings.Dest)
}

func normalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	return raw
}
