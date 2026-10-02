package memo

import "sync"

// Failures are never cached, so a timeout cannot stick. Not singleflight: concurrent misses each make the call.
type Cache[K comparable, V any] struct {
	mu   sync.Mutex
	vals map[K]V
}

func (c *Cache[K, V]) Do(key K, fn func() (V, error)) (V, error) {
	c.mu.Lock()
	v, ok := c.vals[key]
	c.mu.Unlock()
	if ok {
		return v, nil
	}

	v, err := fn()
	if err != nil {
		return v, err
	}

	c.mu.Lock()
	if c.vals == nil {
		c.vals = make(map[K]V)
	}
	c.vals[key] = v
	c.mu.Unlock()
	return v, nil
}

func (c *Cache[K, V]) Forget(key K) {
	c.mu.Lock()
	delete(c.vals, key)
	c.mu.Unlock()
}
