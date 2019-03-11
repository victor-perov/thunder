package graphql

import (
	"testing"
	"time"
)

func addRequest(rObj *RatelimitObject, t *testing.T) *ActiveRequest {
	req, err := rObj.newRequest()
	if err != nil {
		t.Fatalf("error occured, when it was unexpected: %v", err)
	}
	if req == nil {
		t.Fatal("connection ID should not be nil")
	}
	return req
}
func TestInitiating(t *testing.T) {
	rObj := RatelimitHandlerDefault()
	if rObj.predictedDuration == 0 {
		t.Fatal("initiate() should set it equal to `waitTime` value")
	}

	rObj.predictedDuration = time.Duration(10 * time.Second)
	rObj.initiate()

	if rObj.predictedDuration != time.Duration(10*time.Second) {
		t.Fatal("initiate() should not update predictedDuration if it has already set")
	}
}

func TestNewRequest(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(10*time.Second))
	addRequest(rObj, t)
	cnt := rObj.GetSimultaneousRequestsCount()
	if cnt != 1 {
		t.Fatalf("requests bucket should has size of 1 element, but %d got", cnt)
	}
	addRequest(rObj, t)
	cnt = rObj.GetSimultaneousRequestsCount()
	if cnt != 2 {
		t.Fatalf("requests bucket should has size of 1 element, but %d got", cnt)
	}
}

func TestConcurrentServeRequest(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(10*time.Second))
	for i := 0; i <= 10; i++ {
		go rObj.ServeRequest(true)
	}
	time.Sleep(100 * time.Millisecond)
	cnt := rObj.GetSimultaneousRequestsCount()
	if cnt != 10 {
		t.Fatalf("requests bucket should has size of 10 elements, but %d got", cnt)
	}
}

func TestConcurrentServeRequestAndEndRequest(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(10*time.Second))
	for i := 0; i <= 9; i++ {
		go func() {
			req, _ := rObj.ServeRequest(true)
			time.Sleep(100 * time.Millisecond)
			rObj.EndRequest(req, endRequestStateOK)
		}()
	}
	time.Sleep(1000 * time.Millisecond)
	cnt := rObj.GetSimultaneousRequestsCount()
	if cnt != 0 {
		t.Fatalf("requests bucket should be empty, but %d got", cnt)
	}
}

func TestConcurrentServeRequestWhenLimitIsReached(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(10*time.Second))
	rObj.currentMaxRequestsLevel = 2
	rObj.activeRequestsCount = 1
	for i := 0; i <= 9; i++ {
		go func() {
			req, _ := rObj.ServeRequest(true)
			time.Sleep(100 * time.Millisecond)
			rObj.EndRequest(req, endRequestStateOK)
		}()
	}
	time.Sleep(1000 * time.Millisecond)
	cnt := rObj.GetSimultaneousRequestsCount()
	if cnt != 1 {
		t.Fatalf("requests bucket should has one element, but %d got", cnt)
	}
}

