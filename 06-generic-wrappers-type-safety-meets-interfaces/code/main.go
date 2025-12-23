package main

import (
	"context"
	"fmt"
	"reflect"

	"generic-wrappers-demo/pkg"
)

func main() {
	fmt.Println("=== Generic Wrappers Demo ===")
	fmt.Println()

	// --- 1. Demonstrate NewInstanceOf helper ---
	fmt.Println("1. NewInstanceOf helper - creating typed instances:")
	demonstrateNewInstanceOf()

	// --- 2. Register typed command handlers ---
	fmt.Println()
	fmt.Println("2. Registering typed command handlers:")
	orderHandler := setupOrderCommandHandler()

	// --- 3. Show registered handlers ---
	fmt.Println()
	fmt.Println("3. Registered domain commands:")
	for _, cmd := range orderHandler.GetDomainCommands() {
		fmt.Printf("   -> %s\n", cmd.CmdPath())
	}

	// --- 4. Dispatch commands with type safety ---
	fmt.Println()
	fmt.Println("4. Dispatching commands (type-safe at registration, dynamic at runtime):")
	dispatchCommands(orderHandler)

	// --- 5. Demonstrate event handlers with same pattern ---
	fmt.Println()
	fmt.Println("5. Event handlers use the same pattern:")
	demonstrateEventHandlers()

	// --- 6. Show Serialize/Deserialize helpers ---
	fmt.Println()
	fmt.Println("6. Serialize/Deserialize helpers:")
	demonstrateSerialization()

	// --- 7. Demonstrate type inference ---
	fmt.Println()
	fmt.Println("7. Go's type inference in action:")
	fmt.Println("   When you write: pkg.AddDomainCommandHandler(ch, ch.PlaceOrder)")
	fmt.Println("   Go infers T = *PlaceOrderCommand from the handler signature")
	fmt.Println("   No explicit type parameters needed!")

	// --- 8. Show what happens with wrong type ---
	fmt.Println()
	fmt.Println("8. Type safety - wrong type at runtime:")
	demonstrateTypeSafety(orderHandler)

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

func demonstrateNewInstanceOf() {
	// Pointer type - creates actual instance
	cmd := pkg.NewInstanceOf[*pkg.PlaceOrderCommand]()
	fmt.Printf("   -> NewInstanceOf[*PlaceOrderCommand](): %T (non-nil: %v)\n",
		cmd, cmd != nil)

	// Value type - returns zero value
	type ValueCommand struct{ ID string }
	valCmd := pkg.NewInstanceOf[ValueCommand]()
	fmt.Printf("   -> NewInstanceOf[ValueCommand](): %T (zero value)\n", valCmd)

	// The instance can be used for type registration
	cmdType := reflect.TypeOf(cmd)
	fmt.Printf("   -> Type path: %s\n", cmdType.Elem().Name())
}

// OrderCommandHandler demonstrates a typed command handler
type OrderCommandHandler struct {
	*pkg.BaseCommandHandler
}

func setupOrderCommandHandler() *OrderCommandHandler {
	ch := &OrderCommandHandler{
		BaseCommandHandler: pkg.NewBaseCommandHandler("Order"),
	}

	// Register handlers - type inference works automatically!
	// Note: comby.AddDomainCommandHandler(ch, ...) not ch.AddDomainCommandHandler(...)
	// because Go doesn't allow generic methods on non-generic structs
	pkg.AddDomainCommandHandler(ch, ch.PlaceOrder)
	pkg.AddDomainCommandHandler(ch, ch.CancelOrder)
	pkg.AddDomainCommandHandler(ch, ch.ShipOrder)

	fmt.Println("   -> Registered PlaceOrder, CancelOrder, ShipOrder handlers")
	fmt.Println("   -> Each handler has its own concrete type (no interface{} in user code)")

	return ch
}

// PlaceOrder is a typed handler - note the concrete *PlaceOrderCommand type
func (ch *OrderCommandHandler) PlaceOrder(
	ctx context.Context,
	cmd pkg.Command,
	domainCmd *pkg.PlaceOrderCommand, // Concrete type, not interface!
) ([]pkg.Event, error) {
	fmt.Printf("      PlaceOrder called: OrderID=%s, CustomerID=%s, Items=%v\n",
		domainCmd.OrderID, domainCmd.CustomerID, domainCmd.Items)

	return []pkg.Event{
		{Type: "OrderPlaced", Data: domainCmd},
	}, nil
}

// CancelOrder is another typed handler with different concrete type
func (ch *OrderCommandHandler) CancelOrder(
	ctx context.Context,
	cmd pkg.Command,
	domainCmd *pkg.CancelOrderCommand, // Different concrete type
) ([]pkg.Event, error) {
	fmt.Printf("      CancelOrder called: OrderID=%s, Reason=%s\n",
		domainCmd.OrderID, domainCmd.Reason)

	return []pkg.Event{
		{Type: "OrderCancelled", Data: domainCmd},
	}, nil
}

// ShipOrder handler
func (ch *OrderCommandHandler) ShipOrder(
	ctx context.Context,
	cmd pkg.Command,
	domainCmd *pkg.ShipOrderCommand,
) ([]pkg.Event, error) {
	fmt.Printf("      ShipOrder called: OrderID=%s, Method=%s\n",
		domainCmd.OrderID, domainCmd.ShippingMethod)

	return []pkg.Event{
		{Type: "OrderShipped", Data: domainCmd},
	}, nil
}

func dispatchCommands(handler *OrderCommandHandler) {
	ctx := context.Background()

	// Command 1: PlaceOrder
	placeCmd := &pkg.PlaceOrderCommand{
		OrderID:    "order-123",
		CustomerID: "customer-456",
		Items:      []string{"widget", "gadget"},
	}
	events, _ := handler.HandleCommand(ctx, pkg.Command{}, placeCmd)
	fmt.Printf("   -> PlaceOrder returned %d event(s)\n", len(events))

	// Command 2: ShipOrder
	shipCmd := &pkg.ShipOrderCommand{
		OrderID:        "order-123",
		ShippingMethod: "express",
	}
	events, _ = handler.HandleCommand(ctx, pkg.Command{}, shipCmd)
	fmt.Printf("   -> ShipOrder returned %d event(s)\n", len(events))

	// Command 3: CancelOrder (different order)
	cancelCmd := &pkg.CancelOrderCommand{
		OrderID: "order-999",
		Reason:  "customer request",
	}
	events, _ = handler.HandleCommand(ctx, pkg.Command{}, cancelCmd)
	fmt.Printf("   -> CancelOrder returned %d event(s)\n", len(events))
}

// OrderEventHandler demonstrates the same pattern for events
type OrderEventHandler struct {
	*pkg.BaseEventHandler
	processedEvents []string
}

func demonstrateEventHandlers() {
	eh := &OrderEventHandler{
		BaseEventHandler: pkg.NewBaseEventHandler(),
	}

	// Same pattern: package-level generic function
	pkg.AddDomainEventHandler(eh, eh.OnOrderPlaced)
	pkg.AddDomainEventHandler(eh, eh.OnOrderCancelled)

	fmt.Println("   -> Registered OnOrderPlaced, OnOrderCancelled handlers")

	// Dispatch an event
	ctx := context.Background()
	evt := &pkg.OrderPlacedEvent{
		OrderID:    "order-123",
		CustomerID: "customer-456",
		Items:      []string{"widget"},
	}
	eh.HandleEvent(ctx, evt)
}

func (eh *OrderEventHandler) OnOrderPlaced(ctx context.Context, evt *pkg.OrderPlacedEvent) error {
	fmt.Printf("   -> OnOrderPlaced: Order %s placed by %s\n", evt.OrderID, evt.CustomerID)
	eh.processedEvents = append(eh.processedEvents, evt.OrderID)
	return nil
}

func (eh *OrderEventHandler) OnOrderCancelled(ctx context.Context, evt *pkg.OrderCancelledEvent) error {
	fmt.Printf("   -> OnOrderCancelled: Order %s cancelled: %s\n", evt.OrderID, evt.Reason)
	return nil
}

func demonstrateSerialization() {
	cmd := &pkg.PlaceOrderCommand{
		OrderID:    "order-789",
		CustomerID: "customer-012",
		Items:      []string{"item1", "item2"},
	}

	// Serialize
	data, _ := pkg.Serialize(cmd)
	fmt.Printf("   -> Serialized: %s\n", string(data))

	// Deserialize back to typed instance
	restored, _ := pkg.Deserialize(data, cmd)
	fmt.Printf("   -> Deserialized: OrderID=%s, CustomerID=%s\n",
		restored.OrderID, restored.CustomerID)
}

func demonstrateTypeSafety(handler *OrderCommandHandler) {
	ctx := context.Background()

	// Create a command that isn't registered
	type UnknownCommand struct{}

	// This would fail at compile time if you tried to register it
	// because UnknownCommand doesn't implement DomainCmd

	// At runtime, if somehow wrong type reaches handler:
	// (This simulates what the wrapper catches)
	wrongCmd := &pkg.PlaceOrderCommand{OrderID: "test"}
	cancelHandler := handler.GetDomainCommandHandler(&pkg.CancelOrderCommand{})

	if cancelHandler != nil {
		// Calling cancel handler with place command - wrapper catches this!
		_, err := cancelHandler.DomainCmdHandlerFunc(ctx, pkg.Command{}, wrongCmd)
		if err != nil {
			fmt.Printf("   -> Error caught: %v\n", err)
		}
	}
}
