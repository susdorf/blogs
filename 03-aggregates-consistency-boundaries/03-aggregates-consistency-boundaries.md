# Aggregates: The Consistency Boundaries

*This is Part 3 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

In the previous posts, we explored Event Sourcing and CQRS. But where does business logic live? How do we ensure that orders can't be shipped before being paid, or that inventory doesn't go negative?

Enter **Aggregates** — the guardians of business rules and consistency in Domain-Driven Design.

## What Is an Aggregate?

An Aggregate is a cluster of domain objects that:
1. **Encapsulates business logic** — rules are enforced here
2. **Maintains consistency** — all invariants are checked before changes
3. **Acts as a transaction boundary** — changes are atomic within an aggregate
4. **Emits events** — records what happened after successful operations

Think of an Aggregate as the gatekeeper. Nothing changes without going through it, and it ensures all rules are followed.

**Important**: Aggregates are **short-living objects**. They are created, used, and discarded within a single transaction. They are not meant to be persisted directly — only their events are stored. When needed again, the aggregate is reconstructed by replaying its event history.

## Anatomy of an Aggregate

```go
package aggregate

import (
    "errors"
    "time"
)

// Order is our aggregate root
type Order struct {
    // Identity
    ID string

    // State
    CustomerID string
    Status     string
    Items      []OrderItem
    Total      float64

    // Version for optimistic concurrency
    Version int64

    // Uncommitted events (changes not yet persisted)
    changes []Event
}

type OrderItem struct {
    ProductID string
    Quantity  int
    Price     float64
}

// NewOrder creates a new Order aggregate
func NewOrder(id string) *Order {
    return &Order{
        ID:      id,
        Status:  "new",
        Items:   make([]OrderItem, 0),
        changes: make([]Event, 0),
    }
}
```

## Business Logic as Intentions

Aggregates expose methods that represent **intentions** — what the user wants to do. These methods:
1. Validate business rules
2. Create events if validation passes
3. Apply events to update internal state

```go
var (
    ErrOrderAlreadyPlaced = errors.New("order already placed")
    ErrEmptyOrder         = errors.New("order must have at least one item")
    ErrInvalidQuantity    = errors.New("quantity must be positive")
    ErrOrderNotPaid       = errors.New("order must be paid before shipping")
    ErrOrderAlreadyShipped = errors.New("order already shipped")
    ErrCannotCancelShipped = errors.New("cannot cancel shipped order")
)

// Place validates and places a new order
func (o *Order) Place(customerID string, items []OrderItem) error {
    // Business rule: can't place an already placed order
    if o.Status != "new" {
        return ErrOrderAlreadyPlaced
    }

    // Business rule: order must have items
    if len(items) == 0 {
        return ErrEmptyOrder
    }

    // Business rule: all quantities must be positive
    var total float64
    for _, item := range items {
        if item.Quantity <= 0 {
            return ErrInvalidQuantity
        }
        total += float64(item.Quantity) * item.Price
    }

    // All rules passed — emit event
    event := &OrderPlacedEvent{
        OrderID:    o.ID,
        CustomerID: customerID,
        Items:      items,
        Total:      total,
        PlacedAt:   time.Now(),
    }

    o.apply(event, true) // true = this is a new event
    return nil
}

// Pay marks the order as paid
func (o *Order) Pay(paymentID string, amount float64) error {
    if o.Status != "placed" {
        return errors.New("order must be placed before payment")
    }

    if amount < o.Total {
        return errors.New("payment amount insufficient")
    }

    event := &OrderPaidEvent{
        OrderID:   o.ID,
        PaymentID: paymentID,
        Amount:    amount,
        PaidAt:    time.Now(),
    }

    o.apply(event, true)
    return nil
}

// Ship marks the order as shipped
func (o *Order) Ship(trackingNo string) error {
    // Business rule: must be paid before shipping
    if o.Status != "paid" {
        return ErrOrderNotPaid
    }

    event := &OrderShippedEvent{
        OrderID:    o.ID,
        TrackingNo: trackingNo,
        ShippedAt:  time.Now(),
    }

    o.apply(event, true)
    return nil
}

// Cancel cancels the order
func (o *Order) Cancel(reason string) error {
    // Business rule: can't cancel shipped orders
    if o.Status == "shipped" {
        return ErrCannotCancelShipped
    }

    event := &OrderCancelledEvent{
        OrderID:     o.ID,
        Reason:      reason,
        CancelledAt: time.Now(),
    }

    o.apply(event, true)
    return nil
}
```

