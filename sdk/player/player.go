package player

import (
	internalaudio "github.com/Cycl0o0/OpenDeezer/internal/audio"
	sdkdeezer "github.com/Cycl0o0/OpenDeezer/sdk/deezer"
)

// ---- type aliases ----

// State is the player lifecycle state.
type State = internalaudio.State

// Device is an audio output device the user can select.
//
//   - ID        — opaque id passed to [Player.SetDevice]; "" = system default
//   - Name      — human-readable display name
//   - IsDefault — true if this is the system default device
type Device = internalaudio.Device

// ---- state constants ----

const (
	// Stopped means no track is loaded.
	Stopped State = internalaudio.Stopped
	// Loading means a track is buffering before playback starts.
	Loading State = internalaudio.Loading
	// Playing means audio is being output.
	Playing State = internalaudio.Playing
	// Paused means playback is suspended.
	Paused State = internalaudio.Paused
	// Errored means a download or decode error occurred. See [Player.LastError].
	Errored State = internalaudio.Errored
)

// ---- Player ----

// Player streams, decrypts, decodes (MP3 + FLAC) and plays Deezer audio with
// gapless/crossfade transitions, ReplayGain normalisation, and output-device
// selection.
//
// Create one with [NewPlayer]. A Player must be closed with [Player.Close] when
// no longer needed; it owns an audio output device for its lifetime.
//
// All methods are safe for concurrent use.
type Player struct {
	p *internalaudio.Player
}

// NewPlayer initialises the audio output device and starts the playback engine.
// Returns an error if no audio output is available (e.g. headless server).
func NewPlayer() (*Player, error) {
	p, err := internalaudio.NewPlayer()
	if err != nil {
		return nil, err
	}
	return &Player{p: p}, nil
}

// ---- playback ----

// Play starts playing plan immediately, replacing any current track. plan must
// come from [sdk/deezer.Client.PrepareStream] or
// [sdk/deezer.Client.PodcastEpisodeStream]. durationMS is the track's duration
// in milliseconds (used for progress and gapless timing); pass the value from
// the Track metadata.
func (pl *Player) Play(plan *sdkdeezer.StreamPlan, durationMS int64) error {
	return pl.p.Play(plan, durationMS)
}

// Preload prepares the next track for a gapless or crossfaded transition. Call
// it shortly before the current track ends. It is a no-op when gapless and
// crossfade are both disabled.
func (pl *Player) Preload(plan *sdkdeezer.StreamPlan, durationMS int64) {
	pl.p.Preload(plan, durationMS)
}

// Pause suspends playback (no-op if not playing).
func (pl *Player) Pause() { pl.p.Pause() }

// Resume resumes playback after a pause (no-op if not paused).
func (pl *Player) Resume() { pl.p.Resume() }

// TogglePause toggles between playing and paused.
func (pl *Player) TogglePause() { pl.p.TogglePause() }

// Stop halts playback and releases the current source.
func (pl *Player) Stop() { pl.p.Stop() }

// Close stops playback, releases sources, and closes the audio output device.
// The Player must not be used after Close.
func (pl *Player) Close() { pl.p.Close() }

// SeekMS seeks to ms milliseconds from the start of the current track.
func (pl *Player) SeekMS(ms int64) { pl.p.SeekMS(ms) }

// SetOnFinish registers a callback that is called when a track ends naturally
// (not when stopped or skipped). Use this to advance the queue.
func (pl *Player) SetOnFinish(fn func()) { pl.p.SetOnFinish(fn) }

// ---- state ----

// State returns the current playback state.
func (pl *Player) State() State { return pl.p.State() }

// PositionMS returns the playback position in milliseconds.
func (pl *Player) PositionMS() int64 { return pl.p.PositionMS() }

// DurationMS returns the current track's duration in milliseconds.
func (pl *Player) DurationMS() int64 { return pl.p.DurationMS() }

// LastError returns the most recent download or decode error message, or "" if
// none.
func (pl *Player) LastError() string { return pl.p.LastError() }

// Format returns the actual stream format of the current track (e.g. "FLAC",
// "MP3_320"), or "" when nothing is playing.
func (pl *Player) Format() string { return pl.p.Format() }

// ---- volume and gain ----

// Volume returns the current volume (0.0–1.0).
func (pl *Player) Volume() float64 { return pl.p.Volume() }

// SetVolume sets the absolute volume (clamped to 0.0–1.0).
func (pl *Player) SetVolume(v float64) { pl.p.SetVolume(v) }

// SetReplayGain enables (true) or disables (false) loudness normalisation using
// the per-track ReplayGain value from [sdkdeezer.StreamPlan.GainDB].
func (pl *Player) SetReplayGain(on bool) { pl.p.SetReplayGain(on) }

// ReplayGain reports whether ReplayGain normalisation is enabled.
func (pl *Player) ReplayGain() bool { return pl.p.ReplayGain() }

// ---- gapless / crossfade ----

// SetGapless enables (true) or disables (false) gapless transitions. When
// enabled and a next source is preloaded, the transition between tracks has no
// silence. Default: true.
func (pl *Player) SetGapless(on bool) { pl.p.SetGapless(on) }

// Gapless reports whether gapless transitions are enabled.
func (pl *Player) Gapless() bool { return pl.p.Gapless() }

// SetCrossfadeMS sets the crossfade duration in milliseconds. 0 disables
// crossfade. When non-zero, the tail of the current track fades out while the
// next fades in.
func (pl *Player) SetCrossfadeMS(ms int) { pl.p.SetCrossfadeMS(ms) }

// CrossfadeMS returns the current crossfade duration in milliseconds.
func (pl *Player) CrossfadeMS() int { return pl.p.CrossfadeMS() }

// ---- output device selection ----

// Devices lists the available audio output devices. The returned slice always
// includes the system default (ID == "").
func (pl *Player) Devices() ([]Device, error) { return pl.p.Devices() }

// SetDevice switches the output to the given device id. Pass "" for the system
// default.
func (pl *Player) SetDevice(id string) error { return pl.p.SetDevice(id) }

// CurrentDevice returns the id of the currently selected output device, or ""
// for the system default.
func (pl *Player) CurrentDevice() string { return pl.p.CurrentDevice() }
