package controlplane

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"nodehub/internal/protocol"
)

const (
	wsWriteWait      = 10 * time.Second
	helloWait        = 10 * time.Second
	maxPluginsInInfo = 64
)

func (s *Server) handleAgentWebSocket(c *gin.Context) {
	headerID, ok := s.authenticateAgent(c.Request)
	if !ok {
		s.logger.Warn("reject unauthorized agent", "remote", c.ClientIP())
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid agent token or node id"})
		return
	}

	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error.
		return
	}
	conn.SetReadLimit(protocol.MaxMessageBytes)

	// The first frame must be hello. This keeps IDs out of URLs and lets the
	// server validate identity before creating a hub entry.
	_ = conn.SetReadDeadline(time.Now().Add(helloWait))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return
	}
	var hello protocol.Envelope
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Type != protocol.TypeHello || hello.Agent == nil {
		closeWS(conn, websocket.ClosePolicyViolation, "first message must be a valid hello")
		return
	}
	info := sanitizeAgentInfo(*hello.Agent)
	if !protocol.ValidAgentID(info.ID) {
		closeWS(conn, websocket.ClosePolicyViolation, "invalid agent id")
		return
	}
	if headerID != info.ID {
		closeWS(conn, websocket.ClosePolicyViolation, "agent id header does not match hello")
		return
	}
	if blocked, err := s.store.IsNodeDecommissioned(info.ID); err != nil {
		s.logger.Error("check node decommission", "agent_id", info.ID, "error", err)
		closeWS(conn, websocket.CloseTryAgainLater, "node state unavailable")
		return
	} else if blocked {
		s.logger.Warn("reject decommissioned agent", "agent_id", info.ID)
		closeWS(conn, websocket.ClosePolicyViolation, "node has been decommissioned")
		return
	}

	node := newNode(info, remoteIP(c.Request), conn)
	previousNode, previousNodeErr := s.store.GetNode(info.ID)
	s.nodeStateMu.Lock()
	previous := s.hub.Register(node)
	if err := s.store.MarkNodeOnline(info, node.remoteIP, time.Now().UTC()); err != nil {
		s.logger.Error("persist node online state", "agent_id", info.ID, "error", err)
	}
	s.nodeStateMu.Unlock()
	if previousNodeErr == nil && previousNode.DDNSDomain != "" && previousNode.RemoteIP != node.remoteIP {
		s.triggerDDNS(info.ID, node.remoteIP, "agent_reconnect")
	}
	if previous != nil {
		s.logger.Info("agent connection replaced", "agent_id", info.ID)
	}
	s.logger.Info("agent connected",
		"agent_id", info.ID,
		"hostname", info.Hostname,
		"ip", node.remoteIP,
		"plugins", info.Plugins,
	)

	go s.writePump(node)
	s.sendVersionInfo(node)
	go s.syncPortForwardsOnConnect(info.ID)
	go s.syncXrayOnConnect(info.ID)
	s.readPump(node)
	s.logger.Info("agent disconnected", "agent_id", info.ID)
}

// authenticateAgent validates the token and its bound node ID before the
// WebSocket upgrade. The hello frame must later repeat the same ID.
func (s *Server) authenticateAgent(r *http.Request) (string, bool) {
	token := agentTokenFromRequest(r)
	nodeID := strings.TrimSpace(r.Header.Get("X-Agent-ID"))
	if token == "" || !protocol.ValidAgentID(nodeID) {
		return "", false
	}
	if !s.tokens.Authorize(token, nodeID) {
		return "", false
	}
	return nodeID, true
}

func agentTokenFromRequest(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		parts := strings.Fields(auth)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return parts[1]
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Agent-Token"))
}

func (s *Server) readPump(n *Node) {
	defer func() {
		s.nodeStateMu.Lock()
		defer s.nodeStateMu.Unlock()
		if s.hub.Unregister(n) {
			if err := s.store.MarkNodeOffline(n.id, time.Now().UTC()); err != nil {
				s.logger.Error("persist node offline state", "agent_id", n.id, "error", err)
			}
		}
	}()

	n.ws.SetPongHandler(func(string) error {
		n.touch()
		return n.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	})
	n.ws.SetPingHandler(func(data string) error {
		n.touch()
		if err := n.ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(wsWriteWait)); err != nil {
			return err
		}
		return n.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	})
	_ = n.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait))

	for {
		_, raw, err := n.ws.ReadMessage()
		if err != nil {
			return
		}
		n.touch()
		_ = n.ws.SetReadDeadline(time.Now().Add(s.cfg.PongWait))

		var env protocol.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			s.logger.Warn("discard malformed agent message", "agent_id", n.id, "error", err)
			continue
		}
		switch env.Type {
		case protocol.TypeResult:
			s.hub.ResolveResult(env)
		case protocol.TypeTelemetryUpdate:
			if env.Telemetry != nil {
				s.telemetry.Publish(telemetryEventFromSample(n.id, *env.Telemetry, time.Now().UTC()))
			}
		case protocol.TypeTrafficUpdate:
			if env.Traffic != nil {
				if err := s.store.ApplyTrafficUpdate(n.id, *env.Traffic, time.Now().UTC()); err != nil {
					s.logger.Error("apply Xray traffic update", "agent_id", n.id, "error", err)
				}
			}
		default:
			s.logger.Debug("ignore agent message", "agent_id", n.id, "type", env.Type)
		}
	}
}

func (s *Server) writePump(n *Node) {
	ticker := time.NewTicker(s.cfg.PingInterval)
	defer func() {
		ticker.Stop()
		n.Close()
	}()

	for {
		select {
		case <-n.done:
			return
		case raw := <-n.send:
			_ = n.ws.SetWriteDeadline(time.Now().Add(wsWriteWait))
			if err := n.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
				return
			}
		case <-ticker.C:
			if err := n.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(wsWriteWait)); err != nil {
				return
			}
		}
	}
}

func sanitizeAgentInfo(info protocol.AgentInfo) protocol.AgentInfo {
	info.ID = strings.TrimSpace(info.ID)
	info.Hostname = truncate(strings.TrimSpace(info.Hostname), 255)
	info.OS = truncate(strings.TrimSpace(info.OS), 32)
	info.Arch = truncate(strings.TrimSpace(info.Arch), 32)
	info.Version = truncate(strings.TrimSpace(info.Version), 64)
	plugins := make([]string, 0, min(len(info.Plugins), maxPluginsInInfo))
	seen := make(map[string]struct{}, len(info.Plugins))
	for _, rawName := range info.Plugins {
		name := truncate(strings.TrimSpace(rawName), 64)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		plugins = append(plugins, name)
		if len(plugins) == maxPluginsInInfo {
			break
		}
	}
	info.Plugins = plugins
	return info
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func closeWS(conn *websocket.Conn, code int, reason string) {
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = conn.Close()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return fmt.Sprintf("%s…", s[:max-3])
}
