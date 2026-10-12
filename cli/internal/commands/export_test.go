package commands

import (
	"testing"
	"time"
)

func SetFollowPollInterval(t *testing.T, d time.Duration) {
	prev := followPollInterval
	followPollInterval = d
	t.Cleanup(func() { followPollInterval = prev })
}
