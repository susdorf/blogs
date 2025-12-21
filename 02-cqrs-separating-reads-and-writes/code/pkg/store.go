package pkg

import (
	"context"
	"fmt"
	"sync"
)

// EventStore persists and retrieves events
type EventStore struct {
	mu     sync.RWMutex
	events map[string][]any // aggregateID -> events
}

func NewEventStore() *EventStore {
	return &EventStore{
		events: make(map[string][]any),
	}
}

// Append stores new events for an aggregate
func (s *EventStore) Append(ctx context.Context, aggregateID string, events []any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[aggregateID] = append(s.events[aggregateID], events...)
	return nil
}

// Load retrieves all events for an aggregate
func (s *EventStore) Load(ctx context.Context, aggregateID string) ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[aggregateID]
	result := make([]any, len(events))
	copy(result, events)
	return result, nil
}

// AggregateRepository provides load/save operations for aggregates
type AggregateRepository struct {
	store     *EventStore
	readModel *OrderReadModel
}

func NewAggregateRepository(store *EventStore, readModel *OrderReadModel) *AggregateRepository {
	return &AggregateRepository{
		store:     store,
		readModel: readModel,
	}
}

// Save persists uncommitted events from an aggregate
func (r *AggregateRepository) Save(ctx context.Context, aggregate *OrderAggregate) error {
	events := aggregate.GetUncommittedEvents()
	if len(events) == 0 {
		return nil
	}

	// Persist events
	if err := r.store.Append(ctx, aggregate.ID, events); err != nil {
		return err
	}

	// Update read model (projection)
	for _, event := range events {
		if err := r.readModel.Apply(event); err != nil {
			return err
		}
	}

	aggregate.ClearUncommittedEvents()
	return nil
}

// Load rebuilds an aggregate from its event history
func (r *AggregateRepository) Load(ctx context.Context, aggregateID string) (*OrderAggregate, error) {
	events, err := r.store.Load(ctx, aggregateID)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("aggregate not found: %s", aggregateID)
	}

	aggregate := NewOrderAggregate(aggregateID)
	aggregate.LoadFromHistory(events)
	return aggregate, nil
}
