package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lib/pq"
)

func TestPgCode(t *testing.T) {
	wrapped := fmt.Errorf("create cluster: %w", &pq.Error{Code: pgUniqueViolation})
	if got := pgCode(wrapped); got != pgUniqueViolation {
		t.Errorf("pgCode(wrapped unique violation) = %q", got)
	}
	if got := pgCode(errors.New("boom")); got != "" {
		t.Errorf("pgCode(non-pq error) = %q", got)
	}
	if got := pgCode(nil); got != "" {
		t.Errorf("pgCode(nil) = %q", got)
	}
}
