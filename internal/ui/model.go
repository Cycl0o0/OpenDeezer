// Package ui is the Bubble Tea TUI for OpenDeezer: a menu/list browser with an
// always-visible now-playing footer. Network calls run as tea.Cmds.
package ui

import (
	"fmt"
	"image"
	"sync/atomic"
	"time"

	"github.com/Cycl0o0/OpenDeezer/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/internal/control"
	"github.com/Cycl0o0/OpenDeezer/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/internal/mpris"
	"github.com/Cycl0o0/OpenDeezer/internal/queue"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// screen is the current top-level view.
type screen int

const (
	screenLoading screen = iota
	screenMenu
	screenList
	screenSearch
	screenNowPlaying
	screenCredits
	screenQueue
	screenLyrics
	screenHelp
	screenDevices
	screenBlocked // Free account — playback not available
)

// Model is the root Bubble Tea model.
type Model struct {
	client *deezer.Client
	player *audio.Player

	screen     screen
	prevScreen screen // to restore after now-playing / credits
	list       list.Model
	search     textinput.Model
	spinner    spinner.Model
	status     string // transient status / error line
	loading    bool   // a network request is in flight
	ready      bool
	width      int
	height     int

	// artwork for the current track
	curImg      image.Image
	curImgTrack string
	curCover    string // rendered half-block cover

	// playback queue (shared model, see internal/queue)
	q       *queue.Queue
	playing bool // a track is loaded/playing

	// lyrics for the current track (lazily fetched on the lyrics screen)
	lyrics      *deezer.Lyrics
	lyricsTrack string

	acct          deezer.Account // logged-in plan + entitlements
	pendingSeek   int64          // ms to seek to once the next stream is ready (resume)
	searchPodcast bool           // search screen is in podcast mode
	episodeMode   bool           // current queue is podcast episodes (plain streams)

	media mpris.Controller // OS media controls (MPRIS on Linux, no-op elsewhere)

	ctrl      *control.Server                 // control API (remote + MCP); nil if disabled
	ctrlState atomic.Pointer[control.State]   // playback snapshot read by the control HTTP goroutine
	acctSnap  atomic.Pointer[control.Account] // identity snapshot read by the control HTTP goroutine

	finished chan struct{} // signalled by player onFinish
}

// controlCmdMsg is a command from the control API, delivered onto the update
// loop so it runs single-threaded with the rest of the model.
type controlCmdMsg struct {
	kind string // playpause|next|prev|stop|restart|repeat|shuffle|seek|volume|playtrack|playplaylist
	id   string // track/playlist id (playtrack/playplaylist)
	ms   int64  // absolute position for seek
	vol  float64
}

// playNowMsg replaces the queue with tracks and starts playing the first one.
// Used by control "play track/playlist <id>".
type playNowMsg struct {
	tracks   []deezer.Track
	episodes bool
}

// mediaCmdMsg is a media-key/overlay command received from the desktop.
type mediaCmdMsg struct {
	kind string // "playpause" | "next" | "prev" | "stop" | "seek" | "setpos"
	arg  int64  // microseconds for seek/setpos
}

// StartMedia wires OS media controls (MPRIS) to the running program. Commands
// from the desktop are delivered as mediaCmdMsg via the program's Send so they
// run on the Bubble Tea update loop. Call after tea.NewProgram, before Run.
func (m *Model) StartMedia(send func(tea.Msg)) {
	m.media = mpris.New(mpris.Commands{
		PlayPause:   func() { send(mediaCmdMsg{kind: "playpause"}) },
		Next:        func() { send(mediaCmdMsg{kind: "next"}) },
		Prev:        func() { send(mediaCmdMsg{kind: "prev"}) },
		Stop:        func() { send(mediaCmdMsg{kind: "stop"}) },
		Seek:        func(us int64) { send(mediaCmdMsg{kind: "seek", arg: us}) },
		SetPosition: func(_ string, us int64) { send(mediaCmdMsg{kind: "setpos", arg: us}) },
	})
}

// publishMedia pushes the current now-playing state to the desktop.
func (m *Model) publishMedia() {
	if m.media == nil {
		return
	}
	var s mpris.State
	switch m.player.State() {
	case audio.Playing:
		s.Status = "Playing"
	case audio.Paused:
		s.Status = "Paused"
	default:
		s.Status = "Stopped"
	}
	if t, ok := m.q.Current(); ok {
		s.TrackID = t.ID
		s.Title = t.Name
		s.Artist = t.ArtistLine()
		s.Album = t.AlbumName
		s.ArtURL = t.ArtworkURL
		s.LengthUS = t.DurationMS * 1000
	}
	s.PositionUS = m.player.PositionMS() * 1000
	m.media.Update(s)
}

// StartControl starts the control API (remote control + MCP) if enabled in the
// config. Commands arrive as controlCmdMsg via send so they run on the update
// loop; status is served from an atomic snapshot refreshed by publishControl.
// Returns nil (no error) when the API is disabled. Call after tea.NewProgram.
func (m *Model) StartControl(send func(tea.Msg)) error {
	cfg := LoadControl()
	if !cfg.Enabled {
		return nil
	}
	cmds := control.Commands{
		PlayPause:     func() { send(controlCmdMsg{kind: "playpause"}) },
		Next:          func() { send(controlCmdMsg{kind: "next"}) },
		Prev:          func() { send(controlCmdMsg{kind: "prev"}) },
		Stop:          func() { send(controlCmdMsg{kind: "stop"}) },
		Restart:       func() { send(controlCmdMsg{kind: "restart"}) },
		CycleRepeat:   func() { send(controlCmdMsg{kind: "repeat"}) },
		ToggleShuffle: func() { send(controlCmdMsg{kind: "shuffle"}) },
		Seek:          func(ms int64) { send(controlCmdMsg{kind: "seek", ms: ms}) },
		SetVolume:     func(v float64) { send(controlCmdMsg{kind: "volume", vol: v}) },
		PlayTrack:     func(id string) { send(controlCmdMsg{kind: "playtrack", id: id}) },
		PlayPlaylist:  func(id string) { send(controlCmdMsg{kind: "playplaylist", id: id}) },
	}
	status := func() control.State {
		if p := m.ctrlState.Load(); p != nil {
			return *p
		}
		return control.State{State: "stopped"}
	}
	account := func() control.Account {
		if p := m.acctSnap.Load(); p != nil {
			return *p
		}
		return control.Account{}
	}
	m.ctrl = control.New(
		control.Config{Addr: cfg.Addr, Token: cfg.Token, SameAccountOnly: cfg.SameAccount},
		status, account, cmds, m.client,
	)
	m.ctrl.SetVersion(Version)
	if err := m.ctrl.Start(); err != nil {
		m.ctrl = nil
		return err
	}
	return nil
}

// publishControl refreshes the atomic snapshot the control HTTP goroutine reads.
// Called on every tick + track change (mirrors publishMedia), so the snapshot is
// always built on the update loop — the HTTP side only ever reads it.
func (m *Model) publishControl() {
	if m.ctrl == nil {
		return
	}
	var st control.State
	switch m.player.State() {
	case audio.Playing:
		st.State = "playing"
	case audio.Paused:
		st.State = "paused"
	case audio.Loading:
		st.State = "loading"
	case audio.Errored:
		st.State = "error"
	default:
		st.State = "stopped"
	}
	if t, ok := m.q.Current(); ok {
		st.Track = ctrlTrack(t)
	}
	st.PositionMS = m.player.PositionMS()
	st.DurationMS = m.player.DurationMS()
	st.Volume = m.player.Volume()
	switch m.q.Repeat() {
	case queue.RepeatAll:
		st.Repeat = "all"
	case queue.RepeatOne:
		st.Repeat = "one"
	default:
		st.Repeat = "off"
	}
	st.Shuffle = m.q.Shuffle()
	st.Format = m.player.Format()
	tracks := m.q.Tracks()
	if len(tracks) > 0 {
		st.Queue = make([]control.Track, 0, len(tracks))
		for _, t := range tracks {
			st.Queue = append(st.Queue, *ctrlTrack(t))
		}
	}
	m.ctrlState.Store(&st)
}

// publishAccount stores the identity snapshot the control HTTP goroutine reads
// for auth + /whoami. Called on the update loop after login (m.acct is set there),
// so the HTTP side never reads the deezer.Client's login fields directly.
func (m *Model) publishAccount() {
	m.acctSnap.Store(&control.Account{
		UserID: m.acct.UserID, Name: m.acct.Name, Offer: m.acct.Offer,
	})
}

// ctrlTrack maps a deezer.Track to the control-API Track.
func ctrlTrack(t deezer.Track) *control.Track {
	return &control.Track{
		ID: t.ID, Title: t.Name, Artist: t.ArtistLine(), Album: t.AlbumName,
		Explicit: t.Explicit, DurationMS: t.DurationMS,
	}
}

// New builds the root model.
func New(client *deezer.Client, player *audio.Player) *Model {
	ti := textinput.New()
	ti.Placeholder = "Search Deezer…"
	ti.CharLimit = 120

	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := &Model{
		client:   client,
		player:   player,
		screen:   screenLoading,
		list:     l,
		search:   ti,
		spinner:  sp,
		status:   "Logging in…",
		loading:  true,
		q:        queue.New(),
		finished: make(chan struct{}, 1),
	}
	player.SetOnFinish(func() {
		select {
		case m.finished <- struct{}{}:
		default:
		}
	})
	m.applyThemeByName(LoadTheme())
	player.SetReplayGain(LoadReplayGain())
	player.SetGapless(LoadGapless())
	player.SetCrossfadeMS(LoadCrossfadeMS())
	if d := LoadAudioDevice(); d != "" {
		_ = player.SetDevice(d)
	}
	return m
}

// ---- messages ----

type loginDoneMsg struct{ err error }
type tracksMsg struct {
	title  string
	tracks []deezer.Track
}
type playlistsMsg struct {
	title     string
	playlists []deezer.Playlist
}
type searchMsg struct{ results *deezer.SearchResults }
type podcastsMsg struct {
	title    string
	podcasts []deezer.Podcast
}
type episodesMsg struct {
	title    string
	episodes []deezer.Episode
}
type lyricsMsg struct {
	trackID string
	lyrics  *deezer.Lyrics
	err     error
}
type streamReadyMsg struct {
	plan  *deezer.StreamPlan
	track deezer.Track
}
type errMsg struct{ err error }
type statusMsg struct{ text string }
type preloadMsg struct {
	plan *deezer.StreamPlan
	dur  int64
}
type devicesMsg struct{ devices []audio.Device }
type tickMsg time.Time
type trackFinishedMsg struct{}
type artMsg struct {
	trackID string
	img     image.Image
}

// Init kicks off login + the UI tick.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loginCmd(), tickCmd(), m.waitFinish(), m.spinner.Tick)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitFinish blocks on the player's finish channel.
func (m *Model) waitFinish() tea.Cmd {
	return func() tea.Msg {
		<-m.finished
		return trackFinishedMsg{}
	}
}

// ---- commands ----

func (m *Model) loginCmd() tea.Cmd {
	return func() tea.Msg {
		return loginDoneMsg{err: m.client.Login()}
	}
}

func (m *Model) favoritesCmd() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.Favorites()
		if err != nil {
			return errMsg{err}
		}
		return tracksMsg{title: "❤  Liked Songs", tracks: ts}
	}
}

