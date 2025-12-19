package pkg

import (
	"encoding/json"
	"time"
)

// Item represents an order item
type Item struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

// Events describe what happened

type OrderPlacedEvent struct {
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	Items      []Item    `json:"items"`
	Total      float64   `json:"total"`
	PlacedAt   time.Time `json:"placed_at"`
}

type OrderPaidEvent struct {
	OrderID   string    `json:"order_id"`
	PaymentID string    `json:"payment_id"`
	Amount    float64   `json:"amount"`
	PaidAt    time.Time `json:"paid_at"`
}

type OrderShippedEvent struct {
	OrderID    string    `json:"order_id"`
	TrackingNo string    `json:"tracking_no"`
	ShippedAt  time.Time `json:"shipped_at"`
}

// Order represents the order state
type Order struct {
	ID         string
	Status     string
	Items      []Item
	Total      float64
	Customer   string
	TrackingNo string
}

// ReplayOrder rebuilds order state from events
func ReplayOrder(events []Event) *Order {
	order := &Order{}
	for _, evt := range events {
		switch evt.Type {
		case "OrderPlaced":
			var data struct {
				Customer string  `json:"customer"`
				Total    float64 `json:"total"`
			}
			json.Unmarshal(evt.Data, &data)
			order.ID = evt.AggregateID
			order.Status = "placed"
			order.Customer = data.Customer
			order.Total = data.Total
		case "OrderShipped":
			var data struct {
				Tracking string `json:"tracking"`
			}
			json.Unmarshal(evt.Data, &data)
			order.Status = "shipped"
			order.TrackingNo = data.Tracking
		}
	}
	return order
}
