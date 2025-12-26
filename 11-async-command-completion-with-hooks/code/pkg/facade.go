package pkg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// CommandHandler processes commands and returns events
type CommandHandler func(ctx context.Context, cmd *Command) ([]Event, error)

// Facade is the central orchestrator for command processing
type Facade struct {
	hookRegistry   *HookRegistry
	aggregateLocks sync.Map
	handlers       map[string]CommandHandler
	commandBus     chan *Command
	eventBus       chan eventBulk
	processingTime time.Duration // Simulated processing time
	errorRate      float64       // Simulated error rate (0.0-1.0)
	mu             sync.Mutex
}

type eventBulk struct {
	CommandUuid string
	Events      []Event
}

// NewFacade creates a new facade
func NewFacade() *Facade {
	fc := &Facade{
		hookRegistry:   NewHookRegistry(),
		handlers:       make(map[string]CommandHandler),
		commandBus:     make(chan *Command, 100),
		eventBus:       make(chan eventBulk, 100),
		processingTime: 100 * time.Millisecond,
	}

	// Start command and event processors
	go fc.commandLoop()
	go fc.eventLoop()

	return fc
}

// SetProcessingTime sets the simulated processing time
func (fc *Facade) SetProcessingTime(d time.Duration) {
	fc.processingTime = d
}

// SetErrorRate sets the simulated error rate
func (fc *Facade) SetErrorRate(rate float64) {
	fc.errorRate = rate
}

// RegisterHandler registers a command handler for a domain
func (fc *Facade) RegisterHandler(domain string, handler CommandHandler) {
	fc.handlers[domain] = handler
}

// DispatchCommand dispatches a command for async processing
func (fc *Facade) DispatchCommand(ctx context.Context, cmd *Command) error {
	// Register hook if waiting is requested
	if cmd.GetReqCtx().ExecuteWaitToFinish {
		fc.hookRegistry.Register(cmd.GetCmdUuid())
	}

	// Send to command bus
	select {
	case fc.commandBus <- cmd:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitForCmd waits for a command to complete
func (fc *Facade) WaitForCmd(ctx context.Context, cmd *Command) error {
	hook := fc.hookRegistry.Get(cmd.GetCmdUuid())
	if hook == nil {
		return errors.New("command not found or already completed")
	}

	defer fc.hookRegistry.Remove(cmd.GetCmdUuid())

	// Check if already closed (hook might have completed before we started waiting)
	hook.Lock()
	if hook.Closed {
		result := hook.ChResult
		hook.Unlock()
		if len(result.Error) > 0 {
			return errors.New(result.Error)
		}
		return nil
	}
	hook.Unlock()

	// Signal ready to receive
	select {
	case hook.ReceiverCh <- struct{}{}:
	default:
	}

	// Wait for result with timeout fallback
	select {
	case result, ok := <-hook.Ch:
		if !ok {
			// Channel closed, check stored result
			if len(hook.ChResult.Error) > 0 {
				return errors.New(hook.ChResult.Error)
			}
			return nil
		}
		if len(result.Error) > 0 {
			return errors.New(result.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetPendingHooks returns the number of pending hooks
func (fc *Facade) GetPendingHooks() int {
	return fc.hookRegistry.Count()
}

// commandLoop processes commands from the command bus
func (fc *Facade) commandLoop() {
	for cmd := range fc.commandBus {
		go fc.executeCommand(context.Background(), cmd)
	}
}

// eventLoop processes event bulks from the event bus
func (fc *Facade) eventLoop() {
	for bulk := range fc.eventBus {
		fc.processEventBulk(context.Background(), bulk)
	}
}

// executeCommand executes a single command
func (fc *Facade) executeCommand(ctx context.Context, cmd *Command) {
	// Lock aggregate if specified
	if cmd.GetReqCtx().TargetAggregateUuid != "" {
		fc.lockAggregate(cmd.GetReqCtx().TargetAggregateUuid)
		defer fc.unlockAggregate(cmd.GetReqCtx().TargetAggregateUuid)
	}

	// Simulate processing time
	time.Sleep(fc.processingTime)

	// Simulate errors
	if fc.errorRate > 0 && time.Now().UnixNano()%100 < int64(fc.errorRate*100) {
		fc.errorHandlerCmd(ctx, cmd, errors.New("simulated processing error"))
		return
	}

	// Find handler
	handler, ok := fc.handlers[cmd.Domain]
	if !ok {
		fc.errorHandlerCmd(ctx, cmd, fmt.Errorf("no handler for domain: %s", cmd.Domain))
		return
	}

	// Execute handler
	events, err := handler(ctx, cmd)
	if err != nil {
		fc.errorHandlerCmd(ctx, cmd, err)
		return
	}

	// Set command UUID on events
	for i := range events {
		events[i].CommandUuid = cmd.GetCmdUuid()
	}

	// Send events to event bus
	fc.eventBus <- eventBulk{
		CommandUuid: cmd.GetCmdUuid(),
		Events:      events,
	}
}

// processEventBulk processes a bulk of events and notifies the hook
func (fc *Facade) processEventBulk(ctx context.Context, bulk eventBulk) {
	// Process each event (in real system: update projections)
	for range bulk.Events {
		// Simulate event processing
		time.Sleep(10 * time.Millisecond)
	}

	// Notify hook
	if hook := fc.hookRegistry.Get(bulk.CommandUuid); hook != nil {
		hook.Done(ctx, bulk.Events)
	}
}

// errorHandlerCmd handles command errors and notifies the hook
func (fc *Facade) errorHandlerCmd(ctx context.Context, cmd *Command, err error) {
	if hook := fc.hookRegistry.Get(cmd.GetCmdUuid()); hook != nil {
		hook.Error(ctx, err.Error())
	}
}

// lockAggregate locks an aggregate for exclusive access
func (fc *Facade) lockAggregate(aggregateUuid string) {
	// Get or create a mutex for this aggregate
	mu := &sync.Mutex{}
	actual, _ := fc.aggregateLocks.LoadOrStore(aggregateUuid, mu)
	actual.(*sync.Mutex).Lock()
}

// unlockAggregate unlocks an aggregate
func (fc *Facade) unlockAggregate(aggregateUuid string) {
	if mu, ok := fc.aggregateLocks.Load(aggregateUuid); ok {
		mu.(*sync.Mutex).Unlock()
	}
}
