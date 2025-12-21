# CQRS: Separating Reads and Writes

*This is Part 2 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

In the previous post, we explored Event Sourcing — storing what happened instead of current state. But replaying events every time we need to read data is expensive. What if we could optimize reads and writes independently?

This is where **CQRS** (Command Query Responsibility Segregation) comes in.

## The Problem: One Model for Everything

Traditional architectures use the same model for both reading and writing:

```go
type OrderService struct {
    db *sql.DB
}

// Write operation
func (s *OrderService) PlaceOrder(order *Order) error {
    _, err := s.db.Exec(
        "INSERT INTO orders (id, customer_id, status, total) VALUES (?, ?, ?, ?)",
        order.ID, order.CustomerID, order.Status, order.Total,
    )
    return err
}

// Read operation - same table, same model
func (s *OrderService) GetOrder(id string) (*Order, error) {
    row := s.db.QueryRow("SELECT id, customer_id, status, total FROM orders WHERE id = ?", id)
    order := &Order{}
    err := row.Scan(&order.ID, &order.CustomerID, &order.Status, &order.Total)
    return order, err
}

// Read operation - needs JOIN, getting complex
func (s *OrderService) GetOrderWithItems(id string) (*OrderDetails, error) {
    // Complex query joining orders, items, products, customers...
    // The model is getting pulled in different directions
}
```

As requirements grow, this single model becomes a compromise:
- **Write operations** need validation and business rules
- **Read operations** need denormalized data and joins
- **Different consumers** need different views of the same data

## The CQRS Solution: Two Models

CQRS separates the application into two distinct parts:

```
┌─────────────────────────────────────────────────────────────────┐
│                         Application                              │
├────────────────────────────┬────────────────────────────────────┤
│       Command Side         │           Query Side               │
│                            │                                    │
│  ┌──────────────────┐      │      ┌──────────────────┐          │
│  │    Commands      │      │      │     Queries      │          │
│  │  (PlaceOrder,    │      │      │  (GetOrder,      │          │
│  │   CancelOrder)   │      │      │   ListOrders)    │          │
│  └────────┬─────────┘      │      └────────┬─────────┘          │
│           │                │               │                    │
│           ▼                │               ▼                    │
│  ┌──────────────────┐      │      ┌──────────────────┐          │
│  │ Command Handlers │      │      │  Query Handlers  │          │
│  │  (Aggregates)    │      │      │  (Read Models)   │          │
│  └────────┬─────────┘      │      └────────┬─────────┘          │
│           │                │               │                    │
│           ▼                │               ▼                    │
│  ┌──────────────────┐      │      ┌──────────────────┐          │
│  │   Event Store    │ ────────▶   │  Read Database   │          │
│  │  (Write Model)   │  Events     │  (Projections)   │          │
│  └──────────────────┘      │      └──────────────────┘          │
└────────────────────────────┴────────────────────────────────────┘
```

### Commands: Expressing Intent

Commands represent the intention to change state. They are imperative — they tell the system what to do:

```go
// Commands are named as verbs - what we want to happen
type PlaceOrder struct {
    OrderID    string
    CustomerID string
    Items      []OrderItem
}

type CancelOrder struct {
    OrderID string
    Reason  string
}

type ShipOrder struct {
    OrderID    string
    TrackingNo string
}
```

Commands are processed by **Command Handlers** that:
1. Validate the command
2. Load the aggregate
3. Execute business logic
4. Emit events

A simple switch statement routes each command to its handler:

```go
type CommandHandler struct {
    repository *AggregateRepository
}

func (h *CommandHandler) Handle(ctx context.Context, cmd any) error {
    switch c := cmd.(type) {
    case PlaceOrder:
        order := NewOrderAggregate(c.OrderID)
        if err := order.Place(c.CustomerID, c.Items); err != nil {
            return err
        }
        return h.repository.Save(ctx, order)

    case CancelOrder:
        order, err := h.repository.Load(ctx, c.OrderID)
        if err != nil {
            return err
        }
        if err := order.Cancel(c.Reason); err != nil {
            return err
        }
        return h.repository.Save(ctx, order)

    case ShipOrder:
        order, err := h.repository.Load(ctx, c.OrderID)
        if err != nil {
            return err
        }
        if err := order.Ship(c.TrackingNo); err != nil {
            return err
        }
        return h.repository.Save(ctx, order)

    default:
        return fmt.Errorf("unknown command: %T", cmd)
    }
}
```

