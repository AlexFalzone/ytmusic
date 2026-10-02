package fingerprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type Result struct {
	Duration    int // seconds
	Fingerprint string
}

func Generate(ctx context.Context, path string) (Result, error) {
	cmd := exec.CommandContext(ctx, "fpcalc", "-raw", "-json", path)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("fpcalc failed for %q: %w", path, err)
	}

	var raw struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Result{}, fmt.Errorf("fpcalc output parse failed: %w", err)
	}
	if raw.Fingerprint == "" {
		return Result{}, fmt.Errorf("fpcalc returned empty fingerprint for %q", path)
	}

	return Result{
		Duration:    int(raw.Duration),
		Fingerprint: raw.Fingerprint,
	}, nil
}
