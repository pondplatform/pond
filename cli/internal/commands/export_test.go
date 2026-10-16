package commands

import (
	"testing"
	"time"
)

func SetPollInterval(t *testing.T, d time.Duration) {
	prev := pollInterval
	pollInterval = d
	t.Cleanup(func() { pollInterval = prev })
}

func SetHeartbeatInterval(t *testing.T, d time.Duration) {
	prev := heartbeatInterval
	heartbeatInterval = d
	t.Cleanup(func() { heartbeatInterval = prev })
}

// ForceLiveProgress renders deploy --wait as on a terminal of the given width.
func ForceLiveProgress(t *testing.T, width int) {
	prev := forceLiveWidth
	forceLiveWidth = width
	t.Cleanup(func() { forceLiveWidth = prev })
}
