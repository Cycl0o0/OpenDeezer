// Package ui is the Bubble Tea TUI for OpenDeezer: a menu/list browser with an
// always-visible now-playing footer. Network calls run as tea.Cmds.
package ui

import (
	"context"
	"fmt"
	"image"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Cycl0o0/OpenDeezer/v2/internal/audio"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/config"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/control"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/deezer"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discord"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/discovery"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/history"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/i18n"
	odlog "github.com/Cycl0o0/OpenDeezer/v2/internal/log"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/mpris"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/queue"
	"github.com/Cycl0o0/OpenDeezer/v2/internal/update"

	"github.com/charmbracelet/bubbles/key"
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
	screenRemote         // remote-control: device picker (discovered peers)
	screenRemoteInput    // remote-control: type a peer address by hand
	screenRemoteCtl      // remote-control: driving a connected peer
	screenWebRemote      // phone web remote: QR + pairing code
	screenNoInternet     // transport-level connectivity loss — offer retry, don't log out
	screenPlaylistPick   // add-to-playlist picker (borrows m.list like the device picker)
	screenPlaylistPrompt // playlist title input (create / rename)
)

// plPromptKind says what the playlist-title prompt (screenPlaylistPrompt) is for.
type plPromptKind int

const (
	plNone            plPromptKind = iota
	plCreateWithTrack              // "New playlist…" from the add-to-playlist picker
	plCreateEmpty                  // 'N' on the playlists screen
	plRename                       // 'R' on the playlists screen
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
	helpOffset int    // first keybinding shown on the scrollable help screen
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

	// browse holds the tracks of the list currently being browsed (album, liked
	// songs, search results, episodes). It is committed to the play queue only on
	// an explicit play (see activate), so browsing never clobbers the live queue.
	browse []deezer.Track

	// device-picker snapshot: the list the picker temporarily replaced, restored
	// when the picker closes (the picker reuses m.list).
	devSavedItems []list.Item
	devSavedTitle string
	devSavedIndex int

	// lyrics for the current track (lazily fetched on the lyrics screen)
	lyrics      *deezer.Lyrics
	lyricsTrack string

	acct          deezer.Account // logged-in plan + entitlements
	pendingSeek   int64          // ms to seek to once the next stream is ready (resume)
	searchPodcast bool           // search screen is in podcast mode
	searchReturn  screen         // screen restored when search is cancelled
	episodeMode   bool           // current queue is podcast episodes (plain streams)
	sleepStep     int            // sleep-timer cycle position (see cycleSleepTimer)
	downloading   bool           // a track download (D key) is in flight

	// library write-ops state (like toggle, add-to-playlist, playlist CRUD)
	likedIDs     map[string]bool // liked-track ids: seeded at login, refreshed by Liked Songs, toggled optimistically
	ownPlaylists bool            // current screenList shows the user's own playlists (enables N/R/X)
	pickTrack    deezer.Track    // track pending add-to-playlist
	plPrompt     plPromptKind    // what the playlist-title prompt is for
	plTarget     deezer.Playlist // rename/delete target
	plConfirm    bool            // 'X' delete confirmation pending (y/n)
	plReturn     screen          // screen restored when the title prompt closes

	queueSel     int // queue-view cursor (row navigation on the 'u' screen)
	lyricsOffset int // manual wheel-scroll offset for plain (unsynced) lyrics

	// pageArtist is the artist whose profile page is currently shown on
	// screenList ("" ID when the list is any other browse context) — the seed
	// for the 'm' start-radio key when no track/artist row is selected.
	pageArtist deezer.ArtistInfo

	// Local listening history (stats screen + control /history/recent). See
	// internal/ui/history.go for the session fields' lifecycle.
	hist        *history.Store // nil = recording disabled (e.g. no config dir)
	histTrack   deezer.Track   // active session's track ("" ID = none)
	histPosMS   int64          // last observed playback position of histTrack
	histStart   time.Time      // when the session started
	histEpisode bool           // session is a podcast episode (not recorded)

	// Control-API queue snapshot cache: the []control.Track is rebuilt only when
	// the queue's version changes, not on every 1s tick (see publishControl).
	ctrlQueue    []control.Track
	ctrlQueueVer uint64

	// GitHub release check (see updatecheck.go / internal/update). The
	// startup check is silent unless a newer version is found; a manual
	// re-check (About screen) surfaces feedback via m.status.
	updateInfo      update.Info
	updateChecking  bool // manual re-check in flight
	updateChecked   bool // at least one check (startup or manual) has completed
	updateDismissed bool // user closed the footer notice for this session

	media   mpris.Controller // OS media controls (MPRIS on Linux, no-op elsewhere)
	discord discord.Presence // Discord Rich Presence (no-op if no app id)

	ctrl      *control.Server                 // control API (remote + MCP); nil if disabled
	ctrlState atomic.Pointer[control.State]   // playback snapshot read by the control HTTP goroutine
	acctSnap  atomic.Pointer[control.Account] // identity snapshot read by the control HTTP goroutine
	ctrlSend  func(tea.Msg)                   // saved from StartControl; used to wire a web-remote server

	// phone web remote: LAN-bound server + current pairing state.
	webRemoteSrv    *control.Server // server used for web remote (may equal ctrl)
	webRemoteActive bool
	webRemoteCode   string // 6-digit pairing code
	webRemoteURL    string
	webRemoteQR     string // terminal QR block
	ctrlEditToken   bool   // Web Remote screen: editing the control-API token

	// remote control: drive another OpenDeezer client over its control API.
	remote        *control.Client
	remoteState   control.State
	remoteName    string               // peer's account name (from /whoami)
	remoteAddr    string               // peer host:port currently connected/connecting
	remoteClient  string               // peer device type/client (from /whoami)
	remoteVersion string               // peer OpenDeezer version (from /whoami)
	advertiser    *discovery.Responder // LAN advertisement (when our control API is LAN-bound)

	finished chan bool // signalled by player onFinish; true = gapless swap, false = stop
}

