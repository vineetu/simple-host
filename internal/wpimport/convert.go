package wpimport

import (
	"context"
	"errors"
)

// convert is replaced by the real converter.
func convert(ctx context.Context, in Input) (*Result, error) {
	return nil, errors.New("not implemented")
}
