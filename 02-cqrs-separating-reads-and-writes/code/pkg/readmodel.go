package pkg

import (
	"fmt"
	"sync"
	"time"
)

// OrderView is a denormalized read model optimized for queries
type OrderView struct {
	OrderID    string
	CustomerID string
	Status     string
	Items      []ItemView
	Total      float64
	ItemCount  int
	TrackingNo string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ItemView struct {
	ProductID string
	Name      string
	Quantity  int
	Price     float64
}

// OrderReadModel is the projection that maintains query-optimized views
type OrderReadModel struct {
	mu         sync.RWMutex
	orders     map[string]*OrderView
	byCustomer map[string][]*OrderView
}

func NewOrderReadModel() *OrderReadModel {
	return &OrderReadModel{
		orders:     make(map[string]*OrderView),
		byCustomer: make(map[string][]*OrderView),
	}
}

// Apply handles all events that affect this read model using a switch statement
func (rm *OrderReadModel) Apply(event any) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	switch e := event.(type) {
	case OrderPlacedEvent:
		items := make([]ItemView, len(e.Items))
		for i, item := range e.Items {
			items[i] = ItemView{
				ProductID: item.ProductID,
				Name:      item.Name,
				Quantity:  item.Quantity,
				Price:     item.Price,
			}
		}

		view := &OrderView{
			OrderID:    e.OrderID,
			CustomerID: e.CustomerID,
			Status:     "placed",
			Items:      items,
			Total:      e.Total,
			ItemCount:  len(e.Items),
			CreatedAt:  e.PlacedAt,
			UpdatedAt:  e.PlacedAt,
		}

		rm.orders[e.OrderID] = view
		rm.byCustomer[e.CustomerID] = append(rm.byCustomer[e.CustomerID], view)

	case OrderShippedEvent:
		if view, ok := rm.orders[e.OrderID]; ok {
			view.Status = "shipped"
			view.TrackingNo = e.TrackingNo
			view.UpdatedAt = e.ShippedAt
		}

	case OrderCancelledEvent:
		if view, ok := rm.orders[e.OrderID]; ok {
			view.Status = "cancelled"
			view.UpdatedAt = e.CancelledAt
		}
	}

	return nil
}

// Query methods - simple lookups on the denormalized data

func (rm *OrderReadModel) GetOrder(orderID string) (*OrderView, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if view, ok := rm.orders[orderID]; ok {
		return view, nil
	}
	return nil, fmt.Errorf("order not found: %s", orderID)
}

func (rm *OrderReadModel) ListByCustomer(customerID, status string, page, pageSize int) ([]*OrderView, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	orders := rm.byCustomer[customerID]

	// Filter by status if provided
	if status != "" {
		filtered := make([]*OrderView, 0)
		for _, o := range orders {
			if o.Status == status {
				filtered = append(filtered, o)
			}
		}
		orders = filtered
	}

	// Paginate
	start := (page - 1) * pageSize
	if start >= len(orders) {
		return []*OrderView{}, nil
	}
	end := start + pageSize
	if end > len(orders) {
		end = len(orders)
	}

	return orders[start:end], nil
}
