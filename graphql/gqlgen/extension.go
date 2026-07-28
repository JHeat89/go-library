// Package gqlgen adapts the server-agnostic graphql logger to gqlgen's
// handler extension chain. Only services that import this package pull in the
// gqlgen dependency.
//
//	srv := handler.NewDefaultServer(generated.NewExecutableSchema(...))
//	srv.Use(gqlgen.New(graphqllog.New(log)))
package gqlgen

import (
	"context"
	"sync"

	gql "github.com/99designs/gqlgen/graphql"

	graphqllog "github.com/JHeat89/go-library/graphql"
)

// Extension implements gqlgen's HandlerExtension and OperationInterceptor,
// delegating to the agnostic graphql logger.
type Extension struct {
	log *graphqllog.Logger
}

var (
	_ gql.HandlerExtension     = Extension{}
	_ gql.OperationInterceptor = Extension{}
)

// New builds the gqlgen extension. Pass it to srv.Use.
func New(log *graphqllog.Logger) Extension {
	return Extension{log: log}
}

func (Extension) ExtensionName() string { return "GoLibraryLogger" }

func (Extension) Validate(gql.ExecutableSchema) error { return nil }

// InterceptOperation starts the lifecycle trace before execution and finishes
// it when the first response is produced. For subscriptions, the trace covers
// establishment through the first event; per-event errors after that are
// still logged by gqlgen's own error presenter.
func (e Extension) InterceptOperation(ctx context.Context, next gql.OperationHandler) gql.ResponseHandler {
	oc := gql.GetOperationContext(ctx)

	op := graphqllog.Operation{
		Name:      oc.OperationName,
		Query:     oc.RawQuery,
		Variables: oc.Variables,
	}
	if oc.Operation != nil {
		op.Type = string(oc.Operation.Operation)
		if op.Name == "" {
			op.Name = oc.Operation.Name
		}
	}

	ctx, finish := e.log.OperationStart(ctx, op)
	var once sync.Once

	handler := next(ctx)
	return func(ctx context.Context) *gql.Response {
		resp := handler(ctx)
		once.Do(func() {
			var errs []error
			if resp != nil {
				for _, gqlErr := range resp.Errors {
					errs = append(errs, gqlErr)
				}
			}
			finish(errs...)
		})
		return resp
	}
}