Notice that intentions only modify the aggregate **in-memory**. The aggregate itself never persists its changes — it simply collects uncommitted events. Persistence is the responsibility of the caller, typically a Command Handler:

```go
func (h *OrderCommandHandlers) HandlePlaceOrder(ctx context.Context, cmd PlaceOrder) error {
    order := NewOrder(cmd.OrderID)

    // Intention modifies aggregate in-memory only
    if err := order.Place(cmd.CustomerID, cmd.Items); err != nil {
        return err
    }

    // Caller is responsible for persistence
    return h.repository.Save(ctx, order)
}
```

This separation has a significant benefit: **aggregates are trivial to test**. Business logic can be verified without any infrastructure — no databases, no event stores, no repositories:

```go
func TestOrder_CannotShipUnpaidOrder(t *testing.T) {
    order := NewOrder("order-123")
    order.Place("customer-1", []OrderItem{{ProductID: "prod-1", Quantity: 1, Price: 10.0}})

    err := order.Ship("TRACK-123")

    if err != ErrOrderNotPaid {
        t.Errorf("expected ErrOrderNotPaid, got %v", err)
    }
}
```

## Event Handlers: Updating State

Event handlers are a fundamental concept that appears in multiple contexts throughout an event-sourced system:

| Context | Purpose | Maintains State? |
|---------|---------|------------------|
| **Aggregates** | Rebuild internal state from events | Yes (in-memory) |
| **Read Models** | Build queryable projections | Yes (persisted) |
| **Reactors** | Trigger side effects and workflows | No |

Within an **aggregate**, event handlers modify internal state. They are called both when:
- **New events** are created (from intentions)
- **Historical events** are replayed (when loading from storage)

**Read Models** (covered in the previous post) subscribe to events to build denormalized query views.

**Reactors** are a special type of event handler that don't maintain state — they react to events by triggering side effects: sending emails, calling external APIs, or dispatching new commands to orchestrate cross-domain workflows (as we'll see in "Design for Eventual Consistency" below).

Here's how event handlers work within an aggregate:

```go
// apply handles an event, optionally recording it as new
func (o *Order) apply(event Event, isNew bool) {
    // Update state based on event type
    switch e := event.(type) {
    case *OrderPlacedEvent:
        o.onOrderPlaced(e)
    case *OrderPaidEvent:
        o.onOrderPaid(e)
    case *OrderShippedEvent:
        o.onOrderShipped(e)
    case *OrderCancelledEvent:
        o.onOrderCancelled(e)
    }

    // Increment version
    o.Version++

    // Track new events for persistence
    if isNew {
        o.changes = append(o.changes, event)
    }
}

func (o *Order) onOrderPlaced(e *OrderPlacedEvent) {
    o.CustomerID = e.CustomerID
    o.Items = e.Items
    o.Total = e.Total
    o.Status = "placed"
}

func (o *Order) onOrderPaid(e *OrderPaidEvent) {
    o.Status = "paid"
}

func (o *Order) onOrderShipped(e *OrderShippedEvent) {
    o.Status = "shipped"
}

func (o *Order) onOrderCancelled(e *OrderCancelledEvent) {
    o.Status = "cancelled"
}

// GetUncommittedEvents returns events that haven't been persisted
func (o *Order) GetUncommittedEvents() []Event {
    return o.changes
}

// ClearUncommittedEvents clears the list after persistence
func (o *Order) ClearUncommittedEvents() {
    o.changes = make([]Event, 0)
}
```

## Loading Aggregates from Events

To reconstitute an aggregate, we replay all its historical events:

