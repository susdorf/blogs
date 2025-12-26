# Async Command Completion with Hooks

*This is Part 11 of the "Building Event-Sourced Systems in Go" series. This series originated from the development of comby – an application framework developed using the principles of Event Sourcing and Command Query Responsibility Segregation (CQRS) - which ist still closed source today. Code with concepts and techniques is presented, but unlike comby, it is not always suitable for product use.*

---

## Introduction

In event-sourced systems, commands are often processed asynchronously. A client dispatches a command, but when can you tell the user "your order was placed"? The command might still be sitting in a queue, actively being processed, or already complete — and from the caller's perspective, there's no obvious way to know which.

This creates a fundamental tension. Event sourcing naturally leads to async processing: commands produce events, events get persisted, projections get updated. Each step can happen independently, potentially on different machines. But HTTP APIs expect synchronous responses. The client sends a request and waits for an answer. Returning "we received your request" isn't satisfying when the user wants to see their newly created order.

**Hooks** solve this by providing a synchronization mechanism that bridges async command processing with synchronous API responses. They allow callers to optionally wait for a command to complete, while preserving the async-first architecture internally.

## The Problem: Async Commands

Consider a typical flow in a REST API backed by an event-sourced system:

```go
// API handler
func (h *Handler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    cmd := &PlaceOrderCommand{OrderID: uuid.New().String()}

    // Dispatch command - returns immediately
    fc.DispatchCommand(r.Context(), cmd)

    // But is the order created? We don't know yet!
    // The command might still be in the queue...

    // Can we return the order to the user?
    json.NewEncoder(w).Encode(map[string]string{"id": cmd.OrderID})
}
```

The client receives the order ID, but the order might not exist yet in the read model. If they immediately query for it, they might get "not found". This leads to awkward client-side polling, retry logic, or confusing user experiences where newly created resources appear to be missing.

The naive solution — making everything synchronous — throws away the benefits of event sourcing. You'd lose the ability to process commands on different nodes, the natural decoupling between write and read sides, and the scalability that comes from async processing.

What we need is a way to optionally wait for completion when the caller requires it, while keeping the async architecture intact.

## The Request Context: Controlling Execution

Before diving into hooks, we need to understand how a command signals that it wants to wait for completion. This is controlled through the `RequestContext`, which carries metadata about command execution:

```go
type RequestContext struct {
    // Sender information
    SenderTenantUuid   string
    SenderIdentityUuid string
    SenderAccountUuid  string
    SenderSessionUuid  string

    // Target information
    TargetTenantUuid      string
    TargetWorkspaceUuid   string
    TargetAggregateUuid   string
    TargetAggregateVersion int64

    // Execution control
    ExecuteTimeout      int64  // Timeout in seconds
    ExecuteWaitToFinish bool   // Enable hook waiting

    // Tracing
    TraceId int64

    // Custom attributes
    Attributes *Attributes
}
```

The key field is `ExecuteWaitToFinish`. When set to `true`, the facade registers a hook for this command, allowing the caller to wait for its completion. When `false` (the default), the command is dispatched fire-and-forget style.

This explicit opt-in design is intentional. Most internal commands don't need synchronous completion — only API-facing operations where a client is actively waiting. By making it opt-in, we avoid the overhead of hook management for the majority of commands.

```go
// Creating a command that waits for completion
cmd, _ := NewCommand("Order", &PlaceOrderCommand{...})
cmd.GetReqCtx().ExecuteWaitToFinish = true

// Now dispatch and wait
fc.DispatchCommand(ctx, cmd)
fc.WaitForCmd(ctx, cmd)  // Blocks until complete
```

## The Solution: Command Hooks

A Hook is a synchronization primitive that allows waiting for a specific command to complete. It's essentially a rendezvous point where the command processor signals completion and the waiting caller receives the result:

