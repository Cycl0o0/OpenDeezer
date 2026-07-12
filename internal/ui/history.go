package ui

import (
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	tea "github.com/charmbracelet/bubbletea"
)

// Listening-history session tracking. A session starts when a track becomes
// the active one (onTrackChanged) and ends on the next track change, a stop,
// or quit. The seconds listened come from the playback position the TUI
// already tracks: the 1s tick keeps histPosMS fresh, playCurrent/stop refresh
// it just before the player switches away, and a natural finish marks the
// full duration. Flushing clears the session, so a pause/resume (which never
// flushes) cannot double-record a track.

// histSyncPos refreshes the session's last-observed position while the player
// is still on the session's track. Only live states are sampled so a stopped
// or swapped player can't clobber a position already marked (e.g. full).
func (m *Model) histSyncPos() {
	if m.histTrack.ID == "" || m.player == nil {
		return
	}
	switch m.player.State() {
	case audio.Playing, audio.Paused:
		if p := m.player.PositionMS(); p > 0 {
			m.histPosMS = p
		}
	}
}

// histMarkFull records that the session's track played to its natural end.
func (m *Model) histMarkFull() {
	if m.histTrack.ID != "" {
		m.histPosMS = m.histTrack.DurationMS
	}
}

// histStartSession begins a session for the newly active track. Podcast
// episodes are tracked but never recorded (their ids aren't playable tracks).
func (m *Model) histStartSession(t deezer.Track) {
	m.histTrack = t
	m.histPosMS = 0
	m.histStart = time.Now()
	m.histEpisode = m.episodeMode
}

// historyFlushCmd ends the current session and returns a tea.Cmd that records
// it off the update loop (history.Record fsyncs — never block the UI on disk).
// Returns nil when there is nothing to record. The session is cleared here, on
// the update loop, so a second flush is a no-op (duplicate guard).
func (m *Model) historyFlushCmd() tea.Cmd {
	t := m.histTrack
	if t.ID == "" {
		return nil
	}
	secs := m.histPosMS / 1000
	if d := t.DurationMS / 1000; d > 0 && secs > d {
		secs = d
	}
	started := m.histStart
	episode := m.histEpisode
	store := m.hist
	m.histTrack = deezer.Track{}
	m.histPosMS = 0
	m.histEpisode = false
	if store == nil || episode || secs <= 0 {
		return nil
	}
	e := history.Entry{
		TrackID:           t.ID,
		Title:             t.Name,
		Artist:            t.ArtistLine(),
		Album:             t.AlbumName,
		StartedAt:         started.Unix(),
		DurationPlayedSec: secs,
	}
	return func() tea.Msg {
		_ = store.Record(e) // best-effort: stats are a convenience, not critical state
		return nil
	}
}

// historyFlushNow records the pending session synchronously. Only used on the
// quit path, where a goroutine could be killed before the write lands.
func (m *Model) historyFlushNow() {
	if cmd := m.historyFlushCmd(); cmd != nil {
		_ = cmd()
	}
}
