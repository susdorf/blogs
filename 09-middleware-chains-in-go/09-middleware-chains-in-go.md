# Middleware Chains in Go

*This is Part 9 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

Every application needs cross-cutting concerns: logging, authentication, metrics, error handling, tracing. These behaviors cut across domain boundaries — they apply to virtually every operation regardless of which aggregate or domain it touches. Scattering this logic throughout your handlers creates duplication and makes changes painful. When your logging format needs to change, you'd have to update dozens of handlers. When you add a new metric, the same story repeats.

**Middleware chains** solve this elegantly by wrapping handlers with reusable decorators. Each middleware adds exactly one concern, and they compose cleanly into pipelines. The pattern is well-known from HTTP servers (think `net/http` middleware or frameworks like Chi and Echo), but it applies equally well to command and query handlers in a CQRS system.

The beauty of middleware lies in its composability. You can mix and match middlewares for different environments: full observability in production, verbose debugging in development, minimal overhead in tests. Handlers remain focused on business logic while infrastructure concerns live in their own, testable units.

## The Concept: Handler Wrapping

At its core, a middleware wraps a handler function, adding behavior before and/or after the actual execution. Think of it as an onion: each layer can inspect the request on the way in, delegate to the next layer, and then inspect (or modify) the response on the way out.

```go
// Original handler - pure business logic
func handleCommand(ctx context.Context, cmd Command) ([]Event, error) {
    // Business logic only, no logging, no metrics, no auth checks
    return events, nil
}

// Middleware adds logging around the handler
func withLogging(next HandlerFunc) HandlerFunc {
    return func(ctx context.Context, cmd Command) ([]Event, error) {
        // BEFORE: runs before the handler
        log.Printf("Handling command: %s", cmd.Type)

        events, err := next(ctx, cmd)  // Delegate to the wrapped handler

        // AFTER: runs after the handler completes
        if err != nil {
            log.Printf("Command failed: %v", err)
        } else {
            log.Printf("Command succeeded, %d events", len(events))
        }

        return events, err
    }
}
```

The middleware receives the "next" handler in the chain and returns a new handler that wraps it. This wrapping can happen at three points:

1. **Before execution**: Validate input, check permissions, start timers
2. **After execution**: Log results, record metrics, trigger side effects
3. **Around execution**: Using `defer` to ensure cleanup happens regardless of panics

## Defining the Types

The type definitions establish the contract for our middleware system. In Go, function types make this particularly elegant:

```go
// Handler function type - what actually processes commands
type DomainCommandHandlerFunc func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error)

// Middleware transforms a handler into a wrapped handler
type CommandHandlerMiddlewareFunc func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc
```

The key insight: a middleware takes a handler and returns a new handler with the same signature. This uniformity is what enables composition — since input and output types match, middlewares can be chained indefinitely.

Note that our middleware also receives a `context.Context`. This allows middlewares to access configuration or services that were set up during application initialization, making them more flexible than simple function wrappers.

## Building a Middleware Chain

The chain-building function is where the magic happens. Middlewares are applied in reverse order so they execute in the order you'd naturally read them:

```go
func WithCommandHandlerMiddlewares(
    ctx context.Context,
    fn DomainCommandHandlerFunc,
    middlewares ...CommandHandlerMiddlewareFunc,
) DomainCommandHandlerFunc {
    if len(middlewares) == 0 {
        return fn
    }

    wrapped := fn

    // Apply in reverse order
    // If middlewares = [A, B, C]
    // Result is: A(B(C(fn)))
    // Execution order: A -> B -> C -> fn -> C -> B -> A
    for i := len(middlewares) - 1; i >= 0; i-- {
        wrapped = middlewares[i](ctx, wrapped)
    }

    return wrapped
}
```

Why reverse order? This is a common source of confusion, so let's walk through it carefully. Consider this chain:

```go
chain := WithCommandHandlerMiddlewares(ctx, handler, logging, auth, metrics)
```

We want execution to flow like this:

1. `logging` (before) — start timer, log request
2. `auth` (before) — check permissions
3. `metrics` (before) — increment counter
4. `handler` — actual business logic
5. `metrics` (after) — record duration
6. `auth` (after) — maybe log authorization decision
7. `logging` (after) — log response, duration

By wrapping in reverse order, we build: `logging(auth(metrics(handler)))`. When called, `logging` executes first because it's the outermost function. It then calls `auth`, which calls `metrics`, which finally calls the handler. On the way back up, each middleware's "after" code runs in reverse order.

Think of it like Russian nesting dolls: the first middleware in your list becomes the outermost doll, and the handler is at the center. You open them from outside in, and close them from inside out.

## Common Middlewares

Let's look at the middlewares you'll find in almost every production system. Each one addresses a specific cross-cutting concern and can be used independently or composed together.

### Logging Middleware

Logging is typically the outermost middleware — it captures the full duration of request processing, including time spent in other middlewares. Structured logging with contextual fields makes log analysis much easier:

```go
func LoggingMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        start := time.Now()

        logger := Logger.With(
            "command", cmd.GetDomainCmdName(),
            "domain", cmd.GetDomain(),
            "cmdUuid", cmd.GetCmdUuid(),
        )

        logger.Debug("command started")

        events, err := next(ctx, cmd, domainCmd)

        duration := time.Since(start)

        if err != nil {
            logger.Error("command failed",
                "error", err,
                "duration", duration,
            )
        } else {
            logger.Info("command succeeded",
                "events", len(events),
                "duration", duration,
            )
        }

        return events, err
    }
}
```

### Metrics Middleware

Metrics middleware collects quantitative data about your system's behavior. This example uses Prometheus-style counters and histograms, but the pattern applies to any metrics backend. Place it close to the handler to measure actual business logic duration, excluding authentication overhead:

```go
func MetricsMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        start := time.Now()

        events, err := next(ctx, cmd, domainCmd)

        duration := time.Since(start)
        domain := cmd.GetDomain()
        cmdName := cmd.GetDomainCmdName()

        // Record metrics
        commandsTotal.WithLabelValues(domain, cmdName).Inc()
        commandDuration.WithLabelValues(domain, cmdName).Observe(duration.Seconds())

        if err != nil {
            commandErrors.WithLabelValues(domain, cmdName).Inc()
        } else {
            eventsProduced.WithLabelValues(domain).Add(float64(len(events)))
        }

        return events, err
    }
}
```

### Recovery Middleware

Recovery middleware catches panics and converts them to errors, preventing a single misbehaving handler from crashing your entire application. This should typically be the outermost middleware (or second only to logging) to ensure all panics are caught. The stack trace capture is invaluable for debugging:

```go
func RecoveryMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) (events []Event, err error) {
        defer func() {
            if r := recover(); r != nil {
                // Log stack trace
                stack := debug.Stack()
                Logger.Error("panic in command handler",
                    "panic", r,
                    "stack", string(stack),
                    "command", cmd.GetDomainCmdName(),
                )

                // Convert panic to error
                err = fmt.Errorf("internal error: %v", r)
                events = nil
            }
        }()

        return next(ctx, cmd, domainCmd)
    }
}
```

### Validation Middleware

Validation middleware ensures commands meet their contracts before reaching business logic. By centralizing validation, handlers can assume their input is well-formed. This example uses struct tag validation, but you could also implement custom validation logic:

```go
func ValidationMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        // Validate using struct tags
        if err := validator.Validate(domainCmd); err != nil {
            return nil, fmt.Errorf("validation failed: %w", err)
        }

        return next(ctx, cmd, domainCmd)
    }
}
```

### Tracing Middleware

Distributed tracing connects operations across service boundaries. This middleware creates spans that show up in tools like Jaeger or Zipkin, making it possible to follow a request through your entire system. The span attributes provide searchable metadata:

```go
func TracingMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        // Start span
        ctx, span := tracer.Start(ctx, "command."+cmd.GetDomainCmdName())
        defer span.End()

        // Add attributes
        span.SetAttributes(
            attribute.String("command.domain", cmd.GetDomain()),
            attribute.String("command.uuid", cmd.GetCmdUuid()),
        )

        events, err := next(ctx, cmd, domainCmd)

        if err != nil {
            span.RecordError(err)
            span.SetStatus(codes.Error, err.Error())
        } else {
            span.SetAttributes(attribute.Int("events.count", len(events)))
        }

        return events, err
    }
}
```

### Authorization Middleware

Authorization middleware enforces access control before commands reach handlers. It extracts identity information from context (typically set by an authentication layer) and checks permissions. Failed authorization should return early without executing the handler:

```go
func AuthorizationMiddleware(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
    return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        // Extract identity from context
        reqCtx := GetReqCtxFromContext(ctx)
        if reqCtx == nil {
            return nil, ErrUnauthorized
        }

        // Check permission
        permission := getRequiredPermission(cmd.GetDomain(), cmd.GetDomainCmdName())
        if !hasPermission(reqCtx.IdentityUuid, permission) {
            return nil, ErrForbidden
        }

        return next(ctx, cmd, domainCmd)
    }
}
```

## Middleware for Different Handler Types

In a CQRS architecture, commands and queries have fundamentally different characteristics. Commands modify state and produce events, while queries only read data. This distinction often requires different middleware configurations for each handler type.

### Query Handler Middleware

Queries typically need fewer middlewares than commands. There's no need for event publishing, saga coordination, or complex validation. However, authorization and caching become more important:

```go
// Query handler function type
type DomainQueryHandlerFunc func(ctx context.Context, qry Query, domainQry DomainQry) (any, error)

// Query middleware transforms a query handler
type QueryHandlerMiddlewareFunc func(ctx context.Context, fn DomainQueryHandlerFunc) DomainQueryHandlerFunc

// Apply query middlewares - same pattern as commands
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
```

A typical query middleware stack might look different from commands:

```go
func QueryMiddlewares() []QueryHandlerMiddlewareFunc {
    return []QueryHandlerMiddlewareFunc{
        QueryRecoveryMiddleware,      // Catch panics
        QueryTracingMiddleware,       // Distributed tracing
        QueryLoggingMiddleware,       // Request logging (often less verbose)
        QueryAuthorizationMiddleware, // Read permissions differ from write
        QueryCachingMiddleware,       // Queries benefit from caching
    }
}
```

### Event Handler Middleware

Event handlers process events after they've been persisted. They're used for projections, notifications, and triggering subsequent actions. Event middleware serves different purposes:

```go
// Event handler function type
type DomainEventHandlerFunc func(ctx context.Context, evt Event, domainEvt DomainEvt) error

// Event middleware transforms an event handler
type EventHandlerMiddlewareFunc func(ctx context.Context, fn DomainEventHandlerFunc) DomainEventHandlerFunc

func WithEventHandlerMiddlewares(
    ctx context.Context,
    fn DomainEventHandlerFunc,
    middlewares ...EventHandlerMiddlewareFunc,
) DomainEventHandlerFunc {
    if len(middlewares) == 0 {
        return fn
    }

    wrapped := fn
    for i := len(middlewares) - 1; i >= 0; i-- {
        wrapped = middlewares[i](ctx, wrapped)
    }

    return wrapped
}
```

Event middleware typically focuses on reliability and observability rather than authorization:

```go
func EventMiddlewares() []EventHandlerMiddlewareFunc {
    return []EventHandlerMiddlewareFunc{
        EventRecoveryMiddleware,  // Critical: don't let one handler crash others
        EventTracingMiddleware,   // Continue trace from command
        EventLoggingMiddleware,   // Track event processing
        EventRetryMiddleware,     // Retry transient failures
    }
}
```

### Symmetry in the Facade

The Facade manages all three middleware chains, providing a consistent API:

```go
type Facade struct {
    CommandHandlerMiddlewares []CommandHandlerMiddlewareFunc
    QueryHandlerMiddlewares   []QueryHandlerMiddlewareFunc
    EventHandlerMiddlewares   []EventHandlerMiddlewareFunc
    // ...
}

// Registration follows the same pattern for all types
func (fc *Facade) AddCommandHandlerMiddleware(m ...CommandHandlerMiddlewareFunc) error
func (fc *Facade) AddQueryHandlerMiddleware(m ...QueryHandlerMiddlewareFunc) error
func (fc *Facade) AddEventHandlerMiddleware(m ...EventHandlerMiddlewareFunc) error

// Retrieval for use during dispatch
func (fc *Facade) GetCommandHandlerMiddlewares() []CommandHandlerMiddlewareFunc
func (fc *Facade) GetQueryHandlerMiddlewares() []QueryHandlerMiddlewareFunc
func (fc *Facade) GetEventHandlerMiddlewares() []EventHandlerMiddlewareFunc
```

This symmetry makes the system predictable: developers know that any handler type supports the same middleware composition pattern.

## Middleware Factory Pattern

Real-world middlewares often need access to application services — the database for caching, the event store for webhooks, or the permission service for authorization. The factory pattern solves this elegantly:

```go
// Factory function creates middleware with access to services
func WebhookCommandHandlerFunc(fc FacadeInterface) CommandHandlerMiddlewareFunc {
    return func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc {
        return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
            // Execute the actual command handler
            events, err := fn(ctx, cmd, domainCmd)

            // On success, trigger webhooks using facade services
            if err == nil && len(events) > 0 {
                webhooks := fc.GetWebhooksForTenant(cmd.GetTenantUuid())
                for _, wh := range webhooks {
                    if wh.MatchesEventType(events) {
                        go dispatchWebhook(wh, events)
                    }
                }
            }

            return events, err
        }
    }
}

// Authorization middleware with permission service access
func AuthCommandHandlerFunc(fc FacadeInterface) CommandHandlerMiddlewareFunc {
    return func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc {
        return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
            reqCtx := GetReqCtxFromContext(ctx)

            // Use facade to check permissions
            permission := fc.GetRequiredPermission(cmd.GetDomain(), cmd.GetDomainCmdName())
            if !fc.HasPermission(reqCtx.IdentityUuid, permission) {
                return nil, ErrForbidden
            }

            return fn(ctx, cmd, domainCmd)
        }
    }
}
```

Registration uses these factories during application setup:

```go
func SetupFacade(fc *Facade) error {
    // Factories receive the facade and return the actual middleware
    if err := fc.AddCommandHandlerMiddleware(
        WebhookCommandHandlerFunc(fc),
    ); err != nil {
        return err
    }

    if err := fc.AddCommandHandlerMiddleware(
        AuthCommandHandlerFunc(fc),
    ); err != nil {
        return err
    }

    if err := fc.AddQueryHandlerMiddleware(
        AuthQueryHandlerFunc(fc),
    ); err != nil {
        return err
    }

    return nil
}
```

This pattern provides several benefits:

1. **Dependency injection**: Middleware receives dependencies at creation time, not execution time
2. **Testability**: Factories can receive mock facades during testing
3. **Flexibility**: Different facade implementations can change middleware behavior
4. **Encapsulation**: The middleware signature stays clean; service access is internal

## Post-Execution Side Effects

Some middleware needs to act *after* the handler completes successfully. Webhooks, notifications, and audit logging are classic examples. The pattern uses Go's natural control flow:

```go
func NotificationMiddleware(notifier NotificationService) CommandHandlerMiddlewareFunc {
    return func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc {
        return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
            // Execute handler first
            events, err := fn(ctx, cmd, domainCmd)

            // Only notify on success
            if err != nil {
                return events, err
            }

            // Post-execution: send notifications based on events
            for _, evt := range events {
                if subscription := notifier.GetSubscription(evt.GetType()); subscription != nil {
                    // Fire-and-forget or queue for reliable delivery
                    go notifier.Send(subscription, evt)
                }
            }

            return events, err
        }
    }
}
```

For critical side effects that must not be lost, consider using a defer with recovery:

```go
func AuditMiddleware(auditLog AuditService) CommandHandlerMiddlewareFunc {
    return func(ctx context.Context, fn DomainCommandHandlerFunc) DomainCommandHandlerFunc {
        return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
            startTime := time.Now()
            reqCtx := GetReqCtxFromContext(ctx)

            // Execute handler
            events, err := fn(ctx, cmd, domainCmd)

            // Always audit, even on failure
            defer func() {
                auditEntry := AuditEntry{
                    Timestamp:    startTime,
                    Duration:     time.Since(startTime),
                    IdentityUuid: reqCtx.IdentityUuid,
                    Command:      cmd.GetDomainCmdName(),
                    Success:      err == nil,
                    EventCount:   len(events),
                }

                if err != nil {
                    auditEntry.ErrorMessage = err.Error()
                }

                // Audit logging should never cause the command to fail
                if auditErr := auditLog.Record(auditEntry); auditErr != nil {
                    Logger.Error("audit logging failed", "error", auditErr)
                }
            }()

            return events, err
        }
    }
}
```

The key insight: post-execution middleware must decide whether to propagate the original error or potentially mask it. Generally, side effect failures should be logged but not returned to the caller — the primary operation succeeded, and that's what matters.

## Composing Middleware Chains

One of the most powerful aspects of middleware is the ability to compose different stacks for different contexts. A production environment needs full observability and security, while tests benefit from minimal overhead:

```go
// Production middleware stack - full observability and security
func ProductionMiddlewares() []CommandHandlerMiddlewareFunc {
    return []CommandHandlerMiddlewareFunc{
        RecoveryMiddleware,      // Outermost - catches panics from all inner layers
        TracingMiddleware,       // Distributed tracing for cross-service visibility
        LoggingMiddleware,       // Structured request logging
        MetricsMiddleware,       // Prometheus metrics for alerting and dashboards
        AuthorizationMiddleware, // Permission check - reject unauthorized early
        ValidationMiddleware,    // Input validation - reject invalid commands early
    }
}

// Development middleware stack - focus on debugging
func DevelopmentMiddlewares() []CommandHandlerMiddlewareFunc {
    return []CommandHandlerMiddlewareFunc{
        RecoveryMiddleware,        // Still need panic recovery
        VerboseLoggingMiddleware,  // Detailed logging with request/response bodies
        ValidationMiddleware,      // Catch validation errors during development
    }
}

// Test middleware stack - minimal overhead for fast tests
func TestMiddlewares() []CommandHandlerMiddlewareFunc {
    return []CommandHandlerMiddlewareFunc{
        ValidationMiddleware,  // Only validate - tests should verify behavior, not infrastructure
    }
}
```

The order matters here. Notice that `RecoveryMiddleware` is always first (outermost) — it needs to catch panics from any inner middleware. `AuthorizationMiddleware` comes before `ValidationMiddleware` because there's no point validating a command from an unauthorized user. These ordering decisions reflect your system's requirements and failure modes.

## Applying Middlewares in the Facade

The Facade serves as the central dispatcher for commands, and it's the natural place to apply middlewares. Rather than requiring each handler to manually set up its middleware chain, the Facade handles this transparently:

```go
type Facade struct {
    CommandMiddlewares []CommandHandlerMiddlewareFunc
    // ...
}

func (fc *Facade) DispatchCommand(ctx context.Context, cmd Command) ([]Event, error) {
    // Find the domain handler responsible for this command
    handler, err := fc.findCommandHandler(cmd.GetDomain())
    if err != nil {
        return nil, err
    }

    // Get the specific handler function for this command type
    handlerFn := handler.GetHandlerFunc(cmd.GetDomainCmdName())

    // Wrap the handler with all configured middlewares
    wrappedFn := WithCommandHandlerMiddlewares(ctx, handlerFn, fc.CommandMiddlewares...)

    // Execute the wrapped handler - middlewares run automatically
    return wrappedFn(ctx, cmd, cmd.GetDomainCmd())
}
```

This design has an important property: middleware wrapping happens at dispatch time, not at registration time. This means you can add or modify middlewares dynamically, and all subsequently dispatched commands will use the updated chain. It also means the same handler can be wrapped differently depending on context (though in practice, most systems use a consistent middleware stack).

## Conditional Middlewares