```go
type Hook struct {
    mu         sync.Mutex
    CmdUuid    string
    Ch         chan HookResult
    ReceiverCh chan struct{}  // Signals receiver is ready
    Closed     bool
    ChResult   HookResult
}

type HookResult struct {
    Success bool
    Error   error
    Events  []Event
}

func NewHook(cmdUuid string) *Hook {
    return &Hook{
        CmdUuid:    cmdUuid,
        Ch:         make(chan HookResult, 1),
        ReceiverCh: make(chan struct{}, 1),
        Closed:     false,
    }
}
```

The design uses two channels:
- `Ch` carries the actual result (success/failure, any error, produced events)
- `ReceiverCh` signals that someone is actively waiting for the result

The `ReceiverCh` exists to handle a subtle race condition: what if the command completes before anyone calls `WaitForCmd`? Without coordination, the sender might block forever trying to send a result that nobody will receive. With `ReceiverCh`, the sender knows when it's safe to send.

## The Hook Registry

Hooks are tracked centrally in a registry, allowing the command processor to find and notify the appropriate hook when a command completes:

```go
type HookRegistry struct {
    hooks sync.Map  // cmdUuid -> *Hook
}

func (r *HookRegistry) Register(cmdUuid string) *Hook {
    hook := NewHook(cmdUuid)
    r.hooks.Store(cmdUuid, hook)
    return hook
}

func (r *HookRegistry) Get(cmdUuid string) *Hook {
    if h, ok := r.hooks.Load(cmdUuid); ok {
        return h.(*Hook)
    }
    return nil
}

func (r *HookRegistry) Remove(cmdUuid string) {
    r.hooks.Delete(cmdUuid)
}
```

Using `sync.Map` provides thread-safe access without explicit locking, which is important since hooks are accessed from multiple goroutines: the API handler registering and waiting, and the command processor completing.

## Waiting for Completion

The facade provides a `WaitForCmd` method that blocks until the command completes or the context expires:

```go
func (fc *Facade) WaitForCmd(ctx context.Context, cmd Command) error {
    cmdUuid := cmd.GetCmdUuid()

    // Get or create hook
    hook := fc.hookRegistry.Get(cmdUuid)
    if hook == nil {
        // Command might have completed before we could wait
        // Check if it's already in command store
        if fc.isCommandComplete(ctx, cmdUuid) {
            return nil
        }
        return errors.New("command not found")
    }

    // Signal that we're ready to receive
    select {
    case hook.ReceiverCh <- struct{}{}:
    default:
    }

    // Wait for result or timeout
    select {
    case result := <-hook.Ch:
        return result.Error
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

The method first checks if a hook exists. If not, the command might have already completed (a race condition that's actually fine — the command succeeded). The `ReceiverCh` signal tells the sender "I'm here, ready to receive". Then we wait on either the result channel or context cancellation.

This pattern ensures we never wait indefinitely. The context carries a deadline (set by the API handler), and if that deadline passes, we return gracefully rather than blocking forever.

## Completing Hooks

When command processing finishes — whether successfully or with an error — the hook is notified:

```go
func (fc *Facade) completeCommand(ctx context.Context, cmd Command, events []Event, err error) {
    cmdUuid := cmd.GetCmdUuid()

    hook := fc.hookRegistry.Get(cmdUuid)
    if hook == nil {
        return
    }

    result := HookResult{
        Success: err == nil,
        Error:   err,
        Events:  events,
    }

    hook.Close(ctx, &result)
    fc.hookRegistry.Remove(cmdUuid)
}
```

This is called from the command processing pipeline after events have been persisted. The hook result includes whether the command succeeded, any error that occurred, and the events that were produced. Callers waiting on `WaitForCmd` receive this information and can act accordingly.

## Careful Synchronization

The trickiest part of hook implementation is avoiding goroutine leaks and deadlocks. The hook must handle several scenarios:
1. Sender completing before receiver is ready
2. Receiver timing out before sender completes
3. Multiple senders (shouldn't happen, but defensive coding)
4. Hook being closed multiple times

```go
func (h *Hook) Close(ctx context.Context, result *HookResult) {
    h.mu.Lock()
    defer h.mu.Unlock()

    if h.Closed {
        return
    }

    // Store result for late receivers
    if result != nil {
        h.ChResult = *result
    }

    // Try to send result
    if h.Ch != nil && result != nil {
        done := false
        for !done {
            select {
            case <-h.ReceiverCh:
                // Receiver is ready
                select {
                case h.Ch <- *result:
                    done = true
                default:
                    // Channel full, wait a bit
                    time.Sleep(1 * time.Millisecond)
                }
            case <-ctx.Done():
                // Context cancelled, give up
                done = true
            default:
                // No receiver yet, wait a bit
                time.Sleep(1 * time.Millisecond)
            }
        }
    }

    h.Closed = true
    if h.Ch != nil {
        close(h.Ch)
    }
}
```

The loop waits for `ReceiverCh` before attempting to send on `Ch`. This coordination ensures the sender doesn't block indefinitely if no one is waiting. The context timeout provides an escape hatch — if the caller has given up, we shouldn't keep trying to send.

Storing the result in `ChResult` handles the case where a receiver arrives late (after `Close` was called but before the hook was removed from the registry). They can check `Closed` and read `ChResult` directly.

## Error Propagation Through Hooks

When a command fails — whether during command handling or event processing — the error needs to reach the waiting caller. The facade handles this through dedicated error handlers:

```go
func (fc *Facade) errorHandlerCmd(ctx context.Context, cmd Command, err error) error {
    // Cache error for retrieval by other mechanisms
    if fc.CacheStore != nil {
        key := fmt.Sprintf("%s-%s", cmd.GetTenantUuid(), cmd.GetCommandUuid())
        fc.CacheStore.Set(ctx, CacheStoreSetOptionWithKeyValue(key, err.Error()))
    }

    // Notify local hook (this instance)
    if h, ok := fc.LocalHooks.Load(cmd.GetCommandUuid()); ok {
        h.Error(ctx, err.Error())  // Send error result
        fc.unsubscribeHook(ctx, cmd.GetCommandUuid())
    }

    // Notify other instances via broker (distributed systems)
    if fc.Broker != nil && cmd.GetReqCtx().ExecuteWaitToFinish {
        hookResult := &HookResult{Error: err.Error()}
        hookResultBytes := Serialize(hookResult)
        fc.Broker.PublishHook(cmd.GetCommandUuid(), hookResultBytes)
    }

    return err
}
```

Error propagation happens at multiple levels:
1. **Cache**: The error is stored for retrieval by other mechanisms (useful for status queries)
2. **Local hook**: If the waiting caller is on the same instance, notify directly
3. **Broker**: In distributed deployments, publish the error so other instances can notify their waiting callers

Similarly, event handler failures propagate back:

```go
func (fc *Facade) errorHandlerEvt(ctx context.Context, evt Event, err error) error {
    // Notify hook about event handler failure
    if h, ok := fc.LocalHooks.Load(evt.GetCommandUuid()); ok {
        h.Error(ctx, err.Error())
        fc.unsubscribeHook(ctx, evt.GetCommandUuid())
    }
    return err
}
```

This ensures that even if a projection update fails, the waiting caller learns about it rather than waiting forever.

## Event Bulk Processing

Events produced by a command are processed together as a bulk, ensuring they're handled atomically:

```go
type BusEventBulk struct {
    CommandUuid string
    Bulk        [][]byte  // Serialized events
}

