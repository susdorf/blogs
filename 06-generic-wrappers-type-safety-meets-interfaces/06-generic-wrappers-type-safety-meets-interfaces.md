# Generic Wrappers: Type Safety Meets Interfaces

*This is Part 6 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

Here's a common challenge: you want developers to write type-safe handlers, but your framework needs to store and dispatch them dynamically. How do you bridge the gap between generic, type-safe code and non-generic interfaces?

The answer is **Generic Wrappers** — a pattern that provides compile-time type safety for developers while maintaining runtime flexibility for the framework.

This pattern is crucial for usability in comby. It took several iterations during comby's development to arrive at this approach — earlier attempts were either too verbose, required too much boilerplate, or sacrificed type safety. The current solution represents the sweet spot between developer ergonomics and framework flexibility.

It's worth noting the architectural distinction in comby: `Command`, `Event`, and `Query` are envelope types that carry metadata (timestamps, correlation IDs, routing information, etc.), while the actual business payload lives in `DomainCmd`, `DomainEvt`, and `DomainQry`. The domain types contain the data that matters to your business logic — the envelope types are infrastructure concerns. This separation is fundamental to how comby handles serialization, routing, and handler dispatch.

## The Problem: Two Worlds

Consider a command handling system. The framework needs to:
1. Store handlers in a map (keyed by command type)
2. Dispatch commands to the right handler at runtime
3. Allow developers to write handlers with concrete types

```go
// What the framework needs (non-generic for storage)
type DomainCommandHandlerFunc func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error)

// What developers want to write (type-safe)
func (ch *OrderHandler) PlaceOrder(ctx context.Context, cmd Command, domainCmd *PlaceOrderCommand) ([]Event, error) {
    // domainCmd is *PlaceOrderCommand, not DomainCmd interface
    // Full IDE support, type checking
}
```

The framework can't store `func(..., *PlaceOrderCommand)` in a map of `func(..., DomainCmd)`. The signatures don't match.

## The Solution: Generic Wrapper Function

We create a generic function that wraps type-safe handlers:

```go
// TypedDomainCommandHandlerFunc is what developers write
type TypedDomainCommandHandlerFunc[T DomainCmd] func(ctx context.Context, cmd Command, domainCmd T) ([]Event, error)

// AddDomainCommandHandler wraps a typed handler for storage
func AddDomainCommandHandler[T DomainCmd](
    handler CommandHandler,
    fn TypedDomainCommandHandlerFunc[T],
) error {
    // Create an instance of T to get its type information
    domainCmd := NewInstanceOf[T]()

    // Create a non-generic wrapper that calls the generic function
    wrappedFn := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        // Type assertion inside the wrapper
        switch typedDomainCmd := domainCmd.(type) {
        case T:
            return fn(ctx, cmd, typedDomainCmd)
        }
        return nil, fmt.Errorf("invalid domain command type: '%T'", domainCmd)
    }

    // Store the wrapped function
    return handler.addDomainCommandHandler(domainCmd, wrappedFn)
}
```

## How It Works

Let's trace through the flow:

```go
// 1. Developer registers a typed handler
comby.AddDomainCommandHandler(ch, ch.PlaceOrder)

// 2. Inside AddDomainCommandHandler:
//    - T is inferred as *PlaceOrderCommand
//    - domainCmd := NewInstanceOf[*PlaceOrderCommand]() creates an instance
//    - wrappedFn converts DomainCmd -> *PlaceOrderCommand and calls ch.PlaceOrder
//    - The command type path is stored with wrappedFn

// 3. At runtime, when a command arrives:
//    - Framework looks up handler by command type
//    - Calls wrappedFn(ctx, cmd, domainCmd)
//    - wrappedFn does the type assertion and calls the typed handler
```

