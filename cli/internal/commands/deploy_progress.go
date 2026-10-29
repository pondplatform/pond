package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pondplatform/pond/cli/internal/client"
	"github.com/pondplatform/pond/shared/agent/wire"
	api "github.com/pondplatform/pond/shared/server/api"
	"golang.org/x/term"
)

var (
	heartbeatInterval = 30 * time.Second
	spinnerInterval   = 100 * time.Millisecond
	forceLiveWidth    int
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

const clearLine = "\r\x1b[2K"

// progress shows what a deployment is doing. On a terminal it redraws one
// spinner line in place; anywhere else (CI logs, pipes) it prints a plain line
// per change and a heartbeat while nothing changes.
type progress struct {
	out   io.Writer
	width func() int // nil in plain mode

	mu        sync.Mutex
	text      string
	tail      string
	since     time.Time
	frame     int
	drawn     bool
	lastPrint time.Time

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

func newProgress(out io.Writer) *progress {
	p := &progress{out: out, width: liveWidth(out)}
	if p.width != nil {
		p.stop = make(chan struct{})
		p.done = make(chan struct{})
		go p.spin()
	}
	return p
}

func liveWidth(out io.Writer) func() int {
	if forceLiveWidth > 0 {
		w := forceLiveWidth
		return func() int { return w }
	}
	f, ok := out.(*os.File)
	if !ok || os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return func() int {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
		return 80
	}
}

// Update sets what the deployment is doing now and the latest log line of it.
func (p *progress) Update(text, tail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.tail = tail
	if text != p.text {
		p.text, p.since = text, now
		if p.width == nil {
			fmt.Fprintln(p.out, text)
			p.lastPrint = now
		} else {
			p.draw(now)
		}
		return
	}
	if p.width == nil && now.Sub(p.lastPrint) >= heartbeatInterval {
		fmt.Fprintln(p.out, p.line(now))
		p.lastPrint = now
	}
}

// Println prints a line that stays, above the live line.
func (p *progress) Println(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.drawn {
		fmt.Fprint(p.out, clearLine)
		p.drawn = false
	}
	fmt.Fprintln(p.out, line)
	if p.width != nil {
		p.draw(time.Now())
	}
}

// Stop removes the live line. It is safe to call more than once.
func (p *progress) Stop() {
	p.stopOnce.Do(func() {
		if p.width == nil {
			return
		}
		close(p.stop)
		<-p.done
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.drawn {
			fmt.Fprint(p.out, clearLine)
			p.drawn = false
		}
	})
}

func (p *progress) spin() {
	defer close(p.done)
	t := time.NewTicker(spinnerInterval)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case now := <-t.C:
			p.mu.Lock()
			p.frame++
			p.draw(now)
			p.mu.Unlock()
		}
	}
}

func (p *progress) draw(now time.Time) {
	if p.text == "" {
		return
	}
	s := string(spinnerFrames[p.frame%len(spinnerFrames)]) + " " + p.line(now)
	fmt.Fprint(p.out, clearLine+truncate(s, p.width()-1))
	p.drawn = true
}

func (p *progress) line(now time.Time) string {
	s := p.text + " · " + formatDuration(now.Sub(p.since))
	if p.tail != "" {
		s += " · " + p.tail
	}
	return s
}

func truncate(s string, width int) string {
	if width < 1 || utf8.RuneCountInString(s) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}

func formatDuration(d time.Duration) string {
	return max(d, 0).Round(time.Second).String()
}

// deploySteps names the steps of a deployment: one per managed dependency
// (its tofu.apply) and the rollout (helm.upgrade). A delete has one step, its
// service.delete.
type deploySteps struct {
	service string
	deps    map[uuid.UUID]api.DependencyDeploymentSummary // keyed by command ID
}

func newDeploySteps(d *api.Deployment, service string) deploySteps {
	s := deploySteps{service: service, deps: map[uuid.UUID]api.DependencyDeploymentSummary{}}
	for _, dep := range d.Dependencies {
		if dep.CommandID != nil {
			s.deps[*dep.CommandID] = dep
		}
	}
	return s
}