func (fc *Facade) processEventBulk(eventBulk BusEventBulk) error {
    ctx, cancel := context.WithTimeout(context.Background(), fc.DefaultTimeout)
    defer cancel()

    // Execute events in bulk sequentially
    for _, evtBytes := range eventBulk.Bulk {
        if err := fc.executeEventBytes(ctx, evtBytes); err != nil {
            return err
        }
    }

    // Notify hook - command completed successfully
    if h, ok := fc.LocalHooks.Load(eventBulk.CommandUuid); ok {
        h.Done(ctx)  // Send "done" result
        fc.unsubscribeHook(ctx, eventBulk.CommandUuid)
    }

    return nil
}
```

The `BusEventBulk` groups all events from a single command together. This is important for two reasons:

1. **Ordering**: Events from a command must be processed in order. By bundling them, we ensure `OrderPlaced` is processed before `OrderConfirmed`.

2. **Hook notification**: We only notify the hook after *all* events have been processed. If we notified after each event, the caller might query the read model before projections from later events are applied.

## Aggregate Locking

When processing commands that target a specific aggregate, we need to prevent concurrent modifications that could lead to event ordering issues or version conflicts:

```go
func (fc *Facade) executeCommand(ctx context.Context, cmd Command) (int, error) {
    reqCtx := cmd.GetReqCtx()

    // Lock aggregate to prevent concurrent updates
    if err := ValidateUuid(reqCtx.TargetAggregateUuid); err == nil {
        fc.lockAggregate(ctx, reqCtx.TargetAggregateUuid)
        defer fc.unlockAggregate(ctx, reqCtx.TargetAggregateUuid)
    }

    // ... rest of command execution
}

func (fc *Facade) lockAggregate(ctx context.Context, aggregateUuid string) {
    lock := make(chan struct{}, 1)
    actual, loaded := fc.aggregateLocks.LoadOrStore(aggregateUuid, lock)
    if loaded {
        // Another goroutine has the lock, wait for it
        <-actual.(chan struct{})
    }
}

func (fc *Facade) unlockAggregate(ctx context.Context, aggregateUuid string) {
    if lock, ok := fc.aggregateLocks.Load(aggregateUuid); ok {
        select {
        case lock.(chan struct{}) <- struct{}{}:
        default:
        }
        fc.aggregateLocks.Delete(aggregateUuid)
    }
}
```

This lock is held only for the duration of command execution — loading the aggregate, validating the command, and producing events. Once events are persisted, the lock is released. This keeps the critical section small while preventing issues like:

- Two commands modifying the same aggregate simultaneously
- Event version conflicts when both try to persist
- Inconsistent aggregate state from interleaved updates

The lock is per-aggregate, so commands targeting different aggregates can execute concurrently.

## Middleware Integration

Hooks integrate naturally with the middleware system covered in [Part 9: Middleware Chains](/09-middleware-chains-in-go). The facade applies command handler middlewares before executing the actual handler, and any middleware can access the request context to check if completion waiting is enabled:

```go
func (fc *Facade) executeCommand(ctx context.Context, cmd Command) (int, error) {
    // Get configured middlewares
    middlewares := fc.GetCommandHandlerMiddlewares()

    // Find the domain command handler
    handlerFn := fc.findHandler(cmd)

    // Wrap handler with middlewares
    wrapped := WithCommandHandlerMiddlewares(ctx, handlerFn, middlewares...)

    // Execute - middlewares run in order
    events, err := wrapped(ctx, cmd, cmd.GetDomainCmd())

    // ... persist events, notify hooks
}
```

Middlewares like logging, metrics, and tracing execute around the command handler. They're unaware of hooks — that synchronization happens at a higher level. This separation of concerns keeps each component focused: middlewares handle cross-cutting concerns, hooks handle synchronization.

For details on building and composing middleware chains, see [Part 9: Middleware Chains](/09-middleware-chains-in-go).

## Usage Pattern

Putting it all together, here's the complete pattern for synchronous API responses backed by async command processing:

```go
func (h *APIHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    // Create command with wait-for-completion enabled
    cmd := NewCommand(&PlaceOrderCommand{
        OrderID:    uuid.New().String(),
        CustomerID: r.FormValue("customer_id"),
    })
    cmd.GetReqCtx().ExecuteWaitToFinish = true

    // Dispatch command
    if _, err := fc.DispatchCommand(ctx, cmd); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Wait for command to complete
    if err := fc.WaitForCmd(ctx, cmd); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Now we can safely query the result
    order, err := fc.Query(ctx, &GetOrderQuery{OrderID: cmd.OrderID})
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    json.NewEncoder(w).Encode(order)
}
```

The key insight: after `WaitForCmd` returns successfully, we know that:
1. The command handler executed
2. Events were persisted to the event store
3. Event handlers (projections) have processed those events
4. The read model is up-to-date

This makes the subsequent query safe — the order will exist in the read model.

## Timeout Handling

Always use contexts with timeouts to prevent indefinite blocking. Different operations have different appropriate timeouts:

```go
func (h *APIHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    // Timeout for the entire operation
    ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
    defer cancel()

    cmd := NewCommand(&PlaceOrderCommand{...})
    cmd.GetReqCtx().ExecuteWaitToFinish = true

    if _, err := fc.DispatchCommand(ctx, cmd); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }

    // Wait with timeout
    if err := fc.WaitForCmd(ctx, cmd); err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            // Command might still complete eventually
            // Return accepted status with tracking ID
            w.WriteHeader(http.StatusAccepted)
            json.NewEncoder(w).Encode(map[string]string{
                "status": "processing",
                "id":     cmd.OrderID,
            })
            return
        }
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    // Command completed within timeout
    w.WriteHeader(http.StatusCreated)
    // ...
}
```

The `202 Accepted` response acknowledges that the request was received but processing isn't complete. The client can poll a status endpoint using the returned ID. This is better than returning an error — the command will likely succeed, just not within the timeout window.

## Fire and Forget vs. Wait

Not every command needs synchronous completion. Choose based on your requirements:

```go
// Fire and forget - for background processing
func (h *APIHandler) SubmitBackgroundJob(w http.ResponseWriter, r *http.Request) {
    cmd := NewCommand(&ProcessBatchCommand{...})
    // ExecuteWaitToFinish defaults to false

    // Just dispatch, don't wait
    fc.DispatchCommand(r.Context(), cmd)

    // Return immediately with job ID for status checking
    json.NewEncoder(w).Encode(map[string]string{
        "jobId":  cmd.CmdUuid,
        "status": "queued",
    })
}