### Queries: Asking Questions

Queries retrieve data without modifying state. They are questions — they ask the system for information:

```go
// Queries are named as questions - what we want to know
type GetOrder struct {
    OrderID string
}

type ListOrdersByCustomer struct {
    CustomerID string
    Status     string
    Page       int
    PageSize   int
}

type GetOrderStatistics struct {
    StartDate time.Time
    EndDate   time.Time
}
```

Queries are handled by **Query Handlers** that read from optimized **Read Models**. A simple switch statement routes each query:

```go
type QueryHandler struct {
    readModel *OrderReadModel
}

func (h *QueryHandler) Handle(ctx context.Context, query any) (any, error) {
    switch q := query.(type) {
    case GetOrder:
        return h.readModel.GetOrder(q.OrderID)

    case ListOrdersByCustomer:
        return h.readModel.ListByCustomer(q.CustomerID, q.Status, q.Page, q.PageSize)

    default:
        return nil, fmt.Errorf("unknown query: %T", query)
    }
}
```

## Read Models (Projections)

Read models are denormalized views optimized for specific queries. They're built by processing events:

```go
type OrderView struct {
    OrderID      string
    CustomerID   string
    CustomerName string  // Denormalized!
    Status       string
    Items        []ItemView
    Total        float64
    ItemCount    int     // Pre-calculated!
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type OrderReadModel struct {
    mu         sync.RWMutex
    orders     map[string]*OrderView
    byCustomer map[string][]*OrderView
}

// Apply handles all events that affect this read model
func (rm *OrderReadModel) Apply(event any) error {
    rm.mu.Lock()
    defer rm.mu.Unlock()

    switch e := event.(type) {
    case OrderPlacedEvent:
        view := &OrderView{
            OrderID:    e.OrderID,
            CustomerID: e.CustomerID,
            Status:     "placed",
            Items:      toItemViews(e.Items),
            Total:      e.Total,
            ItemCount:  len(e.Items),
            CreatedAt:  e.PlacedAt,
            UpdatedAt:  e.PlacedAt,
        }
        rm.orders[e.OrderID] = view
        rm.byCustomer[e.CustomerID] = append(rm.byCustomer[e.CustomerID], view)

    case OrderShippedEvent:
        if view, ok := rm.orders[e.OrderID]; ok {
            view.Status = "shipped"
            view.UpdatedAt = e.ShippedAt
        }

    case OrderCancelledEvent:
        if view, ok := rm.orders[e.OrderID]; ok {
            view.Status = "cancelled"
            view.UpdatedAt = e.CancelledAt
        }
    }
    return nil
}

// Queries are simple lookups
func (rm *OrderReadModel) GetOrder(orderID string) (*OrderView, error) {
    rm.mu.RLock()
    defer rm.mu.RUnlock()

    if view, ok := rm.orders[orderID]; ok {
        return view, nil
    }
    return nil, ErrOrderNotFound
}

func (rm *OrderReadModel) ListByCustomer(customerID, status string, page, pageSize int) ([]*OrderView, error) {
    rm.mu.RLock()
    defer rm.mu.RUnlock()

    orders := rm.byCustomer[customerID]

    // Filter by status if provided
    if status != "" {
        filtered := make([]*OrderView, 0)
        for _, o := range orders {
            if o.Status == status {
                filtered = append(filtered, o)
            }
        }
        orders = filtered
    }

    // Paginate
    start := (page - 1) * pageSize
    end := start + pageSize
    if start >= len(orders) {
        return []*OrderView{}, nil
    }
    if end > len(orders) {
        end = len(orders)
    }

    return orders[start:end], nil
}
```

### Why Projections Are Fast

Custom projections include exactly the data your application needs — no more, no less. In practice, up to **95% of all requests** can be answered directly from projections, often **without touching a traditional database**. Most projections are stored in **memory** or **caching systems like Redis**, while the original events remain safely stored in the event store.

In a traditional CRUD system, handling a single request often requires:
- Calling **multiple backend services**
- Performing **multiple live database queries**
- Aggregating results manually

With CQRS and projections:
- A **single query endpoint** responds to the request
- It uses a **precomputed projection** tailored for that use case
- The data is **ready-to-serve**, without real-time joins or cross-service lookups

