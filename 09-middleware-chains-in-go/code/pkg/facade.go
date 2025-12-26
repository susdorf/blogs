package pkg

import (
	"context"
	"fmt"
)

// Facade is the central dispatcher for commands and queries
type Facade struct {
	commandMiddlewares []CommandHandlerMiddlewareFunc
	queryMiddlewares   []QueryHandlerMiddlewareFunc
	commandHandlers    map[string]map[string]DomainCommandHandlerFunc
	queryHandlers      map[string]map[string]DomainQueryHandlerFunc
}

// NewFacade creates a new Facade instance
func NewFacade() *Facade {
	return &Facade{
		commandHandlers: make(map[string]map[string]DomainCommandHandlerFunc),
		queryHandlers:   make(map[string]map[string]DomainQueryHandlerFunc),
	}
}

// AddCommandHandlerMiddleware adds middlewares to the command chain
func (fc *Facade) AddCommandHandlerMiddleware(middlewares ...CommandHandlerMiddlewareFunc) {
	fc.commandMiddlewares = append(fc.commandMiddlewares, middlewares...)
}

// AddQueryHandlerMiddleware adds middlewares to the query chain
func (fc *Facade) AddQueryHandlerMiddleware(middlewares ...QueryHandlerMiddlewareFunc) {
	fc.queryMiddlewares = append(fc.queryMiddlewares, middlewares...)
}

// GetCommandHandlerMiddlewares returns the command middleware chain
func (fc *Facade) GetCommandHandlerMiddlewares() []CommandHandlerMiddlewareFunc {
	return fc.commandMiddlewares
}

// GetQueryHandlerMiddlewares returns the query middleware chain
func (fc *Facade) GetQueryHandlerMiddlewares() []QueryHandlerMiddlewareFunc {
	return fc.queryMiddlewares
}

// RegisterCommandHandler registers a handler for a domain command
func (fc *Facade) RegisterCommandHandler(domain, cmdName string, handler DomainCommandHandlerFunc) {
	if fc.commandHandlers[domain] == nil {
		fc.commandHandlers[domain] = make(map[string]DomainCommandHandlerFunc)
	}
	fc.commandHandlers[domain][cmdName] = handler
}

// RegisterQueryHandler registers a handler for a domain query
func (fc *Facade) RegisterQueryHandler(domain, qryName string, handler DomainQueryHandlerFunc) {
	if fc.queryHandlers[domain] == nil {
		fc.queryHandlers[domain] = make(map[string]DomainQueryHandlerFunc)
	}
	fc.queryHandlers[domain][qryName] = handler
}

// DispatchCommand dispatches a command through the middleware chain
func (fc *Facade) DispatchCommand(ctx context.Context, cmd Command) ([]Event, error) {
	// Find handler
	domainHandlers, ok := fc.commandHandlers[cmd.GetDomain()]
	if !ok {
		return nil, fmt.Errorf("no handlers registered for domain: %s", cmd.GetDomain())
	}

	handler, ok := domainHandlers[cmd.GetDomainCmdName()]
	if !ok {
		return nil, fmt.Errorf("no handler for command: %s.%s", cmd.GetDomain(), cmd.GetDomainCmdName())
	}

	// Wrap with middlewares
	wrappedFn := WithCommandHandlerMiddlewares(ctx, handler, fc.commandMiddlewares...)

	// Execute
	return wrappedFn(ctx, cmd, cmd.Payload)
}

// DispatchQuery dispatches a query through the middleware chain
func (fc *Facade) DispatchQuery(ctx context.Context, qry Query) (any, error) {
	// Find handler
	domainHandlers, ok := fc.queryHandlers[qry.GetDomain()]
	if !ok {
		return nil, fmt.Errorf("no handlers registered for domain: %s", qry.GetDomain())
	}

	handler, ok := domainHandlers[qry.GetDomainQryName()]
	if !ok {
		return nil, fmt.Errorf("no handler for query: %s.%s", qry.GetDomain(), qry.GetDomainQryName())
	}

	// Wrap with middlewares
	wrappedFn := WithQueryHandlerMiddlewares(ctx, handler, fc.queryMiddlewares...)

	// Execute
	return wrappedFn(ctx, qry, qry.Payload)
}

// --- Middleware Presets ---

// ProductionMiddlewares returns a standard production middleware stack
func ProductionMiddlewares() []CommandHandlerMiddlewareFunc {
	return []CommandHandlerMiddlewareFunc{
		RecoveryMiddleware,
		TracingMiddleware,
		LoggingMiddleware,
		MetricsMiddleware,
		AuthorizationMiddleware,
		ValidationMiddleware,
	}
}

// DevelopmentMiddlewares returns a development middleware stack
func DevelopmentMiddlewares() []CommandHandlerMiddlewareFunc {
	return []CommandHandlerMiddlewareFunc{
		RecoveryMiddleware,
		LoggingMiddleware,
		ValidationMiddleware,
	}
}

// TestMiddlewares returns a minimal test middleware stack
func TestMiddlewares() []CommandHandlerMiddlewareFunc {
	return []CommandHandlerMiddlewareFunc{
		ValidationMiddleware,
	}
}

// QueryProductionMiddlewares returns a production query middleware stack
func QueryProductionMiddlewares() []QueryHandlerMiddlewareFunc {
	return []QueryHandlerMiddlewareFunc{
		QueryLoggingMiddleware,
		QueryAuthorizationMiddleware,
	}
}
