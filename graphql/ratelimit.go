package graphql

import (
	"sync"
	"time"

	"github.com/satori/go.uuid"
)

type endRequestState int

const (
	maxSimultaneousRequestsDefault int             = 15
	minSimultaneousRequestsDefault int             = 2
	waitTimeDefault                time.Duration   = 3 * time.Second
	endRequestStateOK              endRequestState = 0
	endRequestStateError           endRequestState = 1
	endRequestStateTimedOut        endRequestState = 2
)

// RatelimitObject represents structure of ratelimit object
type RatelimitObject struct {
	// requestsBucket a list of all simultaneous requests
	requestsBucket map[uuid.UUID]activeRequest
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
	waitTime time.Duration
	mux      sync.Mutex
}

// activeRequest provides structure for request that processing by service
// stores id, count of simultaneous requests on moment of starting request,
// date of request start and predicted request duration
type activeRequest struct {
	bucketLevel int
	startedAt   time.Time
	predictedAt time.Time
}

// RatelimitHandlerDefault creates ratelimit object with empty values
func RatelimitHandlerDefault() *RatelimitObject {
	rObj := &RatelimitObject{}
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

// initiate helps initialize ratelimitObject with default values
func (rObj *RatelimitObject) initiate() *RatelimitObject {
	rObj.requestsBucket = make(map[uuid.UUID]activeRequest)
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
func (rObj *RatelimitObject) newRequest() (uuid.UUID, error) {
	connID := uuid.NewV4()
	rObj.mux.Lock()
	predictedAt := time.Now().Add(rObj.predictedDuration)
	rObj.requestsBucket[connID] = activeRequest{
		bucketLevel: len(rObj.requestsBucket) + 1,
		startedAt:   time.Now(),
		predictedAt: predictedAt,
	}
	rObj.mux.Unlock()
	return connID, nil
}

// ServeRequest decides put request into work or not. If amount of simultaneous
// requests is more than allowed (`currentMaxRequestsLevel` level), method will
// wait for up to `RatelimitObject.waitTime` and repeat attempt to start request. If
// request started: returns UUID of request, otherwise returns empty UUID and
// error
func (rObj *RatelimitObject) ServeRequest() (uuid.UUID, error) {
	for i := 0; i < 2; i++ {
		rObj.mux.Lock()
		if len(rObj.requestsBucket) < rObj.currentMaxRequestsLevel {
			rObj.mux.Unlock()
			return rObj.newRequest()
		}
		dur := rObj.predictedDuration
		rObj.mux.Unlock()
		if dur <= rObj.waitTime {
			time.Sleep(dur)
		}
	}
	return uuid.Nil, NewClientError("limit is reached, please try again later")
}

// EndRequest finishes request and removes it from the list of simultaneous
// requests. Method adjustes `predictedDuration` value based on `endState` code.
func (rObj *RatelimitObject) EndRequest(connID uuid.UUID, endState endRequestState) {
	if connID == uuid.Nil {
		return
	}
	rObj.mux.Lock()
	request, ok := rObj.requestsBucket[connID]
	delete(rObj.requestsBucket, connID)
	// we do not want update prediction if endState == endRequestStateError or
	// somehow connection was not found
	if !ok || endState == endRequestStateError {
		rObj.mux.Unlock()
		return
	}
	// if connection was timeouted, we would like to decrease
	// currentMaxRequestsLevel or set it to level of simultaneous requests,
	// which was when request has been accepted
	if endState == endRequestStateTimedOut {
		if request.bucketLevel <= rObj.minRequests {
			rObj.currentMaxRequestsLevel = rObj.minRequests
		} else if request.bucketLevel > rObj.currentMaxRequestsLevel {
			rObj.currentMaxRequestsLevel--
		} else {
			rObj.currentMaxRequestsLevel = request.bucketLevel
		}
	} else if rObj.currentMaxRequestsLevel < rObj.maxRequests {
		// if endState is OK, we would like increment amount of
		// currentMaxRequestsLevel
		rObj.currentMaxRequestsLevel++
	}
	elapsedTime := time.Since(request.startedAt)
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
