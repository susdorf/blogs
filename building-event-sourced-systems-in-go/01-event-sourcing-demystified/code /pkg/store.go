package pkg

import (
	"context"
	"fmt"
	"sync"
)

// InMemoryEventStore is a simple event store for demonstration
type InMemoryEventStore struct {
	mu     sync.RWMutex
	events map[string][]Event // aggregateID -> events
}

func NewInMemoryEventStore() *InMemoryEventStore {
	return &InMemoryEventStore{
		events: make(map[string][]Event),
	}
}

func (s *InMemoryEventStore) Append(ctx context.Context, aggregateID string, newEvents []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing := s.events[aggregateID]
	expectedVersion := int64(len(existing))

	for i, event := range newEvents {
		// Optimistic concurrency check
		if event.Version != expectedVersion+int64(i) {
			return fmt.Errorf("concurrency conflict: expected version %d, got %d",
				expectedVersion+int64(i), event.Version)
		}
	}

	s.events[aggregateID] = append(existing, newEvents...)
	return nil
}

func (s *InMemoryEventStore) Load(ctx context.Context, aggregateID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[aggregateID]
	// Return a copy to prevent mutation
	result := make([]Event, len(events))
	copy(result, events)
	return result, nil
}
