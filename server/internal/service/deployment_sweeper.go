package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pondplatform/pond/server/internal/store"
)

// SweepConfig sets how often the sweeper runs and when it gives up on work.
// A zero field takes its default.
type SweepConfig struct {
	// Interval between sweeps. Default 15s.
	Interval time.Duration
	// CommandTimeout is how long an agent may hold a command without
	// reporting a result before the command fails. Default 1h.
	CommandTimeout time.Duration
	// StallTimeout is how long a deployment may go without progress, while
	// every attempt to reconcile it fails, before it fails. Default 10m.
	StallTimeout time.Duration
}

func (c SweepConfig) withDefaults() SweepConfig {
	if c.Interval <= 0 {
		c.Interval = 15 * time.Second
	}
	if c.CommandTimeout <= 0 {
		c.CommandTimeout = time.Hour
	}
	if c.StallTimeout <= 0 {
		c.StallTimeout = 10 * time.Minute
	}
	return c
}

// sweepBatch bounds the work of one sweep; the rest waits for the next one.
const sweepBatch = 100

// Sweeper finishes what a failed or lost step left undone, so no deployment
// waits forever. Every replica runs one: each step it takes locks the
// deployment and re-checks, so concurrent sweepers do the work once.
type Sweeper struct {
	repos        store.Repos
	orchestrator *orchestrator
	cfg          SweepConfig
	log          *slog.Logger
	now          func() time.Time
}

// Run sweeps every interval until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Sweeper) sweep(ctx context.Context) {
	s.timeOutCommands(ctx)
	s.reconcileStalled(ctx)
}

// timeOutCommands fails commands whose agent never reported a result, for
// instance because the dispatch or the result was lost.
func (s *Sweeper) timeOutCommands(ctx context.Context) {
	cmds, err := s.repos.Deployments.ListDispatchedCommandsBefore(ctx, s.now().Add(-s.cfg.CommandTimeout), sweepBatch)
	if err != nil {
		s.log.Error("list timed-out commands", "err", err)
		return
	}
	reason := fmt.Sprintf("the agent did not report a result within %s", s.cfg.CommandTimeout)
	for _, cmd := range cmds {
		s.log.Warn("command timed out", "command_id", cmd.ID, "deployment_id", cmd.DeploymentID, "type", cmd.Type)
		if err := s.orchestrator.TimeOutCommand(ctx, cmd, reason); err != nil {
			s.log.Error("time out command", "command_id", cmd.ID, "err", err)
		}
	}
}

// reconcileStalled retries deployments a failed step left with nothing in
// flight. One that still cannot move after StallTimeout fails, so the user
// gets an answer instead of a deployment pending forever.
func (s *Sweeper) reconcileStalled(ctx context.Context) {
	stalled, err := s.repos.Deployments.ListStalledDeployments(ctx, sweepBatch)
	if err != nil {
		s.log.Error("list stalled deployments", "err", err)
		return
	}
	for _, d := range stalled {
		_, err := s.orchestrator.Reconcile(ctx, d.ID)
		if err == nil {
			continue
		}
		stalledFor := s.now().Sub(d.LastProgressAt)
		if stalledFor < s.cfg.StallTimeout {
			s.log.Warn("reconcile stalled deployment; will retry", "deployment_id", d.ID, "stalled_for", stalledFor, "err", err)
			continue
		}
		s.log.Error("giving up on stalled deployment", "deployment_id", d.ID, "stalled_for", stalledFor, "err", err)
		reason := fmt.Sprintf("the server could not advance the deployment for %s; its logs for deployment %s have the cause",
			stalledFor.Round(time.Second), d.ID)
		if err := s.orchestrator.Fail(ctx, d.ID, reason); err != nil {
			s.log.Error("fail stalled deployment", "deployment_id", d.ID, "err", err)
		}
	}
}
