package schemabuilder

import (
	"fmt"
	"testing"
)

type resolver struct {
}

func (r resolver) Resolve() {}

// TODO: finish it
func TestPrepareResolveArgs(t *testing.T) {
	s := Schema{}
	schema, err := s.Build()
	if err != nil {
		t.Errorf("Unexpected error: '%s'", err)
	}
	fmt.Print(schema)
}