This difference becomes dramatic at scale. Read-only requests are completely **decoupled** from write operations, enabling high performance without sacrificing consistency or traceability.

## Eventual Consistency

With CQRS, the read model is updated asynchronously after events are written. This means:

```
Command → Event Store → [async] → Read Model → Query
```

There's a brief window where a write has completed but isn't visible in queries yet. This is **eventual consistency**.

```go
// Write side
func (h *CommandHandler) PlaceOrder(ctx context.Context, cmd *PlaceOrder) error {
    order := NewOrderAggregate(cmd.OrderID)
    order.Place(cmd.CustomerID, cmd.Items)

    // Events are persisted
    if err := h.repository.Save(ctx, order); err != nil {
        return err
    }

    // At this point, the write is complete
    // But the read model might not be updated yet!
    return nil
}

// Client code
func (c *Client) PlaceAndVerifyOrder(orderID string, items []Item) error {
    // Place the order
    err := c.commandBus.Dispatch(&PlaceOrder{
        OrderID: orderID,
        Items:   items,
    })
    if err != nil {
        return err
    }

    // Immediately querying might return "not found"!
    order, err := c.queryBus.Query(&GetOrder{OrderID: orderID})
    // err might be ErrOrderNotFound due to eventual consistency

    return nil
}
```

### Handling Eventual Consistency

Several strategies help manage this:

**1. Return the result from the command**
```go
func (h *CommandHandler) PlaceOrder(ctx context.Context, cmd *PlaceOrder) (*OrderPlacedResult, error) {
    // ... process command ...
    return &OrderPlacedResult{
        OrderID: cmd.OrderID,
        Status:  "placed",
    }, nil
}
```

**2. Use synchronous projections for critical paths**
```go
func (h *CommandHandler) PlaceOrder(ctx context.Context, cmd *PlaceOrder) error {
    // ... emit events ...

    // Wait for read model to catch up
    return h.waitForProjection(ctx, cmd.OrderID)
}
```

**3. Design the UI to not need immediate reads**
```
User clicks "Place Order"
→ Show "Order placed successfully"
→ Don't immediately show order details
→ Navigate to order list (which will include new order soon)
```

In **comby**, there's an option to simply wait until a command has finished executing, allowing users to decide whether or not to wait for each command as needed. In practice, users usually wait because the commands are called via the REST API, and REST APIs conventionally expect an updated model. **Comby** achieves this through so-called hooks, which are essentially channels that are sent across the network to subscribers as soon as they are ready. But that's a different, more complex issue.

## Scaling with CQRS

CQRS naturally supports distributed architectures. A common pattern uses a **single write instance** (or a designated set) responsible for processing all commands, while **multiple read instances** handle queries:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        Distributed CQRS                                 │
│                                                                         │
│  ┌─────────────┐     ┌─────────────┐     ┌─────────────┐                │
│  │ Read App A  │     │ Read App B  │     │ Read App C  │  (Scale out)   │
│  │  (Queries)  │     │  (Queries)  │     │  (Queries)  │                │
│  └──────┬──────┘     └──────┬──────┘     └──────┬──────┘                │
│         │                   │                   │                       │
│         │      Commands     │                   │                       │
│         └─────────┬─────────┴─────────┬─────────┘                       │
│                   │                   │                                 │
│                   ▼                   │                                 │
│           ┌─────────────┐             │                                 │
│           │   Broker    │◄────────────┘                                 │
│           │   (NATS)    │          Events                               │
│           └──────┬──────┘                                               │
│                  │                                                      │
│                  ▼                                                      │
│         ┌─────────────────┐                                             │
│         │  Write App      │                                             │
│         │  (Commands +    │  ◄── Single source of truth                 │
│         │   Event Store)  │                                             │
│         └─────────────────┘                                             │
└─────────────────────────────────────────────────────────────────────────┘
```

In this setup:
- **Read-only apps** handle queries directly from their local projections
- **Commands** are forwarded to the write instance via a message broker
- **Events** are published and distributed to all read instances
- Each read instance **updates its own projections** from the event stream

This architecture allows horizontal scaling of read capacity while maintaining consistency through a single authoritative write source. External applications can also subscribe to events without being part of the core system.

## CQRS vs. Traditional CRUD

| Feature                     | CRUD                                  | CQRS + Event Sourcing                          |
|----------------------------|---------------------------------------|------------------------------------------------|
| **Data Handling**          | Overwrites current state              | Appends immutable events; state is replayable  |
| **State History**          | Lost unless manually tracked          | Full, built-in history via the event store     |
| **Read/Write Models**      | Shared schema for reads & writes      | Separate models, each optimized for its purpose|
| **Performance**            | Slower under contention               | High throughput; append-only writes + fast reads|
| **Scalability**            | Mostly vertical; replication is hard  | Naturally horizontal                           |
| **Auditing**               | Requires custom logging               | Built-in audit trail at the event level        |
| **Consistency**            | Strong consistency (ACID)             | Eventual consistency                           |
| **Complexity**             | Easier to start                       | More sophisticated, but highly flexible        |

## When CQRS Makes Sense

CQRS adds complexity. Use it when:

- **Read and write patterns differ significantly**
- **Scalability requirements differ** (many more reads than writes)
- **Complex queries** need denormalized data
- **Multiple read models** serve different consumers
- **Event Sourcing is already in use** (natural fit)

Skip CQRS when:
- Simple CRUD operations suffice
- Read and write patterns are similar
- Team is unfamiliar with the pattern
- Eventual consistency is unacceptable

## Putting It Together

Here's how all handlers are wired together at application startup:

```go
package main

