package graphql

import (
	"bytes"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

type SanitizedError interface {
	error
	SanitizedError() string
}

type SafeError struct {
	inner   error
	code    string
	message string
}

type ClientError SafeError

const maxDepth = 100

type errorExtensions struct {
	Code      string    `json:"code,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type graphQLError struct {
	Message    string          `json:"message"`
	Path       []string        `json:"path"`
	Extensions errorExtensions `json:"extensions,omitempty"`
}

type pathError struct {
	inner error
	path  []string
}

func newGraphQLError(err error) graphQLError {
	return newGraphQLErrorRecursive(err, 0)
}

func flattenError(err error, depth int) error {
	if depth >= maxDepth {
		panic("recursion depth has exceeded the limit")
	}
	switch e := err.(type) {
	case *pathError:
		fErr := flattenError(e.inner, depth+1)
		return fErr
	default:
		return e
	}
}

func newGraphQLErrorRecursive(err error, depth int) graphQLError {
	if depth >= maxDepth {
		panic("recursion depth has exceeded the limit for GraphQL error")
	}
	switch e := err.(type) {
	case *pathError:
		gErr := newGraphQLErrorRecursive(e.inner, depth+1)
		gErr.Path = e.Path()
		return gErr
	case ClientError:
		return graphQLError{
			Message: sanitizeError(e),
			Extensions: errorExtensions{
				Code:      e.code,
				Timestamp: time.Now().UTC(),
			},
		}
	default:
		return newInternalError(e)
	}
}

func newInternalError(err error) graphQLError {
	return newGraphQLError(NewError("INTERNAL_SERVER_ERROR", sanitizeError(err)))
}

func nestPathErrorMulti(path []string, err error) error {
	// Don't nest SanitzedError's, as they are intended for human consumption.
	if se, ok := err.(SanitizedError); ok {
		return se
	}

	if pe, ok := err.(*pathError); ok {
		return &pathError{
			inner: pe.inner,
			path:  append(pe.path, path...),
		}
	}

	return &pathError{
		inner: err,
		path:  path,
	}
}

func nestPathError(key string, err error) error {
	if pe, ok := err.(*pathError); ok {
		return &pathError{
			inner: pe.inner,
			path:  append(pe.path, key),
		}
	}

	return &pathError{
		inner: err,
		path:  []string{key},
	}
}

func (pe *pathError) Path() []string {
	p := []string{}
	for i := len(pe.path) - 1; i >= 0; i-- {
		p = append(p, pe.path[i])
	}
	return p
}

func ErrorCause(err error) error {
	if pe, ok := err.(*pathError); ok {
		return pe.inner
	}
	return err
}

func (pe *pathError) Unwrap() error {
	return pe.inner
}

func (pe *pathError) Error() string {
	var buffer bytes.Buffer
	for i := len(pe.path) - 1; i >= 0; i-- {
		if i < len(pe.path)-1 {
			buffer.WriteString(".")
		}
		buffer.WriteString(pe.path[i])
	}
	buffer.WriteString(": ")
	buffer.WriteString(pe.inner.Error())
	return buffer.String()
}

func (e ClientError) Error() string {
	return e.message
}

func (e ClientError) SanitizedError() string {
	return e.message
}

func (e SafeError) Error() string {
	return e.message
}

func (e SafeError) SanitizedError() string {
	return e.message
}

// Unwrap returns the wrapped error, implementing go's 1.13 error wrapping proposal.
func (e SafeError) Unwrap() error {
	return e.inner
}

func NewError(code string, format string, a ...interface{}) error {
	return ClientError{message: fmt.Sprintf(format, a...), code: code}
}

func NewClientError(format string, a ...interface{}) error {
	return ClientError{message: fmt.Sprintf(format, a...)}
}

func NewSafeError(format string, a ...interface{}) error {
	return SafeError{message: fmt.Sprintf(format, a...)}
}

// WrapAsSafeError wraps an error into a "SafeError", and takes in a message.
// This message can be used like fmt.Sprintf to take in formatting and arguments.
func WrapAsSafeError(err error, format string, a ...interface{}) error {
	return SafeError{inner: err, message: fmt.Sprintf(format, a...)}
}

func sanitizeError(err error) string {
	if sanitized, ok := err.(SanitizedError); ok {
		return sanitized.SanitizedError()
	}
	return "Internal server error"
}

func isCloseError(err error) bool {
	_, ok := err.(*websocket.CloseError)
	return ok || err == websocket.ErrCloseSent
}
