package scanner

import "sync"

// InFlight tracks which issue keys are currently being processed,
// preventing concurrent processing of the same issue.
type InFlight struct {
	mu    sync.Mutex
	items map[string]struct{}
}

func NewInFlight() *InFlight {
	return &InFlight{items: make(map[string]struct{})}
}

// TryAcquire attempts to mark a key as in-flight. Returns true if
// acquired, false if the key is already being processed.
func (f *InFlight) TryAcquire(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.items[key]; exists {
		return false
	}
	f.items[key] = struct{}{}
	return true
}

// Release marks a key as no longer in-flight.
func (f *InFlight) Release(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, key)
}
