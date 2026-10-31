//go:build !unix

package downloader

import "os/exec"

func killGroupOnCancel(*exec.Cmd) {}