func (m *Model) playlistsCmd() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.Playlists()
		if err != nil {
			return errMsg{err}
		}
		return playlistsMsg{title: "≡  My Playlists", playlists: ps}
	}
}

func (m *Model) playlistTracksCmd(p deezer.Playlist) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.PlaylistTracks(p.ID)
		if err != nil {
			return errMsg{err}
		}
		return tracksMsg{title: p.Name, tracks: ts}
	}
}

func (m *Model) albumTracksCmd(a deezer.Album) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.AlbumTracks(a.ID)
		if err != nil {
			return errMsg{err}
		}
		return tracksMsg{title: a.Name, tracks: ts}
	}
}

func (m *Model) searchCmd(q string) tea.Cmd {
	return func() tea.Msg {
		r, err := m.client.Search(q)
		if err != nil {
			return errMsg{err}
		}
		return searchMsg{results: r}
	}
}

func (m *Model) chartsCmd() tea.Cmd {
	return func() tea.Msg {
		ch, err := m.client.Charts("0")
		if err != nil {
			return errMsg{err}
		}
		return searchMsg{results: &deezer.SearchResults{
			Tracks: ch.Tracks, Albums: ch.Albums, Artists: ch.Artists, Playlists: ch.Playlists,
		}}
	}
}

func (m *Model) artistTopCmd(a deezer.ArtistInfo) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.ArtistTop(a.ID)
		if err != nil {
			return errMsg{err}
		}
		return tracksMsg{title: "♪ " + a.Name, tracks: ts}
	}
}

