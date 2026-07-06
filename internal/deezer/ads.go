package deezer

import (
	"encoding/json"
	"fmt"
)

// AdCadence is Deezer's free-tier audio-ad schedule, read from the gw
// deezer.adConfig method. On a paid (Premium/HiFi) account there are no audio
// ads, so Enabled is false.
type AdCadence struct {
	Enabled  bool `json:"enabled"`  // account is ad-supported (Deezer Free)
	Start    int  `json:"start"`    // first audio ad after this many tracks
	Interval int  `json:"interval"` // one audio ad every this many tracks after that
}

// SetAdsDisabled turns off play-reporting (gw log.listen) and the ad cadence.
//
// This is an opt-out for FREE accounts only. Deezer's free tier is ad-supported:
// reporting each play (like the official web player) is what lets Deezer count
// listens — which credits artists and drives the ad schedule. Disabling it makes
// playback ad-free but stops reporting plays, which denies artists their play
// count and breaks Deezer's terms for the free tier. It is exposed only so the
// user can make that choice explicitly, at their own risk. Paid accounts are
// unaffected (they have no ads either way).
func (c *Client) SetAdsDisabled(disabled bool) { c.adsDisabled.Store(disabled) }

// AdsDisabled reports whether play-reporting/ads are turned off.
func (c *Client) AdsDisabled() bool { return c.adsDisabled.Load() }

// loadAdConfig fetches the free-tier audio-ad cadence (gw deezer.adConfig) once
// per session. Best-effort: any failure leaves the cadence disabled.
func (c *Client) loadAdConfig() {
	b, err := c.gw("deezer.adConfig", "{}")
	if err != nil {
		return
	}
	var r struct {
		Results struct {
			Config struct {
				Audio struct {
					Start    int `json:"start"`
					Interval int `json:"interval"`
				} `json:"audio"`
			} `json:"config"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return
	}
	cad := AdCadence{
		Start:    r.Results.Config.Audio.Start,
		Interval: r.Results.Config.Audio.Interval,
	}
	// Ad-supported when Deezer returned an audio-ad cadence and the account isn't
	// entitled to paid on-demand streaming.
	cad.Enabled = cad.Start > 0 && !c.Account().Premium
	c.adMu.Lock()
	c.adCad = cad
	c.adMu.Unlock()
}

// AdCadenceInfo returns the current audio-ad schedule (lazily fetching it once).
func (c *Client) AdCadenceInfo() AdCadence {
	if !c.adsDisabled.Load() {
		c.adOnce.Do(c.loadAdConfig)
	}
	c.adMu.Lock()
	defer c.adMu.Unlock()
	return c.adCad
}

// NowPlaying tells Deezer a track has started (gw log.listen), mirroring the web
// player so plays are counted server-side — the free tier's ad accounting and
// artist play-counts both depend on it. It also advances the local ad-cadence
// counter. Fire-and-forget: the error is returned only for logging and must
// never block playback. A no-op when the user has disabled ads/reporting.
func (c *Client) NowPlaying(trackID string) error {
	if c.adsDisabled.Load() {
		return nil
	}
	c.adOnce.Do(c.loadAdConfig)
	c.adMu.Lock()
	c.adPlays++
	c.adMu.Unlock()
	body := fmt.Sprintf(`{"next_media":{"media":{"id":%s,"type":"song"}}}`, jsonEsc(trackID))
	_, err := c.gw("log.listen", body)
	return err
}

// AdBreakDue reports whether an audio ad is scheduled before the current track,
// per the free-tier cadence (deezer.adConfig). The audio-ad itself is served by
// a third-party ad network, so OpenDeezer does not fetch or play it; this hook
// exposes the schedule for a future ad-audio integration and for the UI. Always
// false when ads are disabled or on a paid account.
func (c *Client) AdBreakDue() bool {
	if c.adsDisabled.Load() {
		return false
	}
	c.adMu.Lock()
	defer c.adMu.Unlock()
	if !c.adCad.Enabled || c.adCad.Start <= 0 {
		return false
	}
	n := c.adPlays
	if n < c.adCad.Start {
		return false
	}
	if c.adCad.Interval <= 0 {
		return n == c.adCad.Start
	}
	return (n-c.adCad.Start)%c.adCad.Interval == 0
}