// Wait for completion - for immediate user feedback
func (h *APIHandler) CreateOrder(w http.ResponseWriter, r *http.Request) {
    cmd := NewCommand(&PlaceOrderCommand{...})
    cmd.GetReqCtx().ExecuteWaitToFinish = true

    fc.DispatchCommand(r.Context(), cmd)
    fc.WaitForCmd(r.Context(), cmd)

    // Return completed order
    // ...
}
```

Fire-and-forget is appropriate when:
- The operation is long-running (batch processing, report generation)
- Immediate confirmation isn't required
- The client will poll for status anyway

Wait-for-completion is appropriate when:
- Users expect immediate feedback (order placed, payment processed)
- The operation is typically fast (< 5 seconds)
- You want to return the created resource in the response

## Advanced: Progress Tracking

For long-running commands, you can extend hooks to report progress:

```go
type HookWithProgress struct {
    *Hook
    ProgressCh chan Progress
}

type Progress struct {
    Percent int
    Message string
}

func (h *HookWithProgress) ReportProgress(percent int, message string) {
    select {
    case h.ProgressCh <- Progress{Percent: percent, Message: message}:
    default:
        // Non-blocking if no one is listening
    }
}

// Handler can stream progress to client via Server-Sent Events
func (h *APIHandler) ProcessWithProgress(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "Streaming not supported", http.StatusInternalServerError)
        return
    }

    cmd := NewCommand(&LongRunningCommand{...})
    hook := fc.DispatchCommandWithProgress(r.Context(), cmd)

    // Stream progress as Server-Sent Events
    w.Header().Set("Content-Type", "text/event-stream")

    for {
        select {
        case progress := <-hook.ProgressCh:
            fmt.Fprintf(w, "data: {\"percent\": %d, \"message\": %q}\n\n",
                progress.Percent, progress.Message)
            flusher.Flush()

        case result := <-hook.Ch:
            if result.Error != nil {
                fmt.Fprintf(w, "data: {\"error\": %q}\n\n", result.Error)
            } else {
                fmt.Fprintf(w, "data: {\"complete\": true}\n\n")
            }
            flusher.Flush()
            return

        case <-r.Context().Done():
            return
        }
    }
}
```

This pattern is useful for file uploads, data imports, or any operation where users benefit from seeing incremental progress.

## Testing Hooks

Hooks can be tested in isolation to verify the synchronization mechanics:

```go
func TestHookCompletion(t *testing.T) {
    registry := NewHookRegistry()

    cmdUuid := "test-cmd-123"
    hook := registry.Register(cmdUuid)

    // Simulate async command completion
    go func() {
        time.Sleep(100 * time.Millisecond)
        hook.Close(context.Background(), &HookResult{
            Success: true,
            Events:  []Event{NewEvent()},
        })
    }()

    // Signal ready to receive
    hook.ReceiverCh <- struct{}{}

    // Wait for result
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
    defer cancel()

    select {
    case result := <-hook.Ch:
        assert.True(t, result.Success)
        assert.Len(t, result.Events, 1)
    case <-ctx.Done():
        t.Fatal("timeout waiting for hook")
    }
}