func (m *Model) lyricsCmd(t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		l, err := m.client.Lyrics(t.ID)
		return lyricsMsg{trackID: t.ID, lyrics: l, err: err}
	}
}

func (m *Model) flowCmd() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.Flow()
		if err != nil {
			return errMsg{err}
		}
		return tracksMsg{title: "⚡ Flow", tracks: ts}
	}
}

func (m *Model) podcastSearchCmd(q string) tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.SearchPodcasts(q)
		if err != nil {
			return errMsg{err}
		}
		return podcastsMsg{title: "🎙 Podcasts", podcasts: ps}
	}
}

func (m *Model) episodesCmd(p deezer.Podcast) tea.Cmd {
	return func() tea.Msg {
		es, err := m.client.PodcastEpisodes(p.ID)
		if err != nil {
			return errMsg{err}
		}
		return episodesMsg{title: p.Name, episodes: es}
	}
}

// episodeStreamCmd resolves + plays a podcast episode (plain stream).
func (m *Model) episodeStreamCmd(t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		plan, err := m.client.PodcastEpisodeStream(t.ID)
		if err != nil {
			return errMsg{fmt.Errorf("resolve episode %q: %w", t.Name, err)}
		}
		return streamReadyMsg{plan: plan, track: t}
	}
}

// likeCurrentCmd adds the currently playing track to favorites.
func (m *Model) likeCurrentCmd(t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.AddFavoriteTrack(t.ID); err != nil {
			return errMsg{err}
		}
		return statusMsg{"❤ Liked: " + t.Name}
	}
}