You might wonder why the syntax is `comby.AddDomainCommandHandler(ch, ch.PlaceOrder)` rather than `ch.AddDomainCommandHandler(ch.PlaceOrder)`. This is a Go language limitation: methods cannot have their own type parameters — only the struct itself can be generic. Since `CommandHandler` is not a generic type, we cannot define a generic method on it. The workaround is a package-level generic function that takes the handler as its first argument. For more details, see the [Go generics proposal](https://go.googlesource.com/proposal/+/refs/heads/master/design/43651-type-parameters.md#methods-may-not-take-additional-type-arguments).

## The NewInstanceOf Helper

A key helper creates instances from type parameters:

```go
func NewInstanceOf[T any]() T {
    var instance T
    instanceType := reflect.TypeOf(instance)

    // Check if the type is a pointer type.
    if instanceType.Kind() == reflect.Ptr {
        // Create a new instance of the type the pointer points to.
        return reflect.New(instanceType.Elem()).Interface().(T)
    }

    // Return the zero value for the type.
    return instance
}

// Usage
cmd := NewInstanceOf[*PlaceOrderCommand]()
// cmd is a valid *PlaceOrderCommand (pointing to zero value)
```

This is necessary because you can't call `new(T)` directly in Go generics when T might be a pointer type.

In comby, `NewInstanceOf` is part of a family of generic helper functions that handle type instantiation and serialization:

```go
// Serialize converts a generic instance into a JSON-encoded byte slice.
func Serialize[T any](inst T) ([]byte, error) {
    return json.Marshal(inst)
}

// Deserialize decodes a JSON-encoded byte slice into a new instance of type T.
func Deserialize[T any](data []byte, inst T) (T, error) {
    copy, _ := DeepCopy[T](inst)
    err := json.Unmarshal(data, &copy)
    if err != nil {
        var zero T
        return zero, err
    }
    return copy, nil
}
```

These helpers work together: `NewInstanceOf` creates typed instances for handler registration, while `Serialize` and `Deserialize` handle the conversion between typed domain objects and their wire format. The pattern of package-level generic functions — rather than methods — appears throughout comby for the same reason discussed earlier: Go's limitation on generic methods.

## Real-World Implementation

Here's the complete pattern from the comby framework:

```go
// command.handler.go

// Non-generic type for storage
type DomainCommandHandlerFunc func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error)

// Generic type for user code
type TypedDomainCommandHandlerFunc[T DomainCmd] func(ctx context.Context, cmd Command, domainCmd T) ([]Event, error)

// Internal interface for adding handlers
type _domainCommandHandlerAdder interface {
    addDomainCommandHandler(domainCmd DomainCmd, fn DomainCommandHandlerFunc) error
}

// Generic wrapper function
func AddDomainCommandHandler[T DomainCmd](
    concreteHandler CommandHandler,
    fn TypedDomainCommandHandlerFunc[T],
) error {
    // Create instance of T for registration
    domainCmd := NewInstanceOf[T]()

    // Create non-generic wrapper
    wrappedFn := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        switch typedDomainCmd := domainCmd.(type) {
        case T:
            return fn(ctx, cmd, typedDomainCmd)
        }
        return nil, fmt.Errorf("invalid domain command type: '%T'", domainCmd)
    }

    // Use internal interface to add handler
    switch _handler := concreteHandler.(type) {
    case _domainCommandHandlerAdder:
        return _handler.addDomainCommandHandler(domainCmd, wrappedFn)
    }

    return fmt.Errorf("could not add domain command handler")
}
```

## The Same Pattern for Events

Events use the identical pattern:

```go
// Non-generic for storage
type DomainEventHandlerFunc func(ctx context.Context, evt Event, domainEvt DomainEvt) error

// Generic for user code
type TypedDomainEventHandlerFunc[T DomainEvt] func(ctx context.Context, evt Event, domainEvt T) error

func AddDomainEventHandler[T DomainEvt](
    concreteHandler EventHandler,
    fn TypedDomainEventHandlerFunc[T],
) error {
    domainEvt := NewInstanceOf[T]()

    wrappedFn := func(ctx context.Context, evt Event, domainEvt DomainEvt) error {
        switch typedEvtData := domainEvt.(type) {
        case T:
            return fn(ctx, evt, typedEvtData)
        }
        return fmt.Errorf("invalid domain event type: '%T'", domainEvt)
    }

    switch _handler := concreteHandler.(type) {
    case _domainEventHandlerAdder:
        return _handler.addDomainEventHandler(domainEvt, wrappedFn)
    }

    return fmt.Errorf("could not add domain event handler")
}
```

## Developer Experience

With generic wrappers, developers write clean, type-safe code:

```go
type OrderCommandHandler struct {
    *comby.BaseCommandHandler
    repo *comby.AggregateRepository[*Order]
}

func NewOrderCommandHandler(repo *comby.AggregateRepository[*Order]) *OrderCommandHandler {
    ch := &OrderCommandHandler{repo: repo}
    ch.BaseCommandHandler = comby.NewBaseCommandHandler()
    ch.Domain = "Order"

    // Register handlers - type inference works!
    comby.AddDomainCommandHandler(ch, ch.PlaceOrder)
    comby.AddDomainCommandHandler(ch, ch.CancelOrder)
    comby.AddDomainCommandHandler(ch, ch.ShipOrder)

    return ch
}

// Handlers are fully typed
func (ch *OrderCommandHandler) PlaceOrder(
    ctx context.Context,
    cmd comby.Command,
    domainCmd *PlaceOrderCommand,  // Concrete type!
) ([]comby.Event, error) {
    // Full type safety
    order := NewOrder(domainCmd.OrderID)
    if err := order.Place(domainCmd.CustomerID, domainCmd.Items); err != nil {
        return nil, err
    }
    return order.GetUncommittedEvents(), nil
}

func (ch *OrderCommandHandler) CancelOrder(
    ctx context.Context,
    cmd comby.Command,
    domainCmd *CancelOrderCommand,  // Different concrete type
) ([]comby.Event, error) {
    order, err := ch.repo.GetAggregate(ctx, domainCmd.OrderID)
    if err != nil {
        return nil, err
    }
    if err := order.Cancel(domainCmd.Reason); err != nil {
        return nil, err
    }
    return order.GetUncommittedEvents(), nil
}
```

## The Registration Flow

Let's trace through registration completely:

```go
// 1. Developer calls:
comby.AddDomainCommandHandler(ch, ch.PlaceOrder)

// 2. Go infers T = *PlaceOrderCommand from ch.PlaceOrder's signature

// 3. Inside AddDomainCommandHandler:
domainCmd := NewInstanceOf[*PlaceOrderCommand]()
// domainCmd is now a *PlaceOrderCommand instance

// 4. Create wrapper:
wrappedFn := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
    switch typedDomainCmd := domainCmd.(type) {
    case *PlaceOrderCommand:
        return ch.PlaceOrder(ctx, cmd, typedDomainCmd)
    }
    return nil, fmt.Errorf("invalid type...")
}

// 5. Register with framework:
ch.addDomainCommandHandler(domainCmd, wrappedFn)
// Framework stores: "Order.PlaceOrderCommand" -> wrappedFn
```

## The Dispatch Flow

When a command arrives:

```go
// 1. Command is received (possibly from network)
cmd := Command{
    Domain:         "Order",
    DomainCmdName:  "PlaceOrderCommand",
    DomainCmdBytes: []byte(`{"orderID":"123","customerID":"456"}`),
}

// 2. Framework deserializes domain command
var domainCmd DomainCmd = &PlaceOrderCommand{}
json.Unmarshal(cmd.DomainCmdBytes, domainCmd)

// 3. Framework looks up handler
handler := handlers["Order.PlaceOrderCommand"]  // This is wrappedFn

// 4. Framework calls handler
events, err := handler(ctx, cmd, domainCmd)

// 5. Inside wrappedFn, type assertion succeeds:
typedDomainCmd := domainCmd.(*PlaceOrderCommand)  // Works!
return ch.PlaceOrder(ctx, cmd, typedDomainCmd)    // Type-safe call
```

## Why Not Just Use Type Assertions Directly?

You could write handlers that take `DomainCmd` and assert internally:

```go
// Alternative: manual type assertion
func (ch *OrderCommandHandler) PlaceOrder(
    ctx context.Context,
    cmd comby.Command,
    domainCmd comby.DomainCmd,  // Interface type
) ([]comby.Event, error) {
    // Manual assertion - repetitive and error-prone
    typedCmd, ok := domainCmd.(*PlaceOrderCommand)
    if !ok {
        return nil, errors.New("invalid command type")
    }

    // Now use typedCmd...
}
```

Problems with this approach:
1. **Boilerplate**: Every handler needs the same assertion code
2. **Runtime errors**: Wrong type only discovered at runtime
3. **No IDE help**: IDE can't know the actual type
4. **Easy to forget**: Missing assertion = panic on nil dereference

Generic wrappers move the assertion to a single place, checked once per registration.

## Advanced: Multiple Type Parameters

The pattern extends to multiple type parameters:

```go
type TypedQueryHandlerFunc[Q DomainQry, R any] func(ctx context.Context, qry Query, domainQry Q) (R, error)

func AddDomainQueryHandler[Q DomainQry, R any](
    handler QueryHandler,
    fn TypedQueryHandlerFunc[Q, R],
) error {
    domainQry := NewInstanceOf[Q]()

    wrappedFn := func(ctx context.Context, qry Query, domainQry DomainQry) (any, error) {
        switch typedDomainQry := domainQry.(type) {
        case Q:
            return fn(ctx, qry, typedDomainQry)
        }
        return nil, fmt.Errorf("invalid query type")
    }

    return handler.addDomainQueryHandler(domainQry, wrappedFn)
}

// Usage
func (qh *OrderQueryHandler) GetOrder(
    ctx context.Context,
    qry comby.Query,
    domainQry *GetOrderQuery,
) (*OrderView, error) {  // Returns concrete *OrderView
    // Implementation
}
```

## Benefits Summary

1. **Developer experience**: Write natural, typed handler functions
2. **Framework flexibility**: Store handlers in homogeneous collections
3. **Single assertion point**: Type checking happens once in the wrapper
4. **Type inference**: Go figures out the types automatically
5. **Compile-time safety**: Wrong handler signatures are caught by compiler

## Summary

Generic wrappers bridge the gap between:
- Type-safe user code (what developers write)
- Dynamic dispatch (what frameworks need)

The pattern:
1. Define a generic function type for user code
2. Define a non-generic function type for storage
3. Create a generic wrapper function that converts between them
4. Use `NewInstanceOf[T]()` to create type instances for registration
5. Type assertion happens inside the wrapper, invisible to users

This is one of the most powerful patterns enabled by Go generics, and you'll find it throughout well-designed frameworks.

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/06-generic-wrappers-type-safety-meets-interfaces/code

When you run the complete example, you'll see generic wrappers in action:

```
=== Generic Wrappers Demo ===

1. NewInstanceOf helper - creating typed instances:
   -> NewInstanceOf[*PlaceOrderCommand](): *pkg.PlaceOrderCommand (non-nil: true)
   -> NewInstanceOf[ValueCommand](): main.ValueCommand (zero value)
   -> Type path: PlaceOrderCommand

2. Registering typed command handlers:
   -> Registered PlaceOrder, CancelOrder, ShipOrder handlers
   -> Each handler has its own concrete type (no interface{} in user code)

3. Registered domain commands:
   -> Order.PlaceOrderCommand
   -> Order.CancelOrderCommand
   -> Order.ShipOrderCommand

4. Dispatching commands (type-safe at registration, dynamic at runtime):
      PlaceOrder called: OrderID=order-123, CustomerID=customer-456, Items=[widget gadget]
   -> PlaceOrder returned 1 event(s)
      ShipOrder called: OrderID=order-123, Method=express
   -> ShipOrder returned 1 event(s)
      CancelOrder called: OrderID=order-999, Reason=customer request
   -> CancelOrder returned 1 event(s)

5. Event handlers use the same pattern:
   -> Registered OnOrderPlaced, OnOrderCancelled handlers
   -> OnOrderPlaced: Order order-123 placed by customer-456

6. Serialize/Deserialize helpers:
   -> Serialized: {"OrderID":"order-789","CustomerID":"customer-012","Items":["item1","item2"]}
   -> Deserialized: OrderID=order-789, CustomerID=customer-012

7. Go's type inference in action:
   When you write: pkg.AddDomainCommandHandler(ch, ch.PlaceOrder)
   Go infers T = *PlaceOrderCommand from the handler signature
   No explicit type parameters needed!

8. Type safety - wrong type at runtime:
   -> Error caught: invalid domain command type: '*pkg.PlaceOrderCommand'

=== Demo Complete ===
```

The demo shows:
1. How `NewInstanceOf` creates typed instances from type parameters
2. Handler registration with automatic type inference
3. The registry storing handlers by command path
4. Runtime dispatch that calls the correct typed handler
5. The same pattern applied to event handlers
6. Serialize/Deserialize helpers working with concrete types
7. Type safety catching mismatches at the wrapper level

---

## What's Next

In the next post, we'll explore **Reflection Done Right** — when to use reflection, how to use it safely, and the utilities that make it practical.


