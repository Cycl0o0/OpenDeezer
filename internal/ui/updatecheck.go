package ui

import (
	"os/exec"
	"runtime"

	"github.com/Cycl0o0/OpenDeezer/internal/update"

	tea "github.com/charmbracelet/bubbletea"
)

// updateCheckMsg carries the result of a GitHub release check (see
// internal/update). A non-nil err means the check failed (offline, rate
// limited, etc.) — treated as "no update", never surfaced as an error unless
// the user asked for a manual re-check.
type updateCheckMsg struct {
	info update.Info
	err  error
}

// updateCheckCmd asks GitHub for the latest release. It's fired once from
// Init (background, non-blocking) and again on a manual re-check from the
// About screen; it never downloads or installs anything.
func (m *Model) updateCheckCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := update.Check(Version)
		return updateCheckMsg{info: info, err: err}
	}
}

// openInBrowser opens url with the OS's default handler, best-effort. Errors
// are intentionally ignored — this never blocks or fails the UI.
func openInBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, bsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