// Application wires all handlers together
type Application struct {
    commands   *CommandHandler
    queries    *QueryHandler
    eventStore *EventStore
    readModel  *OrderReadModel
}

func NewApplication() *Application {
    // Create infrastructure
    eventStore := NewEventStore()
    readModel := NewOrderReadModel()
    repository := NewAggregateRepository(eventStore)

    // Create handlers
    commands := &CommandHandler{repository: repository}
    queries := &QueryHandler{readModel: readModel}

    // Connect event store to read model
    eventStore.Subscribe(func(event any) error {
        return readModel.Apply(event)
    })

    return &Application{
        commands:   commands,
        queries:    queries,
        eventStore: eventStore,
        readModel:  readModel,
    }
}

// Usage
func main() {
    app := NewApplication()
    ctx := context.Background()

    // Handle a command
    err := app.commands.Handle(ctx, PlaceOrder{
        OrderID:    "order-123",
        CustomerID: "customer-456",
        Items:      []OrderItem{{ProductID: "prod-1", Quantity: 2}},
    })
    if err != nil {
        log.Fatal(err)
    }

    // Query the result (after projection catches up)
    result, err := app.queries.Handle(ctx, GetOrder{OrderID: "order-123"})
    if err != nil {
        log.Fatal(err)
    }
    order := result.(*OrderView)
    fmt.Printf("Order status: %s\n", order.Status)
}
```

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/02-cqrs-separating-reads-and-writes/code

When you run the complete example, you'll see CQRS in action:

```
=== CQRS Demo ===

1. Placing an order...
   -> Order placed successfully

2. Querying the order...
   Order ID:    order-123
   Customer:    customer-456
   Status:      placed
   Total:       $109.97
   Items:       2

3. Shipping the order...
   -> Order shipped successfully

4. Querying the updated order...
   Order ID:    order-123
   Customer:    customer-456
   Status:      shipped
   Total:       $109.97
   Items:       2
   Tracking:    TRK-789

5. Placing a second order...
   -> Second order placed

6. Cancelling the second order...
   -> Order cancelled

7. Listing all orders for customer-456...
   Found 2 orders:
   - order-123: shipped (109.97)
   - order-456: cancelled (59.97)

8. Trying to ship a cancelled order (should fail)...
   -> Expected error: order cannot be shipped: status is cancelled

=== Demo Complete ===
```

Notice how:
1. **Commands express intent** — `PlaceOrder`, `ShipOrder`, `CancelOrder` tell the system what to do
2. **Queries return optimized views** — `GetOrder` and `ListOrdersByCustomer` read from denormalized projections
3. **Events update read models** — The `OrderReadModel` is automatically updated when events are dispatched
4. **Business rules are enforced** — A cancelled order cannot be shipped (the aggregate protects its invariants)

---

## What's Next

In the next post, we'll explore **Aggregates** — the consistency boundaries that encapsulate business logic and ensure data integrity in an event-sourced system.