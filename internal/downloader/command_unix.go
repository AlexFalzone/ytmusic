//go:build unix

package downloader

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// yt-dlp runs ffmpeg as a child, which killing yt-dlp alone would leave running in a folder about to be removed.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
