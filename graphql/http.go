package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/samsarahq/thunder/batch"
	"github.com/samsarahq/thunder/reactive"
)

func HTTPHandler(schema *Schema, middlewares ...MiddlewareFunc) http.Handler {
	return &httpHandler{
		schema:      schema,
		middlewares: middlewares,
	}
}

// HTTPHandlerWithErrorHandling works as HTTPHandler
// but in addition provides passing errorHandler func
// which will catch errors happened outside middleware
func HTTPHandlerWithErrorHandling(schema *Schema, errorHandler outsideMiddlewareErrorHandlerFunc, middlewares ...MiddlewareFunc) http.Handler {
	return &httpHandler{
		schema:       schema,
		errorHandler: errorHandler,
		middlewares:  middlewares,
	}
}

type httpHandler struct {
	schema       *Schema
	errorHandler outsideMiddlewareErrorHandlerFunc
	middlewares  []MiddlewareFunc
}

type httpPostBody struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type httpResponse struct {
	Data   interface{} `json:"data"`
	Errors interface{} `json:"errors"`
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writeResponse := func(value interface{}, err error) {
		response := httpResponse{}
		if err != nil {
			response.Errors = []interface{}{newGraphQLError(err)}
		} else {
			response.Data = value
		}

		responseJSON, err := json.Marshal(response)
		if err != nil {
			if h.errorHandler != nil {
				h.errorHandler(err)
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.Write(responseJSON)
	}

	if r.Method != "POST" {
		writeResponse(nil, NewClientError("request must be a POST"))
		return
	}

	if r.Body == nil {
		writeResponse(nil, NewClientError("request must include a query"))
		return
	}

	var params httpPostBody
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		if h.errorHandler != nil {
			h.errorHandler(err)
		}
		writeResponse(nil, NewClientError("request must have a valid JSON structure"))
		return
	}

	query, err := Parse(params.Query, params.Variables)
	if err != nil {
		writeResponse(nil, err)
		return
	}

	schema := h.schema.Query
	if query.Kind == "mutation" {
		schema = h.schema.Mutation
	}
	if err := PrepareQuery(schema, query.SelectionSet); err != nil {
		writeResponse(nil, err)
		return
	}

	var wg sync.WaitGroup
	e := Executor{}

	wg.Add(1)
	runner := reactive.NewRerunner(r.Context(), func(ctx context.Context) (interface{}, error) {
		defer wg.Done()

		ctx = batch.WithBatching(ctx)

		var middlewares []MiddlewareFunc
		middlewares = append(middlewares, h.middlewares...)
		middlewares = append(middlewares, func(input *ComputationInput, next MiddlewareNextFunc) *ComputationOutput {
			output := next(input)
			output.Current, output.Error = e.Execute(input.Ctx, schema, nil, input.ParsedQuery)
			return output
		})

		output := RunMiddlewares(middlewares, &ComputationInput{
			Ctx:         ctx,
			ParsedQuery: query,
			Query:       params.Query,
			Variables:   params.Variables,
		})
		current, err := output.Current, output.Error

		if err != nil {
			if ErrorCause(err) != context.Canceled {
				writeResponse(nil, err)
			}
			return nil, err
		}

		writeResponse(current, nil)
		return nil, nil
	}, DefaultMinRerunInterval)

	wg.Wait()
	runner.Stop()
}