Sometimes you need middleware that only applies under certain conditions. Perhaps verbose logging should only activate for specific domains, or rate limiting should skip internal service calls. A higher-order middleware can wrap any middleware with a condition:

```go
func ConditionalMiddleware(
    condition func(ctx context.Context, cmd Command) bool,
    middleware CommandHandlerMiddlewareFunc,
) CommandHandlerMiddlewareFunc {
    return func(ctx context.Context, next DomainCommandHandlerFunc) DomainCommandHandlerFunc {
        return func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
            if condition(ctx, cmd) {
                // Condition met: apply the middleware
                wrapped := middleware(ctx, next)
                return wrapped(ctx, cmd, domainCmd)
            }
            // Condition not met: skip directly to next handler
            return next(ctx, cmd, domainCmd)
        }
    }
}

// Usage: only log for specific domain
loggingForOrders := ConditionalMiddleware(
    func(ctx context.Context, cmd Command) bool {
        return cmd.GetDomain() == "Order"
    },
    LoggingMiddleware,
)

// Usage: skip rate limiting for internal calls
rateLimitForExternal := ConditionalMiddleware(
    func(ctx context.Context, cmd Command) bool {
        reqCtx := GetReqCtxFromContext(ctx)
        return reqCtx != nil && !reqCtx.IsInternalCall
    },
    RateLimitMiddleware,
)
```

This pattern keeps individual middlewares simple and focused, while providing flexible composition at the configuration level.

## Domain-Specific Middlewares

While global middlewares apply to all commands, some concerns are domain-specific. A payment domain might need PCI compliance logging, while a messaging domain needs spam filtering. Rather than cluttering global middleware with domain-specific logic, configure middlewares per domain:

```go
type DomainMiddlewareConfig struct {
    Global   []CommandHandlerMiddlewareFunc            // Applied to all commands
    ByDomain map[string][]CommandHandlerMiddlewareFunc // Applied after global, per domain
}

func (fc *Facade) GetMiddlewaresForDomain(domain string) []CommandHandlerMiddlewareFunc {
    var result []CommandHandlerMiddlewareFunc

    // Global middlewares first - these always run
    result = append(result, fc.MiddlewareConfig.Global...)

    // Domain-specific middlewares - appended after global ones
    if domainMiddlewares, ok := fc.MiddlewareConfig.ByDomain[domain]; ok {
        result = append(result, domainMiddlewares...)
    }

    return result
}
```

Usage in configuration:

```go
config := DomainMiddlewareConfig{
    Global: []CommandHandlerMiddlewareFunc{
        RecoveryMiddleware,
        LoggingMiddleware,
        AuthorizationMiddleware,
    },
    ByDomain: map[string][]CommandHandlerMiddlewareFunc{
        "Payment": {
            PCIComplianceMiddleware,  // Log all payment operations for audit
            FraudDetectionMiddleware, // Check for suspicious patterns
        },
        "Messaging": {
            SpamFilterMiddleware,     // Filter spam before processing
            RateLimitMiddleware,      // Prevent message flooding
        },
    },
}
```

This approach keeps the global configuration clean while allowing domains to extend it with their specific requirements.

## Testing with Middlewares

One of the significant benefits of the middleware pattern is testability. Each middleware can be tested in isolation, verifying that it correctly wraps handlers and applies its logic. You don't need the full application context — just a mock handler and the middleware under test:

```go
func TestLoggingMiddleware(t *testing.T) {
    // Track if handler was called
    called := false
    innerHandler := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        called = true
        return []Event{NewEvent()}, nil
    }

    // Apply middleware
    wrapped := LoggingMiddleware(context.Background(), innerHandler)

    // Execute
    cmd := NewTestCommand()
    events, err := wrapped(context.Background(), cmd, nil)

    // Verify
    assert.NoError(t, err)
    assert.True(t, called)
    assert.Len(t, events, 1)
    // Could also capture and verify log output
}

func TestRecoveryMiddleware(t *testing.T) {
    // Handler that panics
    panickingHandler := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        panic("something went wrong")
    }

    // Apply recovery middleware
    wrapped := RecoveryMiddleware(context.Background(), panickingHandler)

    // Should not panic, should return error
    events, err := wrapped(context.Background(), NewTestCommand(), nil)

    assert.Error(t, err)
    assert.Contains(t, err.Error(), "internal error")
    assert.Nil(t, events)
}
```

