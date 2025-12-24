package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
)

// ============================================================================
// Domain Interfaces
// ============================================================================

type DomainCmd interface {
	CommandName() string
}

type DomainEvt interface {
	EventName() string
}

// ============================================================================
// Sample Domain Commands
// ============================================================================

type PlaceOrderCommand struct {
	OrderID    string  `json:"orderId"`
	CustomerID string  `json:"customerId"`
	Total      float64 `json:"total"`
}

func (c *PlaceOrderCommand) CommandName() string { return "PlaceOrderCommand" }

type CancelOrderCommand struct {
	OrderID string `json:"orderId"`
	Reason  string `json:"reason"`
}

func (c *CancelOrderCommand) CommandName() string { return "CancelOrderCommand" }

type ShipOrderCommand struct {
	OrderID        string `json:"orderId"`
	TrackingNumber string `json:"trackingNumber"`
	Carrier        string `json:"carrier"`
}

func (c *ShipOrderCommand) CommandName() string { return "ShipOrderCommand" }

// ============================================================================
// Sample Domain Events
// ============================================================================

type OrderPlacedEvent struct {
	OrderID    string  `json:"orderId"`
	CustomerID string  `json:"customerId"`
	Total      float64 `json:"total"`
}

func (e *OrderPlacedEvent) EventName() string { return "OrderPlacedEvent" }

type OrderCancelledEvent struct {
	OrderID string `json:"orderId"`
	Reason  string `json:"reason"`
}

func (e *OrderCancelledEvent) EventName() string { return "OrderCancelledEvent" }

type OrderShippedEvent struct {
	OrderID        string `json:"orderId"`
	TrackingNumber string `json:"trackingNumber"`
}

func (e *OrderShippedEvent) EventName() string { return "OrderShippedEvent" }

// ============================================================================
// Reflection Utilities
// ============================================================================

func GetTypePath(val any) string {
	if val == nil {
		return ""
	}
	t := reflect.TypeOf(val)
	if t.Kind() == reflect.Ptr {
		return t.Elem().Name()
	}
	return t.Name()
}

func GetTypeName(val any) string {
	return GetTypePath(val)
}

func DeepCopy[T any](src T) (T, error) {
	var zero T
	data, err := json.Marshal(src)
	if err != nil {
		return zero, err
	}
	var dst T
	if err := json.Unmarshal(data, &dst); err != nil {
		return zero, err
	}
	return dst, nil
}

// Deserialize creates a new instance of the same concrete type as template
// and unmarshals data into it. This works with interface types by using
// reflection to determine the underlying concrete type.
func Deserialize[T any](data []byte, template T) (T, error) {
	var zero T

	// Get the type of the template
	t := reflect.TypeOf(template)
	if t == nil {
		return zero, errors.New("template is nil")
	}

	// Create a new instance of the same type
	var newInstance reflect.Value
	if t.Kind() == reflect.Ptr {
		// For pointer types, create a new pointer to a new value
		newInstance = reflect.New(t.Elem())
	} else {
		// For value types, create a new value
		newInstance = reflect.New(t).Elem()
	}

	// Unmarshal into the new instance
	target := newInstance.Interface()
	if t.Kind() != reflect.Ptr {
		// If template was a value type, we need to unmarshal into a pointer
		target = newInstance.Addr().Interface()
	}

	if err := json.Unmarshal(data, target); err != nil {
		return zero, err
	}

	// Return the result as the expected type
	return newInstance.Interface().(T), nil
}

// ============================================================================
// Generic SyncMap (type-safe wrapper around sync.Map)
// ============================================================================

type SyncMap[K comparable, V any] struct {
	m sync.Map
}

func (s *SyncMap[K, V]) Store(key K, value V) {
	s.m.Store(key, value)
}

