package pkg

import (
	"context"
	"fmt"
)

// Queries - ask questions (what we want to know)

type GetOrder struct {
	OrderID string
}

type ListOrdersByCustomer struct {
	CustomerID string
	Status     string // Optional filter
	Page       int
	PageSize   int
}

// QueryHandler routes queries using a simple switch statement
type QueryHandler struct {
	ReadModel *OrderReadModel
}

func (h *QueryHandler) Handle(ctx context.Context, query any) (any, error) {
	switch q := query.(type) {
	case GetOrder:
		return h.ReadModel.GetOrder(q.OrderID)
	case ListOrdersByCustomer:
		page := q.Page
		if page < 1 {
			page = 1
		}
		pageSize := q.PageSize
		if pageSize < 1 {
			pageSize = 10
		}
		return h.ReadModel.ListByCustomer(q.CustomerID, q.Status, page, pageSize)
	default:
		return nil, fmt.Errorf("unknown query: %T", query)
	}
}
