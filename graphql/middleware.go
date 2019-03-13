package graphql

import (
	"context"
)

type ComputationInput struct {
	Id                   string
	Query                string
	RequestsCount        int
	RequestsLimit        int
	ParsedQuery          *Query
	Variables            map[string]interface{}
	Ctx                  context.Context
	Previous             interface{}
	IsInitialComputation bool
	Extensions           map[string]interface{}
}

type ComputationOutput struct {
	Metadata map[string]interface{}
	Current  interface{}
	Error    error
}

// outsideMiddlewareErrorHandlerFunc type describes function
// that will be executed in case if error happens outside processing of MiddlewareFunc
type outsideMiddlewareErrorHandlerFunc func(err error, query *string)
type responseHook func(response []byte)
type MiddlewareFunc func(input *ComputationInput, next MiddlewareNextFunc) *ComputationOutput
type MiddlewareNextFunc func(input *ComputationInput) *ComputationOutput

func RunMiddlewares(middlewares []MiddlewareFunc, input *ComputationInput) *ComputationOutput {
	var run func(index int, middlewares []MiddlewareFunc, input *ComputationInput) *ComputationOutput
	run = func(index int, middlewares []MiddlewareFunc, input *ComputationInput) *ComputationOutput {
		if index >= len(middlewares) {
			return &ComputationOutput{
				Metadata: make(map[string]interface{}),
			}
		}

		middleware := middlewares[index]
		return middleware(input, func(input *ComputationInput) *ComputationOutput {
			return run(index+1, middlewares, input)
		})
	}

	return run(0, middlewares, input)
}