// peerDevice is a discovered Connect device + what it's currently playing.
type peerDevice struct {
	dev        discovery.Device
	nowPlaying string
}

// devicesDiscoveredMsg carries the result of a LAN device scan.
type devicesDiscoveredMsg struct{ peers []peerDevice }

// remoteConnMsg is the result of connecting to a peer (whoami + initial status).
type remoteConnMsg struct {
	client     *control.Client
	addr       string
	name       string
	clientType string
	version    string
	state      control.State
	err        error
}

// remoteStateMsg carries a polled/post-command status from the connected peer.
type remoteStateMsg struct {
	state control.State
	err   error
}

// controlCmdMsg is a command from the control API, delivered onto the update
// loop so it runs single-threaded with the rest of the model.
type controlCmdMsg struct {
	kind string // playpause|next|prev|stop|restart|repeat|shuffle|repeat-set|shuffle-set|seek|volume|playtrack|playplaylist|sleep|sleepcancel
	id   string // track/playlist id (playtrack/playplaylist)
	ms   int64  // absolute position for seek; minutes for sleep
	vol  float64
	mode string // target repeat mode for repeat-set (off|all|one)
	on   bool   // target shuffle state for shuffle-set
	eot  bool   // end-of-track mode for sleep
}

// parseRepeatMode maps a control-API repeat mode string ("off"|"all"|"one") to a
// queue.Repeat. The control server validates the mode before dispatching, so an
// unknown value defaults to RepeatOff.
func parseRepeatMode(mode string) queue.Repeat {
	switch mode {
	case "all":
		return queue.RepeatAll
	case "one":
		return queue.RepeatOne
	default:
		return queue.RepeatOff
	}
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
	m.publishDiscord()
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

// publishDiscord pushes the now-playing track to Discord Rich Presence. No-op
// when no app id is configured.
func (m *Model) publishDiscord() {
	if m.discord == nil {
		return
	}
	var ds discord.State
	switch m.player.State() {
	case audio.Playing:
		ds.Status = "playing"
	case audio.Paused:
		ds.Status = "paused"
	default:
		ds.Status = "stopped"
	}
	if t, ok := m.q.Current(); ok {
		ds.Title = t.Name
		ds.Artist = t.ArtistLine()
		ds.Album = t.AlbumName
		ds.DurationMS = t.DurationMS
	}
	ds.PositionMS = m.player.PositionMS()
	m.discord.Update(ds)
}

// StartControl starts the control API (remote control + MCP) if enabled in the
// config. Commands arrive as controlCmdMsg via send so they run on the update
// loop; status is served from an atomic snapshot refreshed by publishControl.
// Returns nil (no error) when the API is disabled. Call after tea.NewProgram.
func (m *Model) StartControl(send func(tea.Msg)) error {
	m.ctrlSend = send // saved for web remote server creation
	cfg := LoadControl()
	if !cfg.Enabled {
		return nil
	}
	if cfg.SameAccount && cfg.Token == "" {
		odlog.Warn("control api: LAN-exposed with same-account auth only; the Deezer user id " +
			"is not a strong secret. Set OPENDEEZER_CONTROL_TOKEN for a real credential.")
	}
	// Shared with the web-remote server: simple commands are marshaled onto the
	// update loop via send; queue edits round-trip an error the same way (see
	// buildControlCommands for the full thread-safety contract).
	cmds := buildControlCommands(send, m.client, m.hist)
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
	m.ctrl.SetClientInfo("tui", "OpenDeezer TUI")
	// EQ setters are atomic-swap on the player, safe to call straight from the
	// HTTP goroutine (no round-trip through the update loop needed).
	pl := m.player
	m.ctrl.SetEQ(control.PlayerEQ(func() control.EQController {
		if pl != nil {
			return pl
		}
		return nil
	}, audio.EQPresetNames))
	if err := m.ctrl.Start(); err != nil {
		m.ctrl = nil
		return err
	}
	// Advertise on the LAN (OpenDeezer Connect) when reachable (non-loopback).
	if !config.IsLoopbackAddr(cfg.Addr) {
		if _, port, err := net.SplitHostPort(m.ctrl.Addr()); err == nil {
			if p, e := strconv.Atoi(port); e == nil {
				info := func() discovery.Info {
					name := ""
					if a := m.acctSnap.Load(); a != nil {
						name = a.Name
					}
					return discovery.Info{Name: name, Client: "tui", Version: Version}
				}
				if resp, e := discovery.Advertise(info, p); e == nil {
					m.advertiser = resp
				}
			}
		}
	}
	return nil
}

// publishControl refreshes the atomic snapshot the control HTTP goroutine reads.
// Called on every tick + track change (mirrors publishMedia), so the snapshot is
// always built on the update loop — the HTTP side only ever reads it. When the
// snapshot meaningfully changed (track/state/seek/queue edit — not a plain +1s
// position tick), the SSE event loop is nudged so subscribers see the change
// immediately instead of on the next fallback tick.
func (m *Model) publishControl() {
	if m.ctrl == nil {
		return
	}
	prev := m.ctrlState.Load()
	prevQueueVer := m.ctrlQueueVer
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
	st.SleepActive = m.player.SleepActive()
	st.SleepEndOfTrack = m.player.SleepEndOfTrack()
	st.SleepRemainingMS = m.player.SleepRemainingMS()
	st.Queue = m.ctrlQueueSnapshot()
	m.ctrlState.Store(&st)
	if m.ctrlQueueVer != prevQueueVer || controlStateChanged(prev, &st) {
		m.ctrl.NotifyStateChanged()
		if m.webRemoteSrv != nil && m.webRemoteSrv != m.ctrl {
			m.webRemoteSrv.NotifyStateChanged()
		}
	}
}

// controlStateChanged reports whether the new snapshot differs from the last
// one in a way SSE subscribers should hear about now: playback state, active
// track, mode/volume flips, or a position discontinuity (a seek), but NOT the
// ordinary ~1s forward creep of the position between ticks — that would turn
// every tick into an event and defeat the server's coalescing.
func controlStateChanged(prev, cur *control.State) bool {
	if prev == nil {
		return true
	}
	if prev.State != cur.State || prev.Repeat != cur.Repeat ||
		prev.Shuffle != cur.Shuffle || prev.Volume != cur.Volume ||
		prev.SleepActive != cur.SleepActive {
		return true
	}
	var pid, cid string
	if prev.Track != nil {
		pid = prev.Track.ID
	}
	if cur.Track != nil {
		cid = cur.Track.ID
	}
	if pid != cid {
		return true
	}
	// Ticks advance the position by ~1s; anything backwards or well past that
	// is a seek/restart worth publishing immediately.
	d := cur.PositionMS - prev.PositionMS
	return d < 0 || d > 3000
}

// ctrlQueueSnapshot returns the control-API view of the play queue, rebuilding
// the []control.Track only when the queue's version changed since the last
// call. publishControl runs on every 1s tick, and re-allocating a big queue's
// snapshot each second was pure waste; position/state fields still refresh
// every tick. Version 0 means never mutated, i.e. an empty queue, so the zero
// cache is already correct at startup. The returned slice is shared across
// snapshots and must be treated as read-only (the HTTP side only marshals it).
func (m *Model) ctrlQueueSnapshot() []control.Track {
	v := m.q.Version()
	if v == m.ctrlQueueVer {
		return m.ctrlQueue
	}
	tracks := m.q.Tracks()
	if len(tracks) == 0 {
		m.ctrlQueue = nil
	} else {
		cq := make([]control.Track, 0, len(tracks))
		for _, t := range tracks {
			cq = append(cq, *ctrlTrack(t))
		}
		m.ctrlQueue = cq
	}
	m.ctrlQueueVer = v
	return m.ctrlQueue
}

// sleepMinutes maps the sleep-timer cycle step to a duration (0 = off, -1 slot
// used for end-of-track). Steps: off → 15 → 30 → 45 → 60 → end-of-track → off.
var sleepMinutes = []int{0, 15, 30, 45, 60, -1}

// cycleSleepTimer advances the sleep-timer selection and applies it to the player.
func (m *Model) cycleSleepTimer() {
	m.sleepStep = (m.sleepStep + 1) % len(sleepMinutes)
	switch mins := sleepMinutes[m.sleepStep]; {
	case mins == 0:
		m.player.CancelSleepTimer()
	case mins < 0:
		m.player.SetSleepTimer(0, true) // end-of-track
	default:
		m.player.SetSleepTimer(time.Duration(mins)*time.Minute, false)
	}
}

// sleepStatus renders a human-readable sleep-timer status line.
func sleepStatus(p *audio.Player) string {
	if !p.SleepActive() {
		return i18n.T("Sleep timer off")
	}
	if p.SleepEndOfTrack() {
		return i18n.T("Sleep: at end of track")
	}
	rem := p.SleepRemainingMS() / 1000
	if rem >= 60 {
		return i18n.Tf("Sleep: pausing in %d min", int((rem+59)/60))
	}
	return i18n.Tf("Sleep: pausing in %ds", int(rem))
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
	ct := &control.Track{
		ID: t.ID, Title: t.Name, Artist: t.ArtistLine(), Album: t.AlbumName,
		Explicit: t.Explicit, DurationMS: t.DurationMS, ArtworkURL: t.ArtworkURL,
	}
	if len(t.Artists) > 0 {
		ct.ArtistID = t.Artists[0].ID
	}
	return ct
}

// remoteTrack converts a control API track pointer to a deezer.Track for local
// use (e.g. lyrics fetch, artist browse). ArtistID is preserved so the artist
// view can open directly by id.
func remoteTrack(t *control.Track) deezer.Track {
	if t == nil {
		return deezer.Track{}
	}
	var artists []deezer.Artist
	if t.Artist != "" {
		artists = []deezer.Artist{{ID: t.ArtistID, Name: t.Artist}}
	}
	return deezer.Track{
		ID: t.ID, Name: t.Title, DurationMS: t.DurationMS,
		Artists: artists, AlbumName: t.Album, Explicit: t.Explicit,
	}
}

// newBrowseList builds the shared browse list. Filtering is enabled but moved
// off the default "/" (which is Deezer search everywhere in the app) onto
// ctrl+f; handleKey forwards ctrl+f to the list only on list screens.
func newBrowseList() list.Model {
	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.KeyMap.Filter = key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("ctrl+f", "filter"))
	return l
}

