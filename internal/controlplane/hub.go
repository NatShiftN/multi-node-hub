package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"nodehub/internal/protocol"
)

var (
	ErrNodeNotFound   = errors.New("node not found")
	ErrNodeOffline    = errors.New("node offline")
	ErrCommandTimeout = errors.New("command timed out")
)

const (
	nodeSendQueue = 128
	sendTimeout   = 2 * time.Second
)

// NodeView is the public REST representation. It intentionally never includes
// credentials or the WebSocket connection.
type NodeView struct {
	ID            string    `json:"id"`
	Hostname      string    `json:"hostname"`
	IP            string    `json:"ip"`
	Status        string    `json:"status"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSeen      time.Time `json:"last_seen"`
	OnlineSeconds int64     `json:"online_seconds"`
	AgentVersion  string    `json:"agent_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	Plugins       []string  `json:"plugins"`
}

type Node struct {
	id          string
	info        protocol.AgentInfo
	remoteIP    string
	connectedAt time.Time
	lastSeen    atomic.Int64

	ws        *websocket.Conn
	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func newNode(info protocol.AgentInfo, remoteIP string, ws *websocket.Conn) *Node {
	now := time.Now().UTC()
	n := &Node{
		id:          info.ID,
		info:        info,
		remoteIP:    remoteIP,
		connectedAt: now,
		ws:          ws,
		send:        make(chan []byte, nodeSendQueue),
		done:        make(chan struct{}),
	}
	n.lastSeen.Store(now.UnixNano())
	return n
}

func (n *Node) touch() {
	n.lastSeen.Store(time.Now().UTC().UnixNano())
}

func (n *Node) view(now time.Time) NodeView {
	lastSeen := time.Unix(0, n.lastSeen.Load()).UTC()
	if lastSeen.IsZero() {
		lastSeen = n.connectedAt
	}
	online := int64(now.Sub(n.connectedAt).Seconds())
	if online < 0 {
		online = 0
	}
	plugins := append([]string(nil), n.info.Plugins...)
	hostname := n.info.Hostname
	if hostname == "" {
		hostname = n.id
	}
	return NodeView{
		ID:            n.id,
		Hostname:      hostname,
		IP:            n.remoteIP,
		Status:        "online",
		ConnectedAt:   n.connectedAt,
		LastSeen:      lastSeen,
		OnlineSeconds: online,
		AgentVersion:  n.info.Version,
		OS:            n.info.OS,
		Arch:          n.info.Arch,
		Plugins:       plugins,
	}
}

func (n *Node) enqueue(raw []byte) error {
	timer := time.NewTimer(sendTimeout)
	defer timer.Stop()
	select {
	case n.send <- raw:
		return nil
	case <-n.done:
		return ErrNodeOffline
	case <-timer.C:
		return errors.New("agent send queue is full")
	}
}

// Close is idempotent and safe to call from both pumps.
func (n *Node) Close() {
	n.closeOnce.Do(func() {
		close(n.done)
		_ = n.ws.Close()
	})
}

type pendingCommand struct {
	requestID string
	node      *Node
	result    chan protocol.Envelope
}

type Hub struct {
	mu      sync.RWMutex
	nodes   map[string]*Node
	pending map[string]*pendingCommand
}

func NewHub() *Hub {
	return &Hub{
		nodes:   make(map[string]*Node),
		pending: make(map[string]*pendingCommand),
	}
}

// Register replaces an existing connection with the same node ID. The old
// connection is closed so a restarted agent does not leave a zombie entry.
func (h *Hub) Register(n *Node) *Node {
	h.mu.Lock()
	previous := h.nodes[n.id]
	h.nodes[n.id] = n
	h.mu.Unlock()

	if previous != nil {
		h.failPendingFor(previous, errors.New("agent reconnected; command aborted"))
		previous.Close()
	}
	return previous
}

func (h *Hub) Unregister(n *Node) bool {
	h.mu.Lock()
	removed := false
	if current, ok := h.nodes[n.id]; ok && current == n {
		delete(h.nodes, n.id)
		removed = true
	}
	h.mu.Unlock()

	h.failPendingFor(n, ErrNodeOffline)
	n.Close()
	return removed
}

func (h *Hub) Disconnect(nodeID string) {
	h.mu.Lock()
	node := h.nodes[nodeID]
	if node != nil {
		delete(h.nodes, nodeID)
	}
	h.mu.Unlock()
	if node != nil {
		h.failPendingFor(node, ErrNodeOffline)
		node.Close()
	}
}

func (h *Hub) List() []NodeView {
	h.mu.RLock()
	nodes := make([]*Node, 0, len(h.nodes))
	for _, n := range h.nodes {
		nodes = append(nodes, n)
	}
	h.mu.RUnlock()

	now := time.Now().UTC()
	views := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, n.view(now))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].ConnectedAt.Equal(views[j].ConnectedAt) {
			return views[i].ID < views[j].ID
		}
		return views[i].ConnectedAt.Before(views[j].ConnectedAt)
	})
	return views
}

