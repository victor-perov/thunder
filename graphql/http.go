package graphql

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/samsarahq/thunder/batch"
	"github.com/samsarahq/thunder/reactive"
	"github.com/satori/go.uuid"
)

func HTTPHandler(schema *Schema, middlewares ...MiddlewareFunc) http.Handler {
	return &httpHandler{
		schema:      schema,
		middlewares: middlewares,
	}
}

// HTTPHandlerWithHooks works as HTTPHandler but in addition provides passing
// errorHandler func which will catch errors happened outside middleware
func HTTPHandlerWithHooks(schema *Schema, maxRequests int, minRequests int, waitTime time.Duration, errorHandler outsideMiddlewareErrorHandlerFunc, successfulResponseHook responseHook, middlewares ...MiddlewareFunc) http.Handler {
	return &httpHandler{
		schema:                  schema,
		maxRequests:             maxRequests,
		minRequests:             minRequests,
		currentMaxRequestsLevel: maxRequests,
		waitTime:                waitTime,
		errorHandler:            errorHandler,
		middlewares:             middlewares,
		successfulResponseHook:  successfulResponseHook,
	}
}

type endRequestState int

const (
	maxSimultaneousRequestsDefault int             = 15
	minSimultaneousRequestsDefault int             = 2
	waitTimeDefault                time.Duration   = 3 * time.Second
	endRequestStateOK              endRequestState = 0
	endRequestStateError           endRequestState = 1
	endRequestStateTimedOut        endRequestState = 2
)

// activeRequest provides structure for request that processing by service
// stores id, count of simultaneous requests on moment of starting request,
// date of request start and predicted request duration
type activeRequest struct {
	id          uuid.UUID
	bucketLevel int
	startedAt   time.Time
	predictedAt time.Time
}

type httpHandler struct {
	// requestsBucket a list of all simultaneous requests
	requestsBucket []activeRequest
	// maxRequests the max possible amount of simultaneous requests that will
	// have service
	maxRequests int
	// minRequests the min possible amount of simultaneous requests
	minRequests int
	// currentMaxRequestsLevel an amount of simultaneous requests that could be
	// processed in current situation. This count is depends on timeouted
	// requests to databases
	currentMaxRequestsLevel int
	// predictedDuration a duration that could take current request. Calculation
	// depends on real duration of previous requests
	predictedDuration time.Duration
	// if simultaneous requests of service reach level of
	// currentMaxRequestsLevel service will wait `waitTime` before repeating
	// attempt to serve request
	waitTime               time.Duration
	schema                 *Schema
	errorHandler           outsideMiddlewareErrorHandlerFunc
	successfulResponseHook responseHook
	middlewares            []MiddlewareFunc
}

type httpPostBody struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type httpResponse struct {
	Data   interface{} `json:"data"`
	Errors interface{} `json:"errors"`
}

// newRequest creates activeRequest object. Method grants that request will be
// served
func (h *httpHandler) newRequest() (uuid.UUID, error) {
	connID := uuid.NewV4()
	predictedAt := time.Now().Add(h.predictedDuration)
	h.requestsBucket = append(h.requestsBucket, activeRequest{
		id:          connID,
		bucketLevel: len(h.requestsBucket) + 1,
		startedAt:   time.Now(),
		predictedAt: predictedAt,
	})
	return connID, nil
}

// serveRequest decides put request into work or not. If amount of simultaneous
// requests is more than allowed (`currentMaxRequestsLevel` level), method will
// wait for up to `httpHandler.waitTime` and repeat attempt to start request. If
// request started: returns UUID of request, otherwise returns empty UUID and
// error
func (h *httpHandler) serveRequest(isInitial bool) (uuid.UUID, error) {
	if len(h.requestsBucket) < h.currentMaxRequestsLevel {
		return h.newRequest()
	}
	if isInitial && h.predictedDuration <= h.waitTime {
		time.Sleep(h.predictedDuration)
		return h.serveRequest(false)
	}
	return uuid.Nil, NewClientError("limit is reached, please try again later")
}

