package main

import (
	"context"
	"fmt"

	"middleware-chains-demo/pkg"
)

func main() {
	fmt.Println("=== Middleware Chains Demo ===")
	fmt.Println()

	// --- 1. Basic middleware chain application ---
	fmt.Println("1. Basic Middleware Chain (manual application):")
	fmt.Println("   Applying [Logging, Validation] middlewares to a simple handler")
	fmt.Println()

	simpleHandler := func(ctx context.Context, cmd pkg.Command, domainCmd pkg.DomainCmd) ([]pkg.Event, error) {
		fmt.Println("    [HANDLER] Executing business logic...")
		return []pkg.Event{{Uuid: "evt-1", DataType: "OrderCreated"}}, nil
	}

	// Apply middlewares manually
	wrapped := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		simpleHandler,
		pkg.LoggingMiddleware,
		pkg.ValidationMiddleware,
	)

	cmd := pkg.Command{
		Uuid:    "cmd-1",
		Domain:  "orders",
		CmdName: "CreateOrder",
		Payload: map[string]string{"item": "book"},
	}

	events, err := wrapped(context.Background(), cmd, cmd.Payload)
	fmt.Printf("\n   Result: %d events, error: %v\n", len(events), err)

	// --- 2. Middleware execution order ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("2. Middleware Execution Order:")
	fmt.Println("   Chain: [Recovery, Tracing, Logging, Metrics, Validation]")
	fmt.Println("   Watch how they execute in order and 'unwind' in reverse")
	fmt.Println()

	fullChain := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		simpleHandler,
		pkg.RecoveryMiddleware,
		pkg.TracingMiddleware,
		pkg.LoggingMiddleware,
		pkg.MetricsMiddleware,
		pkg.ValidationMiddleware,
	)

	_, _ = fullChain(context.Background(), cmd, cmd.Payload)

	// --- 3. Authorization middleware ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("3. Authorization Middleware:")
	fmt.Println()

	fmt.Println("   3a. Request WITHOUT authorization context:")
	authChain := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		simpleHandler,
		pkg.LoggingMiddleware,
		pkg.AuthorizationMiddleware,
	)
	_, err = authChain(context.Background(), cmd, cmd.Payload)
	fmt.Printf("   Result: error = %v\n", err)

	fmt.Println()
	fmt.Println("   3b. Request WITH valid authorization (admin role):")
	reqCtx := &pkg.RequestContext{
		IdentityUuid: "user-123",
		TenantUuid:   "tenant-A",
		Roles:        []string{"admin"},
	}
	ctx := pkg.WithRequestContext(context.Background(), reqCtx)
	events, err = authChain(ctx, cmd, cmd.Payload)
	fmt.Printf("   Result: %d events, error: %v\n", len(events), err)

	fmt.Println()
	fmt.Println("   3c. Request with INSUFFICIENT permissions:")
	reqCtx = &pkg.RequestContext{
		IdentityUuid: "user-456",
		TenantUuid:   "tenant-A",
		Roles:        []string{"inventory:write"}, // wrong domain
	}
	ctx = pkg.WithRequestContext(context.Background(), reqCtx)
	_, err = authChain(ctx, cmd, cmd.Payload)
	fmt.Printf("   Result: error = %v\n", err)

	// --- 4. Recovery middleware (panic handling) ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("4. Recovery Middleware (panic handling):")
	fmt.Println()

	panicHandler := func(ctx context.Context, cmd pkg.Command, domainCmd pkg.DomainCmd) ([]pkg.Event, error) {
		panic("something went terribly wrong!")
	}

	recoveryChain := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		panicHandler,
		pkg.RecoveryMiddleware,
		pkg.LoggingMiddleware,
	)

	events, err = recoveryChain(context.Background(), cmd, cmd.Payload)
	fmt.Printf("   Result: events = %v, error = %v\n", events, err)
	fmt.Println("   (Notice: the panic was caught and converted to an error)")

	// --- 5. Using the Facade ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("5. Facade with Middleware Stack:")
	fmt.Println()

	facade := pkg.NewFacade()

	// Register middlewares
	facade.AddCommandHandlerMiddleware(pkg.ProductionMiddlewares()...)
	facade.AddQueryHandlerMiddleware(pkg.QueryProductionMiddlewares()...)

	// Register handlers
	facade.RegisterCommandHandler("orders", "CreateOrder", func(ctx context.Context, cmd pkg.Command, domainCmd pkg.DomainCmd) ([]pkg.Event, error) {
		return []pkg.Event{
			{Uuid: "evt-1", Domain: "orders", DataType: "OrderCreated"},
			{Uuid: "evt-2", Domain: "orders", DataType: "InventoryReserved"},
		}, nil
	})

	facade.RegisterQueryHandler("orders", "GetOrder", func(ctx context.Context, qry pkg.Query, domainQry pkg.DomainQry) (any, error) {
		return map[string]string{"orderId": "order-123", "status": "pending"}, nil
	})

	fmt.Println("   5a. Dispatching command through Facade:")
	reqCtx = &pkg.RequestContext{
		IdentityUuid: "user-789",
		Roles:        []string{"orders:write"},
	}
	ctx = pkg.WithRequestContext(context.Background(), reqCtx)
	events, err = facade.DispatchCommand(ctx, cmd)
	fmt.Printf("\n   Result: %d events produced\n", len(events))

	fmt.Println()
	fmt.Println("   5b. Dispatching query through Facade:")
	reqCtx = &pkg.RequestContext{
		IdentityUuid: "user-789",
		Roles:        []string{"orders:read"},
	}
	ctx = pkg.WithRequestContext(context.Background(), reqCtx)
	qry := pkg.Query{Domain: "orders", QryName: "GetOrder"}
	result, err := facade.DispatchQuery(ctx, qry)
	fmt.Printf("\n   Result: %v\n", result)

	// --- 6. Conditional middlewares ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("6. Conditional Middleware:")
	fmt.Println("   Metrics only for 'orders' domain")
	fmt.Println()

	conditionalMetrics := pkg.ConditionalMiddleware(
		func(ctx context.Context, cmd pkg.Command) bool {
			return cmd.GetDomain() == "orders"
		},
		pkg.MetricsMiddleware,
	)

	conditionalChain := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		simpleHandler,
		pkg.LoggingMiddleware,
		conditionalMetrics,
	)

	fmt.Println("   6a. Command for 'orders' domain (metrics WILL run):")
	ordersCmd := pkg.Command{Domain: "orders", CmdName: "CreateOrder"}
	_, _ = conditionalChain(context.Background(), ordersCmd, nil)

	fmt.Println()
	fmt.Println("   6b. Command for 'inventory' domain (metrics WON'T run):")
	inventoryCmd := pkg.Command{Domain: "inventory", CmdName: "UpdateStock"}
	_, _ = conditionalChain(context.Background(), inventoryCmd, nil)

	// --- 7. Different middleware stacks ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("7. Environment-Specific Middleware Stacks:")
	fmt.Println()

	fmt.Printf("   Production stack: %d middlewares\n", len(pkg.ProductionMiddlewares()))
	fmt.Println("   [Recovery, Tracing, Logging, Metrics, Authorization, Validation]")
	fmt.Println()
	fmt.Printf("   Development stack: %d middlewares\n", len(pkg.DevelopmentMiddlewares()))
	fmt.Println("   [Recovery, Logging, Validation]")
	fmt.Println()
	fmt.Printf("   Test stack: %d middlewares\n", len(pkg.TestMiddlewares()))
	fmt.Println("   [Validation]")

	// --- 8. Validation failure ---
	fmt.Println()
	fmt.Println("─────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("8. Validation Middleware (early rejection):")
	fmt.Println()

	validationChain := pkg.WithCommandHandlerMiddlewares(
		context.Background(),
		simpleHandler,
		pkg.LoggingMiddleware,
		pkg.ValidationMiddleware,
	)

	invalidCmd := pkg.Command{Domain: "", CmdName: "CreateOrder"} // missing domain
	_, err = validationChain(context.Background(), invalidCmd, nil)
	fmt.Printf("   Invalid command result: error = %v\n", err)

	fmt.Println()
	fmt.Println("=== Demo Complete ===")
}