// New builds the root model.
func New(client *deezer.Client, player *audio.Player) *Model {
	ti := textinput.New()
	ti.Placeholder = i18n.T("Search Deezer…")
	ti.CharLimit = 120

	l := newBrowseList()

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
		finished: make(chan bool, 1),
	}
	// Local listening history (stats screen + control /history/recent).
	// Best-effort: without a config dir, recording is simply disabled.
	m.hist, _ = history.Default()
	player.SetOnFinish(func() {
		// Capture whether this is a gapless swap (still Playing) or a stop, at the
		// instant of the signal — the UI must not re-sample the live state later,
		// which user input racing the finish could have changed.
		swapped := player.State() == audio.Playing
		select {
		case m.finished <- swapped:
		default:
		}
	})
	m.discord = discord.New(LoadDiscordAppID())
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
	title     string
	tracks    []deezer.Track
	favorites bool // this is the Liked Songs list — refresh the liked-ids cache
}
type playlistsMsg struct {
	title     string
	playlists []deezer.Playlist
}

// artistPageMsg carries a full artist profile (top tracks + albums + related).
type artistPageMsg struct{ page *deezer.ArtistPage }

// likedIDsMsg seeds/refreshes the liked-track id cache behind the 'f' toggle.
type likedIDsMsg struct{ ids map[string]bool }

