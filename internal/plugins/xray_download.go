package plugins

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maxXrayArchiveBytes = 150 << 20
	maxXrayBinaryBytes  = 200 << 20
)

func xrayAssetName() (string, error) {
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return "Xray-linux-64.zip", nil
		case "arm64":
			return "Xray-linux-arm64-v8a.zip", nil
		case "386":
			return "Xray-linux-32.zip", nil
		case "arm":
			return "Xray-linux-arm32-v7a.zip", nil
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return "Xray-windows-64.zip", nil
		case "386":
			return "Xray-windows-32.zip", nil
		case "arm64":
			return "Xray-windows-arm64-v8a.zip", nil
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64":
			return "Xray-macos-64.zip", nil
		case "arm64":
			return "Xray-macos-arm64-v8a.zip", nil
		}
	}
	return "", fmt.Errorf("no Xray release asset for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func xrayDownloadURL(override string) (string, error) {
	asset, err := xrayAssetName()
	if err != nil {
		return "", err
	}
	if override == "" {
		return "https://github.com/XTLS/Xray-core/releases/latest/download/" + asset, nil
	}
	if strings.HasSuffix(override, "/") {
		return override + asset, nil
	}
	return override, nil
}

func downloadXrayBinary(ctx context.Context, url, destination string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build Xray download request: %w", err)
	}
	req.Header.Set("User-Agent", "nodehub-agent")
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download Xray: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download Xray: HTTP %s", response.Status)
	}
	if response.ContentLength > maxXrayArchiveBytes {
		return fmt.Errorf("Xray archive is too large: %d bytes", response.ContentLength)
	}

	archive, err := os.CreateTemp(filepath.Dir(destination), "xray-download-*.zip")
	if err != nil {
		return fmt.Errorf("create Xray temp archive: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)

	written, err := io.Copy(archive, io.LimitReader(response.Body, maxXrayArchiveBytes+1))
	closeErr := archive.Close()
	if err != nil {
		return fmt.Errorf("write Xray archive: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close Xray archive: %w", closeErr)
	}
	if written > maxXrayArchiveBytes {
		return fmt.Errorf("Xray archive exceeds %d bytes", maxXrayArchiveBytes)
	}
	return extractXrayFromArchive(archivePath, destination)
}

func extractXrayFromArchive(archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open Xray archive: %w", err)
	}
	defer reader.Close()

	binaryName := "xray"
	if runtime.GOOS == "windows" {
		binaryName = "xray.exe"
	}
	var entry *zip.File
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || file.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if strings.EqualFold(filepath.Base(file.Name), binaryName) {
			entry = file
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("Xray archive does not contain %s", binaryName)
	}
	if entry.UncompressedSize64 > maxXrayBinaryBytes {
		return fmt.Errorf("Xray binary is too large: %d bytes", entry.UncompressedSize64)
	}

	source, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open Xray binary in archive: %w", err)
	}
	defer source.Close()

	temp, err := os.CreateTemp(filepath.Dir(destination), "xray-binary-*")
	if err != nil {
		return fmt.Errorf("create Xray binary temp file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, err := io.Copy(temp, io.LimitReader(source, maxXrayBinaryBytes+1))
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("extract Xray binary: %w", err)
	}
	if written > maxXrayBinaryBytes {
		_ = temp.Close()
		return fmt.Errorf("Xray binary exceeds %d bytes", maxXrayBinaryBytes)
	}
	if err := temp.Chmod(0o755); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod Xray binary: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Xray binary: %w", err)
	}
	if err := os.Rename(tempPath, destination); err != nil {
		// Windows refuses to replace an existing executable in some states.
		_ = os.Remove(destination)
		if retryErr := os.Rename(tempPath, destination); retryErr != nil {
			return fmt.Errorf("install Xray binary: %w", retryErr)
		}
	}
	return nil
}
