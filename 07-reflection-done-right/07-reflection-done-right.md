# Reflection Done Right

*This is Part 7 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

Reflection in Go has a reputation: powerful but dangerous. Used carelessly, it leads to runtime panics, unreadable code, and performance problems. Used wisely, it enables elegant solutions impossible with static types alone.

In this post, we'll explore when reflection is appropriate, how to use it safely, and the utility functions that make it practical.

## When to Use Reflection

Reflection is appropriate when you need to:
1. **Work with types unknown at compile time** (e.g., deserializing JSON into any struct)
2. **Inspect type metadata** (e.g., getting the name of a type for logging/routing)
3. **Create instances dynamically** (e.g., factory functions in generic code)
4. **Implement generic algorithms** pre-Go 1.18 (less needed now)

Reflection is inappropriate when:
- Regular interfaces or generics would work
- Performance is critical (reflection is slow)
- Code readability is more important than flexibility

## Core Reflection Utilities

Let's build a set of safe, tested utilities.

### Nil Checking

Go's nil behavior is tricky. An interface containing a nil pointer is not nil:

```go
var order *Order = nil
var agg Aggregate = order

fmt.Println(order == nil)  // true
fmt.Println(agg == nil)    // false! Interface is not nil, just contains nil
```

Safe nil check:

```go
func IsNil(val any) bool {
    if val == nil {
        return true
    }

    v := reflect.ValueOf(val)
    switch v.Kind() {
    case reflect.Ptr, reflect.Map, reflect.Array, reflect.Chan, reflect.Slice, reflect.Interface:
        return v.IsNil()
    }

    return false
}

// Usage
var order *Order = nil
var agg Aggregate = order

fmt.Println(IsNil(order))  // true
fmt.Println(IsNil(agg))    // true - correctly detects nil inside interface
```

### Getting Type Names

For logging, routing, and registration, we often need type names:

```go
func GetTypeName(val any) string {
    if val == nil {
        return ""
    }

    t := reflect.TypeOf(val)

    // Handle pointer types - get underlying type name
    if t.Kind() == reflect.Ptr {
        return t.Elem().Name()
    }

    return t.Name()
}

// Usage
cmd := &PlaceOrderCommand{}
fmt.Println(GetTypeName(cmd))  // "PlaceOrderCommand"

order := Order{}
fmt.Println(GetTypeName(order))  // "Order"
```

### Getting Fully Qualified Type Paths

For unique identification, include the package path:

```go
func GetTypePath(val any) string {
    if val == nil {
        return ""
    }

    t := reflect.TypeOf(val)

    // Handle function types specially
    if t.Kind() == reflect.Func {
        return getFuncPath(val)
    }

    // Handle pointer types
    if t.Kind() == reflect.Ptr {
        t = t.Elem()
    }

    // Combine package path and type name
    pkgPath := t.PkgPath()
    typeName := t.Name()

    if pkgPath == "" {
        return typeName
    }

    return pkgPath + "." + typeName
}

func getFuncPath(val any) string {
    // Use runtime to get function name
    ptr := reflect.ValueOf(val).Pointer()
    fn := runtime.FuncForPC(ptr)
    if fn == nil {
        return ""
    }
    return fn.Name()
}

// Usage
cmd := &PlaceOrderCommand{}
fmt.Println(GetTypePath(cmd))
// "github.com/myapp/domain/order/command.PlaceOrderCommand"
```

### Dynamic Instance Creation

Creating instances from type parameters requires reflection for pointer types:

```go
func NewInstanceOf[T any]() T {
    var instance T
    instanceType := reflect.TypeOf(instance)

    // For non-pointer types, zero value is fine
    if instanceType == nil {
        return instance
    }

    // For pointer types, we need to allocate
    if instanceType.Kind() == reflect.Ptr {
        // Create new instance of the underlying type
        newInstance := reflect.New(instanceType.Elem())
        return newInstance.Interface().(T)
    }

    return instance
}

// Usage
order := NewInstanceOf[*Order]()
// order is *Order pointing to a valid (zero-valued) Order

cmd := NewInstanceOf[PlaceOrderCommand]()
// cmd is PlaceOrderCommand{} (zero value)
```

## Safe Type Conversion

### Interface to Int64

Type switches provide safe conversion without reflection overhead:

```go
func InterfaceToInt64(val any) int64 {
    switch v := val.(type) {
    case int64:
        return v
    case int:
        return int64(v)
    case string:
        if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
            return parsed
        }
        return 0
    default:
        return 0
    }
}

// Usage
fmt.Println(InterfaceToInt64(42))        // 42
fmt.Println(InterfaceToInt64(int64(99))) // 99
fmt.Println(InterfaceToInt64("123"))     // 123
fmt.Println(InterfaceToInt64("abc"))     // 0
```

