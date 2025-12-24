# Provider Pattern: Lazy Deserialization

*This is Part 8 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

In distributed systems, commands and events travel across process boundaries as serialized bytes. The receiving system needs to deserialize them into the correct types — but how does it know which type to use?

The **Provider Pattern** solves this by maintaining a registry of types that enables lazy deserialization: bytes are only converted to concrete types when actually needed, using pre-registered type information.

## The Problem: Type Resolution

Consider a command arriving over a message queue:

```go
type CommandMessage struct {
    Domain        string // "Order"
    CommandName   string // "PlaceOrderCommand"
    CommandBytes  []byte // JSON payload
}

func ProcessCommand(msg CommandMessage) error {
    // We have bytes, but what type should we deserialize into?
    // json.Unmarshal needs a concrete type!

    var cmd ???  // What goes here?
    json.Unmarshal(msg.CommandBytes, &cmd)
}
```

Without knowing the concrete type, we can't deserialize. We could use a giant switch statement:

```go
func deserializeCommand(name string, data []byte) (DomainCmd, error) {
    switch name {
    case "PlaceOrderCommand":
        var cmd PlaceOrderCommand
        return &cmd, json.Unmarshal(data, &cmd)
    case "CancelOrderCommand":
        var cmd CancelOrderCommand
        return &cmd, json.Unmarshal(data, &cmd)
    // ... hundreds more cases
    }
    return nil, fmt.Errorf("unknown command: %s", name)
}
```

This doesn't scale. Every new command requires modifying this central function.

## The Solution: Type Registry

The Provider Pattern maintains a registry mapping type names to template instances:

```go
type DomainCommandProvider struct {
    registry SyncMap[string, domainCommandEntry]
}

type domainCommandEntry struct {
    Domain     string
    DomainCmd  DomainCmd  // Template instance for type info
}

func NewDomainCommandProvider() *DomainCommandProvider {
    return &DomainCommandProvider{}
}
```

## Registration: Building the Registry

Commands are registered when the application starts:

```go
func (p *DomainCommandProvider) RegisterDomainCommand(domain string, cmd DomainCmd) error {
    // Get unique identifier for this command type
    typePath := GetTypePath(cmd)

    entry := domainCommandEntry{
        Domain:    domain,
        DomainCmd: cmd,
    }

    // Store with full identifier: "Order.PlaceOrderCommand"
    key := fmt.Sprintf("%s.%s", domain, typePath)
    p.registry.Store(key, entry)

    return nil
}

// Called during application initialization
func RegisterOrderCommands(provider *DomainCommandProvider) {
    provider.RegisterDomainCommand("Order", &PlaceOrderCommand{})
    provider.RegisterDomainCommand("Order", &CancelOrderCommand{})
    provider.RegisterDomainCommand("Order", &ShipOrderCommand{})
}
```

## Providing: Lazy Deserialization

When a command arrives, we look up the type and deserialize:

```go
func (p *DomainCommandProvider) ProvideDomainCommand(cmd *Command) error {
    // Build lookup key
    key := fmt.Sprintf("%s.%s", cmd.Domain, cmd.DomainCmdName)

    // Find registered type
    entry, ok := p.registry.Load(key)
    if !ok {
        return fmt.Errorf("unknown command: %s", key)
    }

    // Deserialize using template for type information
    domainCmd, err := Deserialize(cmd.DomainCmdBytes, entry.DomainCmd)
    if err != nil {
        return fmt.Errorf("failed to deserialize %s: %w", key, err)
    }

    // Attach deserialized command
    cmd.DomainCmd = domainCmd
    return nil
}
```

The key insight: `Deserialize` uses the template instance to determine the target type:

```go
func Deserialize[T any](data []byte, template T) (T, error) {
    // Create fresh instance with same type as template
    result, _ := DeepCopy(template)

    err := json.Unmarshal(data, &result)
    if err != nil {
        var zero T
        return zero, err
    }

    return result, nil
}
```

## Complete Provider Implementation

Here's a production-ready implementation:

