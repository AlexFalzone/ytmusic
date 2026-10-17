package fingerprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type Result struct {
	Duration    int // seconds
	Fingerprint string
}

// Without -raw fpcalc prints the compressed string AcoustID expects; -raw prints an integer array it rejects.
func Generate(ctx context.Context, path string) (Result, error) {
	cmd := exec.CommandContext(ctx, "fpcalc", "-json", path)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return Result{}, fmt.Errorf("fpcalc: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return Result{}, fmt.Errorf("fpcalc: %w", err)
	}

	var raw struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Result{}, fmt.Errorf("parsing fpcalc output: %w", err)
	}
	if raw.Fingerprint == "" {
		return Result{}, fmt.Errorf("fpcalc returned an empty fingerprint")
	}

	return Result{
		Duration:    int(raw.Duration),
		Fingerprint: raw.Fingerprint,
	}, nil
}