### Interface to Map

Converting structs to maps is useful for flexible processing:

```go
func InterfaceToMap(val any) (map[string]any, error) {
    if val == nil {
        return nil, nil
    }

    // Use JSON as intermediate format for safe conversion
    data, err := json.Marshal(val)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal: %w", err)
    }

    var result map[string]any
    if err := json.Unmarshal(data, &result); err != nil {
        return nil, fmt.Errorf("failed to unmarshal: %w", err)
    }

    return result, nil
}

// Usage
order := &Order{
    ID:     "order-123",
    Status: "placed",
    Total:  99.99,
}

m, _ := InterfaceToMap(order)
// m = map[string]any{"ID": "order-123", "Status": "placed", "Total": 99.99}
```

### Finding Values in Nested Structures

When working with dynamic data (e.g., JSON from external APIs), recursive search is invaluable:

```go
func FindInterfaceByKey(val any, key string) (any, bool) {
    // Must be a map to search
    mobj, ok := val.(map[string]any)
    if !ok {
        return nil, false
    }

    for k, v := range mobj {
        // Direct match
        if k == key {
            return v, true
        }

        // Recurse into nested maps
        if m, ok := v.(map[string]any); ok {
            if res, ok := FindInterfaceByKey(m, key); ok {
                return res, true
            }
        }

        // Recurse into arrays
        if arr, ok := v.([]any); ok {
            for _, elem := range arr {
                if res, ok := FindInterfaceByKey(elem, key); ok {
                    return res, true
                }
            }
        }
    }

    return nil, false
}

// Usage
data := map[string]any{
    "user": map[string]any{
        "profile": map[string]any{
            "email": "user@example.com",
        },
    },
}

email, found := FindInterfaceByKey(data, "email")
fmt.Println(email, found)  // "user@example.com" true
```

## Deep Copy with Reflection

Creating true copies (not just pointer copies) requires reflection:

```go
func DeepCopy[T any](src T) (T, bool) {
    var zero T

    if IsNil(src) {
        return zero, false
    }

    // Use JSON round-trip for simple deep copy
    data, err := json.Marshal(src)
    if err != nil {
        return zero, false
    }

    var dst T
    if err := json.Unmarshal(data, &dst); err != nil {
        return zero, false
    }

    return dst, true
}

// Usage
original := &Order{ID: "123", Items: []Item{{SKU: "ABC"}}}
copied, ok := DeepCopy(original)

// Modifying copy doesn't affect original
copied.Items[0].SKU = "XYZ"
fmt.Println(original.Items[0].SKU)  // "ABC"
```

## Generic Serialization

Type-safe serialization helpers:

```go
func Serialize[T any](inst T) ([]byte, error) {
    return json.Marshal(inst)
}

func Deserialize[T any](data []byte, template T) (T, error) {
    // Create a copy of template to avoid modifying it
    result, _ := DeepCopy(template)

    err := json.Unmarshal(data, &result)
    if err != nil {
        var zero T
        return zero, err
    }

    return result, nil
}

// Usage
order := &Order{ID: "123", Status: "placed"}

// Serialize
data, _ := Serialize(order)

// Deserialize with type safety
restored, _ := Deserialize(data, &Order{})
// restored is *Order
```

## Combining Generics with Reflection

Go 1.18+ generics and reflection serve complementary purposes. Generics provide compile-time type safety; reflection handles runtime dynamism. The best designs use both:

```go
// Generic type parameter ensures type safety at registration
// Reflection extracts type information for runtime dispatch
func RegisterAggregate[T Aggregate](facade *Facade, factory func() T) error {
    agg := factory()

    // Validate at registration time
    if len(agg.GetDomain()) == 0 {
        return fmt.Errorf("aggregate has no Domain")
    }

    // Store using reflection-derived key
    facade.AggregateMap.Store(agg.GetIdentifier(), agg)

    // Register domain events with reflection
    for _, evt := range agg.GetDomainEvents() {
        path := GetTypePath(evt)  // Reflection here
        facade.EventProvider.Register(agg.GetDomain(), path, evt)
    }

    return nil
}

// Usage - compiler ensures OrderAggregate implements Aggregate
err := RegisterAggregate(facade, func() *OrderAggregate {
    return NewOrderAggregate()
})
```

### Creating Unique Registration Keys

Combine domain context with reflection-derived type paths for unique keys:

```go
func RegisterCommandHandler[T CommandHandler](facade *Facade, handler T) error {
    // Build unique keys for each command this handler processes
    for _, cmd := range handler.GetDomainCommands() {
        // Domain + TypePath = globally unique key
        key := fmt.Sprintf("%s-%s", handler.GetDomain(), GetTypePath(cmd))

        if facade.HasHandler(key) {
            return fmt.Errorf("handler already exists: %s", key)
        }

        facade.RegisterHandler(key, handler)
    }
    return nil
}
```

This pattern ensures:
1. **Type safety**: Generic constraints catch interface violations at compile time
2. **Uniqueness**: TypePath-based keys prevent duplicate registrations
3. **Discoverability**: Runtime introspection enables dynamic dispatch

## Real-World Application: Provider Pattern

Here's how these utilities enable the provider pattern:

```go
type DomainCommandProvider struct {
    commands SyncMap[string, DomainCmd]  // path -> template instance
}

func (p *DomainCommandProvider) Register(domain string, cmd DomainCmd) error {
    // Get unique path for this command type
    path := GetTypePath(cmd)
    key := domain + "." + path

    p.commands.Store(key, cmd)
    return nil
}

func (p *DomainCommandProvider) Provide(cmd *Command) error {
    key := cmd.Domain + "." + cmd.DomainCmdName

    template, ok := p.commands.Load(key)
    if !ok {
        return fmt.Errorf("unknown command: %s", key)
    }

    // Deserialize using template for type information
    domainCmd, err := Deserialize(cmd.DomainCmdBytes, template)
    if err != nil {
        return err
    }

    cmd.DomainCmd = domainCmd
    return nil
}
```

## Performance Considerations

Reflection is slow compared to static code. Here are the costs:

```go
// Benchmark results (approximate)
// Direct field access:        ~0.3 ns/op
// reflect.ValueOf + Field:    ~50 ns/op
// json.Marshal (small):       ~500 ns/op
// json.Marshal (large):       ~5000 ns/op
```

Mitigation strategies:

### Cache Type Information

```go
var typeCache sync.Map

func GetCachedTypePath(val any) string {
    t := reflect.TypeOf(val)

    if cached, ok := typeCache.Load(t); ok {
        return cached.(string)
    }

    path := computeTypePath(t)
    typeCache.Store(t, path)
    return path
}
```

### Use Reflection at Registration, Not Runtime

```go
// GOOD: Reflect once during registration
func Register(handler CommandHandler) {
    for _, cmd := range handler.GetCommands() {
        path := GetTypePath(cmd)  // Reflection here
        handlers[path] = handler
    }
}

// Runtime dispatch uses cached path
func Dispatch(cmd Command) {
    handler := handlers[cmd.Path]  // No reflection
    handler.Handle(cmd)
}
```

### Avoid Reflection in Hot Paths

```go
// BAD: Reflection on every call
func Process(events []Event) {
    for _, evt := range events {
        name := GetTypeName(evt.Data)  // Reflection in loop!
        // ...
    }
}

// GOOD: Store type info in the event
type Event struct {
    TypeName string  // Computed once at creation
    Data     any
}
```

## Safety Patterns

### Compile-Time Interface Verification

Go doesn't require explicit interface declarations, which can lead to runtime surprises. Use this pattern to verify interface compliance at compile time:

```go
// Verify BaseAggregate implements all required interfaces
var _ Aggregate = (*BaseAggregate)(nil)
var _ EventHandler = (*BaseAggregate)(nil)
var _ AttributeProvider = (*BaseAggregate)(nil)
```

This creates a zero-cost compile-time check. If `BaseAggregate` doesn't implement `Aggregate`, you get a compiler error instead of a runtime panic:

```
cannot use (*BaseAggregate)(nil) (type *BaseAggregate) as type Aggregate:
    *BaseAggregate does not implement Aggregate (missing GetDomain method)
```

Use this pattern whenever you:
- Define types that must implement specific interfaces
- Refactor interface methods
- Work in large codebases where interface changes might be missed

### Always Handle Nil

```go
func GetTypeName(val any) string {
    if val == nil {
        return ""  // Don't panic!
    }
    // ...
}
```

### Use Type Switches When Possible

```go
// BETTER than reflection when types are known
func ProcessEvent(evt DomainEvt) error {
    switch e := evt.(type) {
    case *OrderPlacedEvent:
        return handleOrderPlaced(e)
    case *OrderShippedEvent:
        return handleOrderShipped(e)
    default:
        return fmt.Errorf("unknown event: %T", evt)
    }
}
```

### Validate Before Operating