func (h *Hub) Get(id string) (NodeView, bool) {
	h.mu.RLock()
	n, ok := h.nodes[id]
	h.mu.RUnlock()
	if !ok {
		return NodeView{}, false
	}
	return n.view(time.Now().UTC()), true
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

func (h *Hub) Execute(ctx context.Context, nodeID, plugin string, payload json.RawMessage, timeout time.Duration) (protocol.Envelope, time.Duration, error) {
	started := time.Now()
	if plugin == "" {
		return protocol.Envelope{}, 0, errors.New("plugin is required")
	}
	if timeout <= 0 {
		return protocol.Envelope{}, 0, errors.New("command timeout must be positive")
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return protocol.Envelope{}, 0, errors.New("payload is not valid JSON")
	}

	h.mu.RLock()
	node := h.nodes[nodeID]
	h.mu.RUnlock()
	if node == nil {
		return protocol.Envelope{}, 0, ErrNodeNotFound
	}

	requestID, err := newRequestID()
	if err != nil {
		return protocol.Envelope{}, 0, fmt.Errorf("generate request id: %w", err)
	}
	pending := &pendingCommand{
		requestID: requestID,
		node:      node,
		result:    make(chan protocol.Envelope, 1),
	}

	h.mu.Lock()
	if current := h.nodes[nodeID]; current != node {
		h.mu.Unlock()
		return protocol.Envelope{}, 0, ErrNodeOffline
	}
	h.pending[requestID] = pending
	h.mu.Unlock()
	defer h.removePending(requestID, pending)

	env := protocol.Envelope{
		Type:      protocol.TypeCommand,
		RequestID: requestID,
		Plugin:    plugin,
		Payload:   payload,
		TimeoutMS: timeout.Milliseconds(),
		SentAt:    time.Now().UTC(),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return protocol.Envelope{}, 0, fmt.Errorf("encode command: %w", err)
	}
	if err := node.enqueue(raw); err != nil {
		return protocol.Envelope{}, 0, fmt.Errorf("send command: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case result := <-pending.result:
		return result, time.Since(started), nil
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return protocol.Envelope{}, time.Since(started), fmt.Errorf("%w after %s", ErrCommandTimeout, timeout)
		}
		return protocol.Envelope{}, time.Since(started), waitCtx.Err()
	case <-node.done:
		return protocol.Envelope{}, time.Since(started), ErrNodeOffline
	}
}

func (h *Hub) ResolveResult(env protocol.Envelope) {
	if env.RequestID == "" {
		return
	}
	h.mu.Lock()
	pending, ok := h.pending[env.RequestID]
	if ok {
		delete(h.pending, env.RequestID)
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	select {
	case pending.result <- env:
	default:
	}
}

func (h *Hub) removePending(requestID string, pending *pendingCommand) {
	h.mu.Lock()
	if current, ok := h.pending[requestID]; ok && current == pending {
		delete(h.pending, requestID)
	}
	h.mu.Unlock()
}

func (h *Hub) failPendingFor(node *Node, cause error) {
	type failed struct {
		requestID string
		ch        chan protocol.Envelope
	}
	var toNotify []failed

	h.mu.Lock()
	for id, pending := range h.pending {
		if pending.node == node {
			delete(h.pending, id)
			toNotify = append(toNotify, failed{requestID: id, ch: pending.result})
		}
	}
	h.mu.Unlock()

	for _, item := range toNotify {
		item.ch <- protocol.Envelope{
			Type:      protocol.TypeResult,
			RequestID: item.requestID,
			Error:     cause.Error(),
			SentAt:    time.Now().UTC(),
		}
	}
}

func (h *Hub) CloseAll() {
	h.mu.Lock()
	nodes := make([]*Node, 0, len(h.nodes))
	for id, n := range h.nodes {
		nodes = append(nodes, n)
		delete(h.nodes, id)
	}
	h.mu.Unlock()

	for _, n := range nodes {
		h.failPendingFor(n, errors.New("control plane shutting down"))
		n.Close()
	}
}

func newRequestID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