```go
package comby

type DomainCommandProvider interface {
    RegisterDomainCommand(domain string, domainCmd DomainCmd) error
    ProvideDomainCommand(cmd Command) error
    GetDomainFor(domainCmdPath string) string
    GetDomainCommands() ([]string, []DomainCmd)
}

type domainCommandProvider struct {
    cmdMap SyncMap[string, domainCommandProviderEntry]
}

type domainCommandProviderEntry struct {
    Domain    string
    DomainCmd DomainCmd
}

func NewDomainCommandProvider() DomainCommandProvider {
    return &domainCommandProvider{}
}

func (p *domainCommandProvider) RegisterDomainCommand(domain string, domainCmd DomainCmd) error {
    if domainCmd == nil {
        return errors.New("domain command cannot be nil")
    }

    typePath := GetTypePath(domainCmd)
    if typePath == "" {
        return errors.New("could not determine type path")
    }

    key := fmt.Sprintf("%s.%s", domain, typePath)

    // Check for duplicates
    if _, exists := p.cmdMap.Load(key); exists {
        return fmt.Errorf("command already registered: %s", key)
    }

    p.cmdMap.Store(key, domainCommandProviderEntry{
        Domain:    domain,
        DomainCmd: domainCmd,
    })

    return nil
}

func (p *domainCommandProvider) ProvideDomainCommand(cmd Command) error {
    if cmd.GetDomain() == "" || cmd.GetDomainCmdName() == "" {
        return errors.New("command missing domain or name")
    }

    key := fmt.Sprintf("%s.%s", cmd.GetDomain(), cmd.GetDomainCmdName())

    entry, ok := p.cmdMap.Load(key)
    if !ok {
        return fmt.Errorf("unknown command type: %s", key)
    }

    // Get serialized bytes
    cmdBytes := cmd.GetDomainCmdBytes()
    if len(cmdBytes) == 0 {
        return errors.New("command has no serialized data")
    }

    // Deserialize using registered template
    domainCmd, err := Deserialize(cmdBytes, entry.DomainCmd)
    if err != nil {
        return fmt.Errorf("deserialization failed: %w", err)
    }

    return cmd.SetDomainCmd(domainCmd)
}

func (p *domainCommandProvider) GetDomainFor(domainCmdPath string) string {
    var domain string
    p.cmdMap.Range(func(key string, entry domainCommandProviderEntry) bool {
        if GetTypePath(entry.DomainCmd) == domainCmdPath {
            domain = entry.Domain
            return false // Stop iteration
        }
        return true
    })
    return domain
}

func (p *domainCommandProvider) GetDomainCommands() ([]string, []DomainCmd) {
    var domains []string
    var commands []DomainCmd

    p.cmdMap.Range(func(key string, entry domainCommandProviderEntry) bool {
        domains = append(domains, entry.Domain)
        commands = append(commands, entry.DomainCmd)
        return true
    })

    return domains, commands
}
```

## Event Provider

The same pattern works for events:

```go
type DomainEventProvider interface {
    RegisterDomainEvent(domain string, domainEvt DomainEvt, isOwner bool) error
    ProvideDomainEvent(evt Event) error
    GetDomainFor(domainEvtPath string) string
}

type domainEventProvider struct {
    evtMap SyncMap[string, domainEventProviderEntry]
}

type domainEventProviderEntry struct {
    Domain    string
    DomainEvt DomainEvt
    IsOwner   bool  // Does this domain own the event?
}

func (p *domainEventProvider) RegisterDomainEvent(domain string, domainEvt DomainEvt, isOwner bool) error {
    typePath := GetTypePath(domainEvt)
    key := fmt.Sprintf("%s.%s", domain, typePath)

    // Events can be registered by multiple domains (subscribers)
    // But only one domain can be the owner (publisher)
    if existing, ok := p.evtMap.Load(key); ok {
        if isOwner && existing.IsOwner {
            return fmt.Errorf("event %s already has an owner", key)
        }
    }

    p.evtMap.Store(key, domainEventProviderEntry{
        Domain:    domain,
        DomainEvt: domainEvt,
        IsOwner:   isOwner,
    })

    return nil
}

func (p *domainEventProvider) ProvideDomainEvent(evt Event) error {
    key := fmt.Sprintf("%s.%s", evt.GetDomain(), evt.GetDomainEvtName())

    entry, ok := p.evtMap.Load(key)
    if !ok {
        return fmt.Errorf("unknown event type: %s", key)
    }

    evtBytes := evt.GetDomainEvtBytes()
    if len(evtBytes) == 0 {
        return nil // No data to deserialize
    }

    domainEvt, err := Deserialize(evtBytes, entry.DomainEvt)
    if err != nil {
        return fmt.Errorf("event deserialization failed: %w", err)
    }

    return evt.SetDomainEvt(domainEvt)
}
```

## Integration with the Facade

The providers are part of the central facade:

```go
type Facade struct {
    DomainCommandProvider DomainCommandProvider
    DomainEventProvider   DomainEventProvider
    // ... other components
}

func NewFacade(opts ...FacadeOption) (*Facade, error) {
    fc := &Facade{
        DomainCommandProvider: NewDomainCommandProvider(),
        DomainEventProvider:   NewDomainEventProvider(),
    }
    // ... apply options
    return fc, nil
}
```

## Automatic Registration

When command handlers are registered, their commands are automatically added to the provider:

```go
func RegisterCommandHandler[T CommandHandler](fc *Facade, ch T) error {
    domain := ch.GetDomain()

    // Register each command type this handler supports
    for _, domainCmd := range ch.GetDomainCommands() {
        if err := fc.DomainCommandProvider.RegisterDomainCommand(domain, domainCmd); err != nil {
            return err
        }
    }

    // Store the handler for dispatch
    fc.CommandHandlers.Store(domain, ch)
    return nil
}
```

Similarly for aggregates and their events:

```go
func RegisterAggregate[T Aggregate](fc *Facade, fn NewAggregateFunc[T]) error {
    agg := fn()
    domain := agg.GetDomain()

    // Register all events this aggregate emits
    for _, domainEvt := range agg.GetDomainEvents() {
        // true = this aggregate owns (produces) these events
        if err := fc.DomainEventProvider.RegisterDomainEvent(domain, domainEvt, true); err != nil {
            return err
        }
    }

    fc.Aggregates.Store(domain, agg)
    return nil
}
```