```go
// LoadFromHistory rebuilds the aggregate from events
func (o *Order) LoadFromHistory(events []Event) {
    for _, event := range events {
        o.apply(event, false) // false = not a new event
    }
}

// Repository handles persistence
type OrderRepository struct {
    eventStore EventStore
}

func (r *OrderRepository) Load(ctx context.Context, orderID string) (*Order, error) {
    events, err := r.eventStore.Load(ctx, orderID)
    if err != nil {
        return nil, err
    }

    if len(events) == 0 {
        return nil, ErrOrderNotFound
    }

    order := NewOrder(orderID)
    order.LoadFromHistory(events)
    return order, nil
}

func (r *OrderRepository) Save(ctx context.Context, order *Order) error {
    events := order.GetUncommittedEvents()
    if len(events) == 0 {
        return nil // Nothing to save
    }

    err := r.eventStore.Append(ctx, order.ID, events)
    if err != nil {
        return err
    }

    order.ClearUncommittedEvents()
    return nil
}
```

## The State Machine Perspective

An aggregate can be viewed as a state machine:

```
                    ┌─────────────────┐
                    │      new        │
                    └────────┬────────┘
                             │ Place()
                             ▼
                   ┌─────────────────┐
         ┌─────────│     placed      │─────────┐
         │         └────────┬────────┘         │
         │                  │ Pay()            │ Cancel()
         │                  ▼                  │
         │         ┌─────────────────┐         │
         │         │      paid       │         │
         │         └────────┬────────┘         │
         │                  │ Ship()           │
         │                  ▼                  ▼
         │         ┌─────────────────┐  ┌─────────────────┐
         │         │    shipped      │  │   cancelled     │
         │         └─────────────────┘  └─────────────────┘
         │                                     ▲
         └─────────────────────────────────────┘
```

Business rules are the guards on state transitions:
- `Place()`: Only from "new" state
- `Pay()`: Only from "placed" state
- `Ship()`: Only from "paid" state
- `Cancel()`: From any state except "shipped"

## Aggregate Structure: References, Entities, and Value Objects

An aggregate typically contains three types of members:

```go
type Order struct {
    ID         string       // Aggregate identity

    // References — IDs pointing to other aggregates
    CustomerID string       // Reference to Customer aggregate

    // Entities — objects with their own identity, managed by this aggregate
    Items      []OrderItem  // Entities within the aggregate

    // Value Objects — simple immutable values
    Status     string
    Total      float64
}
```

### References

An aggregate can only maintain **references by ID** to other aggregates. It never holds direct object references. This ensures each aggregate remains the sole authority over its internal state.

### Entities

Entities are objects with their own identity that live within the aggregate boundary. The key characteristic: **entities cannot exist without their parent aggregate**.

Consider an `Identity` aggregate with a `Profile` entity. If the `Identity` is deleted, the `Profile` is automatically deleted as well. This strong lifecycle dependency is why `Profile` is modeled as an entity, not a separate aggregate.

```go
type OrderItem struct {
    ItemID    string  // Local identity (unique within Order)
    ProductID string
    Quantity  int
    Price     float64
}

// Adding an item goes through the aggregate root
func (o *Order) AddItem(productID string, quantity int, price float64) error {
    if o.Status != "placed" {
        return errors.New("can only add items to placed orders")
    }

    // Check if item already exists
    for _, item := range o.Items {
        if item.ProductID == productID {
            // Update quantity instead
            event := &OrderItemQuantityChangedEvent{
                OrderID:     o.ID,
                ItemID:      item.ItemID,
                OldQuantity: item.Quantity,
                NewQuantity: item.Quantity + quantity,
            }
            o.apply(event, true)
            return nil
        }
    }

    // Add new item
    event := &OrderItemAddedEvent{
        OrderID:   o.ID,
        ItemID:    generateItemID(),
        ProductID: productID,
        Quantity:  quantity,
        Price:     price,
    }
    o.apply(event, true)
    return nil
}

// You can't modify OrderItem directly — must go through Order
// o.Items[0].Quantity = 5  // BAD: bypasses business rules
// o.UpdateItem(productID, 5, price)  // GOOD: goes through aggregate
```

### Value Objects

