package graphql

import (
	"sync"
	"time"
)

type endRequestState int

const (
	maxSimultaneousRequestsDefault int             = 15
	minSimultaneousRequestsDefault int             = 2
	waitTimeDefault                time.Duration   = 3 * time.Second
	endRequestStateOK              endRequestState = 0
	endRequestStateError           endRequestState = 1
	endRequestStateCanceled        endRequestState = 2
)

// RatelimitObject represents structure of ratelimit object
type RatelimitObject struct {
	// requestsBucket a list of all simultaneous requests
	activeRequestsCount int
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
	// currentMaxRequestsLevel service will wait up to `waitTime` before
	// repeating attempt to serve request
	waitTime time.Duration
	mux      sync.Mutex
}

// ActiveRequest provides structure for request that processing by service
// stores id, count of simultaneous requests on moment of starting request,
// date of request start and predicted request duration
type ActiveRequest struct {
	startedAt   time.Time
	predictedAt time.Time
}

// RatelimitHandlerDefault creates ratelimit object with empty values
func RatelimitHandlerDefault() *RatelimitObject {
	rObj := RatelimitObject{}
	return rObj.initiate()
}

// RatelimitHandler creates ratelimit struct object
func RatelimitHandler(maxRequests int, minRequests int, waitTime time.Duration) *RatelimitObject {
	rObj := RatelimitObject{
		maxRequests:             maxRequests,
		minRequests:             minRequests,
		currentMaxRequestsLevel: maxRequests,
		waitTime:                waitTime,
		predictedDuration:       waitTime,
	}
	return rObj.initiate()
}

// GetSimultaneousRequestsCount returns number of simultaneous requests
func (rObj *RatelimitObject) GetSimultaneousRequestsCount() int {
	rObj.mux.Lock()
	defer rObj.mux.Unlock()
	return rObj.activeRequestsCount
}

// GetActualRequestsLimit returns number of simultaneous requests
func (rObj *RatelimitObject) GetActualRequestsLimit() int {
	rObj.mux.Lock()
	defer rObj.mux.Unlock()
	return rObj.currentMaxRequestsLevel
}

// ServeRequest decides put request into work or not. If amount of simultaneous
// requests is more than allowed (`currentMaxRequestsLevel` level), method will
// wait for up to `RatelimitObject.waitTime` and repeat attempt to start
// request. If request started: returns pointer to request, otherwise returns
// nil and error
func (rObj *RatelimitObject) ServeRequest(isInitial bool) (*ActiveRequest, error) {
	rObj.mux.Lock()
	if rObj.activeRequestsCount < rObj.currentMaxRequestsLevel {
		rObj.mux.Unlock()
		return rObj.newRequest()
	}
	dur := rObj.predictedDuration
	rObj.mux.Unlock()
	if isInitial && dur <= rObj.waitTime {
		time.Sleep(dur)
		return rObj.ServeRequest(false)
	}
	return nil, NewClientError("limit is reached, please try again later")
}

// EndRequest finishes request and removes it from the list of simultaneous
// requests. Method adjustes `predictedDuration` value based on `endState` code.
func (rObj *RatelimitObject) EndRequest(request *ActiveRequest, endState endRequestState) {
	if request == nil {
		return
	}
	rObj.mux.Lock()
	rObj.activeRequestsCount--
	elapsedTime := time.Since(request.startedAt)
	// if we got an error we should adjust currentMaxRequestsLevel by decreasing
	// it according spent time on query
	if endState != endRequestStateOK {
		if rObj.currentMaxRequestsLevel > rObj.minRequests {
			if elapsedTime > rObj.predictedDuration || elapsedTime > rObj.waitTime {
				rObj.currentMaxRequestsLevel -= (rObj.currentMaxRequestsLevel - rObj.minRequests) / 2
			} else {
				rObj.currentMaxRequestsLevel--
			}
		}
	} else if rObj.currentMaxRequestsLevel < rObj.maxRequests {
		// if endState is OK, we would like increment amount of
		// currentMaxRequestsLevel
		rObj.currentMaxRequestsLevel++
	}

	// we do not want update prediction if endState == endRequestStateError
	if endState == endRequestStateError {
		rObj.mux.Unlock()
		return
	}
	// if real request time is longer than expected we will update prediction to
	// the real request time
	if elapsedTime >= rObj.predictedDuration {
		rObj.predictedDuration = elapsedTime
	} else {
		// if real request time is less than expected we decrease it on an half
		// of difference
		rObj.predictedDuration -= (elapsedTime + rObj.predictedDuration) / 2
	}
	rObj.mux.Unlock()
}

// initiate helps initialize ratelimitObject with default values
func (rObj *RatelimitObject) initiate() *RatelimitObject {
	if rObj.waitTime == 0 {
		rObj.waitTime = waitTimeDefault
	}
	if rObj.predictedDuration == 0 {
		rObj.predictedDuration = rObj.waitTime
	}
	if rObj.maxRequests == 0 {
		rObj.maxRequests = maxSimultaneousRequestsDefault
		rObj.currentMaxRequestsLevel = maxSimultaneousRequestsDefault
	}
	if rObj.minRequests == 0 {
		rObj.minRequests = minSimultaneousRequestsDefault
	}
	return rObj
}

// newRequest creates activeRequest object. Method grants that request will be
// served
func (rObj *RatelimitObject) newRequest() (*ActiveRequest, error) {
	now := time.Now()
	rObj.mux.Lock()
	predictedAt := now.Add(rObj.predictedDuration)
	rObj.activeRequestsCount++
	rObj.mux.Unlock()
	req := ActiveRequest{
		startedAt:   now,
		predictedAt: predictedAt,
	}
	return &req, nil
}