## The Dispatch Flow

Here's how it all comes together:

```go
// 1. Command arrives from external source (HTTP, message queue, etc.)
cmdMessage := CommandMessage{
    Domain:       "Order",
    CommandName:  "github.com/myapp/domain/order/command.PlaceOrderCommand",
    CommandBytes: []byte(`{"orderID":"123","customerID":"456"}`),
}

// 2. Create command envelope
cmd := NewCommand()
cmd.SetDomain(cmdMessage.Domain)
cmd.SetDomainCmdName(cmdMessage.CommandName)
cmd.SetDomainCmdBytes(cmdMessage.CommandBytes)

// 3. Provider deserializes into correct type
err := fc.DomainCommandProvider.ProvideDomainCommand(cmd)
// cmd.DomainCmd is now *PlaceOrderCommand

// 4. Find and invoke handler
handler := fc.CommandHandlers.Get(cmd.GetDomain())
events, err := handler.Handle(ctx, cmd)
```

## Benefits of the Provider Pattern

1. **Decoupled registration**: Each domain registers its own types
2. **Lazy deserialization**: Bytes converted only when needed
3. **Type safety**: Correct types are instantiated automatically
4. **Extensible**: New commands/events require no central changes
5. **Introspection**: Can list all registered types for documentation

## Querying Registered Types

The provider enables runtime introspection:

```go
// List all commands for documentation
func (fc *Facade) GetAPIDocumentation() []CommandDoc {
    domains, commands := fc.DomainCommandProvider.GetDomainCommands()

    var docs []CommandDoc
    for i, cmd := range commands {
        docs = append(docs, CommandDoc{
            Domain:    domains[i],
            Name:      GetTypeName(cmd),
            Path:      GetTypePath(cmd),
            Schema:    generateJSONSchema(cmd),
        })
    }
    return docs
}
```

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/08-provider-pattern-lazy-deserialization/code

When you run the complete example, you'll see the Provider Pattern in action:

```
=== Provider Pattern: Lazy Deserialization Demo ===

1. Creating providers:
   -> DomainCommandProvider created
   -> DomainEventProvider created

2. Registering command types:
   -> Registered: Order.PlaceOrderCommand
   -> Registered: Order.CancelOrderCommand
   -> Registered: Order.ShipOrderCommand

3. Registering event types:
   -> Registered: Order.OrderPlacedEvent (owner: true)
   -> Registered: Order.OrderCancelledEvent (owner: true)
   -> Registered: Order.OrderShippedEvent (owner: true)

4. Simulating incoming command from message queue:
   -> Raw JSON: {"orderId":"order-123","customerId":"cust-456","total":99.99}
   -> Domain: Order, CommandName: PlaceOrderCommand

5. Creating command envelope (bytes not yet deserialized):
   -> cmd.DomainCmd before provide: <nil> (nil)

6. Provider performs lazy deserialization:
   -> cmd.DomainCmd after provide: *main.PlaceOrderCommand
   -> OrderID: order-123
   -> CustomerID: cust-456
   -> Total: 99.99

7. Event deserialization (same pattern):
   -> evt.DomainEvt type: *main.OrderPlacedEvent
   -> Event data: OrderID=order-123, Total=99.99

8. Error handling - unknown command type:
   -> Error (expected): unknown command type: Order.UnknownCommand

9. Error handling - duplicate registration:
   -> Error (expected): command already registered: Order.PlaceOrderCommand

10. Introspection - listing registered types:
    Registered Commands:
      [1] Order.PlaceOrderCommand
      [2] Order.CancelOrderCommand
      [3] Order.ShipOrderCommand
    Registered Events:
      [1] Order.OrderPlacedEvent
      [2] Order.OrderCancelledEvent
      [3] Order.OrderShippedEvent

11. Deserializing different command types:
    -> CancelOrderCommand: Deserialized to *main.CancelOrderCommand
    -> ShipOrderCommand: Deserialized to *main.ShipOrderCommand

12. Key benefit - no giant switch statement needed:
    Instead of:
      switch name {
      case "PlaceOrderCommand": ...
      case "CancelOrderCommand": ...
      // hundreds more cases
      }

    We simply call:
      provider.ProvideDomainCommand(cmd)
    And the correct type is resolved automatically!

=== Demo Complete ===
```

## Summary

The Provider Pattern enables:

1. **Registration**: Types are registered with their metadata at startup
2. **Lookup**: Type information is retrieved by name/path
3. **Instantiation**: New instances are created using templates
4. **Deserialization**: Bytes are converted to concrete types

This pattern is essential for:
- Message-based systems (commands/events over queues)
- Plugin architectures (dynamically loaded types)
- API documentation (runtime type introspection)

---

## What's Next

In the next post, we'll explore **Middleware Chains** — composable decorators that add cross-cutting concerns like logging, authentication, and metrics to handlers.



