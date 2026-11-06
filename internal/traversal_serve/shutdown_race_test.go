package traversal_serve

import (
	"context"
	"testing"
)

// A session whose host sent shutdown ends cleanly even when the plugin's
// context was cancelled too, as a plugin cancelling on stdin EOF does
// (cutting-garden#342); without a shutdown the cancellation is the result.
func TestCancelledSessionEnd(t *testing.T) {
	received := make(chan struct{})
	close(received)
	if err := cancelledSessionEnd(context.Canceled, received); err != nil {
		t.Errorf("shutdown received, yet the session ended with %v", err)
	}

	if err := cancelledSessionEnd(context.Canceled, make(chan struct{})); err == nil {
		t.Error("no shutdown received, yet the cancelled session ended cleanly")
	}
}
