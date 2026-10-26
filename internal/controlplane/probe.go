package controlplane

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const probeConcurrency = 16

func (s *Server) probeLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.ProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.probeOnce(ctx)
		}
	}
}

func (s *Server) probeOnce(ctx context.Context) {
	nodes, err := s.store.ListProbeCandidates()
	if err != nil {
		s.logger.Error("list TCP probe candidates", "error", err)
		return
	}
	sem := make(chan struct{}, probeConcurrency)
	var wait sync.WaitGroup
	for _, node := range nodes {
		node := node
		address := probeAddress(node.Address, node.RemoteIP, s.cfg.ProbePort)
		if address == "" {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			dialer := &net.Dialer{Timeout: s.cfg.ProbeTimeout}
			probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
			defer cancel()
			conn, err := dialer.DialContext(probeCtx, "tcp", address)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				s.nodeStateMu.Lock()
				status, newlyMarked, updateErr := s.store.RecordProbeFailure(
					node.NodeID,
					s.cfg.ProbeFailureThreshold,
					time.Now().UTC(),
				)
				s.nodeStateMu.Unlock()
				if updateErr != nil {
					s.logger.Error("record TCP probe failure", "node_id", node.NodeID, "error", updateErr)
					return
				}
				if newlyMarked {
					s.logger.Warn("node marked offline_network_error",
						"node_id", node.NodeID,
						"address", address,
						"status", status,
					)
					s.notifyNodeOffline(node, address)
					// A generic network error is not evidence of GFW blocking.
					// Only a domestic-probe-confirmed gfw_blocked state may
					// trigger AutoHeal.
				}
				return
			}
			_ = conn.Close()

			// The overseas control plane can reach the node. Ask independent
			// domestic probes before declaring a GFW block.
			host, portText, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return
			}
			port, _ := strconv.Atoi(portText)
			if s.gfwProbe != nil && host != "" && port > 0 {
				domesticCtx, domesticCancel := context.WithTimeout(ctx, s.gfwProbeTimeout())
				result, domesticErr := s.gfwProbe.Check(domesticCtx, host, port)
				domesticCancel()
				if ctx.Err() != nil {
					return
				}
				if domesticErr != nil {
					// A failed/split probe is not evidence either way. Keep the
					// last state and retry on the next interval.
					s.logger.Warn("domestic GFW probe inconclusive",
						"node_id", node.NodeID,
						"address", address,
						"error", domesticErr,
					)
					return
				}
				if result.Blocked {
					s.nodeStateMu.Lock()
					status, newlyMarked, updateErr := s.store.RecordGFWBlocked(
						node.NodeID,
						s.gfwThreshold(),
						time.Now().UTC(),
					)
					s.nodeStateMu.Unlock()
					if updateErr != nil {
						s.logger.Error("record GFW block", "node_id", node.NodeID, "error", updateErr)
						return
					}
					if newlyMarked {
						s.logger.Warn("node marked gfw_blocked",
							"node_id", node.NodeID,
							"address", address,
							"status", status,
							"probe", result.Provider,
							"detail", result.Detail,
						)
						s.notifyNodeGFWBlocked(node, address, result.Detail)
						s.maybeAutoHeal(node.NodeID)
					}
					return
				}
				// Domestic probes reached the target: the node is healthy.
			}

			s.nodeStateMu.Lock()
			_, agentOnline := s.hub.Get(node.NodeID)
			if updateErr := s.store.RecordProbeSuccess(node.NodeID, agentOnline); updateErr != nil {
				s.logger.Error("record TCP probe success", "node_id", node.NodeID, "error", updateErr)
			}
			s.nodeStateMu.Unlock()
		}()
	}
	wait.Wait()
}

func (s *Server) gfwThreshold() int {
	if s.cfg.GFWProbeThreshold <= 0 {
		return 2
	}
	return s.cfg.GFWProbeThreshold
}

func (s *Server) gfwProbeTimeout() time.Duration {
	if s.cfg.GFWProbeTimeout <= 0 {
		return 10 * time.Second
	}
	return s.cfg.GFWProbeTimeout
}

func probeAddress(address, remoteIP string, port int) string {
	host := strings.TrimSpace(address)
	if host == "" {
		host = strings.TrimSpace(remoteIP)
	}
	if host == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
