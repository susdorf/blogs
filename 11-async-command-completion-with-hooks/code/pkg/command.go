package pkg

import (
	"fmt"
	"time"
)

// RequestContext carries metadata about command execution
type RequestContext struct {
	SenderIdentityUuid  string
	TargetAggregateUuid string
	ExecuteTimeout      time.Duration
	ExecuteWaitToFinish bool
}

// Command represents a command in the system
type Command struct {
	Uuid      string
	Name      string
	Domain    string
	Data      interface{}
	ReqCtx    *RequestContext
	CreatedAt time.Time
}

// NewCommand creates a new command with a generated UUID
func NewCommand(name, domain string, data interface{}) *Command {
	return &Command{
		Uuid:      fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
		Name:      name,
		Domain:    domain,
		Data:      data,
		ReqCtx:    &RequestContext{},
		CreatedAt: time.Now(),
	}
}

// GetCmdUuid returns the command UUID
func (c *Command) GetCmdUuid() string {
	return c.Uuid
}

// GetReqCtx returns the request context
func (c *Command) GetReqCtx() *RequestContext {
	return c.ReqCtx
}

// Event represents a domain event
type Event struct {
	Uuid          string
	CommandUuid   string
	AggregateUuid string
	Domain        string
	Name          string
	Data          interface{}
	CreatedAt     time.Time
}

// NewEvent creates a new event
func NewEvent(name, domain, aggregateUuid string, data interface{}) Event {
	return Event{
		Uuid:          fmt.Sprintf("evt-%d", time.Now().UnixNano()),
		AggregateUuid: aggregateUuid,
		Domain:        domain,
		Name:          name,
		Data:          data,
		CreatedAt:     time.Now(),
	}
}