// favToggleMsg reports a like/unlike round-trip so an optimistic cache update
// can be rolled back on failure.
type favToggleMsg struct {
	id    string
	name  string
	liked bool // the state we tried to set
	err   error
}

// playlistPickMsg opens the add-to-playlist picker with the user's playlists.
type playlistPickMsg struct {
	playlists []deezer.Playlist
	track     deezer.Track
}

// playlistOpMsg reports a playlist write op (add/create/rename/delete).
type playlistOpMsg struct {
	status  string
	refresh bool // reload the playlists list when it is on screen
	err     error
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
	plan    *deezer.StreamPlan
	dur     int64
	trackID string // the queue's PeekNext at request time; drop if it changed
}
type devicesMsg struct{ devices []audio.Device }
type tickMsg time.Time

// trackFinishedMsg reports a track finishing. swapped is captured at the finish
// signal: true when the player gaplessly swapped in the preloaded next track,
// false when it stopped.
type trackFinishedMsg struct{ swapped bool }
type artMsg struct {
	trackID string
	img     image.Image
}

// downloadDoneMsg reports the result of a track download (D key).
type downloadDoneMsg struct {
	name string
	path string
	err  error
}

// Init kicks off login + the UI tick. The update check runs alongside them in
// the background (see updatecheck.go) and never delays startup.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loginCmd(), tickCmd(), m.waitFinish(), m.spinner.Tick, m.updateCheckCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitFinish blocks on the player's finish channel.
func (m *Model) waitFinish() tea.Cmd {
	return func() tea.Msg {
		return trackFinishedMsg{swapped: <-m.finished}
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
		return tracksMsg{title: "❤  " + i18n.T("Liked Songs"), tracks: ts, favorites: true}
	}
}

