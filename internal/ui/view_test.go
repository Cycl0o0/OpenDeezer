package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestAvailableBodyHeightTracksRenderedFooter(t *testing.T) {
	tests := []struct {
		name   string
		total  int
		footer string
		want   int
	}{
		{name: "base footer", total: 20, footer: "border\nnow\nprogress\nhelp", want: 16},
		{name: "status and update", total: 20, footer: "border\nstatus\nupdate\nnow\nprogress\nhelp", want: 14},
		{name: "tiny terminal", total: 2, footer: "border", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := availableBodyHeight(tt.total, tt.footer); got != tt.want {
				t.Fatalf("availableBodyHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestBodyAndDynamicFooterFitTerminalHeight(t *testing.T) {
	const terminalRows = 20
	footer := "border\nstatus\nupdate\nnow playing\nprogress\nhelp"
	body := padTo([]string{"content"}, availableBodyHeight(terminalRows, footer))
	if got := lipgloss.Height(body + "\n" + footer); got != terminalRows {
		t.Fatalf("rendered height = %d, want %d", got, terminalRows)
	}
}

func TestPadToFitsExactHeight(t *testing.T) {
	if got := padTo([]string{"one", "two", "three"}, 2); got != "one\ntwo" {
		t.Fatalf("clamped content = %q", got)
	}
	if got := padTo([]string{"one"}, 3); got != "one\n\n" {
		t.Fatalf("padded content = %q", got)
	}
}

func TestFooterHelpCompactsForNarrowTerminals(t *testing.T) {
	wide := (&Model{width: 120}).footerHelp("on", "all", 80)
	if !strings.Contains(wide, "shuf:on") {
		t.Fatalf("wide footer should include playback state: %q", wide)
	}

	narrow := (&Model{width: 30}).footerHelp("on", "all", 80)
	if strings.Contains(narrow, "shuf") || !strings.Contains(narrow, "/") || !strings.Contains(narrow, "q") {
		t.Fatalf("narrow footer should retain compact primary controls: %q", narrow)
	}
}

func TestHelpViewScrollsWithinAvailableRows(t *testing.T) {
	m := &Model{}
	first := m.helpView(8)
	if got := lipgloss.Height(first); got != 8 {
		t.Fatalf("help height = %d, want 8", got)
	}

	m.helpOffset = 5
	later := m.helpView(8)
	if later == first || !strings.Contains(later, "6-10/") {
		t.Fatalf("help did not advance to the requested page: %q", later)
	}
}
