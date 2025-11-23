package progress

import (
	"fmt"
	"sync"
	"time"
)

// Bar represents a simple progress bar
type Bar struct {
	total     int
	current   int
	mu        sync.Mutex
	startTime time.Time
	lastPrint time.Time
	done      bool
}

// New creates a new progress bar
func New(total int) *Bar {
	return &Bar{
		total:     total,
		current:   0,
		startTime: time.Now(),
		lastPrint: time.Now(),
	}
}

// Increment increases the progress counter
func (b *Bar) Increment() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.current++

	// Update display every 500ms or when complete
	now := time.Now()
	if now.Sub(b.lastPrint) > 500*time.Millisecond || b.current >= b.total {
		b.render()
		b.lastPrint = now
	}
}

// Finish marks the progress as complete
func (b *Bar) Finish() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.done {
		b.current = b.total
		b.render()
		fmt.Println() // New line after completion
		b.done = true
	}
}

// render displays the progress bar
func (b *Bar) render() {
	if b.done {
		return
	}

	percentage := float64(b.current) / float64(b.total) * 100
	elapsed := time.Since(b.startTime)

	// Calculate ETA
	var eta time.Duration
	if b.current > 0 {
		avgTime := elapsed / time.Duration(b.current)
		remaining := b.total - b.current
		eta = avgTime * time.Duration(remaining)
	}

	// Progress bar width
	barWidth := 40
	filled := int(float64(barWidth) * float64(b.current) / float64(b.total))

	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}

	// Format output
	fmt.Printf("\r[%s] %d/%d (%.1f%%) - Elapsed: %s - ETA: %s   ",
		bar,
		b.current,
		b.total,
		percentage,
		formatDuration(elapsed),
		formatDuration(eta),
	)
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