func TestHookTimeout(t *testing.T) {
    registry := NewHookRegistry()

    cmdUuid := "test-cmd-456"
    hook := registry.Register(cmdUuid)

    // Don't complete the command

    // Short timeout
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()

    hook.ReceiverCh <- struct{}{}

    select {
    case <-hook.Ch:
        t.Fatal("should have timed out")
    case <-ctx.Done():
        assert.Equal(t, context.DeadlineExceeded, ctx.Err())
    }
}

func TestHookErrorPropagation(t *testing.T) {
    registry := NewHookRegistry()

    cmdUuid := "test-cmd-789"
    hook := registry.Register(cmdUuid)

    expectedErr := errors.New("command validation failed")

    go func() {
        time.Sleep(50 * time.Millisecond)
        hook.Close(context.Background(), &HookResult{
            Success: false,
            Error:   expectedErr,
        })
    }()

    hook.ReceiverCh <- struct{}{}

    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
    defer cancel()

    select {
    case result := <-hook.Ch:
        assert.False(t, result.Success)
        assert.Equal(t, expectedErr, result.Error)
    case <-ctx.Done():
        t.Fatal("timeout waiting for hook")
    }
}
```

## Integration with Distributed Systems

In distributed environments where commands might be processed on a different instance than where they were dispatched, hooks need external coordination. A message broker bridges the gap:

```go
type DistributedHookRegistry struct {
    redis *redis.Client
    local *HookRegistry
}

func (r *DistributedHookRegistry) WaitForCmd(ctx context.Context, cmdUuid string) error {
    // Try local first - command might be processed on this instance
    if hook := r.local.Get(cmdUuid); hook != nil {
        return r.waitLocal(ctx, hook)
    }

    // Subscribe to Redis for cross-instance completion
    pubsub := r.redis.Subscribe(ctx, "cmd:complete:"+cmdUuid)
    defer pubsub.Close()

    select {
    case msg := <-pubsub.Channel():
        var result HookResult
        json.Unmarshal([]byte(msg.Payload), &result)
        return result.Error
    case <-ctx.Done():
        return ctx.Err()
    }
}

func (r *DistributedHookRegistry) CompleteCommand(ctx context.Context, cmdUuid string, result *HookResult) {
    // Complete local hook
    if hook := r.local.Get(cmdUuid); hook != nil {
        hook.Close(ctx, result)
    }

    // Publish to Redis for other instances
    data, _ := json.Marshal(result)
    r.redis.Publish(ctx, "cmd:complete:"+cmdUuid, data)
}
```

The pattern is straightforward:
1. On dispatch, subscribe to a completion channel for this command UUID
2. On completion (any instance), publish to that channel
3. The waiting instance receives the message and unblocks

Redis Pub/Sub works well here because:
- It's fire-and-forget (no persistence needed for transient completion notifications)
- It's fast (sub-millisecond latency)
- It naturally supports the "exactly one subscriber" pattern we need

## Running an Example

Source: https://github.com/susdorf/building-event-sourced-systems-in-go/tree/main/11-async-command-completion-with-hooks/code

When you run the complete example, you'll see hooks in action across various scenarios:

```
=== Async Command Completion with Hooks Demo ===

