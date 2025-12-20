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

A naive approach uses a switch statement, but this quickly becomes unwieldy. A more elegant solution uses **typed handler functions** registered by command type:

```go
// CommandHandlerFunc is a typed handler for a specific command
type CommandHandlerFunc[T any] func(ctx context.Context, cmd T) error

// CommandDispatcher routes commands to their registered handlers
type CommandDispatcher struct {
    handlers map[reflect.Type]any
}

func NewCommandDispatcher() *CommandDispatcher {
    return &CommandDispatcher{
        handlers: make(map[reflect.Type]any),
    }
}

// Register adds a typed handler for a specific command type
func Register[T any](d *CommandDispatcher, handler CommandHandlerFunc[T]) {
    var zero T
    d.handlers[reflect.TypeOf(zero)] = handler
}

// Dispatch routes a command to its registered handler
func (d *CommandDispatcher) Dispatch(ctx context.Context, cmd any) error {
    handler, ok := d.handlers[reflect.TypeOf(cmd)]
    if !ok {
        return fmt.Errorf("no handler for command: %T", cmd)
    }
    // Type-safe invocation via reflection
    return invokeHandler(ctx, handler, cmd)
}
```

Each command handler is a standalone function, registered at startup:

```go
type OrderCommandHandlers struct {
    repository *AggregateRepository
}

func (h *OrderCommandHandlers) HandlePlaceOrder(ctx context.Context, cmd PlaceOrder) error {
    order := NewOrderAggregate(cmd.OrderID)
    if err := order.Place(cmd.CustomerID, cmd.Items); err != nil {
        return err
    }
    return h.repository.Save(ctx, order)
}

func (h *OrderCommandHandlers) HandleCancelOrder(ctx context.Context, cmd CancelOrder) error {
    order, err := h.repository.Load(ctx, cmd.OrderID)
    if err != nil {
        return err
    }
    if err := order.Cancel(cmd.Reason); err != nil {
        return err
    }
    return h.repository.Save(ctx, order)
}

// Registration at startup
func SetupCommandHandlers(dispatcher *CommandDispatcher, repo *AggregateRepository) {
    handlers := &OrderCommandHandlers{repository: repo}
    Register(dispatcher, handlers.HandlePlaceOrder)
    Register(dispatcher, handlers.HandleCancelOrder)
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

Queries are handled by **Query Handlers** that read from optimized **Read Models**. Following the same pattern as commands, we use typed handler registration:

```go
// QueryHandlerFunc is a typed handler returning a specific result type
type QueryHandlerFunc[Q any, R any] func(ctx context.Context, query Q) (R, error)

// QueryDispatcher routes queries to their registered handlers
type QueryDispatcher struct {
    handlers map[reflect.Type]any
}

func NewQueryDispatcher() *QueryDispatcher {
    return &QueryDispatcher{
        handlers: make(map[reflect.Type]any),
    }
}

// RegisterQuery adds a typed handler for a specific query type
func RegisterQuery[Q any, R any](d *QueryDispatcher, handler QueryHandlerFunc[Q, R]) {
    var zero Q
    d.handlers[reflect.TypeOf(zero)] = handler
}

// Query dispatches a query and returns the result
func Query[R any](d *QueryDispatcher, ctx context.Context, query any) (R, error) {
    var zero R
    handler, ok := d.handlers[reflect.TypeOf(query)]
    if !ok {
        return zero, fmt.Errorf("no handler for query: %T", query)
    }
    return invokeQueryHandler[R](ctx, handler, query)
}
```

Query handlers are standalone functions focused on a single query:

```go
type OrderQueryHandlers struct {
    readModel *OrderReadModel
}

func (h *OrderQueryHandlers) HandleGetOrder(ctx context.Context, q GetOrder) (*OrderView, error) {
    return h.readModel.GetOrder(q.OrderID)
}

func (h *OrderQueryHandlers) HandleListByCustomer(ctx context.Context, q ListOrdersByCustomer) ([]*OrderView, error) {
    return h.readModel.ListByCustomer(q.CustomerID, q.Status, q.Page, q.PageSize)
}

// Registration at startup
func SetupQueryHandlers(dispatcher *QueryDispatcher, readModel *OrderReadModel) {
    handlers := &OrderQueryHandlers{readModel: readModel}
    RegisterQuery(dispatcher, handlers.HandleGetOrder)
    RegisterQuery(dispatcher, handlers.HandleListByCustomer)
}
```

## Read Models (Projections)

Read models are denormalized views optimized for specific queries. They're built by processing events. Following the same pattern as commands and queries, we use typed event handler registration:

```go
// EventHandlerFunc is a typed handler for a specific event
type EventHandlerFunc[T any] func(event T) error

// EventDispatcher routes events to registered handlers (supports multiple handlers per event)
type EventDispatcher struct {
    handlers map[reflect.Type][]any
}

func NewEventDispatcher() *EventDispatcher {
    return &EventDispatcher{
        handlers: make(map[reflect.Type][]any),
    }
}

// RegisterEventHandler adds a handler for a specific event type
func RegisterEventHandler[T any](d *EventDispatcher, handler EventHandlerFunc[T]) {
    var zero T
    eventType := reflect.TypeOf(zero)
    d.handlers[eventType] = append(d.handlers[eventType], handler)
}

