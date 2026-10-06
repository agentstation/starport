//go:build !windows

package blob

import "context"

// retryBlockedReplace runs the replace once. A POSIX rename replaces the
// target atomically even while another handle holds it open.
func retryBlockedReplace(_ context.Context, replace func() error) error {
	return replace()
}