func TestServeRequestWhenLimitIsReached(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(1*time.Second))
	rObj.currentMaxRequestsLevel = 2
	rObj.activeRequestsCount = 1

	req, err := rObj.ServeRequest(true)
	if err != nil {
		t.Fatalf("unexpected error occured: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	rObj.EndRequest(req, endRequestStateOK)
	cnt := rObj.GetSimultaneousRequestsCount()

	if cnt != 1 {
		t.Fatalf("requests bucket should has one element, but %d got", cnt)
	}
}

func TestServeRequestWhenLimitIsReachedShouldReturnAnError(t *testing.T) {
	rObj := RatelimitHandler(10, 2, time.Duration(100*time.Millisecond))
	rObj.currentMaxRequestsLevel = 2
	rObj.activeRequestsCount = 10

	_, err := rObj.ServeRequest(true)
	if err == nil {
		t.Fatalf("expected error did not occured")
	}
}

func TestEndrequest(t *testing.T) {
	waitTime := time.Duration(2 * time.Second)
	rObj := RatelimitHandler(2, 1, waitTime)

	req := addRequest(rObj, t)
	processingTime := time.Duration(100 * time.Millisecond)
	time.Sleep(processingTime)
	rObj.EndRequest(req, endRequestStateOK)
	if rObj.GetSimultaneousRequestsCount() != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	updatedPredicted := rObj.predictedDuration
	if updatedPredicted >= waitTime {
		t.Fatalf("predictedDuration should be decreased, but it is not: was %v, now %v", waitTime, updatedPredicted)
	}

	req = addRequest(rObj, t)
	time.Sleep(processingTime)
	rObj.EndRequest(req, endRequestStateError)
	if rObj.GetSimultaneousRequestsCount() != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	if rObj.predictedDuration != updatedPredicted {
		t.Fatal("predictedDuration should not be changed")
	}
}

func TestEndrequestWith504(t *testing.T) {
	waitTime := time.Duration(2 * time.Second)
	rObj := RatelimitHandler(5, 1, waitTime)
	req := addRequest(rObj, t)
	processingTime := time.Duration(100 * time.Millisecond)

	time.Sleep(waitTime + processingTime)
	rObj.EndRequest(req, endRequestStateCanceled)
	if rObj.GetSimultaneousRequestsCount() != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	if rObj.predictedDuration < waitTime {
		t.Fatal("predictedDuration should increase, but it is not")
	}
	if rObj.currentMaxRequestsLevel != 3 {
		t.Fatalf("current max level of bucket should be eq 3: %d", rObj.currentMaxRequestsLevel)
	}
	req = addRequest(rObj, t)
	rObj.EndRequest(req, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 4 {
		t.Fatalf("current max level of bucket should be eq 4: %d", rObj.currentMaxRequestsLevel)
	}
	req = addRequest(rObj, t)
	rObj.EndRequest(req, endRequestStateCanceled)
	if rObj.currentMaxRequestsLevel != 3 {
		t.Fatalf("current max level of bucket should be eq 3: %d", rObj.currentMaxRequestsLevel)
	}
	req = addRequest(rObj, t)
	rObj.EndRequest(req, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 4 {
		t.Fatalf("current max level of bucket should be eq 4: %d", rObj.currentMaxRequestsLevel)
	}
	req = addRequest(rObj, t)
	rObj.EndRequest(req, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 5 {
		t.Fatalf("current max level of bucket should be eq 5 (max): %d", rObj.currentMaxRequestsLevel)
	}
	addRequest(rObj, t)
	req = addRequest(rObj, t)
	rObj.EndRequest(req, endRequestStateCanceled)
	if rObj.currentMaxRequestsLevel != 4 {
		t.Fatalf("current max level of bucket should changed to 4, but got: %d", rObj.currentMaxRequestsLevel)
	}
}

func TestServeRequestReturnNoError(t *testing.T) {
	rObj := RatelimitHandler(2, 1, time.Duration(2*time.Second))
	connID, err := rObj.ServeRequest(true)
	if err != nil {
		t.Fatalf("error is not expected: %v", err)
	}
	if rObj.GetSimultaneousRequestsCount() != 1 {
		t.Fatal("request is not in the bucket, when it should be")
	}

	rObj.EndRequest(connID, endRequestStateOK)
	if rObj.GetSimultaneousRequestsCount() != 0 {
		t.Fatal("request should be deleted")
	}
}

func TestServeRequestReturnError(t *testing.T) {
	rObj := RatelimitHandler(2, 1, time.Duration(100*time.Millisecond))
	if _, err := rObj.ServeRequest(true); err != nil {
		t.Fatalf("we got an unexpected error: %v", err)
	}
	if _, err := rObj.ServeRequest(true); err != nil {
		t.Fatalf("we got an unexpected error: %v", err)
	}
	if _, err := rObj.ServeRequest(true); err == nil {
		t.Fatalf("we got an unexpected error: %v", err)
	}
}
