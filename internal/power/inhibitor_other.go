//go:build !darwin && !linux

package power

import (
	"context"
	"fmt"
)

func startInhibitorProcess(ctx context.Context) (func(), error) {
	_ = ctx
	return nil, fmt.Errorf("sleep inhibition is not supported on this platform")
}