// endRequest finishes request and removes it from the list of simultaneous
// requests. Method adjustes `predictedDuration` value based on `endState` code.
func (h *httpHandler) endRequest(connID uuid.UUID, endState endRequestState) {
	if connID == uuid.Nil {
		return
	}
	var connection activeRequest
	for i, conn := range h.requestsBucket {
		if conn.id == connID {
			connection = conn
			h.requestsBucket = append(h.requestsBucket[:i], h.requestsBucket[i+1:]...)
			break
		}
	}
	// we do not want update prediction if endState == endRequestStateError or
	// somehow connection was not found
	if connection.id == uuid.Nil || endState == endRequestStateError {
		return
	}
	// if connection was timeouted, we would like to decrease
	// currentMaxRequestsLevel or set it to level of simultaneous requests,
	// which was when request has been accepted
	if endState == endRequestStateTimedOut {
		if connection.bucketLevel <= h.minRequests {
			h.currentMaxRequestsLevel = h.minRequests
		} else if connection.bucketLevel > h.currentMaxRequestsLevel {
			h.currentMaxRequestsLevel--
		} else {
			h.currentMaxRequestsLevel = connection.bucketLevel
		}
	} else if h.currentMaxRequestsLevel < h.maxRequests {
		// if endState is OK, we would like increment amount of
		// currentMaxRequestsLevel
		h.currentMaxRequestsLevel++
	}
	elapsedTime := time.Since(connection.startedAt)
	// if real request time is longer than expected we will update prediction to
	// the real request time
	if elapsedTime >= h.predictedDuration {
		h.predictedDuration = elapsedTime
	} else {
		// if real request time is less than expected we decrease it on an half
		// of difference
		h.predictedDuration = (elapsedTime + h.predictedDuration) / 2
	}
}

func (h *httpHandler) setRatelimitValues() {
	if h.waitTime == 0 {
		h.waitTime = waitTimeDefault
	}
	if h.predictedDuration == 0 {
		h.predictedDuration = h.waitTime
	}
	if h.maxRequests == 0 {
		h.maxRequests = maxSimultaneousRequestsDefault
		h.currentMaxRequestsLevel = maxSimultaneousRequestsDefault
	}
	if h.minRequests == 0 {
		h.minRequests = minSimultaneousRequestsDefault
	}
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setRatelimitValues()
	writeResponse := func(value interface{}, err error, query *string, connID uuid.UUID) {
		response := httpResponse{}
		endStateValue := endRequestStateOK
		if err != nil {
			endStateValue = endRequestStateError
			if h.errorHandler != nil {
				h.errorHandler(flattenError(err, 0), query)
			}
			response.Errors = []interface{}{newGraphQLError(err)}
		} else {
			response.Data = value
		}

		h.endRequest(connID, endStateValue)
		responseJSON, err := json.Marshal(response)
		if err != nil {
			if h.errorHandler != nil {
				h.errorHandler(err, query)
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}

		if h.successfulResponseHook != nil && response.Errors == nil {
			h.successfulResponseHook(responseJSON)
		}
		w.Write(responseJSON)
	}

	if r.Method != "POST" {
		writeResponse(nil, NewClientError("request must be a POST"), nil, uuid.Nil)
		return
	}

	if r.Body == nil {
		writeResponse(nil, NewClientError("request must include a query"), nil, uuid.Nil)
		return
	}

	var params httpPostBody
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		if h.errorHandler != nil {
			h.errorHandler(err, nil)
		}
		writeResponse(nil, NewClientError("request must have a valid JSON structure"), nil, uuid.Nil)
		return
	}

	connID, err := h.serveRequest(true)
	if err != nil {
		writeResponse(nil, err, nil, uuid.Nil)
		return
	}

	query, err := Parse(params.Query, params.Variables)
	if err != nil {
		writeResponse(nil, err, &params.Query, connID)
		return
	}

	schema := h.schema.Query
	if query.Kind == "mutation" {
		schema = h.schema.Mutation
	}
	if err := PrepareQuery(schema, query.SelectionSet); err != nil {
		writeResponse(nil, err, &params.Query, connID)
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
				writeResponse(nil, err, &params.Query, connID)
			} else {
				// we would like to end request and free from the list of
				// simultaneous requests
				h.endRequest(connID, endRequestStateTimedOut)
			}
			return nil, err
		}

		writeResponse(current, nil, nil, connID)
		return nil, nil
	}, DefaultMinRerunInterval)

	wg.Wait()
	runner.Stop()
}
