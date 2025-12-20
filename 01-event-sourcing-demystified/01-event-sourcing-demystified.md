# Event Sourcing Demystified

*Part 1 of the "Building Event-Sourced Systems in Go" series*

---

## Introduction

Most applications store the current state of their data. When a user updates their profile, we overwrite the old values with new ones. When an order status changes from "pending" to "shipped," we update a single row in the database.

But what if we're asking the wrong question? Instead of asking "What is the current state?", what if we asked "What happened?"

This shift in perspective is the essence of **Event Sourcing**.

## The Traditional Approach: State-Based Storage

Consider a simple e-commerce order:

```go
type Order struct {
    ID        string
    Status    string
    Items     []Item
    Total     float64
    UpdatedAt time.Time
}
```

In a traditional system, when the order ships, we simply update the status:

```sql
UPDATE orders SET status = 'shipped', updated_at = NOW() WHERE id = '123';
```

This works, but we've lost information. We know the order is shipped, but:
- When was it placed?
- When was it paid?
- Was it ever modified?
- Who made each change?

We've traded history for simplicity.

## The Event Sourcing Approach: Store What Happened

Event Sourcing flips this model. Instead of storing current state, we store the sequence of events that led to that state:

```go
// Events describe what happened
type OrderPlacedEvent struct {
    OrderID      string
    CustomerID   string
    Items        []Item
    Total        float64
    PlacedAt     time.Time
}

type OrderPaidEvent struct {
    OrderID   string
    PaymentID string
    Amount    float64
    PaidAt    time.Time
}

type OrderShippedEvent struct {
    OrderID    string
    TrackingNo string
    ShippedAt  time.Time
}
```

The order's history becomes a sequence:

```
[OrderPlacedEvent] → [OrderPaidEvent] → [OrderShippedEvent]
```

To get the current state, we replay these events:

```go
// Note: This is not production code.
func (o *Order) Apply(evt Event) {
    switch e := evt.(type) {
    case *OrderPlacedEvent:
        o.ID = e.OrderID
        o.Status = "placed"
        o.Items = e.Items
        o.Total = e.Total
    case *OrderPaidEvent:
        o.Status = "paid"
    case *OrderShippedEvent:
        o.Status = "shipped"
        o.TrackingNo = e.TrackingNo
    }
}
```

Applying the events could look like this:

```go
o := &Order{...}
o.Apply(&OrderPlacedEvent{...})
o.Apply(&OrderPaidEvent{...})
o.Apply(&OrderShippedEvent{...})
```

## The Append-Only Log

Events are stored in an **append-only log**. This is a crucial constraint:

- Events are **immutable** — once written, never changed
- Events are **ordered** — sequence matters
- Events are **complete** — contain all information about what happened

```
┌─────────────────────────────────────────────────────────┐
│                    Event Store                          │
├─────────────────────────────────────────────────────────┤
│ Seq 1: OrderPlacedEvent   { orderId: "123", ... }       │
│ Seq 2: OrderPaidEvent     { orderId: "123", ... }       │
│ Seq 3: OrderShippedEvent  { orderId: "123", ... }       │
│ Seq 4: OrderPlacedEvent   { orderId: "456", ... }       │
│ ...                                                     │
└─────────────────────────────────────────────────────────┘
```

In Go, a simple event store interface might look like:

```go
type EventStore interface {
    // Append events for an aggregate
    Append(ctx context.Context, aggregateID string, events []Event) error

    // Load all events for an aggregate
    Load(ctx context.Context, aggregateID string) ([]Event, error)

    // Load events starting from a specific version
    LoadFrom(ctx context.Context, aggregateID string, fromVersion int64) ([]Event, error)
}
```

## Time Travel: State at Any Point

One of the most powerful capabilities of Event Sourcing is reconstructing state at any point in time.

```go
func (store *EventStore) GetStateAt(aggregateID string, timestamp time.Time) (*Order, error) {
    events, err := store.Load(ctx, aggregateID)
    if err != nil {
        return nil, err
    }

    order := &Order{}
    for _, event := range events {
        if event.Timestamp().After(timestamp) {
            break // Stop at the requested point in time
        }
        order.Apply(event)
    }

    return order, nil
}
```

This enables:
- **Debugging**: See exactly what the state was when a bug occurred
- **Auditing**: Reconstruct state for compliance reporting
- **Analytics**: Answer questions about historical patterns

## Audit Trail for Free

Every event is a record of something that happened. You get a complete audit trail without any additional code:

```go
type Event interface {
    EventID() string
    EventType() string
    AggregateID() string
    Timestamp() time.Time
    Version() int64
    Data() interface{}

    // Audit information
    UserID() string      // Who triggered this event
    CorrelationID() string // Which request/transaction
}
```

