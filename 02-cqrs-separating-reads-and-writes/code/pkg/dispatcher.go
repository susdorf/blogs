package pkg

// This file is intentionally minimal.
// Instead of using reflection-based dispatchers, we use simple switch statements
// in CommandHandler, QueryHandler, and the ReadModel's Apply method.
// See commands.go, queries.go, and readmodel.go for the implementations.
