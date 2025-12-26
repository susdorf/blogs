package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"async-hooks-demo/pkg"
)

func main() {
	fmt.Println("=== Async Command Completion with Hooks Demo ===")
	fmt.Println()

	// Create facade and register handlers
	fc := pkg.NewFacade()
	fc.SetProcessingTime(50 * time.Millisecond)
	registerHandlers(fc)

	// --- 1. Fire and Forget (no waiting) ---
	fmt.Println("1. Fire and Forget (ExecuteWaitToFinish = false):")
	cmd := pkg.NewCommand("PlaceOrder", "orders", map[string]string{"orderId": "order-1"})
	// Default: ExecuteWaitToFinish = false

	err := fc.DispatchCommand(context.Background(), cmd)
	fmt.Printf("   -> Command dispatched: %s\n", cmd.GetCmdUuid())
	fmt.Printf("   -> Dispatch error: %v\n", err)
	fmt.Printf("   -> Pending hooks: %d (none, because we're not waiting)\n", fc.GetPendingHooks())
	fmt.Println("   -> Returned immediately, command processing in background")

	time.Sleep(200 * time.Millisecond) // Let it complete

	// --- 2. Wait for Completion ---
	fmt.Println()
	fmt.Println("2. Wait for Completion (ExecuteWaitToFinish = true):")
	cmd = pkg.NewCommand("PlaceOrder", "orders", map[string]string{"orderId": "order-2"})
	cmd.GetReqCtx().ExecuteWaitToFinish = true

	start := time.Now()
	err = fc.DispatchCommand(context.Background(), cmd)
	fmt.Printf("   -> Command dispatched: %s\n", cmd.GetCmdUuid())
	fmt.Printf("   -> Pending hooks after dispatch: %d\n", fc.GetPendingHooks())

	err = fc.WaitForCmd(context.Background(), cmd)
	duration := time.Since(start)
	fmt.Printf("   -> Wait completed in %v\n", duration.Round(time.Millisecond))
	fmt.Printf("   -> Error: %v\n", err)
	fmt.Printf("   -> Pending hooks after wait: %d\n", fc.GetPendingHooks())

	// --- 3. Timeout Handling ---
	fmt.Println()
	fmt.Println("3. Timeout Handling (context deadline exceeded):")
	fc.SetProcessingTime(500 * time.Millisecond) // Slow processing
	cmd = pkg.NewCommand("SlowOperation", "orders", nil)
	cmd.GetReqCtx().ExecuteWaitToFinish = true

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	fc.DispatchCommand(ctx, cmd)
	fmt.Printf("   -> Command dispatched with 100ms timeout\n")
	fmt.Printf("   -> Processing takes 500ms (will timeout)\n")

	start = time.Now()
	err = fc.WaitForCmd(ctx, cmd)
	duration = time.Since(start)
	fmt.Printf("   -> Wait returned after %v\n", duration.Round(time.Millisecond))
	fmt.Printf("   -> Error: %v\n", err)

	fc.SetProcessingTime(50 * time.Millisecond) // Reset
	time.Sleep(500 * time.Millisecond)          // Let slow command complete

	// --- 4. Error Propagation ---
	fmt.Println()
	fmt.Println("4. Error Propagation (command handler returns error):")
	cmd = pkg.NewCommand("InvalidOperation", "orders", map[string]string{"invalid": "true"})
	cmd.GetReqCtx().ExecuteWaitToFinish = true

	fc.DispatchCommand(context.Background(), cmd)
	err = fc.WaitForCmd(context.Background(), cmd)
	fmt.Printf("   -> Command: InvalidOperation\n")
	fmt.Printf("   -> Error propagated: %v\n", err)

	// --- 5. Aggregate Locking ---
	fmt.Println()
	fmt.Println("5. Aggregate Locking (concurrent commands to same aggregate):")
	fc.SetProcessingTime(100 * time.Millisecond)

	var wg sync.WaitGroup
	results := make([]string, 3)
	starts := make([]time.Time, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := pkg.NewCommand("UpdateOrder", "orders", map[string]int{"seq": idx})
			cmd.GetReqCtx().ExecuteWaitToFinish = true
			cmd.GetReqCtx().TargetAggregateUuid = "order-123" // Same aggregate!

			starts[idx] = time.Now()
			fc.DispatchCommand(context.Background(), cmd)
			fc.WaitForCmd(context.Background(), cmd)
			results[idx] = fmt.Sprintf("Command %d completed after %v",
				idx, time.Since(starts[idx]).Round(time.Millisecond))
		}(i)
	}

	wg.Wait()
	fmt.Println("   -> 3 concurrent commands targeting same aggregate:")
	for _, r := range results {
		fmt.Printf("      %s\n", r)
	}
	fmt.Println("   -> Commands serialized due to aggregate locking")

	// --- 6. Different Aggregates (parallel execution) ---
	fmt.Println()
	fmt.Println("6. Different Aggregates (parallel execution):")
	start = time.Now()

	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := pkg.NewCommand("UpdateOrder", "orders", nil)
			cmd.GetReqCtx().ExecuteWaitToFinish = true
			cmd.GetReqCtx().TargetAggregateUuid = fmt.Sprintf("order-%d", idx) // Different aggregates

			fc.DispatchCommand(context.Background(), cmd)
			fc.WaitForCmd(context.Background(), cmd)
		}(i)
	}

	wg.Wait()
	totalTime := time.Since(start)
	fmt.Printf("   -> 3 commands to different aggregates completed in %v\n",
		totalTime.Round(time.Millisecond))
	fmt.Println("   -> Executed in parallel (not serialized)")

	// --- 7. Hook Registry State ---
	fmt.Println()
	fmt.Println("7. Hook Registry State:")
	fmt.Printf("   -> Pending hooks: %d\n", fc.GetPendingHooks())

	// Create some pending hooks and wait for them
	var cmds []*pkg.Command
	for i := 0; i < 3; i++ {
		cmd := pkg.NewCommand("PendingOp", "orders", nil)
		cmd.GetReqCtx().ExecuteWaitToFinish = true
		fc.DispatchCommand(context.Background(), cmd)
		cmds = append(cmds, cmd)
	}

	fmt.Printf("   -> After dispatching 3 commands: %d pending hooks\n", fc.GetPendingHooks())

	// Wait for all commands
	for _, cmd := range cmds {
		fc.WaitForCmd(context.Background(), cmd)
	}
	fmt.Printf("   -> After waiting for all: %d pending hooks (cleaned up)\n", fc.GetPendingHooks())

	// --- 8. Multiple Events from Single Command ---
	fmt.Println()
	fmt.Println("8. Multiple Events from Single Command:")
	cmd = pkg.NewCommand("ComplexOperation", "orders", map[string]string{"orderId": "order-complex"})
	cmd.GetReqCtx().ExecuteWaitToFinish = true

	fc.DispatchCommand(context.Background(), cmd)
	err = fc.WaitForCmd(context.Background(), cmd)
	fmt.Printf("   -> ComplexOperation produces multiple events\n")
	fmt.Printf("   -> Hook notified only after ALL events processed\n")
	fmt.Printf("   -> Error: %v\n", err)

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}

