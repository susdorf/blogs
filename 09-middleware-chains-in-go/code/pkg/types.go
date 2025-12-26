package pkg

import "context"

// Event represents a domain event
type Event struct {
	Uuid          string
	TenantUuid    string
	AggregateUuid string
	Domain        string
	DataType      string
	Data          string
}

// Command represents a command to be handled
type Command struct {
	Uuid       string
	TenantUuid string
	Domain     string
	CmdName    string
	Payload    any
}

func (c Command) GetDomain() string      { return c.Domain }
func (c Command) GetDomainCmdName() string { return c.CmdName }
func (c Command) GetCmdUuid() string     { return c.Uuid }
func (c Command) GetTenantUuid() string  { return c.TenantUuid }

// DomainCmd is the domain-specific command payload
type DomainCmd any

// Query represents a query to be handled
type Query struct {
	Uuid       string
	TenantUuid string
	Domain     string
	QryName    string
	Payload    any
}

func (q Query) GetDomain() string      { return q.Domain }
func (q Query) GetDomainQryName() string { return q.QryName }

// DomainQry is the domain-specific query payload
type DomainQry any

// RequestContext holds identity and authorization information
type RequestContext struct {
	IdentityUuid   string
	TenantUuid     string
	Roles          []string
	IsInternalCall bool
}

// Context key for request context
type ctxKey string

const reqCtxKey ctxKey = "request_context"

func WithRequestContext(ctx context.Context, reqCtx *RequestContext) context.Context {
	return context.WithValue(ctx, reqCtxKey, reqCtx)
}

func GetReqCtxFromContext(ctx context.Context) *RequestContext {
	if v := ctx.Value(reqCtxKey); v != nil {
		return v.(*RequestContext)
	}
	return nil
}
