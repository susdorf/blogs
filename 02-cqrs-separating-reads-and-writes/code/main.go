package main

import (
	"context"
	"fmt"

	"cqrs-demo/pkg"
)

// Application wires all handlers together
type Application struct {
	Commands   *pkg.CommandHandler
	Queries    *pkg.QueryHandler
	EventStore *pkg.EventStore
	ReadModel  *pkg.OrderReadModel
}

func NewApplication() *Application {
	// Create infrastructure
	eventStore := pkg.NewEventStore()
	readModel := pkg.NewOrderReadModel()
	repository := pkg.NewAggregateRepository(eventStore, readModel)

	// Create handlers
	commands := &pkg.CommandHandler{Repository: repository}
	queries := &pkg.QueryHandler{ReadModel: readModel}

	return &Application{
		Commands:   commands,
		Queries:    queries,
		EventStore: eventStore,
		ReadModel:  readModel,
	}
}

func main() {
	app := NewApplication()
	ctx := context.Background()

	fmt.Println("=== CQRS Demo ===")
	fmt.Println()

	// --- Command Side: Place an order ---
	fmt.Println("1. Placing an order...")
	err := app.Commands.Handle(ctx, pkg.PlaceOrder{
		OrderID:    "order-123",
		CustomerID: "customer-456",
		Items: []pkg.OrderItem{
			{ProductID: "prod-1", Name: "Widget", Quantity: 2, Price: 29.99},
			{ProductID: "prod-2", Name: "Gadget", Quantity: 1, Price: 49.99},
		},
	})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	fmt.Println("   -> Order placed successfully")

	// --- Query Side: Get the order ---
	fmt.Println()
	fmt.Println("2. Querying the order...")
	result, err := app.Queries.Handle(ctx, pkg.GetOrder{OrderID: "order-123"})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	order := result.(*pkg.OrderView)
	printOrder(order)

	// --- Command Side: Ship the order ---
	fmt.Println()
	fmt.Println("3. Shipping the order...")
	err = app.Commands.Handle(ctx, pkg.ShipOrder{
		OrderID:    "order-123",
		TrackingNo: "TRK-789",
	})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	fmt.Println("   -> Order shipped successfully")

	// --- Query Side: Get updated order ---
	fmt.Println()
	fmt.Println("4. Querying the updated order...")
	result, err = app.Queries.Handle(ctx, pkg.GetOrder{OrderID: "order-123"})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	order = result.(*pkg.OrderView)
	printOrder(order)

	// --- Place another order and cancel it ---
	fmt.Println()
	fmt.Println("5. Placing a second order...")
	err = app.Commands.Handle(ctx, pkg.PlaceOrder{
		OrderID:    "order-456",
		CustomerID: "customer-456",
		Items: []pkg.OrderItem{
			{ProductID: "prod-3", Name: "Thingamajig", Quantity: 3, Price: 19.99},
		},
	})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	fmt.Println("   -> Second order placed")

	fmt.Println()
	fmt.Println("6. Cancelling the second order...")
	err = app.Commands.Handle(ctx, pkg.CancelOrder{
		OrderID: "order-456",
		Reason:  "Customer changed their mind",
	})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	fmt.Println("   -> Order cancelled")

	// --- Query Side: List all orders for customer ---
	fmt.Println()
	fmt.Println("7. Listing all orders for customer-456...")
	result, err = app.Queries.Handle(ctx, pkg.ListOrdersByCustomer{
		CustomerID: "customer-456",
		Page:       1,
		PageSize:   10,
	})
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}
	orders := result.([]*pkg.OrderView)
	fmt.Printf("   Found %d orders:\n", len(orders))
	for _, o := range orders {
		fmt.Printf("   - %s: %s (%.2f)\n", o.OrderID, o.Status, o.Total)
	}

	// --- Demonstrate validation ---
	fmt.Println()
	fmt.Println("8. Trying to ship a cancelled order (should fail)...")
	err = app.Commands.Handle(ctx, pkg.ShipOrder{
		OrderID:    "order-456",
		TrackingNo: "TRK-000",
	})
	if err != nil {
		fmt.Printf("   -> Expected error: %v\n", err)
	}

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

func printOrder(order *pkg.OrderView) {
	fmt.Printf("   Order ID:    %s\n", order.OrderID)
	fmt.Printf("   Customer:    %s\n", order.CustomerID)
	fmt.Printf("   Status:      %s\n", order.Status)
	fmt.Printf("   Total:       $%.2f\n", order.Total)
	fmt.Printf("   Items:       %d\n", order.ItemCount)
	if order.TrackingNo != "" {
		fmt.Printf("   Tracking:    %s\n", order.TrackingNo)
	}
}
