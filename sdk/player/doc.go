// Package player provides in-process Deezer audio playback via [Player].
//
// The audio backend is chosen at build time:
//   - default — miniaudio via [github.com/gen2brain/malgo] (adds output-device
//     selection, works on macOS, Linux and Windows)
//   - build tag "otosink" — [github.com/ebitengine/oto/v3] (used for the
//     macOS GUI where malgo's CoreAudio callback is unreliable in a c-archive)
//
// Both backends require cgo. If you only need API access, search, or
// download/decrypt without local playback, skip this package and use
// [sdk/deezer] alone.
//
// # Basic playback
//
//	p, err := player.NewPlayer()
//	if err != nil { log.Fatal(err) }
//	defer p.Close()
//
//	plan, _ := dzClient.PrepareStream(trackID)
//	p.Play(plan, track.DurationMS)
//
//	// Advance when the track ends.
//	p.SetOnFinish(func() { queue.Advance() })
//
// # Preload for gapless transitions
//
//	p.Preload(nextPlan, nextTrack.DurationMS)
//
// # Volume and ReplayGain
//
//	p.SetVolume(0.8)          // 80 %
//	p.SetReplayGain(true)     // loudness normalisation using the track's gain
package player
