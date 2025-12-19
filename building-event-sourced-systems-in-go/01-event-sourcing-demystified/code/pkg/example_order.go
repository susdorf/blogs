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

// Order represents the current state of an order
type Order struct {
	ID         string
	Status     string
	Customer   string
	Total      float64
	TrackingNo string
}

// Apply updates the order state based on an event
func (o *Order) Apply(evt Event) {
	switch evt.Type {
	case "OrderPlaced":
		var data struct {
			Customer string  `json:"customer"`
			Total    float64 `json:"total"`
		}
		json.Unmarshal(evt.Data, &data)
		o.ID = evt.AggregateID
		o.Status = "placed"
		o.Customer = data.Customer
		o.Total = data.Total
	case "OrderShipped":
		var data struct {
			Tracking string `json:"tracking"`
		}
		json.Unmarshal(evt.Data, &data)
		o.Status = "shipped"
		o.TrackingNo = data.Tracking
	}
}
