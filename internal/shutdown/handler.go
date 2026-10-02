package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type Handler struct {
	ctx        context.Context
	cancel     context.CancelFunc
	cleanupFns []func()
	mu         sync.Mutex
	once       sync.Once
}

func New() *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{
		ctx:    ctx,
		cancel: cancel,
	}
}

func (h *Handler) Context() context.Context {
	return h.ctx
}

func (h *Handler) AddCleanup(fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupFns = append(h.cleanupFns, fn)
}

func (h *Handler) Listen() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		h.Shutdown()
	}()
}

func (h *Handler) Shutdown() {
	h.once.Do(func() {
		h.cancel()

		h.mu.Lock()
		fns := h.cleanupFns
		h.mu.Unlock()

		for _, fn := range fns {
			fn()
		}
	})
}
