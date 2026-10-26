//go:build windows

package plugins

import (
	"errors"
	"os"
	"os/exec"
)

func configureXrayProcess(_ *exec.Cmd) {}

func terminateXrayProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func killXrayProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
