package pkg

import (
	"context"
	"sync"
	"time"
)

// HookResult contains the result of a command execution
type HookResult struct {
	Success bool
	Error   string
	Events  []Event
}

// Hook is a synchronization primitive for waiting on command completion
type Hook struct {
	mu         sync.Mutex // exported via method access
	CmdUuid    string
	Ch         chan HookResult
	ReceiverCh chan struct{}
	Closed     bool
	ChResult   HookResult
}

// Lock acquires the hook's mutex
func (h *Hook) Lock() {
	h.mu.Lock()
}

// Unlock releases the hook's mutex
func (h *Hook) Unlock() {
	h.mu.Unlock()
}

// NewHook creates a new hook for the given command UUID
func NewHook(cmdUuid string) *Hook {
	return &Hook{
		CmdUuid:    cmdUuid,
		Ch:         make(chan HookResult, 1),
		ReceiverCh: make(chan struct{}, 1),
		Closed:     false,
	}
}

// Done signals successful completion
func (h *Hook) Done(ctx context.Context, events []Event) {
	h.closeWithResult(ctx, &HookResult{
		Success: true,
		Events:  events,
	})
}

// Error signals completion with an error
func (h *Hook) Error(ctx context.Context, errMsg string) {
	h.closeWithResult(ctx, &HookResult{
		Success: false,
		Error:   errMsg,
	})
}

// closeWithResult sends the result to waiting receivers
func (h *Hook) closeWithResult(ctx context.Context, result *HookResult) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.Closed {
		return
	}

	// Store result for late receivers
	if result != nil {
		h.ChResult = *result
	}

	// Try to send result - wait for receiver to be ready (with timeout)
	if h.Ch != nil && result != nil {
		timeout := time.After(100 * time.Millisecond)
		done := false
		for !done {
			select {
			case <-h.ReceiverCh:
				// Receiver is ready
				select {
				case h.Ch <- *result:
					done = true
				default:
					time.Sleep(1 * time.Millisecond)
				}
			case <-ctx.Done():
				done = true
			case <-timeout:
				// No receiver within timeout, continue anyway
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

// HookRegistry manages hooks for pending commands
type HookRegistry struct {
	hooks sync.Map // cmdUuid -> *Hook
}

// NewHookRegistry creates a new hook registry
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{}
}

// Register creates and stores a hook for the given command UUID
func (r *HookRegistry) Register(cmdUuid string) *Hook {
	hook := NewHook(cmdUuid)
	r.hooks.Store(cmdUuid, hook)
	return hook
}

// Get retrieves a hook by command UUID
func (r *HookRegistry) Get(cmdUuid string) *Hook {
	if h, ok := r.hooks.Load(cmdUuid); ok {
		return h.(*Hook)
	}
	return nil
}

// Remove deletes a hook from the registry
func (r *HookRegistry) Remove(cmdUuid string) {
	r.hooks.Delete(cmdUuid)
}

// Count returns the number of registered hooks
func (r *HookRegistry) Count() int {
	count := 0
	r.hooks.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
