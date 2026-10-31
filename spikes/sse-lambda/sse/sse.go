// Package sse writes Server-Sent Events. Spike 1.3 uses it to test whether
// events streamed from Lambda reach the client one by one.
package sse

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Stream writes n "tick" events to w, the first at once and then one every
// interval. Each event goes out in a single Write, so a streaming transport
// can forward it immediately. It stops early with ctx's error if ctx ends.
func Stream(ctx context.Context, w io.Writer, n int, every time.Duration) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for i := 1; i <= n; i++ {
		if i > 1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		// sent_ms lets the client compare send time with arrival time.
		event := fmt.Sprintf("id: %d\nevent: tick\ndata: {\"i\":%d,\"sent_ms\":%d}\n\n", i, i, time.Now().UnixMilli())
		if _, err := io.WriteString(w, event); err != nil {
			return err
		}
	}
	return nil
}
