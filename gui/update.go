package gui

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"

	"github.com/natemsz/axon/internal/sys"
)

// updateVersion is the newer release the startup check found, ""
// when there is none: the page shows its Update button for it.
func (s *server) updateVersion() string {
	if s.opts.Update == nil {
		return ""
	}
	return s.opts.Update.Version
}

// updateApply is the page's Update button: fetch the release the
// startup check found, proving it the way internal/update
// requires, then run the downloaded program. Run from anywhere
// but its installed place, a downloaded axon installs itself: it
// stops this axon, copies itself in and starts the installed
// copy, which is the update landing. Any failure is reported to
// the page and leaves this axon running as it was.
func (s *server) updateApply(r *http.Request) (any, error) {
	rel, up := s.opts.Update, s.opts.Updater
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
