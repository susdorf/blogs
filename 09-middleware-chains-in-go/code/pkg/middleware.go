package pkg

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"
)

// DomainCommandHandlerFunc is the type for command handlers
type DomainCommandHandlerFunc func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error)

// CommandHandlerMiddlewareFunc transforms a handler into a wrapped handler
type CommandHandlerMiddlewareFunc func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc

// DomainQueryHandlerFunc is the type for query handlers
type DomainQueryHandlerFunc func(ctx context.Context, qry Query, domainQry DomainQry) (any, error)

// QueryHandlerMiddlewareFunc transforms a query handler into a wrapped handler
type QueryHandlerMiddlewareFunc func(ctx context.Context, fn DomainQueryHandlerFunc) DomainQueryHandlerFunc

// WithCommandHandlerMiddlewares applies middlewares to a command handler
func WithCommandHandlerMiddlewares(
	ctx context.Context,
	fn DomainCommandHandlerFunc,
	middlewares ...CommandHandlerMiddlewareFunc,
) DomainCommandHandlerFunc {
	if len(middlewares) == 0 {
		return fn
	}

	wrapped := fn

	// Apply in reverse order so first middleware in list is outermost
	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](ctx, wrapped)
	}

	return wrapped
}

// WithQueryHandlerMiddlewares applies middlewares to a query handler
func WithQueryHandlerMiddlewares(
	ctx context.Context,
	fn DomainQueryHandlerFunc,
	middlewares ...QueryHandlerMiddlewareFunc,
) DomainQueryHandlerFunc {
	if len(middlewares) == 0 {
		return fn
	}

	wrapped := fn

	for i := len(middlewares) - 1; i >= 0; i-- {
		wrapped = middlewares[i](ctx, wrapped)
	}

	return wrapped
}

// --- Command Handler Middlewares ---

// LoggingMiddleware logs command execution
func LoggingMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		start := time.Now()

		fmt.Printf("    [LOG] Starting command: %s (domain: %s)\n", cmd.GetDomainCmdName(), cmd.GetDomain())

		events, err := next(ctx, cmd, domainCmd)

		duration := time.Since(start)
		if err != nil {
			fmt.Printf("    [LOG] Command failed: %s (duration: %v, error: %v)\n",
				cmd.GetDomainCmdName(), duration, err)
		} else {
			fmt.Printf("    [LOG] Command succeeded: %s (duration: %v, events: %d)\n",
				cmd.GetDomainCmdName(), duration, len(events))
		}

		return events, err
	}
}

// MetricsMiddleware records command metrics
func MetricsMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		start := time.Now()

		events, err := next(ctx, cmd, domainCmd)

		duration := time.Since(start)
		status := "success"
		if err != nil {
			status = "error"
		}

		fmt.Printf("    [METRICS] command=%s domain=%s status=%s duration_ms=%.2f events=%d\n",
			cmd.GetDomainCmdName(), cmd.GetDomain(), status, float64(duration.Microseconds())/1000, len(events))

		return events, err
	}
}

// RecoveryMiddleware catches panics and converts them to errors
func RecoveryMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) (events []Event, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				fmt.Printf("    [RECOVERY] Panic caught in %s: %v\n", cmd.GetDomainCmdName(), r)
				fmt.Printf("    [RECOVERY] Stack trace:\n%s\n", string(stack))
				err = fmt.Errorf("internal error: %v", r)
				events = nil
			}
		}()

		return next(ctx, cmd, domainCmd)
	}
}

// ValidationMiddleware validates command input
func ValidationMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		// Simple validation: check required fields
		if cmd.GetDomain() == "" {
			return nil, fmt.Errorf("validation failed: domain is required")
		}
		if cmd.GetDomainCmdName() == "" {
			return nil, fmt.Errorf("validation failed: command name is required")
		}

		fmt.Printf("    [VALIDATION] Command %s passed validation\n", cmd.GetDomainCmdName())
		return next(ctx, cmd, domainCmd)
	}
}