Value objects are immutable and have no identity — they are defined solely by their attributes. `Status` and `Total` in our Order example are value objects.

### Entity vs. Aggregate: The Lifecycle Question

How do you decide whether something should be an entity or a separate aggregate? Ask: **Can it exist independently?**

| Scenario | Model As | Reason |
|----------|----------|--------|
| `Order` and `OrderItem` | Aggregate + Entity | An OrderItem cannot exist without its Order |
| `Identity` and `Profile` | Aggregate + Entity | A Profile cannot exist without its Identity |
| `Identity` and `Account` | Two Aggregates | An Account can have multiple Identities; both can exist independently |
| `Customer` and `Order` | Two Aggregates | Customers exist without orders; orders reference customers by ID |

## Aggregate Design Guidelines

### Keep Aggregates Small

Large aggregates lead to:
- More concurrency conflicts
- Larger event streams to replay
- Complex business logic

```go
// BAD: Customer aggregate with all orders
type Customer struct {
    ID     string
    Name   string
    Orders []Order  // This will grow unbounded!
}

// GOOD: Separate aggregates with references
type Customer struct {
    ID   string
    Name string
}

type Order struct {
    ID         string
    CustomerID string  // Reference to Customer
}
```

### Reference Other Aggregates by ID

Don't embed aggregates within aggregates:

```go
// BAD: Embedded aggregate
type Order struct {
    ID       string
    Customer *Customer  // Direct reference
}

// GOOD: Reference by ID
type Order struct {
    ID         string
    CustomerID string  // Reference by ID
}
```

### Design for Eventual Consistency

When coordinating multiple operations, the approach depends on whether you're working within a single domain or across domains.

#### Within the Same Domain: Command Handlers Coordinate

When multiple intentions target the same aggregate or aggregates within the same domain, the **Command Handler** orchestrates them directly:

```go
type OrderCommandHandlers struct {
    repository *OrderRepository
}

// Single command handler coordinates multiple aggregate intentions
func (h *OrderCommandHandlers) HandlePlaceAndPayOrder(ctx context.Context, cmd PlaceAndPayOrder) error {
    order := NewOrder(cmd.OrderID)

    // First intention: place the order
    if err := order.Place(cmd.CustomerID, cmd.Items); err != nil {
        return err
    }

    // Second intention: immediately pay (same aggregate)
    if err := order.Pay(cmd.PaymentID, cmd.Amount); err != nil {
        return err
    }

    // Both operations are saved atomically
    return h.repository.Save(ctx, order)
}

// Registration at startup
func SetupOrderCommandHandlers(dispatcher *CommandDispatcher, repo *OrderRepository) {
    handlers := &OrderCommandHandlers{repository: repo}
    Register(dispatcher, handlers.HandlePlaceOrder)
    Register(dispatcher, handlers.HandlePlaceAndPayOrder)
}
```

This works because all operations happen on the same aggregate within a single transaction boundary.

#### Across Domains: A Dedicated Provisioning Domain with Reactors

When operations span multiple domains or aggregates, a best practice is to introduce a **dedicated provisioning domain**. This domain contains **Reactors** — specialized components that:

1. Subscribe to events from other domains
2. Issue commands to coordinate workflows
3. Act as the orchestration layer between bounded contexts

```go
// Reactor in the Provisioning domain coordinates cross-domain workflows
type OrderFulfillmentReactor struct {
    commandBus          *CommandDispatcher
    orderRepository     *OrderRepository
    inventoryRepository *InventoryRepository
}

func NewOrderFulfillmentReactor(commandBus *CommandDispatcher) *OrderFulfillmentReactor {
    return &OrderFulfillmentReactor{commandBus: commandBus}
}

// React to OrderPlacedEvent from Order domain
func (r *OrderFulfillmentReactor) OnOrderPlaced(ctx context.Context, event OrderPlacedEvent) error {
    // Issue command to Inventory domain
    return r.commandBus.Dispatch(ctx, ReserveInventory{
        OrderID: event.OrderID,
        Items:   event.Items,
    })
}

// React to InventoryReservedEvent from Inventory domain
func (r *OrderFulfillmentReactor) OnInventoryReserved(ctx context.Context, event InventoryReservedEvent) error {
    // Issue command back to Order domain
    return r.commandBus.Dispatch(ctx, MarkOrderReadyForPayment{
        OrderID: event.OrderID,
    })
}

// Registration at startup
func SetupOrderFulfillmentReactor(events *EventDispatcher, commandBus *CommandDispatcher) {
    reactor := NewOrderFulfillmentReactor(commandBus)
    RegisterEventHandler(events, reactor.OnOrderPlaced)
    RegisterEventHandler(events, reactor.OnInventoryReserved)
}
```

