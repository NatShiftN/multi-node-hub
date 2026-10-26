package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"nodehub/internal/notifier"
)

func (s *Server) backupLoop(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), s.cfg.BackupHour, 0, 0, 0, time.Local)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if path, err := s.backupOnce(); err != nil {
				s.logger.Error("scheduled database backup", "error", err)
			} else {
				s.logger.Info("scheduled database backup completed", "path", path)
			}
		}
	}
}

func (s *Server) backupOnce() (string, error) {
	if err := os.MkdirAll(s.cfg.BackupDir, 0o700); err != nil {
		return "", err
	}
	name := "nodehub_" + time.Now().Format("20060102_150405") + ".db"
	path := filepath.Join(s.cfg.BackupDir, name)
	if err := s.store.BackupTo(path); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	if stat, err := os.Stat(path); err == nil {
		s.auditSystem("system:backup", "backup", "", map[string]any{
			"file": name,
			"size": stat.Size(),
		})
		s.notify(notifier.Event{
			Type:     "backup_completed",
			Title:    "Database backup completed",
			Message:  "A consistent SQLite snapshot was written.",
			Severity: "info",
			Fields: map[string]string{
				"file": name,
				"size": fmt.Sprintf("%d", stat.Size()),
			},
		})
		s.notifications.SendDocument(context.Background(), path, "NodeHub database backup "+name)
	}
	return path, nil
}

func (s *Server) handleBackupDownload(c *gin.Context) {
	tempDir, err := os.MkdirTemp("", "nodehub-backup-*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create backup workspace"})
		return
	}
	defer os.RemoveAll(tempDir)
	path := filepath.Join(tempDir, "nodehub.db")
	if err := s.store.BackupTo(path); err != nil {
		s.logger.Error("manual database backup", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database backup failed"})
		return
	}
	file, err := os.Open(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to open backup"})
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stat backup"})
		return
	}
	filename := "nodehub_" + time.Now().Format("20060102_150405") + ".db"
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.DataFromReader(http.StatusOK, stat.Size(), "application/octet-stream", file, nil)
	s.audit(c, "backup_download", "", map[string]any{"file": filename, "size": stat.Size()})
}
