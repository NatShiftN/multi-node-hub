package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type instanceCleaner interface {
	DeleteInstance(ctx context.Context, instanceID string) error
}

func (s *Server) orphanCleanupLoop(ctx context.Context) {
	// Catch up immediately after a restart, then retry on a bounded interval.
	s.cleanupCloudOrphans(ctx)
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanupCloudOrphans(ctx)
		}
	}
}

func (s *Server) cleanupCloudOrphans(ctx context.Context) {
	orphans, err := s.store.ListCloudOrphans(50)
	if err != nil {
		s.logger.Error("list cloud orphans", "error", err)
		return
	}
	for _, orphan := range orphans {
		provider, ok := s.cloudProviderFor(orphan.Provider)
		if !ok {
			continue
		}
		cleaner, ok := provider.(instanceCleaner)
		if !ok {
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := cleaner.DeleteInstance(attemptCtx, orphan.InstanceID)
		cancel()
		detail := ""
		if err != nil && strings.Contains(err.Error(), "HTTP 404") {
			// Already deleted outside the control plane.
			err = nil
		}
		if err != nil {
			detail = err.Error()
		}
		nextAttempts := orphan.Attempts + 1
		if markErr := s.store.MarkCloudOrphanResult(orphan.ID, detail, err == nil, time.Now().UTC()); markErr != nil {
			s.logger.Error("update cloud orphan", "id", orphan.ID, "error", markErr)
		}
		if err != nil {
			s.logger.Warn("cloud orphan cleanup failed",
				"provider", orphan.Provider, "instance_id", orphan.InstanceID,
				"attempt", nextAttempts, "error", err)
			if nextAttempts >= 12 {
				s.notifyCloudOrphan(orphan.Provider, orphan.InstanceID, fmt.Sprintf("gave up after %d attempts: %v", nextAttempts, err))
			}
			continue
		}
		s.logger.Info("cloud orphan deleted",
			"provider", orphan.Provider, "instance_id", orphan.InstanceID, "attempt", nextAttempts)
	}
}
