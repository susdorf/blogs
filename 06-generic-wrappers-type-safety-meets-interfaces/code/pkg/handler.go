package pkg

import (
	"context"
	"fmt"
)

// DomainCommandHandler represents an entry in the command handler's registry.
type DomainCommandHandler struct {
	DomainCmdPath        string
	DomainCmdName        string
	DomainCmd            DomainCmd
	DomainCmdHandlerFunc DomainCommandHandlerFunc
}

// CommandHandler interface for handling domain-specific commands.
type CommandHandler interface {
	GetDomainCommandHandler(domainCmd DomainCmd) *DomainCommandHandler
	GetDomainCommands() []DomainCmd
}

// _domainCommandHandlerAdder is an internal interface for adding handlers.
type _domainCommandHandlerAdder interface {
	addDomainCommandHandler(domainCmd DomainCmd, fn DomainCommandHandlerFunc) error
}

// AddDomainCommandHandler wraps a typed handler for storage in the registry.
// This is the key function that bridges type-safe user code with framework storage.
func AddDomainCommandHandler[T DomainCmd](concreteHandler CommandHandler, fn TypedDomainCommandHandlerFunc[T]) error {
	// Create a new empty instance of the domain command type T (used as a key).
	domainCmd := NewInstanceOf[T]()

	// Wrap the typed handler function to match the DomainCommandHandlerFunc signature.
	wrappedFn := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		switch typedDomainCmd := domainCmd.(type) {
		case T:
			return fn(ctx, cmd, typedDomainCmd)
		}
		return nil, fmt.Errorf("invalid domain command type: '%T'", domainCmd)
	}

	// Attempt to add the wrapped handler function to the concrete command handler.
	switch _handler := concreteHandler.(type) {
	case _domainCommandHandlerAdder:
		return _handler.addDomainCommandHandler(domainCmd, wrappedFn)
	}

	return fmt.Errorf("could not add domain command handler")
}

// --- BaseCommandHandler implementation ---

// BaseCommandHandler provides a base implementation for command handlers.
type BaseCommandHandler struct {
	Domain   string
	handlers map[string]*DomainCommandHandler
}

// NewBaseCommandHandler creates a new BaseCommandHandler.
func NewBaseCommandHandler(domain string) *BaseCommandHandler {
	return &BaseCommandHandler{
		Domain:   domain,
		handlers: make(map[string]*DomainCommandHandler),
	}
}

func (h *BaseCommandHandler) addDomainCommandHandler(domainCmd DomainCmd, fn DomainCommandHandlerFunc) error {
	path := domainCmd.CmdPath()
	h.handlers[path] = &DomainCommandHandler{
		DomainCmdPath:        path,
		DomainCmdName:        fmt.Sprintf("%T", domainCmd),
		DomainCmd:            domainCmd,
		DomainCmdHandlerFunc: fn,
	}
	return nil
}

func (h *BaseCommandHandler) GetDomainCommandHandler(domainCmd DomainCmd) *DomainCommandHandler {
	return h.handlers[domainCmd.CmdPath()]
}

func (h *BaseCommandHandler) GetDomainCommands() []DomainCmd {
	cmds := make([]DomainCmd, 0, len(h.handlers))
	for _, handler := range h.handlers {
		cmds = append(cmds, handler.DomainCmd)
	}
	return cmds
}

// HandleCommand dispatches a command to the appropriate handler.
func (h *BaseCommandHandler) HandleCommand(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
	handler := h.GetDomainCommandHandler(domainCmd)
	if handler == nil {
		return nil, fmt.Errorf("no handler registered for command: %s", domainCmd.CmdPath())
	}
	return handler.DomainCmdHandlerFunc(ctx, cmd, domainCmd)
}

// --- Event Handler with same pattern ---

type DomainEventHandler struct {
	DomainEvtPath        string
	DomainEvt            DomainEvt
	DomainEvtHandlerFunc DomainEventHandlerFunc
}

type EventHandler interface {
	GetDomainEventHandler(domainEvt DomainEvt) *DomainEventHandler
}

type _domainEventHandlerAdder interface {
	addDomainEventHandler(domainEvt DomainEvt, fn DomainEventHandlerFunc) error
}

// AddDomainEventHandler wraps a typed event handler for storage.
func AddDomainEventHandler[T DomainEvt](concreteHandler EventHandler, fn TypedDomainEventHandlerFunc[T]) error {
	domainEvt := NewInstanceOf[T]()

	wrappedFn := func(ctx context.Context, domainEvt DomainEvt) error {
		switch typedDomainEvt := domainEvt.(type) {
		case T:
			return fn(ctx, typedDomainEvt)
		}
		return fmt.Errorf("invalid domain event type: '%T'", domainEvt)
	}

	switch _handler := concreteHandler.(type) {
	case _domainEventHandlerAdder:
		return _handler.addDomainEventHandler(domainEvt, wrappedFn)
	}

	return fmt.Errorf("could not add domain event handler")
}

// BaseEventHandler provides a base implementation for event handlers.
type BaseEventHandler struct {
	handlers map[string]*DomainEventHandler
}

func NewBaseEventHandler() *BaseEventHandler {
	return &BaseEventHandler{
		handlers: make(map[string]*DomainEventHandler),
	}
}

func (h *BaseEventHandler) addDomainEventHandler(domainEvt DomainEvt, fn DomainEventHandlerFunc) error {
	path := domainEvt.EvtPath()
	h.handlers[path] = &DomainEventHandler{
		DomainEvtPath:        path,
		DomainEvt:            domainEvt,
		DomainEvtHandlerFunc: fn,
	}
	return nil
}

func (h *BaseEventHandler) GetDomainEventHandler(domainEvt DomainEvt) *DomainEventHandler {
	return h.handlers[domainEvt.EvtPath()]
}

func (h *BaseEventHandler) HandleEvent(ctx context.Context, domainEvt DomainEvt) error {
	handler := h.GetDomainEventHandler(domainEvt)
	if handler == nil {
		return fmt.Errorf("no handler registered for event: %s", domainEvt.EvtPath())
	}
	return handler.DomainEvtHandlerFunc(ctx, domainEvt)
}