func registerHandlers(fc *pkg.Facade) {
	fc.RegisterHandler("orders", func(ctx context.Context, cmd *pkg.Command) ([]pkg.Event, error) {
		switch cmd.Name {
		case "PlaceOrder":
			return []pkg.Event{
				pkg.NewEvent("OrderPlaced", "orders", "order-new", cmd.Data),
			}, nil

		case "UpdateOrder":
			return []pkg.Event{
				pkg.NewEvent("OrderUpdated", "orders", cmd.GetReqCtx().TargetAggregateUuid, cmd.Data),
			}, nil

		case "InvalidOperation":
			return nil, fmt.Errorf("validation failed: invalid operation requested")

		case "SlowOperation":
			return []pkg.Event{
				pkg.NewEvent("SlowOpCompleted", "orders", "slow-1", nil),
			}, nil

		case "ComplexOperation":
			// Produces multiple events
			return []pkg.Event{
				pkg.NewEvent("OrderValidated", "orders", "complex-1", nil),
				pkg.NewEvent("InventoryReserved", "orders", "complex-1", nil),
				pkg.NewEvent("PaymentProcessed", "orders", "complex-1", nil),
				pkg.NewEvent("OrderConfirmed", "orders", "complex-1", nil),
			}, nil

		case "PendingOp":
			return []pkg.Event{
				pkg.NewEvent("OpCompleted", "orders", "pending", nil),
			}, nil

		default:
			return []pkg.Event{
				pkg.NewEvent(cmd.Name+"Completed", cmd.Domain, "default", cmd.Data),
			}, nil
		}
	})
}
