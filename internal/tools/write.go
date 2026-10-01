package tools

import (
	"context"
	"errors"
)

// write runs a write view. Filled in with the write framework.
func (d Deps) write(ctx context.Context, tool string, v View, in Input) (any, error) {
	return nil, errors.New("writes are not implemented yet")
}
