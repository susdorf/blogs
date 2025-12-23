package pkg

import "context"

// Event represents a domain event (simplified for demo)
type Event struct {
	Type string
	Data any
}

// Command represents a command envelope with metadata
type Command struct {
	ID            string
	Domain        string
	DomainCmdName string
}

// DomainCmd is the interface for domain command payloads
type DomainCmd interface {
	CmdPath() string
}

// DomainEvt is the interface for domain event payloads
type DomainEvt interface {
	EvtPath() string
}

// --- Sample Domain Commands ---

type PlaceOrderCommand struct {
	OrderID    string
	CustomerID string
	Items      []string
}

func (c *PlaceOrderCommand) CmdPath() string { return "Order.PlaceOrderCommand" }

type CancelOrderCommand struct {
	OrderID string
	Reason  string
}

func (c *CancelOrderCommand) CmdPath() string { return "Order.CancelOrderCommand" }

type ShipOrderCommand struct {
	OrderID        string
	ShippingMethod string
}

func (c *ShipOrderCommand) CmdPath() string { return "Order.ShipOrderCommand" }

// --- Sample Domain Events ---

type OrderPlacedEvent struct {
	OrderID    string
	CustomerID string
	Items      []string
}

func (e *OrderPlacedEvent) EvtPath() string { return "Order.OrderPlacedEvent" }

type OrderCancelledEvent struct {
	OrderID string
	Reason  string
}

func (e *OrderCancelledEvent) EvtPath() string { return "Order.OrderCancelledEvent" }

// --- Handler Function Types ---

// DomainCommandHandlerFunc is the non-generic type for storage
type DomainCommandHandlerFunc func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error)

// TypedDomainCommandHandlerFunc is the generic type for user code
type TypedDomainCommandHandlerFunc[T DomainCmd] func(ctx context.Context, cmd Command, domainCmd T) ([]Event, error)

// DomainEventHandlerFunc is the non-generic type for event handlers
type DomainEventHandlerFunc func(ctx context.Context, domainEvt DomainEvt) error

// TypedDomainEventHandlerFunc is the generic type for typed event handlers
type TypedDomainEventHandlerFunc[T DomainEvt] func(ctx context.Context, domainEvt T) error