// Dispatch sends an event to all registered handlers
func (d *EventDispatcher) Dispatch(event any) error {
    handlers, ok := d.handlers[reflect.TypeOf(event)]
    if !ok {
        return nil // No handlers for this event is valid
    }
    for _, handler := range handlers {
        if err := invokeEventHandler(handler, event); err != nil {
            return err
        }
    }
    return nil
}
```

**Important distinction**: Unlike commands and queries — where by convention exactly one handler processes each type — events can have **multiple handlers**. This is intentional: a single `OrderPlacedEvent` might update an `OrderReadModel`, a `CustomerStatisticsModel`, and an `InventoryProjection` simultaneously. The `EventDispatcher` stores handlers in a slice (`[]any`) rather than a single value, and iterates through all of them on dispatch.

The read model registers handler functions for each event type it cares about:

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

// Each event handler is a separate method
func (rm *OrderReadModel) OnOrderPlaced(event OrderPlacedEvent) error {
    rm.mu.Lock()
    defer rm.mu.Unlock()

    view := &OrderView{
        OrderID:    event.OrderID,
        CustomerID: event.CustomerID,
        Status:     "placed",
        Items:      toItemViews(event.Items),
        Total:      event.Total,
        ItemCount:  len(event.Items),
        CreatedAt:  event.PlacedAt,
        UpdatedAt:  event.PlacedAt,
    }
    rm.orders[event.OrderID] = view
    rm.byCustomer[event.CustomerID] = append(rm.byCustomer[event.CustomerID], view)
    return nil
}

func (rm *OrderReadModel) OnOrderShipped(event OrderShippedEvent) error {
    rm.mu.Lock()
    defer rm.mu.Unlock()

    if view, ok := rm.orders[event.OrderID]; ok {
        view.Status = "shipped"
        view.UpdatedAt = event.ShippedAt
    }
    return nil
}

// Registration at startup
func SetupOrderProjection(dispatcher *EventDispatcher, readModel *OrderReadModel) {
    RegisterEventHandler(dispatcher, readModel.OnOrderPlaced)
    RegisterEventHandler(dispatcher, readModel.OnOrderShipped)
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

Here's how all dispatchers are wired together at application startup:

```go
package main

// Application wires all dispatchers together
type Application struct {
    commands *CommandDispatcher
    queries  *QueryDispatcher
    events   *EventDispatcher

    eventStore *EventStore
    readModel  *OrderReadModel
}

func NewApplication() *Application {
    // Create dispatchers
    commands := NewCommandDispatcher()
    queries := NewQueryDispatcher()
    events := NewEventDispatcher()

    // Create infrastructure
    eventStore := NewEventStore()
    readModel := NewOrderReadModel()
    repository := NewAggregateRepository(eventStore)

    // Register command handlers
    orderCommands := &OrderCommandHandlers{repository: repository}
    Register(commands, orderCommands.HandlePlaceOrder)
    Register(commands, orderCommands.HandleCancelOrder)
    Register(commands, orderCommands.HandleShipOrder)

    // Register query handlers
    orderQueries := &OrderQueryHandlers{readModel: readModel}
    RegisterQuery(queries, orderQueries.HandleGetOrder)
    RegisterQuery(queries, orderQueries.HandleListByCustomer)

    // Register event handlers (projections)
    RegisterEventHandler(events, readModel.OnOrderPlaced)
    RegisterEventHandler(events, readModel.OnOrderShipped)
    RegisterEventHandler(events, readModel.OnOrderCancelled)

    // Connect event store to event dispatcher
    eventStore.Subscribe(func(event any) error {
        return events.Dispatch(event)
    })

    return &Application{
        commands:   commands,
        queries:    queries,
        events:     events,
        eventStore: eventStore,
        readModel:  readModel,
    }
}

// Usage
func main() {
    app := NewApplication()
    ctx := context.Background()

    // Dispatch a command
    err := app.commands.Dispatch(ctx, PlaceOrder{
        OrderID:    "order-123",
        CustomerID: "customer-456",
        Items:      []OrderItem{{ProductID: "prod-1", Quantity: 2}},
    })
    if err != nil {
        log.Fatal(err)
    }

    // Query the result (after projection catches up)
    order, err := Query[*OrderView](app.queries, ctx, GetOrder{OrderID: "order-123"})
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Order status: %s\n", order.Status)
}
```

This registry-based approach provides several benefits over switch statements:
- **Type safety**: Each handler has its exact input/output types
- **Separation of concerns**: Handlers are standalone, testable functions
- **Extensibility**: Adding new commands/queries/events requires no changes to dispatchers
- **Framework evolution**: The registration mechanism can be extended (validation, middleware, etc.)

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
5. **Typed dispatch** — Each handler is registered by its command/query/event type, no switch statements needed

## What's Next

In the next post, we'll explore **Aggregates** — the consistency boundaries that encapsulate business logic and ensure data integrity in an event-sourced system.

---

*This is Part 2 of the "Building Event-Sourced Systems in Go" series. We've seen how CQRS separates concerns and enables optimization. Next, we'll dive into the heart of domain logic: Aggregates.*
