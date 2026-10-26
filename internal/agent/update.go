package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodehub/internal/protocol"
)

const (
	maxAgentUpdateSize = 128 << 20
	updateRetryDelay   = 10 * time.Minute
)

type Updater struct {
	currentVersion string
	logger         *slog.Logger
	client         *http.Client

	mu          sync.Mutex
	inFlight    bool
	lastAttempt time.Time
	lastTarget  string

	execPath      string
	restart       func(context.Context) error
	exit          func(int)
	allowNonLinux bool
}

func NewUpdater(currentVersion string, logger *slog.Logger) *Updater {
	if logger == nil {
		logger = slog.Default()
	}
	executable, _ := os.Executable()
	return &Updater{
		currentVersion: currentVersion,
		logger:         logger,
		client:         &http.Client{Timeout: 2 * time.Minute},
		execPath:       executable,
		restart: func(ctx context.Context) error {
			return exec.CommandContext(ctx, "systemctl", "restart", "nodehub-agent").Run()
		},
		exit: os.Exit,
	}
}

func (u *Updater) Offer(info protocol.AgentVersionInfo) {
	if info.Version == "" {
		return
	}
	if info.OS != "" && info.OS != runtime.GOOS {
		u.logger.Info("skip OTA: target OS differs", "target", info.OS, "runtime", runtime.GOOS)
		return
	}
	if info.Arch != "" && info.Arch != runtime.GOARCH {
		u.logger.Info("skip OTA: target arch differs", "target", info.Arch, "runtime", runtime.GOARCH)
		return
	}
	if compareVersions(info.Version, u.currentVersion) <= 0 {
		return
	}
	u.mu.Lock()
	if u.inFlight {
		u.mu.Unlock()
		return
	}
	if u.lastAttempt.Add(updateRetryDelay).After(time.Now()) && u.lastTarget == info.Version {
		u.mu.Unlock()
		return
	}
	u.inFlight = true
	u.lastAttempt = time.Now()
	u.lastTarget = info.Version
	u.mu.Unlock()

	go func() {
		defer func() {
			u.mu.Lock()
			u.inFlight = false
			u.mu.Unlock()
		}()
		u.logger.Warn("OTA update starting", "current", u.currentVersion, "target", info.Version)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := u.apply(ctx, info); err != nil {
			u.logger.Error("OTA update failed", "target", info.Version, "error", err)
		}
	}()
}

func (u *Updater) apply(ctx context.Context, info protocol.AgentVersionInfo) error {
	if runtime.GOOS != "linux" && !u.allowNonLinux {
		return errors.New("OTA rename/restart is only enabled on Linux agents")
	}
	if info.DownloadURL == "" || info.SHA256 == "" {
		return errors.New("OTA metadata is missing download URL or checksum")
	}
	parsed, err := url.Parse(info.DownloadURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("OTA download URL must be http(s)")
	}
	execPath := u.execPath
	if execPath == "" {
		return errors.New("cannot resolve current executable")
	}
	temp, err := os.CreateTemp(filepath.Dir(execPath), "."+filepath.Base(execPath)+".new-*")
	if err != nil {
		return fmt.Errorf("create OTA temporary file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		_ = temp.Close()
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o755); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, info.DownloadURL, nil)
	if err != nil {
		return err
	}
	response, err := u.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("OTA download HTTP %d", response.StatusCode)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(response.Body, maxAgentUpdateSize+1))
	if err != nil {
		return err
	}
	if written > maxAgentUpdateSize {
		return errors.New("OTA binary exceeds size limit")
	}
	if info.Size > 0 && written != info.Size {
		return fmt.Errorf("OTA size mismatch: got %d want %d", written, info.Size)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(digest, info.SHA256) {
		return errors.New("OTA checksum mismatch")
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	// Rename is atomic and avoids "text file busy" from writing the live inode.
	if err := os.Rename(tempPath, execPath); err != nil {
		return fmt.Errorf("replace agent binary: %w", err)
	}
	cleanup = false
	u.logger.Warn("OTA binary replaced; restarting agent", "path", execPath)

	restartCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := u.restart(restartCtx); err != nil {
		u.logger.Warn("systemctl restart failed; exiting for service manager", "error", err)
	}
	time.Sleep(200 * time.Millisecond)
	u.exit(0)
	return nil
}

func compareVersions(a, b string) int {
	parse := func(raw string) ([]int, string) {
		raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V"))
		core := raw
		pre := ""
		if index := strings.IndexByte(raw, '-'); index >= 0 {
			core = raw[:index]
			pre = raw[index+1:]
		}
		parts := strings.Split(core, ".")
		nums := make([]int, 0, len(parts))
		for _, part := range parts {
			value, _ := strconv.Atoi(part)
			nums = append(nums, value)
		}
		return nums, pre
	}
	aNums, aPre := parse(a)
	bNums, bPre := parse(b)
	for index := 0; index < len(aNums) || index < len(bNums); index++ {
		var left, right int
		if index < len(aNums) {
			left = aNums[index]
		}
		if index < len(bNums) {
			right = bNums[index]
		}
		if left > right {
			return 1
		}
		if left < right {
			return -1
		}
	}
	if aPre == bPre {
		return 0
	}
	if aPre == "" {
		return 1
	}
	if bPre == "" {
		return -1
	}
	if aPre > bPre {
		return 1
	}
	return -1
}