// preloadNextCmd resolves the deterministic next track and hands it to the
// player for a gapless/crossfaded transition. No-op when not applicable.
func (m *Model) preloadNextCmd() tea.Cmd {
	if m.episodeMode || (!m.player.Gapless() && m.player.CrossfadeMS() == 0) {
		return nil
	}
	t, ok := m.q.PeekNext()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		plan, err := m.client.PrepareStream(t.ID)
		if err != nil {
			return nil
		}
		return preloadMsg{plan: plan, dur: t.DurationMS}
	}
}

func (m *Model) devicesCmd() tea.Cmd {
	return func() tea.Msg {
		ds, err := m.player.Devices()
		if err != nil {
			return errMsg{err}
		}
		return devicesMsg{devices: ds}
	}
}

func (m *Model) streamCmd(t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		plan, err := m.client.PrepareStream(t.ID)
		if err != nil {
			return errMsg{fmt.Errorf("resolve %q: %w", t.Name, err)}
		}
		return streamReadyMsg{plan: plan, track: t}
	}
}

// coverCmd fetches + decodes a track's artwork (no-op message on failure).
func (m *Model) coverCmd(trackID, url string) tea.Cmd {
	return func() tea.Msg {
		img, err := fetchCover(url)
		if err != nil {
			return artMsg{trackID: trackID, img: nil}
		}
		return artMsg{trackID: trackID, img: img}
	}
}