This architecture provides clear separation of concerns: each domain handles its own business logic, while the provisioning domain's reactors coordinate the workflow through events and commands.

#### Why a Dedicated Provisioning Domain?

In practice, implementing processes in a dedicated domain like `provisioning` is a common and recommended pattern. The key advantage: **process state becomes visible and recoverable**.

Consider a multi-step workflow:
1. Reserve inventory (Inventory domain)
2. Process payment (Payment domain)
3. Send confirmation email (Notification domain)

If the system crashes after step 1 completes, what happens? Without tracking process state, you might:
- Re-run step 1 (duplicate reservation)
- Skip step 1 but not know where you stopped
- Lose track of the process entirely

With a provisioning domain that persists each step as an event, the process becomes **resumable**:

```go
type OrderFulfillmentProcess struct {
    OrderID           string
    InventoryReserved bool
    PaymentProcessed  bool
    EmailSent         bool
}

func (p *OrderFulfillmentProcess) OnInventoryReserved(event InventoryReservedEvent) {
    p.InventoryReserved = true
    // This state change is persisted as an event
}
```

After a restart, the provisioning domain loads its event history and reconstructs the process state. It immediately sees: "Step 1 is done, steps 2 and 3 are pending." An administrator can inspect the current state, or the system can automatically resume from where it stopped.

This visibility is invaluable for:
- **Debugging**: See exactly where a process failed
- **Monitoring**: Track in-flight processes and their progress
- **Recovery**: Resume interrupted workflows without data loss or duplicate actions

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/03-aggregates-consistency-boundaries/code

When you run the complete example, you'll see aggregates in action:

```
=== Aggregates Demo ===

1. Creating and placing an order...
   -> Order placed (in-memory), status: placed, total: $109.97
   -> Uncommitted events: 1
   -> Events persisted to store

2. Loading order from event store (simulating new request)...
   -> Order loaded, status: placed, version: 1

3. Processing payment...
   -> Payment processed, status: paid
   -> Events persisted

4. Shipping the order...
   -> Order shipped, status: shipped, tracking: TRK-12345

5. Trying to cancel shipped order (business rule validation)...
   -> Expected error: cannot cancel shipped order

6. Creating another order to show state machine...
   -> Trying to ship before payment...
   -> Expected error: order must be paid before shipping
   -> Cancelling unpaid order (allowed)...
   -> Order cancelled, status: cancelled

=== Event Store Contents ===

order-123:
   [0] OrderPlaced
   [1] OrderPaid
   [2] OrderShipped

order-456:
   [0] OrderPlaced
   [1] OrderCancelled

=== Testing Business Logic (no infrastructure needed) ===
   PASS: Cannot ship unpaid order
   PASS: Cannot cancel shipped order
   PASS: Cannot place empty order

=== Demo Complete ===
```

Notice how:
1. **Intentions modify state in-memory** — the aggregate collects uncommitted events until explicitly saved
2. **Aggregates are short-lived** — each step loads a fresh aggregate from the event store
3. **Business rules are enforced** — the state machine prevents invalid transitions (ship before pay, cancel after ship)
4. **Events tell the story** — the event store shows exactly what happened to each order
5. **Testing is trivial** — business logic is verified without any database or infrastructure

---

## What's Next

Now that we understand the conceptual foundations — Event Sourcing, CQRS, and Aggregates — it's time to dive into Go-specific implementation patterns. In the next post, we'll explore **Part 04: Functional Options** — a powerful pattern for flexible, extensible configuration.