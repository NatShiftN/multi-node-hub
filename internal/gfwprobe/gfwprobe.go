// Package gfwprobe queries independent domestic probes. A domestic probe is
// deliberately a separate trust domain from the overseas control plane: a
// local TCP success only tells us the node is reachable from the control plane.
package gfwprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Result struct {
	Blocked   bool
	Reachable bool
	Detail    string
	Provider  string
}

type Probe interface {
	Name() string
	Check(ctx context.Context, host string, port int) (Result, error)
}

type HTTPProbe struct {
	name   string
	url    string
	token  string
	client *http.Client
}

func NewHTTPProbe(rawURL, token string, timeout time.Duration) (*HTTPProbe, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("GFW probe URL must be http(s)")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	name := parsed.Hostname()
	if name == "" {
		name = "domestic-probe"
	}
	return &HTTPProbe{
		name:   name,
		url:    parsed.String(),
		token:  strings.TrimSpace(token),
		client: &http.Client{Timeout: timeout},
	}, nil
}

func (p *HTTPProbe) Name() string { return p.name }

type probeRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type probeResponse struct {
	Blocked   *bool  `json:"blocked"`
	Reachable *bool  `json:"reachable"`
	Detail    string `json:"detail"`
}

func (p *HTTPProbe) Check(ctx context.Context, host string, port int) (Result, error) {
	payload, err := json.Marshal(probeRequest{Host: host, Port: port, Protocol: "tcp"})
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if p.token != "" {
		request.Header.Set("Authorization", "Bearer "+p.token)
	}
	response, err := p.client.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return Result{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, fmt.Errorf("probe HTTP %d", response.StatusCode)
	}
	var decoded probeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Result{}, fmt.Errorf("decode probe response: %w", err)
	}
	if decoded.Reachable == nil {
		return Result{}, errors.New("probe response is missing reachable")
	}
	reachable := *decoded.Reachable
	blocked := false
	if decoded.Blocked != nil {
		blocked = *decoded.Blocked
	}
	if blocked && reachable {
		return Result{}, errors.New("probe response contradicts itself (blocked and reachable)")
	}
	if !blocked && !reachable && decoded.Detail == "" {
		return Result{}, errors.New("probe response is neither reachable nor blocked")
	}
	return Result{
		Blocked:   blocked,
		Reachable: reachable,
		Detail:    decoded.Detail,
		Provider:  p.name,
	}, nil
}

// Multi fans out to several domestic probes and requires a majority of the
// successful reports to agree that the target is blocked. Probe failures are
// not treated as blocking; they simply reduce the sample.
type Multi struct {
	probes     []Probe
	minReports int
}

func NewMulti(probes []Probe, minReports int) (*Multi, error) {
	if len(probes) < 2 {
		return nil, errors.New("at least two independent domestic probes are required for cross-validation")
	}
	if minReports < 2 {
		minReports = 2
	}
	if minReports > len(probes) {
		minReports = len(probes)
	}
	return &Multi{probes: probes, minReports: minReports}, nil
}

func (m *Multi) Name() string { return "domestic-matrix" }

func (m *Multi) Check(ctx context.Context, host string, port int) (Result, error) {
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, len(m.probes))
	for _, probe := range m.probes {
		probe := probe
		go func() {
			result, err := probe.Check(ctx, host, port)
			results <- outcome{result: result, err: err}
		}()
	}
	successes := 0
	blocked := 0
	reachable := 0
	details := make([]string, 0, len(m.probes))
	for range m.probes {
		outcome := <-results
		if outcome.err != nil {
			details = append(details, "error: "+outcome.err.Error())
			continue
		}
		successes++
		if outcome.result.Blocked {
			blocked++
		} else if outcome.result.Reachable {
			reachable++
		}
		if outcome.result.Detail != "" {
			details = append(details, outcome.result.Detail)
		}
	}
	if successes < m.minReports {
		return Result{}, fmt.Errorf("only %d/%d domestic probes succeeded (minimum %d)", successes, len(m.probes), m.minReports)
	}
	result := Result{Detail: strings.Join(details, "; "), Provider: m.Name()}
	switch {
	case blocked*2 > successes:
		result.Blocked = true
		result.Reachable = false
	case reachable*2 > successes:
		result.Blocked = false
		result.Reachable = true
	default:
		return Result{}, errors.New("domestic probes are split; refusing to change state")
	}
	return result, nil
}
