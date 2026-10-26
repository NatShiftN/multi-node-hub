// Package notifier sends operational events to webhooks and chat bots.
package notifier

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Message   string            `json:"message"`
	Severity  string            `json:"severity"`
	Timestamp time.Time         `json:"timestamp"`
	Fields    map[string]string `json:"fields,omitempty"`
}

func (e Event) Text() string {
	var builder strings.Builder
	title := e.Title
	if title == "" {
		title = e.Type
	}
	builder.WriteString(title)
	if e.Message != "" {
		builder.WriteString("\n")
		builder.WriteString(e.Message)
	}
	if len(e.Fields) > 0 {
		keys := make([]string, 0, len(e.Fields))
		for key := range e.Fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString("\n")
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(e.Fields[key])
		}
	}
	return builder.String()
}

type Notifier interface {
	Name() string
	Send(ctx context.Context, event Event) error
}

type DocumentNotifier interface {
	Name() string
	SendDocument(ctx context.Context, path, caption string) error
}

type Service struct {
	mu        sync.RWMutex
	notifiers []Notifier
	queue     chan Event
	logger    *slog.Logger
}

func NewService(logger *slog.Logger, notifiers ...Notifier) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		notifiers: notifiers,
		queue:     make(chan Event, 128),
		logger:    logger,
	}
}

func (s *Service) SetNotifiers(notifiers []Notifier) {
	s.mu.Lock()
	s.notifiers = append([]Notifier(nil), notifiers...)
	s.mu.Unlock()
}

func (s *Service) HasNotifiers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.notifiers) > 0
}

func (s *Service) Start(ctx context.Context) {
	// Keep the queue consumer alive even when settings are empty at startup;
	// Telegram/webhook can be configured later from the UI.
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.queue:
			s.deliver(ctx, event)
		}
	}
}

func (s *Service) Notify(event Event) {
	if !s.HasNotifiers() {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	select {
	case s.queue <- event:
	default:
		s.logger.Warn("notification queue is full; dropping event", "event", event.Type)
	}
}

func (s *Service) deliver(ctx context.Context, event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.mu.RLock()
	targets := append([]Notifier(nil), s.notifiers...)
	s.mu.RUnlock()
	for _, target := range targets {
		sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := target.Send(sendCtx, event)
		cancel()
		if err != nil {
			s.logger.Warn("send notification", "notifier", target.Name(), "event", event.Type, "error", err)
		}
	}
}

func (s *Service) SendDocument(ctx context.Context, path, caption string) {
	s.mu.RLock()
	targets := append([]Notifier(nil), s.notifiers...)
	s.mu.RUnlock()
	for _, target := range targets {
		document, ok := target.(DocumentNotifier)
		if !ok {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := document.SendDocument(sendCtx, path, caption)
		cancel()
		if err != nil {
			s.logger.Warn("send notification document", "notifier", target.Name(), "error", err)
		}
	}
}

// SendNow is used by tests and explicit synchronous delivery paths.
func (s *Service) SendNow(ctx context.Context, event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	s.deliver(ctx, event)
}