```go
func SetField(obj any, fieldName string, value any) error {
    v := reflect.ValueOf(obj)

    // Must be a pointer to modify
    if v.Kind() != reflect.Ptr {
        return errors.New("obj must be a pointer")
    }

    v = v.Elem()

    // Must be a struct
    if v.Kind() != reflect.Struct {
        return errors.New("obj must point to a struct")
    }

    field := v.FieldByName(fieldName)

    // Field must exist
    if !field.IsValid() {
        return fmt.Errorf("no field named %s", fieldName)
    }

    // Field must be settable
    if !field.CanSet() {
        return fmt.Errorf("field %s is not settable", fieldName)
    }

    // Types must be compatible
    val := reflect.ValueOf(value)
    if !val.Type().AssignableTo(field.Type()) {
        return fmt.Errorf("cannot assign %T to field %s of type %s",
            value, fieldName, field.Type())
    }

    field.Set(val)
    return nil
}
```

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/07-reflection-done-right/code

When you run the complete example, you'll see all reflection utilities in action:

```
=== Reflection Done Right Demo ===

1. Nil Checking (the tricky interface-nil case):
   order == nil:      true
   agg == nil:        false (interface contains nil pointer!)
   IsNil(order):      true
   IsNil(agg):        true (correctly detects nil inside interface)

2. Getting Type Names:
   GetTypeName(cmd):   "PlaceOrderCommand"
   GetTypeName(evt):   "OrderPlacedEvent"
   GetTypeName(order): "Order"
   GetTypeName(nil):   ""

3. Fully Qualified Type Paths:
   GetTypePath(cmd): "main.PlaceOrderCommand"
   GetTypePath(evt): "main.OrderPlacedEvent"
   GetTypePath(func): "main.main"

4. Dynamic Instance Creation with Generics:
   NewInstanceOf[*Order](): &{ID: Status: Total:0 Items:[]} (valid pointer)
   NewInstanceOf[Item]():   {SKU: Quantity:0 Price:0} (zero value struct)

5. Safe Type Conversion:
   InterfaceToInt64(42):        42
   InterfaceToInt64(int64(99)): 99
   InterfaceToInt64("123"):     123
   InterfaceToInt64("abc"):     0 (invalid string returns 0)
   InterfaceToInt64(3.14):      0 (unsupported type returns 0)

6. Interface to Map Conversion:
   Order as map:
      status: placed
      total: 149.99
      items: [map[price:49.99 quantity:2 sku:ABC]]
      id: order-123

7. Finding Values in Nested Structures:
   FindInterfaceByKey(data, "email"):    user@example.com (found: true)
   FindInterfaceByKey(data, "theme"):    dark (found: true)
   FindInterfaceByKey(data, "total"):    99.99 (found: true) (first match)
   FindInterfaceByKey(data, "missing"): <nil> (found: false)

8. Deep Copy (true copy, not pointer copy):
   Original: &{ID:order-1 Status:placed Total:0 Items:[...]}
   DeepCopy successful: true
   After modifying copy:
      Original.Status: "placed" (unchanged)
      Copied.Status:   "shipped"
      Original.Items[0].SKU: "ABC" (unchanged)
      Copied.Items[0].SKU:   "XYZ"

9. Compile-time Interface Verification:
   The following lines verify interfaces at compile time:
      var _ Aggregate = (*Order)(nil)
      var _ DomainEvent = (*OrderPlacedEvent)(nil)
   -> If Order didn't implement Aggregate, this file wouldn't compile!

10. Performance Comparison:
   100000 iterations:
      Direct field access:    30.791µs
      Reflection access:      4.295ms (~140x slower)
      Cached type lookup:     1.004ms
   -> Reflection is powerful but use it wisely!

=== Demo Complete ===
```

## Summary

Reflection is a sharp tool. Use it wisely:

1. **Appropriate use cases**: Type inspection, dynamic instantiation, serialization
2. **Build utilities**: Centralize reflection in tested helper functions
3. **Handle nil**: Always check for nil to avoid panics
4. **Cache results**: Avoid repeated reflection on the same types
5. **Reflect early**: Do reflection at registration, not in hot paths
6. **Prefer alternatives**: Type switches and generics when possible
7. **Combine with generics**: Use generics for compile-time safety, reflection for runtime flexibility
8. **Verify interfaces**: Use `var _ Interface = (*Type)(nil)` for compile-time checks

The utilities we've built form the foundation for dynamic dispatch, serialization, and the provider pattern we'll explore next.

---

## What's Next

In the next post, we'll explore the **Provider Pattern** — using reflection utilities to build a registry for lazy deserialization and dynamic type resolution.