// likedIDsCmd silently seeds the liked-track id cache behind the 'f' toggle
// (run once after login). Failures are ignored — the cache just stays empty
// until the Liked Songs view is opened.
func (m *Model) likedIDsCmd() tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.Favorites()
		if err != nil {
			return nil
		}
		return likedIDsMsg{ids: likedSet(ts)}
	}
}

// likedSet builds the liked-ids cache from a favorites track list.
func likedSet(ts []deezer.Track) map[string]bool {
	ids := make(map[string]bool, len(ts))
	for _, t := range ts {
		ids[t.ID] = true
	}
	return ids
}

func (m *Model) playlistsCmd() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.Playlists()
		if err != nil {
			return errMsg{err}
		}
		return playlistsMsg{title: "≡  " + i18n.T("My Playlists"), playlists: ps}
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

// artistPageCmd fetches the full artist profile (top tracks + albums + related
// artists) rendered as a sectioned list. Sub-list failures are tolerated by
// the client, so a partial page still opens.
func (m *Model) artistPageCmd(a deezer.ArtistInfo) tea.Cmd {
	return func() tea.Msg {
		p, err := m.client.ArtistProfile(a.ID)
		if err != nil {
			return errMsg{err}
		}
		return artistPageMsg{page: p}
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

// trackMixCmd starts a "song radio" seeded from a track: the mix replaces the
// queue and starts playing, exactly like Flow feeding playNowMsg.
func (m *Model) trackMixCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.TrackMix(id)
		if err != nil {
			return errMsg{err}
		}
		if len(ts) == 0 {
			return statusMsg{text: i18n.T("No mix available")}
		}
		return playNowMsg{tracks: ts}
	}
}