// AuthorizationMiddleware checks permissions
func AuthorizationMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		reqCtx := GetReqCtxFromContext(ctx)
		if reqCtx == nil {
			return nil, fmt.Errorf("unauthorized: no request context")
		}

		// Simple role check: admin can do anything, others need domain permission
		hasPermission := false
		for _, role := range reqCtx.Roles {
			if role == "admin" || role == cmd.GetDomain()+":write" {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			fmt.Printf("    [AUTH] Denied: user %s lacks permission for %s:%s\n",
				reqCtx.IdentityUuid, cmd.GetDomain(), cmd.GetDomainCmdName())
			return nil, fmt.Errorf("forbidden: insufficient permissions")
		}

		fmt.Printf("    [AUTH] Authorized: user %s for %s:%s\n",
			reqCtx.IdentityUuid, cmd.GetDomain(), cmd.GetDomainCmdName())
		return next(ctx, cmd, domainCmd)
	}
}

// TracingMiddleware adds distributed tracing
func TracingMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
	return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
		traceID := fmt.Sprintf("trace-%d", time.Now().UnixNano())
		spanID := fmt.Sprintf("span-%d", time.Now().UnixNano())

		fmt.Printf("    [TRACE] Starting span: command=%s trace_id=%s span_id=%s\n",
			cmd.GetDomainCmdName(), traceID, spanID)

		events, err := next(ctx, cmd, domainCmd)

		status := "OK"
		if err != nil {
			status = "ERROR"
		}
		fmt.Printf("    [TRACE] Ending span: trace_id=%s status=%s\n", traceID, status)

		return events, err
	}
}

// --- Conditional Middleware ---

// ConditionalMiddleware wraps a middleware with a condition
func ConditionalMiddleware(
	condition func(ctx context.Context, cmd Command) bool,
	middleware CommandHandlerMiddlewareFunc,
) CommandHandlerMiddlewareFunc {
	return func(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
		return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
			if condition(ctx, cmd) {
				wrapped := middleware(ctx, next)
				return wrapped(ctx, cmd, domainCmd)
			}
			return next(ctx, cmd, domainCmd)
		}
	}
}

// --- Query Handler Middlewares ---

// QueryLoggingMiddleware logs query execution
func QueryLoggingMiddleware(ctx context.Context, next DomainQueryHandlerFunc) DomainQueryHandlerFunc {
	return func(ctx context.Context, qry Query, domainQry DomainQry) (any, error) {
		start := time.Now()

		fmt.Printf("    [LOG] Starting query: %s (domain: %s)\n", qry.GetDomainQryName(), qry.GetDomain())

		result, err := next(ctx, qry, domainQry)

		duration := time.Since(start)
		if err != nil {
			fmt.Printf("    [LOG] Query failed: %s (duration: %v)\n", qry.GetDomainQryName(), duration)
		} else {
			fmt.Printf("    [LOG] Query succeeded: %s (duration: %v)\n", qry.GetDomainQryName(), duration)
		}

		return result, err
	}
}

// QueryAuthorizationMiddleware checks read permissions
func QueryAuthorizationMiddleware(ctx context.Context, next DomainQueryHandlerFunc) DomainQueryHandlerFunc {
	return func(ctx context.Context, qry Query, domainQry DomainQry) (any, error) {
		reqCtx := GetReqCtxFromContext(ctx)
		if reqCtx == nil {
			return nil, fmt.Errorf("unauthorized: no request context")
		}

		// Check for read permission
		hasPermission := false
		for _, role := range reqCtx.Roles {
			if role == "admin" || role == qry.GetDomain()+":read" {
				hasPermission = true
				break
			}
		}

		if !hasPermission {
			fmt.Printf("    [AUTH] Query denied: user %s lacks read permission for %s\n",
				reqCtx.IdentityUuid, qry.GetDomain())
			return nil, fmt.Errorf("forbidden: insufficient permissions")
		}

		fmt.Printf("    [AUTH] Query authorized: user %s for %s\n",
			reqCtx.IdentityUuid, qry.GetDomain())
		return next(ctx, qry, domainQry)
	}
}
