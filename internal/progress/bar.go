package progress

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const barWidth = 40

type Bar struct {
	total     int
	current   int
	mu        sync.Mutex
	startTime time.Time
	lastPrint time.Time
	done      bool
}

func New(total int) *Bar {
	return &Bar{
		total:     total,
		startTime: time.Now(),
		lastPrint: time.Now(),
	}
}

func (b *Bar) Increment() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.current++

	now := time.Now()
	if now.Sub(b.lastPrint) > 500*time.Millisecond || b.current >= b.total {
		b.render()
		b.lastPrint = now
	}
}

func (b *Bar) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.done {
		b.current = b.total
		b.render()
		fmt.Println()
		b.done = true
	}
}

func (b *Bar) render() {
	if b.done {
		return
	}

	percentage := float64(b.current) / float64(b.total) * 100
	elapsed := time.Since(b.startTime)

	var eta time.Duration
	if b.current > 0 {
		eta = elapsed / time.Duration(b.current) * time.Duration(b.total-b.current)
	}

	filled := min(barWidth*b.current/b.total, barWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	fmt.Printf("\r[%s] %d/%d (%.1f%%) - Elapsed: %s - ETA: %s   ",
		bar, b.current, b.total, percentage, formatDuration(elapsed), formatDuration(eta))
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