1. Fire and Forget (ExecuteWaitToFinish = false):
   -> Command dispatched: cmd-1766748990399428000
   -> Dispatch error: <nil>
   -> Pending hooks: 0 (none, because we're not waiting)
   -> Returned immediately, command processing in background

2. Wait for Completion (ExecuteWaitToFinish = true):
   -> Command dispatched: cmd-1766748990600539000
   -> Pending hooks after dispatch: 1
   -> Wait completed in 62ms
   -> Error: <nil>
   -> Pending hooks after wait: 0

3. Timeout Handling (context deadline exceeded):
   -> Command dispatched with 100ms timeout
   -> Processing takes 500ms (will timeout)
   -> Wait returned after 101ms
   -> Error: context deadline exceeded

4. Error Propagation (command handler returns error):
   -> Command: InvalidOperation
   -> Error propagated: validation failed: invalid operation requested

5. Aggregate Locking (concurrent commands to same aggregate):
   -> 3 concurrent commands targeting same aggregate:
      Command 0 completed after 112ms
      Command 1 completed after 313ms
      Command 2 completed after 212ms
   -> Commands serialized due to aggregate locking

6. Different Aggregates (parallel execution):
   -> 3 commands to different aggregates completed in 134ms
   -> Executed in parallel (not serialized)

7. Hook Registry State:
   -> Pending hooks: 0
   -> After dispatching 3 commands: 3 pending hooks
   -> After waiting for all: 0 pending hooks (cleaned up)

8. Multiple Events from Single Command:
   -> ComplexOperation produces multiple events
   -> Hook notified only after ALL events processed
   -> Error: <nil>

=== Demo Complete ===
```

The demo illustrates key behaviors:

1. **Fire and Forget**: Without `ExecuteWaitToFinish`, no hook is registered and dispatch returns immediately
2. **Wait for Completion**: With the flag set, the caller blocks until the command finishes
3. **Timeout Handling**: Context deadlines prevent indefinite waits — useful for API timeouts
4. **Error Propagation**: Command handler errors flow back through the hook to the waiting caller
5. **Aggregate Locking**: Commands targeting the same aggregate are serialized (~100ms each = ~300ms total)
6. **Parallel Execution**: Commands to different aggregates run concurrently (~130ms for all three)
7. **Hook Cleanup**: Registry properly cleans up after commands complete
8. **Bulk Events**: Hooks only fire after all events from a command are processed

## Summary

Async command completion with hooks provides a clean solution to the synchronous API / asynchronous processing tension:

1. **Problem**: Commands process asynchronously, but APIs need synchronous responses to satisfy user expectations

2. **Solution**: Hooks provide synchronization between dispatch and completion, letting callers optionally wait

3. **Control**: The `ExecuteWaitToFinish` flag in `RequestContext` explicitly opts into waiting behavior

4. **Implementation**: Channels with careful locking to avoid races and goroutine leaks

5. **Error handling**: Errors propagate through hooks just like success, ensuring callers learn about failures

6. **Aggregate locking**: Prevents concurrent modifications to the same aggregate during command execution

7. **Bulk processing**: Events are processed together, and hooks only fire after all events complete

8. **Timeouts**: Always use contexts to prevent indefinite waits; handle timeouts gracefully with 202 responses

This pattern bridges the gap between event-sourced async processing and traditional request-response APIs, giving you the best of both worlds: the architectural benefits of event sourcing with the user experience of synchronous operations.

---

## What's Next

We've covered the core architecture patterns. In the next posts, we'll explore real-world concerns: **Multi-Tenancy**, **Workspaces**, **Domain Registration**, and **Testing**.


