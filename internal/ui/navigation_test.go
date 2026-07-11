package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

func navigationModel(s screen) *Model {
	return &Model{
		screen: s,
		list:   list.New(nil, list.NewDefaultDelegate(), 80, 20),
		search: textinput.New(),
	}
}

func TestGlobalSearchResetsPodcastModeAndReturnsToOrigin(t *testing.T) {
	m := navigationModel(screenList)
	m.searchPodcast = true

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.screen != screenSearch || m.searchPodcast {
		t.Fatalf("global search state = (screen %v, podcasts %v)", m.screen, m.searchPodcast)
	}
	if m.searchReturn != screenList || !m.search.Focused() {
		t.Fatalf("search should remember and focus its originating list")
	}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenList || m.search.Focused() {
		t.Fatalf("escape should restore the originating list and blur search")
	}
}

func TestSearchDoesNotOverwriteOverlayReturnScreen(t *testing.T) {
	m := navigationModel(screenHelp)
	m.prevScreen = screenList

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenHelp || m.prevScreen != screenList {
		t.Fatalf("search return changed overlay history: screen=%v prev=%v", m.screen, m.prevScreen)
	}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.screen != screenList {
		t.Fatalf("overlay escape returned to %v, want list", m.screen)
	}
}

func TestHelpKeysScrollOverlayInsteadOfHiddenList(t *testing.T) {
	m := navigationModel(screenHelp)

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.helpOffset != 1 {
		t.Fatalf("help offset = %d, want 1", m.helpOffset)
	}
	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyPgUp})
	if m.helpOffset != 0 {
		t.Fatalf("page up should clamp help offset to zero, got %d", m.helpOffset)
	}
}
