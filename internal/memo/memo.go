// Package memo remembers the results of expensive calls for one run: provider
// searches, MusicBrainz lookups, fingerprints. Nothing outlives the run, and
// a failure is never remembered, so a timeout cannot stick.
package memo

import "sync"

// Cache maps keys to the results of successful calls. The zero value is ready
// to use. Concurrent misses on one key may each make the call; the last
// result is kept, which is harmless for calls that answer the same each time.
type Cache[K comparable, V any] struct {
	mu   sync.Mutex
	vals map[K]V
}

// Do returns the value remembered for key, or calls fn and remembers what it
// returns unless it fails.
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