func (s *SyncMap[K, V]) Load(key K) (V, bool) {
	val, ok := s.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

func (s *SyncMap[K, V]) Range(fn func(key K, value V) bool) {
	s.m.Range(func(k, v any) bool {
		return fn(k.(K), v.(V))
	})
}

// ============================================================================
// Command Envelope (carries serialized command data)
// ============================================================================

type Command struct {
	Domain         string
	DomainCmdName  string
	DomainCmdBytes []byte
	DomainCmd      DomainCmd // Populated after deserialization
}

func (c *Command) GetDomain() string         { return c.Domain }
func (c *Command) GetDomainCmdName() string  { return c.DomainCmdName }
func (c *Command) GetDomainCmdBytes() []byte { return c.DomainCmdBytes }
func (c *Command) SetDomainCmd(cmd DomainCmd) error {
	c.DomainCmd = cmd
	return nil
}

// ============================================================================
// Event Envelope (carries serialized event data)
// ============================================================================

type Event struct {
	Domain         string
	DomainEvtName  string
	DomainEvtBytes []byte
	DomainEvt      DomainEvt // Populated after deserialization
}

func (e *Event) GetDomain() string         { return e.Domain }
func (e *Event) GetDomainEvtName() string  { return e.DomainEvtName }
func (e *Event) GetDomainEvtBytes() []byte { return e.DomainEvtBytes }
func (e *Event) SetDomainEvt(evt DomainEvt) error {
	e.DomainEvt = evt
	return nil
}

// ============================================================================
// Domain Command Provider
// ============================================================================

type domainCommandEntry struct {
	Domain    string
	DomainCmd DomainCmd
}

type DomainCommandProvider struct {
	registry SyncMap[string, domainCommandEntry]
}

func NewDomainCommandProvider() *DomainCommandProvider {
	return &DomainCommandProvider{}
}

func (p *DomainCommandProvider) RegisterDomainCommand(domain string, cmd DomainCmd) error {
	if cmd == nil {
		return errors.New("domain command cannot be nil")
	}

	typePath := GetTypePath(cmd)
	if typePath == "" {
		return errors.New("could not determine type path")
	}

	key := fmt.Sprintf("%s.%s", domain, typePath)

	if _, exists := p.registry.Load(key); exists {
		return fmt.Errorf("command already registered: %s", key)
	}

	p.registry.Store(key, domainCommandEntry{
		Domain:    domain,
		DomainCmd: cmd,
	})

	return nil
}

func (p *DomainCommandProvider) ProvideDomainCommand(cmd *Command) error {
	if cmd.GetDomain() == "" || cmd.GetDomainCmdName() == "" {
		return errors.New("command missing domain or name")
	}

	key := fmt.Sprintf("%s.%s", cmd.GetDomain(), cmd.GetDomainCmdName())

	entry, ok := p.registry.Load(key)
	if !ok {
		return fmt.Errorf("unknown command type: %s", key)
	}

	cmdBytes := cmd.GetDomainCmdBytes()
	if len(cmdBytes) == 0 {
		return errors.New("command has no serialized data")
	}

	domainCmd, err := Deserialize(cmdBytes, entry.DomainCmd)
	if err != nil {
		return fmt.Errorf("deserialization failed: %w", err)
	}

	return cmd.SetDomainCmd(domainCmd)
}

func (p *DomainCommandProvider) GetDomainCommands() ([]string, []DomainCmd) {
	var domains []string
	var commands []DomainCmd

	p.registry.Range(func(key string, entry domainCommandEntry) bool {
		domains = append(domains, entry.Domain)
		commands = append(commands, entry.DomainCmd)
		return true
	})

	return domains, commands
}

// ============================================================================
// Domain Event Provider
// ============================================================================

type domainEventEntry struct {
	Domain    string
	DomainEvt DomainEvt
	IsOwner   bool
}

type DomainEventProvider struct {
	registry SyncMap[string, domainEventEntry]
}

func NewDomainEventProvider() *DomainEventProvider {
	return &DomainEventProvider{}
}

func (p *DomainEventProvider) RegisterDomainEvent(domain string, evt DomainEvt, isOwner bool) error {
	if evt == nil {
		return errors.New("domain event cannot be nil")
	}

	typePath := GetTypePath(evt)
	key := fmt.Sprintf("%s.%s", domain, typePath)

	if existing, ok := p.registry.Load(key); ok {
		if isOwner && existing.IsOwner {
			return fmt.Errorf("event %s already has an owner", key)
		}
	}

	p.registry.Store(key, domainEventEntry{
		Domain:    domain,
		DomainEvt: evt,
		IsOwner:   isOwner,
	})

	return nil
}

func (p *DomainEventProvider) ProvideDomainEvent(evt *Event) error {
	key := fmt.Sprintf("%s.%s", evt.GetDomain(), evt.GetDomainEvtName())

	entry, ok := p.registry.Load(key)
	if !ok {
		return fmt.Errorf("unknown event type: %s", key)
	}

	evtBytes := evt.GetDomainEvtBytes()
	if len(evtBytes) == 0 {
		return nil
	}

	domainEvt, err := Deserialize(evtBytes, entry.DomainEvt)
	if err != nil {
		return fmt.Errorf("event deserialization failed: %w", err)
	}

	return evt.SetDomainEvt(domainEvt)
}

func (p *DomainEventProvider) GetDomainEvents() ([]string, []DomainEvt) {
	var domains []string
	var events []DomainEvt

	p.registry.Range(func(key string, entry domainEventEntry) bool {
		domains = append(domains, entry.Domain)
		events = append(events, entry.DomainEvt)
		return true
	})

	return domains, events
}

// ============================================================================
// Main Demo
// ============================================================================

func main() {
	fmt.Println("=== Provider Pattern: Lazy Deserialization Demo ===")
	fmt.Println()

	// 1. Create providers
	fmt.Println("1. Creating providers:")
	cmdProvider := NewDomainCommandProvider()
	evtProvider := NewDomainEventProvider()
	fmt.Println("   -> DomainCommandProvider created")
	fmt.Println("   -> DomainEventProvider created")
	fmt.Println()

	// 2. Register command types
	fmt.Println("2. Registering command types:")
	cmdProvider.RegisterDomainCommand("Order", &PlaceOrderCommand{})
	cmdProvider.RegisterDomainCommand("Order", &CancelOrderCommand{})
	cmdProvider.RegisterDomainCommand("Order", &ShipOrderCommand{})
	fmt.Println("   -> Registered: Order.PlaceOrderCommand")
	fmt.Println("   -> Registered: Order.CancelOrderCommand")
	fmt.Println("   -> Registered: Order.ShipOrderCommand")
	fmt.Println()

	// 3. Register event types
	fmt.Println("3. Registering event types:")
	evtProvider.RegisterDomainEvent("Order", &OrderPlacedEvent{}, true)
	evtProvider.RegisterDomainEvent("Order", &OrderCancelledEvent{}, true)
	evtProvider.RegisterDomainEvent("Order", &OrderShippedEvent{}, true)
	fmt.Println("   -> Registered: Order.OrderPlacedEvent (owner: true)")
	fmt.Println("   -> Registered: Order.OrderCancelledEvent (owner: true)")
	fmt.Println("   -> Registered: Order.OrderShippedEvent (owner: true)")
	fmt.Println()

	// 4. Simulate incoming command (as if from message queue)
	fmt.Println("4. Simulating incoming command from message queue:")
	incomingJSON := `{"orderId":"order-123","customerId":"cust-456","total":99.99}`
	fmt.Printf("   -> Raw JSON: %s\n", incomingJSON)
	fmt.Printf("   -> Domain: Order, CommandName: PlaceOrderCommand\n")
	fmt.Println()

	// 5. Create command envelope with raw bytes
	fmt.Println("5. Creating command envelope (bytes not yet deserialized):")
	cmd := &Command{
		Domain:         "Order",
		DomainCmdName:  "PlaceOrderCommand",
		DomainCmdBytes: []byte(incomingJSON),
	}
	fmt.Printf("   -> cmd.DomainCmd before provide: %v (nil)\n", cmd.DomainCmd)
	fmt.Println()

	// 6. Provider deserializes into correct type (LAZY!)
	fmt.Println("6. Provider performs lazy deserialization:")
	err := cmdProvider.ProvideDomainCommand(cmd)
	if err != nil {
		fmt.Printf("   -> Error: %v\n", err)
	} else {
		fmt.Printf("   -> cmd.DomainCmd after provide: %T\n", cmd.DomainCmd)
		if placeCmd, ok := cmd.DomainCmd.(*PlaceOrderCommand); ok {
			fmt.Printf("   -> OrderID: %s\n", placeCmd.OrderID)
			fmt.Printf("   -> CustomerID: %s\n", placeCmd.CustomerID)
			fmt.Printf("   -> Total: %.2f\n", placeCmd.Total)
		}
	}
	fmt.Println()

	// 7. Demonstrate event deserialization
	fmt.Println("7. Event deserialization (same pattern):")
	eventJSON := `{"orderId":"order-123","customerId":"cust-456","total":99.99}`
	evt := &Event{
		Domain:         "Order",
		DomainEvtName:  "OrderPlacedEvent",
		DomainEvtBytes: []byte(eventJSON),
	}
	err = evtProvider.ProvideDomainEvent(evt)
	if err != nil {
		fmt.Printf("   -> Error: %v\n", err)
	} else {
		fmt.Printf("   -> evt.DomainEvt type: %T\n", evt.DomainEvt)
		if placedEvt, ok := evt.DomainEvt.(*OrderPlacedEvent); ok {
			fmt.Printf("   -> Event data: OrderID=%s, Total=%.2f\n", placedEvt.OrderID, placedEvt.Total)
		}
	}
	fmt.Println()

	// 8. Error handling: unknown command type
	fmt.Println("8. Error handling - unknown command type:")
	unknownCmd := &Command{
		Domain:         "Order",
		DomainCmdName:  "UnknownCommand",
		DomainCmdBytes: []byte(`{}`),
	}
	err = cmdProvider.ProvideDomainCommand(unknownCmd)
	if err != nil {
		fmt.Printf("   -> Error (expected): %v\n", err)
	}
	fmt.Println()

	// 9. Error handling: duplicate registration
	fmt.Println("9. Error handling - duplicate registration:")
	err = cmdProvider.RegisterDomainCommand("Order", &PlaceOrderCommand{})
	if err != nil {
		fmt.Printf("   -> Error (expected): %v\n", err)
	}
	fmt.Println()

	// 10. Introspection: list all registered types
	fmt.Println("10. Introspection - listing registered types:")
	domains, commands := cmdProvider.GetDomainCommands()
	fmt.Println("    Registered Commands:")
	for i, cmd := range commands {
		fmt.Printf("      [%d] %s.%s\n", i+1, domains[i], GetTypeName(cmd))
	}

	evtDomains, events := evtProvider.GetDomainEvents()
	fmt.Println("    Registered Events:")
	for i, evt := range events {
		fmt.Printf("      [%d] %s.%s\n", i+1, evtDomains[i], GetTypeName(evt))
	}
	fmt.Println()

	// 11. Demonstrate different command types
	fmt.Println("11. Deserializing different command types:")
	testCases := []struct {
		name string
		json string
	}{
		{"CancelOrderCommand", `{"orderId":"order-123","reason":"Customer request"}`},
		{"ShipOrderCommand", `{"orderId":"order-123","trackingNumber":"TRACK-789","carrier":"DHL"}`},
	}

	for _, tc := range testCases {
		cmd := &Command{
			Domain:         "Order",
			DomainCmdName:  tc.name,
			DomainCmdBytes: []byte(tc.json),
		}
		err := cmdProvider.ProvideDomainCommand(cmd)
		if err != nil {
			fmt.Printf("    -> %s: Error: %v\n", tc.name, err)
		} else {
			fmt.Printf("    -> %s: Deserialized to %T\n", tc.name, cmd.DomainCmd)
		}
	}
	fmt.Println()

	// 12. Show the benefit: no switch statement needed
	fmt.Println("12. Key benefit - no giant switch statement needed:")
	fmt.Println("    Instead of:")
	fmt.Println("      switch name {")
	fmt.Println("      case \"PlaceOrderCommand\": ...")
	fmt.Println("      case \"CancelOrderCommand\": ...")
	fmt.Println("      // hundreds more cases")
	fmt.Println("      }")
	fmt.Println()
	fmt.Println("    We simply call:")
	fmt.Println("      provider.ProvideDomainCommand(cmd)")
	fmt.Println("    And the correct type is resolved automatically!")
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
}
