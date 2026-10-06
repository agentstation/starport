package blob

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

const (
	replaceRetryFirstDelay = time.Millisecond
	replaceRetryMaxDelay   = 50 * time.Millisecond
	replaceRetryBound      = 2 * time.Second
)

// retryBlockedReplace retries a replace that another open handle blocks.
// Windows refuses to replace a file while a reader or another process holds
// it, and it reports access denied or a sharing violation until the holder
// closes it. The retry stops at the bound or when the context ends.
func retryBlockedReplace(ctx context.Context, replace func() error) error {
	deadline := time.Now().Add(replaceRetryBound)
	delay := replaceRetryFirstDelay
	for {
		err := replace()
		if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return err
		}
		if time.Until(deadline) < delay {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(delay):
		}
		delay = min(2*delay, replaceRetryMaxDelay)
	}
}
