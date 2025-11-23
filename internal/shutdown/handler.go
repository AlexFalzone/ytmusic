package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// Handler manages graceful shutdown
type Handler struct {
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	cleanupFns []func()
	mu         sync.Mutex
}

// New creates a new shutdown handler
func New() *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{
		ctx:    ctx,
		cancel: cancel,
	}
}

// Context returns the shutdown context
func (h *Handler) Context() context.Context {
	return h.ctx
}

// AddCleanup registers a cleanup function to be called on shutdown
func (h *Handler) AddCleanup(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupFns = append(h.cleanupFns, fn)
}

// Listen starts listening for shutdown signals
func (h *Handler) Listen() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		h.Shutdown()
	}()
}

// Shutdown triggers graceful shutdown
func (h *Handler) Shutdown() {
	h.cancel()

	// Run cleanup functions
	h.mu.Lock()
	fns := h.cleanupFns
	h.mu.Unlock()

	for _, fn := range fns {
		fn()
	}
}

// Wait waits for all work to complete
func (h *Handler) Wait() {
	h.wg.Wait()
}

// Add increments the work counter
func (h *Handler) Add(delta int) {
	h.wg.Add(delta)
}

// Done decrements the work counter
func (h *Handler) Done() {
	h.wg.Done()
}
