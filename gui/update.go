package gui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/natemsz/axon/internal/sys"
	"github.com/natemsz/axon/internal/update"
)

// updateCheckTimeout bounds the background update check, the way
// it bounded the check when it ran before the window opened.
const updateCheckTimeout = 4 * time.Second

// checkForUpdate runs the self-update check now that the window
// is up, in the background, so starting axon never waits on the
// network: the page shows its Update button when (and if) the
// check finds a newer release. A check that fails (offline, an
// answer that does not read) is logged and forgotten, never
// fatal, and nothing is downloaded until the player takes the
// button.
func (s *server) checkForUpdate(ctx context.Context) {
	up := s.opts.Updater
	if up == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
		defer cancel()
		rel, err := up.Check(ctx, s.opts.Version)
		if err != nil {
			s.logf("update check: %v", err)
			return
		}
		if rel == nil {
			return
		}
		s.mu.Lock()
		s.update = rel
		s.mu.Unlock()
		s.logf("update available: axon %s (this is %s)", rel.Version, s.opts.Version)
	}()
}

// foundUpdate is the newer release the background check found,
// nil when there is none (or the check has not answered yet).
func (s *server) foundUpdate() *update.Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update
}

// updateVersion is the newer release the background check found,
// "" when there is none: the page shows its Update button for it.
func (s *server) updateVersion() string {
	if rel := s.foundUpdate(); rel != nil {
		return rel.Version
	}
	return ""
}

// updateApply is the page's Update button: fetch the release the
// background check found, proving it the way internal/update
// requires, then run the downloaded program. Run from anywhere
// but its installed place, a downloaded axon installs itself: it
// stops this axon, copies itself in and starts the installed
// copy, which is the update landing. Any failure is reported to
// the page and leaves this axon running as it was.
func (s *server) updateApply(r *http.Request) (any, error) {
	rel, up := s.foundUpdate(), s.opts.Updater
	if rel == nil || up == nil {
		return nil, apiError{"no update is available"}
	}
	dir, err := os.MkdirTemp("", "axon-update-")
	if err != nil {
		return nil, err
	}
	path, err := up.Download(r.Context(), rel, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, apiError{"the update was not applied: " + err.Error()}
	}
	cmd := exec.Command(path)
	sys.Detach(cmd)
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("the update downloaded but would not start: %w", err)
	}
	_ = cmd.Process.Release()
	s.logf("updating to axon %s: the new copy is installing itself", rel.Version)
	return map[string]string{"updating": rel.Version}, nil
}
