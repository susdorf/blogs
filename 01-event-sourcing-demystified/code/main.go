package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	es "event-sourcing-demystified/pkg"
)

func main() {
	store := es.NewInMemoryEventStore()
	ctx := context.Background()

	fmt.Println("=== Event Sourcing Demo ===")
	fmt.Println()

	// Place an order
	orderID := "order-123"
	fmt.Println("1. Placing order...")
	placedEvent := es.Event{
		ID:            "evt-1",
		Type:          "OrderPlaced",
		AggregateID:   orderID,
		AggregateType: "Order",
		Version:       0,
		Timestamp:     time.Now(),
		Data:          json.RawMessage(`{"customer":"Alice","total":99.99}`),
	}
	store.Append(ctx, orderID, []es.Event{placedEvent})
	fmt.Printf("   -> Appended: %s (version %d)\n", placedEvent.Type, placedEvent.Version)

	// Ship the order
	fmt.Println("2. Shipping order...")
	shippedEvent := es.Event{
		ID:            "evt-2",
		Type:          "OrderShipped",
		AggregateID:   orderID,
		AggregateType: "Order",
		Version:       1,
		Timestamp:     time.Now(),
		Data:          json.RawMessage(`{"tracking":"TRK-456"}`),
	}
	store.Append(ctx, orderID, []es.Event{shippedEvent})
	fmt.Printf("   -> Appended: %s (version %d)\n", shippedEvent.Type, shippedEvent.Version)

	// Show the event store contents
	fmt.Println()
	fmt.Println("=== Event Store Contents ===")
	events, _ := store.Load(ctx, orderID)
	for i, evt := range events {
		fmt.Printf("   [%d] %s | %s | v%d\n", i, evt.Type, evt.AggregateID, evt.Version)
	}

	// Replay events to reconstruct state
	fmt.Println()
	fmt.Println("=== Replaying Events ===")
	order := &es.Order{}
	for _, evt := range events {
		order.Apply(evt)
		fmt.Printf("   Applied %-15s -> status=%s\n", evt.Type, order.Status)
	}

	// Final state
	fmt.Println()
	fmt.Println("=== Current State (reconstructed from events) ===")
	fmt.Printf("   Order ID:   %s\n", order.ID)
	fmt.Printf("   Customer:   %s\n", order.Customer)
	fmt.Printf("   Total:      %.2f\n", order.Total)
	fmt.Printf("   Status:     %s\n", order.Status)
	fmt.Printf("   Tracking:   %s\n", order.TrackingNo)
}
