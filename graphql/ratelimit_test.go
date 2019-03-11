package graphql

import (
	"testing"
	"time"

	"github.com/satori/go.uuid"
)

func addRequest(rObj *RatelimitObject, t *testing.T) uuid.UUID {
	ID, err := rObj.newRequest()
	if err != nil {
		t.Fatalf("error occured, when it was unexpected: %v", err)
	}
	if ID == uuid.Nil {
		t.Fatal("connection ID should not be nil")
	}
	return ID
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
	if cnt := len(rObj.requestsBucket); cnt != 1 {
		t.Fatalf("requests bucket should has size of 1 element, but %d got", cnt)
	}
	addRequest(rObj, t)

	if cnt := len(rObj.requestsBucket); cnt != 2 {
		t.Fatalf("requests bucket should has size of 1 element, but %d got", cnt)
	}
}

func TestEndrequest(t *testing.T) {
	waitTime := time.Duration(2 * time.Second)
	rObj := RatelimitHandler(2, 1, waitTime)
	connID := addRequest(rObj, t)
	rObj.EndRequest(uuid.NewV4(), endRequestStateOK)
	if len(rObj.requestsBucket) != 1 {
		t.Fatal("request should not be deleted form the bucket")
	}
	processingTime := time.Duration(100 * time.Millisecond)
	time.Sleep(processingTime)
	rObj.EndRequest(connID, endRequestStateOK)
	if len(rObj.requestsBucket) != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	updatedPredicted := rObj.predictedDuration
	if updatedPredicted >= waitTime {
		t.Fatal("predictedDuration should be decreased, but it is not")
	}

	connID = addRequest(rObj, t)
	time.Sleep(processingTime)
	rObj.EndRequest(connID, endRequestStateError)
	if len(rObj.requestsBucket) != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	if rObj.predictedDuration != updatedPredicted {
		t.Fatal("predictedDuration should not be changed")
	}
}

func TestEndrequestWith504(t *testing.T) {
	waitTime := time.Duration(2 * time.Second)
	rObj := RatelimitHandler(4, 1, waitTime)
	connID := addRequest(rObj, t)
	processingTime := time.Duration(100 * time.Millisecond)
	time.Sleep(processingTime)
	rObj.EndRequest(connID, endRequestStateTimedOut)
	if len(rObj.requestsBucket) != 0 {
		t.Fatal("request should be deleted form the bucket")
	}
	if rObj.predictedDuration > waitTime {
		t.Fatal("predictedDuration should be decreased, but it is not")
	}
	if rObj.currentMaxRequestsLevel != 1 {
		t.Fatalf("current max level of bucket should be decreased: %d", rObj.currentMaxRequestsLevel)
	}
	connID = addRequest(rObj, t)
	rObj.EndRequest(connID, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 2 {
		t.Fatalf("current max level of bucket should increase: %d", rObj.currentMaxRequestsLevel)
	}
	connID = addRequest(rObj, t)
	rObj.EndRequest(connID, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 3 {
		t.Fatalf("current max level of bucket should increase: %d", rObj.currentMaxRequestsLevel)
	}
	connID = addRequest(rObj, t)
	rObj.EndRequest(connID, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 4 {
		t.Fatalf("current max level of bucket should increase: %d", rObj.currentMaxRequestsLevel)
	}
	connID = addRequest(rObj, t)
	rObj.EndRequest(connID, endRequestStateOK)
	if rObj.currentMaxRequestsLevel != 4 {
		t.Fatalf("current max level of bucket should not change: %d", rObj.currentMaxRequestsLevel)
	}
	addRequest(rObj, t)
	connID = addRequest(rObj, t)
	rObj.EndRequest(connID, endRequestStateTimedOut)
	if rObj.currentMaxRequestsLevel != 2 {
		t.Fatalf("current max level of bucket should changed to 2, but got: %d", rObj.currentMaxRequestsLevel)
	}
}

func TestServeRequestReturnNoError(t *testing.T) {
	rObj := RatelimitHandler(2, 1, time.Duration(2*time.Second))
	connID, err := rObj.ServeRequest(true)
	if err != nil {
		t.Fatalf("error is not expected: %v", err)
	}
	if len(rObj.requestsBucket) != 1 {
		t.Fatal("request is not in the bucket, when it should be")
	}

	rObj.EndRequest(connID, endRequestStateOK)
	if len(rObj.requestsBucket) != 0 {
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
