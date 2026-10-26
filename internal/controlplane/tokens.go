package controlplane

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"nodehub/internal/protocol"
	"nodehub/internal/store"
)

const deployTokenTTL = 15 * time.Minute

type tokenPersistence interface {
	ListTokens() ([]store.Token, error)
	ReplaceToken(nodeID, tokenHash string, at time.Time) error
	MarkTokenUsed(tokenHash string, at time.Time) error
	DeleteToken(tokenHash string) error
}

type deployTokenRecord struct {
	nodeID    string
	expiresAt time.Time
	connected bool
}

// TokenStore authorizes both the legacy shared token and per-deployment
// NodeTokens. Only SHA-256 digests are retained, in memory and in SQLite.
// Tokens survive control-plane restarts so deployed agents reconnect cleanly.
type TokenStore struct {
	mu          sync.Mutex
	staticToken string
	tokens      map[[sha256.Size]byte]*deployTokenRecord
	ttl         time.Duration
	now         func() time.Time
	repo        tokenPersistence
	logger      *slog.Logger
}

func NewTokenStore(staticToken string, ttl time.Duration, repo tokenPersistence, logger *slog.Logger) *TokenStore {
	if ttl <= 0 {
		ttl = deployTokenTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TokenStore{
		staticToken: staticToken,
		tokens:      make(map[[sha256.Size]byte]*deployTokenRecord),
		ttl:         ttl,
		now:         time.Now,
		repo:        repo,
		logger:      logger,
	}
}

// Load hydrates the authorization cache from SQLite. It is called once during
// control-plane startup, before the HTTP server accepts agents.
func (s *TokenStore) Load() error {
	if s.repo == nil {
		return nil
	}
	records, err := s.repo.ListTokens()
	if err != nil {
		return fmt.Errorf("load node tokens: %w", err)
	}
	loaded := make(map[[sha256.Size]byte]*deployTokenRecord, len(records))
	for _, record := range records {
		rawHash, err := hex.DecodeString(record.TokenHash)
		if err != nil || len(rawHash) != sha256.Size {
			s.logger.Warn("skip invalid token hash in database", "node_id", record.NodeID)
			continue
		}
		var digest [sha256.Size]byte
		copy(digest[:], rawHash)
		loaded[digest] = &deployTokenRecord{
			nodeID:    record.NodeID,
			expiresAt: record.CreatedAt.Add(s.ttl),
			connected: record.FirstUsedAt != nil,
		}
	}
	s.mu.Lock()
	s.tokens = loaded
	s.mu.Unlock()
	return nil
}

// StaticToken returns the shared deployment token configured at startup. It
// is used by the DD bootstrap path so reinstallation does not invalidate the
// currently connected Agent's per-node credential.
func (s *TokenStore) StaticToken() string { return s.staticToken }

// Issue creates a 256-bit NodeToken, persists only its digest, and binds it to
// nodeID. Any previous token for the same node is revoked.
func (s *TokenStore) Issue(nodeID string) (string, error) {
	if !protocol.ValidAgentID(nodeID) {
		return "", errors.New("invalid node id")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(digest[:])
	now := s.now()

	if s.repo != nil {
		if err := s.repo.ReplaceToken(nodeID, tokenHash, now); err != nil {
			return "", fmt.Errorf("persist node token: %w", err)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for sum, record := range s.tokens {
		if record.nodeID == nodeID || (!record.connected && now.After(record.expiresAt)) {
			delete(s.tokens, sum)
		}
	}
	s.tokens[digest] = &deployTokenRecord{
		nodeID:    nodeID,
		expiresAt: now.Add(s.ttl),
	}
	return token, nil
}

// Authorize checks a raw token against the shared token or a pending/active
// deployment token. Pending tokens expire after the provisioning TTL; once an
// agent has connected, the token remains valid for reconnects.
func (s *TokenStore) Authorize(token, nodeID string) bool {
	if token == "" || !protocol.ValidAgentID(nodeID) {
		return false
	}
	if s.staticToken != "" &&
		subtle.ConstantTimeCompare([]byte(token), []byte(s.staticToken)) == 1 {
		return true
	}

	digest := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(digest[:])
	now := s.now()

	s.mu.Lock()
	record, ok := s.tokens[digest]
	if !ok || record.nodeID != nodeID {
		s.mu.Unlock()
		return false
	}
	if !record.connected && now.After(record.expiresAt) {
		delete(s.tokens, digest)
		s.mu.Unlock()
		if s.repo != nil {
			if err := s.repo.DeleteToken(tokenHash); err != nil {
				s.logger.Warn("delete expired token", "node_id", nodeID, "error", err)
			}
		}
		return false
	}
	wasConnected := record.connected
	record.connected = true
	s.mu.Unlock()

	if !wasConnected && s.repo != nil {
		if err := s.repo.MarkTokenUsed(tokenHash, now); err != nil {
			s.logger.Warn("persist token first use", "node_id", nodeID, "error", err)
		}
	}
	return true
}

// RevokeNode removes every in-memory deployment token bound to nodeID and
// deletes their persisted digests. The shared static token is deliberately
// handled by the node decommission tombstone instead.
func (s *TokenStore) RevokeNode(nodeID string) {
	if s == nil || nodeID == "" {
		return
	}
	s.mu.Lock()
	var hashes []string
	for digest, record := range s.tokens {
		if record.nodeID == nodeID {
			hashes = append(hashes, hex.EncodeToString(digest[:]))
			delete(s.tokens, digest)
		}
	}
	s.mu.Unlock()
	if s.repo != nil {
		for _, hash := range hashes {
			if err := s.repo.DeleteToken(hash); err != nil {
				s.logger.Warn("delete revoked node token", "node_id", nodeID, "error", err)
			}
		}
	}
}

// Revoke removes a deployment token, for example after SSH installation fails.
func (s *TokenStore) Revoke(token string) {
	if token == "" {
		return
	}
	digest := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(digest[:])
	s.mu.Lock()
	delete(s.tokens, digest)
	s.mu.Unlock()
	if s.repo != nil {
		if err := s.repo.DeleteToken(tokenHash); err != nil {
			s.logger.Warn("delete node token", "error", err)
		}
	}
}

func (s *TokenStore) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	now := s.now()
	for _, record := range s.tokens {
		if !record.connected && !now.After(record.expiresAt) {
			count++
		}
	}
	return count
}