Compliance requirements like "show me all changes to this customer's data" become trivial queries against the event store.

## A Simple Implementation in Go

Let's build a minimal event-sourced system:

```go
package eventsourcing

import (
    "context"
    "encoding/json"
    "sync"
    "time"
)

// Event represents something that happened
type Event struct {
    ID            string          `json:"id"`
    Type          string          `json:"type"`
    AggregateID   string          `json:"aggregate_id"`
    AggregateType string          `json:"aggregate_type"`
    Version       int64           `json:"version"`
    Timestamp     time.Time       `json:"timestamp"`
    Data          json.RawMessage `json:"data"`
}

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
```

## Using the Event Store

```go
func main() {
    store := NewInMemoryEventStore()
    ctx := context.Background()

    // Place an order
    orderID := "order-123"
    placedEvent := Event{
        ID:            "evt-1",
        Type:          "OrderPlaced",
        AggregateID:   orderID,
        AggregateType: "Order",
        Version:       0,
        Timestamp:     time.Now(),
        Data:          json.RawMessage(`{"customer":"Alice","total":99.99}`),
    }

    store.Append(ctx, orderID, []Event{placedEvent})

    // Ship the order
    shippedEvent := Event{
        ID:            "evt-2",
        Type:          "OrderShipped",
        AggregateID:   orderID,
        AggregateType: "Order",
        Version:       1,
        Timestamp:     time.Now(),
        Data:          json.RawMessage(`{"tracking":"TRK-456"}`),
    }

    store.Append(ctx, orderID, []Event{shippedEvent})

    // Replay to get current state
    events, _ := store.Load(ctx, orderID)
    order := &Order{}
    for _, evt := range events {
        order.Apply(evt)
    }

    fmt.Printf("Order %s: status=%s, tracking=%s\n",
        order.ID, order.Status, order.TrackingNo)
}

// Order represents the current state of an order
type Order struct {
    ID         string
    Status     string
    Customer   string
    Total      float64
    TrackingNo string
}

// Apply updates the order state based on an event
func (o *Order) Apply(evt Event) {
    switch evt.Type {
    case "OrderPlaced":
        var data struct {
            Customer string  `json:"customer"`
            Total    float64 `json:"total"`
        }
        json.Unmarshal(evt.Data, &data)
        o.ID = evt.AggregateID
        o.Status = "placed"
        o.Customer = data.Customer
        o.Total = data.Total
    case "OrderShipped":
        var data struct {
            Tracking string `json:"tracking"`
        }
        json.Unmarshal(evt.Data, &data)
        o.Status = "shipped"
        o.TrackingNo = data.Tracking
    }
}
```


## Running an Example

Source: https://github.com/susdorf/blogs/tree/main/building-event-sourced-systems-in-go/01-event-sourcing-demystified/code

When you run the complete example, you'll see Event Sourcing in action:

```
=== Event Sourcing Demo ===

1. Placing order...
   -> Appended: OrderPlaced (version 0)
2. Shipping order...
   -> Appended: OrderShipped (version 1)

=== Event Store Contents ===
   [0] OrderPlaced | order-123 | v0
   [1] OrderShipped | order-123 | v1

=== Replaying Events ===
   Applied OrderPlaced     -> status=placed
   Applied OrderShipped    -> status=shipped

=== Current State (reconstructed from events) ===
   Order ID:   order-123
   Customer:   Alice
   Total:      99.99
   Status:     shipped
   Tracking:   TRK-456
```

Notice how:
1. **Events are appended** to the store with incrementing versions
2. **The event store** contains the complete history of what happened
3. **Replaying events** reconstructs the current state step by step
4. **The final state** is derived entirely from the event sequence


## When to Use Event Sourcing

Event Sourcing shines when:

- **Audit requirements** are strict (finance, healthcare, legal)
- **Temporal queries** are needed ("What was the state last Tuesday?")
- **Complex domain logic** benefits from event-driven thinking
- **Debugging production issues** requires understanding what happened
- **Analytics** need access to historical data

It may not be worth the complexity when:

- Simple CRUD is sufficient
- History has no business value
- The team is unfamiliar with the pattern

## What's Next

In the next post, we'll explore **CQRS (Command Query Responsibility Segregation)** — a pattern that pairs naturally with Event Sourcing to optimize both reads and writes.

---

*This is Part 1 of the "Building Event-Sourced Systems in Go" series. In the upcoming posts, we'll dive deeper into CQRS, Aggregates, and the Go patterns that make these architectures elegant.*