## Performance Considerations

Each middleware adds a function call overhead, which in Go is minimal but not zero. For high-throughput systems processing thousands of commands per second, these considerations become relevant:

1. **Order matters for latency perception**: Put fast middlewares before slow ones. If logging takes 1ms and authentication takes 50ms, putting logging first means you start recording before the slow operation. More importantly, putting authorization early rejects unauthorized requests before wasting time on validation or business logic.

2. **Avoid allocations in hot paths**: Logging middleware that creates new string builders or maps for every request will cause GC pressure. Reuse buffers or use structured logging that minimizes allocations.

3. **Use conditional middlewares judiciously**: Verbose debugging middleware can be wrapped in conditions that only activate during development or when a debug flag is set.

4. **Measure actual impact**: Profile your middleware chain to find real bottlenecks. The function call overhead is almost never the problem — it's usually what the middleware does (database lookups for auth, network calls for metrics) that matters.

```go
// Benchmark middleware overhead
func BenchmarkMiddlewareChain(b *testing.B) {
    handler := func(ctx context.Context, cmd Command, domainCmd DomainCmd) ([]Event, error) {
        return nil, nil
    }

    wrapped := WithCommandHandlerMiddlewares(
        context.Background(),
        handler,
        LoggingMiddleware,
        MetricsMiddleware,
        ValidationMiddleware,
    )

    cmd := NewTestCommand()
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        wrapped(ctx, cmd, nil)
    }
}
```

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/09-middleware-chains-in-go/code

When you run the complete example, you'll see middleware chains in action across various scenarios:

```
=== Middleware Chains Demo ===

1. Basic Middleware Chain (manual application):
   Applying [Logging, Validation] middlewares to a simple handler

    [LOG] Starting command: CreateOrder (domain: orders)
    [VALIDATION] Command CreateOrder passed validation
    [HANDLER] Executing business logic...
    [LOG] Command succeeded: CreateOrder (duration: 46.916µs, events: 1)

   Result: 1 events, error: <nil>

─────────────────────────────────────────────────────

2. Middleware Execution Order:
   Chain: [Recovery, Tracing, Logging, Metrics, Validation]
   Watch how they execute in order and 'unwind' in reverse

    [TRACE] Starting span: command=CreateOrder trace_id=trace-123 span_id=span-456
    [LOG] Starting command: CreateOrder (domain: orders)
    [VALIDATION] Command CreateOrder passed validation
    [HANDLER] Executing business logic...
    [METRICS] command=CreateOrder domain=orders status=success duration_ms=0.00 events=1
    [LOG] Command succeeded: CreateOrder (duration: 13.5µs, events: 1)
    [TRACE] Ending span: trace_id=trace-123 status=OK

─────────────────────────────────────────────────────

3. Authorization Middleware:

   3a. Request WITHOUT authorization context:
    [LOG] Starting command: CreateOrder (domain: orders)
    [LOG] Command failed: CreateOrder (error: unauthorized: no request context)
   Result: error = unauthorized: no request context

   3b. Request WITH valid authorization (admin role):
    [LOG] Starting command: CreateOrder (domain: orders)
    [AUTH] Authorized: user user-123 for orders:CreateOrder
    [HANDLER] Executing business logic...
    [LOG] Command succeeded: CreateOrder (duration: 2.25µs, events: 1)
   Result: 1 events, error: <nil>

   3c. Request with INSUFFICIENT permissions:
    [LOG] Starting command: CreateOrder (domain: orders)
    [AUTH] Denied: user user-456 lacks permission for orders:CreateOrder
    [LOG] Command failed: CreateOrder (error: forbidden: insufficient permissions)
   Result: error = forbidden: insufficient permissions

─────────────────────────────────────────────────────

4. Recovery Middleware (panic handling):

    [LOG] Starting command: CreateOrder (domain: orders)
    [RECOVERY] Panic caught in CreateOrder: something went terribly wrong!
    [RECOVERY] Stack trace: ...
   Result: events = [], error = internal error: something went terribly wrong!
   (Notice: the panic was caught and converted to an error)

─────────────────────────────────────────────────────

5. Facade with Middleware Stack:

   5a. Dispatching command through Facade:
    [TRACE] Starting span: command=CreateOrder trace_id=trace-789 span_id=span-012
    [LOG] Starting command: CreateOrder (domain: orders)
    [AUTH] Authorized: user user-789 for orders:CreateOrder
    [VALIDATION] Command CreateOrder passed validation
    [METRICS] command=CreateOrder domain=orders status=success duration_ms=0.00 events=2
    [LOG] Command succeeded: CreateOrder (duration: 5.125µs, events: 2)
    [TRACE] Ending span: trace_id=trace-789 status=OK

   Result: 2 events produced

   5b. Dispatching query through Facade:
    [LOG] Starting query: GetOrder (domain: orders)
    [AUTH] Query authorized: user user-789 for orders
    [LOG] Query succeeded: GetOrder (duration: 1.292µs)

   Result: map[orderId:order-123 status:pending]

─────────────────────────────────────────────────────

6. Conditional Middleware:
   Metrics only for 'orders' domain

   6a. Command for 'orders' domain (metrics WILL run):
    [LOG] Starting command: CreateOrder (domain: orders)
    [HANDLER] Executing business logic...
    [METRICS] command=CreateOrder domain=orders status=success duration_ms=0.00 events=1
    [LOG] Command succeeded: CreateOrder (duration: 2.042µs, events: 1)

   6b. Command for 'inventory' domain (metrics WON'T run):
    [LOG] Starting command: UpdateStock (domain: inventory)
    [HANDLER] Executing business logic...
    [LOG] Command succeeded: UpdateStock (duration: 1.084µs, events: 1)

─────────────────────────────────────────────────────

7. Environment-Specific Middleware Stacks:

   Production stack: 6 middlewares
   [Recovery, Tracing, Logging, Metrics, Authorization, Validation]

   Development stack: 3 middlewares
   [Recovery, Logging, Validation]

   Test stack: 1 middlewares
   [Validation]

─────────────────────────────────────────────────────

8. Validation Middleware (early rejection):

    [LOG] Starting command: CreateOrder (domain: )
    [LOG] Command failed: CreateOrder (error: validation failed: domain is required)
   Invalid command result: error = validation failed: domain is required

=== Demo Complete ===
```

## Summary

Middleware chains are a fundamental pattern for managing cross-cutting concerns in any handler-based system. They bring several key benefits:

1. **Separation of concerns**: Each middleware does exactly one thing. Logging middleware logs, auth middleware authorizes, metrics middleware records metrics. No mixing of responsibilities.

2. **Reusability**: The same middleware can wrap command handlers, query handlers, and event handlers (with appropriate type signatures). Write once, use everywhere.

3. **Composability**: Mix and match middlewares for different environments. Production gets full observability; tests get minimal overhead. Domain-specific middlewares extend the global chain without modifying it.

4. **Testability**: Test each middleware in isolation with mock handlers. Test your handlers without middleware overhead. Test the full chain in integration tests.

5. **Flexibility**: Add new cross-cutting concerns by writing a new middleware and adding it to the chain. Remove concerns by removing the middleware. No handler code changes required.

The implementation pattern is straightforward:

1. Define your handler function type with a consistent signature
2. Define your middleware type as a function that takes and returns a handler
3. Apply middlewares in reverse order so they execute in reading order
4. Execute the wrapped handler — all middlewares run automatically

For CQRS systems, remember that commands, queries, and events may need different middleware stacks. Commands need authorization and validation; queries benefit from caching; events need reliable delivery guarantees. The middleware pattern accommodates all these variations through composition.

---

## What's Next

In the next post, we'll explore **Projections** — building optimized read models from event streams, the "Query" side of CQRS.