// label returns what a command does, as an ongoing and as a finished action.
func (s deploySteps) label(cmd api.CommandSummary) (doing, done string) {
	switch cmd.Type {
	case wire.CommandTofuApply:
		name := "dependency"
		if dep, ok := s.deps[cmd.ID]; ok {
			name = fmt.Sprintf("%s (%s)", dep.Name, dep.Type)
		}
		return "Provisioning " + name, "Provisioned " + name
	case wire.CommandHelmUpgrade:
		return "Rolling out " + s.service, "Rolled out " + s.service
	case wire.CommandServiceDelete:
		return "Deleting " + s.service, "Deleted " + s.service
	}
	return cmd.Type, cmd.Type
}

// finished describes a command that has ended, with how long it took.
func (s deploySteps) finished(cmd api.CommandSummary) string {
	doing, done := s.label(cmd)
	took := formatDuration(cmd.UpdatedAt.Sub(cmd.CreatedAt))
	switch cmd.Status {
	case api.CommandStatusSucceeded:
		return fmt.Sprintf("✓ %s  %s", done, took)
	case api.CommandStatusFailed:
		return fmt.Sprintf("✗ %s failed after %s", doing, took)
	}
	return fmt.Sprintf("- %s %s", doing, cmd.Status)
}

// current describes what the deployment is doing now, and which command's
// log shows it best (nil when no command is running).
func (s deploySteps) current(d *api.Deployment) (string, *uuid.UUID) {
	total, n := len(d.Commands), 1
	// The final command, a deploy's helm.upgrade or a delete's service.delete,
	// is counted before it exists.
	final := false
	var running, queued []api.CommandSummary
	for _, cmd := range d.Commands {
		final = final || cmd.Type == wire.CommandHelmUpgrade || cmd.Type == wire.CommandServiceDelete
		switch cmd.Status {
		case api.CommandStatusSucceeded:
			n++
		case api.CommandStatusDispatched:
			running = append(running, cmd)
		case api.CommandStatusQueued:
			queued = append(queued, cmd)
		}
	}
	if !final {
		total++
	}
	n = min(n, total)

	var text string
	switch {
	case d.Status == api.DeploymentStatusCancelling && len(running) > 0:
		text = "Cancelling: waiting for the agent to stop " + lowerFirst(s.join(running))
	case d.Status == api.DeploymentStatusCancelling:
		text = "Cancelling"
	case len(running) > 0:
		text = s.join(running)
	case len(queued) > 0:
		text = s.join(queued) + ": waiting for the agent"
	default:
		text = "Preparing rollout"
	}

	var tail *uuid.UUID
	if len(running) > 0 {
		tail = &running[0].ID
	}
	return fmt.Sprintf("[%d/%d] %s", n, total, text), tail
}

// join names several commands, sharing the verb when they have the same one:
// "Provisioning cache (redis), db (postgres)".
func (s deploySteps) join(cmds []api.CommandSummary) string {
	var labels []string
	verb := ""
	for i, cmd := range cmds {
		doing, _ := s.label(cmd)
		v, _, _ := strings.Cut(doing, " ")
		if i == 0 {
			verb = v
		} else if v != verb {
			verb = ""
		}
		labels = append(labels, doing)
	}
	if verb == "" || len(labels) == 1 {
		return strings.Join(labels, ", ")
	}
	for i := 1; i < len(labels); i++ {
		labels[i] = strings.TrimPrefix(labels[i], verb+" ")
	}
	return strings.Join(labels, ", ")
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

// logTail returns the last log line of a command, or "" when there is none
// or it can't be fetched.
func logTail(ctx context.Context, c client.ServerClient, id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	logs, err := c.GetCommandLogs(ctx, *id)
	if err != nil {
		return ""
	}
	for i := len(logs) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, logs[i].Line))
		if line != "" {
			return line
		}
	}
	return ""
}