// artistMixCmd starts an "artist radio" (smart radio) seeded from an artist.
func (m *Model) artistMixCmd(id string) tea.Cmd {
	return func() tea.Msg {
		ts, err := m.client.ArtistMix(id)
		if err != nil {
			return errMsg{err}
		}
		if len(ts) == 0 {
			return statusMsg{text: i18n.T("No mix available")}
		}
		return playNowMsg{tracks: ts}
	}
}

func (m *Model) podcastSearchCmd(q string) tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.SearchPodcasts(q)
		if err != nil {
			return errMsg{err}
		}
		return podcastsMsg{title: "🎙 " + i18n.T("Podcasts"), podcasts: ps}
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

// favToggleCmd likes or unlikes a track. The caller already flipped the cached
// liked set optimistically; favToggleMsg carries enough to roll that back.
func (m *Model) favToggleCmd(t deezer.Track, like bool) tea.Cmd {
	return func() tea.Msg {
		var err error
		if like {
			err = m.client.AddFavoriteTrack(t.ID)
		} else {
			err = m.client.RemoveFavoriteTrack(t.ID)
		}
		return favToggleMsg{id: t.ID, name: t.Name, liked: like, err: err}
	}
}

// playlistPickCmd fetches the user's playlists for the add-to-playlist picker.
func (m *Model) playlistPickCmd(t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		ps, err := m.client.Playlists()
		if err != nil {
			return errMsg{err}
		}
		return playlistPickMsg{playlists: ps, track: t}
	}
}

// addToPlaylistCmd appends a track to one of the user's playlists.
func (m *Model) addToPlaylistCmd(p deezer.Playlist, t deezer.Track) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.AddToPlaylist(p.ID, t.ID); err != nil {
			return playlistOpMsg{err: err}
		}
		return playlistOpMsg{status: i18n.Tf("Added to %s", p.Name)}
	}
}

// createPlaylistCmd creates a playlist, optionally seeded with tracks.
func (m *Model) createPlaylistCmd(title string, seed []string) tea.Cmd {
	return func() tea.Msg {
		if _, err := m.client.CreatePlaylist(title, seed); err != nil {
			return playlistOpMsg{err: err}
		}
		return playlistOpMsg{status: i18n.Tf("Created playlist %s", title), refresh: true}
	}
}

// renamePlaylistCmd retitles a playlist the user owns.
func (m *Model) renamePlaylistCmd(p deezer.Playlist, title string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.RenamePlaylist(p.ID, title); err != nil {
			return playlistOpMsg{err: err}
		}
		return playlistOpMsg{status: i18n.Tf("Renamed to %s", title), refresh: true}
	}
}

// deletePlaylistCmd deletes a playlist the user owns (after the y/n confirm).
func (m *Model) deletePlaylistCmd(p deezer.Playlist) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.DeletePlaylist(p.ID); err != nil {
			return playlistOpMsg{err: err}
		}
		return playlistOpMsg{status: i18n.T("Playlist deleted"), refresh: true}
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
		return preloadMsg{plan: plan, dur: t.DurationMS, trackID: t.ID}
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

// nowPlayingCmd reports a track play to Deezer (gw log.listen) off the update
// loop, so the free tier's ad accounting and artist play-counts work like the
// official client. A no-op when the user has disabled ads/reporting.
func (m *Model) nowPlayingCmd(id string) tea.Cmd {
	return func() tea.Msg {
		_ = m.client.NowPlaying(id)
		return nil
	}
}

// downloadCmd saves a full track to the shared download folder off the update
// loop (D key). Downloads are premium-only; the client enforces the gate
// (deezer.ErrPremiumRequired), so a free account gets a clear failure message.
func (m *Model) downloadCmd(t deezer.Track) tea.Cmd {
	id, name := t.ID, t.Name
	return func() tea.Msg {
		path, err := m.client.SaveTrack(context.Background(), id, config.LoadDownloadDir())
		return downloadDoneMsg{name: name, path: path, err: err}
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
