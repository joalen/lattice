package ports

import (
	"context"
	"time"
)

const cleanupTimeout = 5 * time.Second

// DeleteDetached removes temporary keys even when ctx is already cancelled or
// expired, so an abandoned request does not leave them behind.
func DeleteDetached(ctx context.Context, s SetStore, keys ...string) {
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	_ = s.Delete(ctx, keys...)
}
