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

	// Place an order
	orderID := "order-123"
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

	// Ship the order
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

	// Replay to get current state
	events, _ := store.Load(ctx, orderID)
	order := es.ReplayOrder(events)

	fmt.Printf("Order %s: status=%s, tracking=%s\n",
		order.ID, order.Status, order.TrackingNo)
}
