package schemabuilder

import (
	"fmt"
	"testing"
)

type resolver struct {

}

func (r resolver) Resolve(){}

func TestPrepareResolveArgs(t *testing.T){
	s := Schema{}
	schema, err := s.Build()
	if err != nil {
		fmt.Println(err)
	}

	s.
}
