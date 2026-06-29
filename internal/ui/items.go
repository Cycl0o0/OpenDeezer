package ui

import "github.com/Cycl0o0/OpenDeezer/internal/deezer"

// rowKind identifies what a list row represents.
type rowKind int

const (
	rowMenu rowKind = iota
	rowTrack
	rowPlaylist
	rowAlbum
	rowArtist
	rowPodcast
	rowEpisode
	rowDevice
	rowPeer // a discovered OpenDeezer Connect device
)

// menuAction is the action a rowMenu triggers.
type menuAction int

const (
	actLiked menuAction = iota
	actPlaylists
	actSearch
	actCharts
	actResume
	actFlow
	actPodcasts
	actRemote
	actRemoteManual // enter a device address by hand
	actWebRemote    // phone web remote (QR + pairing code)
)

// row is a single list entry. It implements bubbles/list.Item.
type row struct {
	kind     rowKind
	title    string
	desc     string
	action   menuAction        // for rowMenu
	track    deezer.Track      // for rowTrack
	playlist deezer.Playlist   // for rowPlaylist
	album    deezer.Album      // for rowAlbum
	artist   deezer.ArtistInfo // for rowArtist
	podcast  deezer.Podcast    // for rowPodcast
	episode  deezer.Episode    // for rowEpisode
	deviceID string            // for rowDevice ("" = system default)
	peerAddr string            // for rowPeer (host:port)
}

func (r row) Title() string       { return r.title }
func (r row) Description() string { return r.desc }
func (r row) FilterValue() string { return r.title }

func trackRow(t deezer.Track) row {
	title := t.Name
	if t.Explicit {
		title = "🅴 " + title // explicit-content badge
	}
	return row{kind: rowTrack, title: title, desc: t.ArtistLine() + " · " + t.AlbumName, track: t}
}

func playlistRow(p deezer.Playlist) row {
	d := p.Owner
	if d == "" {
		d = "playlist"
	}
	return row{kind: rowPlaylist, title: p.Name, desc: d, playlist: p}
}

func albumRow(a deezer.Album) row {
	name := ""
	if len(a.Artists) > 0 {
		name = a.Artists[0].Name
	}
	return row{kind: rowAlbum, title: "💿 " + a.Name, desc: name, album: a}
}

func artistRow(a deezer.ArtistInfo) row {
	return row{kind: rowArtist, title: "♪ " + a.Name, desc: "artist", artist: a}
}

func podcastRow(p deezer.Podcast) row {
	return row{kind: rowPodcast, title: "🎙 " + p.Name, desc: "podcast", podcast: p}
}

func episodeRow(e deezer.Episode) row {
	d := e.ReleaseDate
	if d == "" {
		d = "episode"
	}
	return row{kind: rowEpisode, title: e.Title, desc: d, episode: e}
}

func deviceRow(id, name string, current bool) row {
	title := name
	desc := "output device"
	if current {
		title = "● " + name
		desc = "current"
	}
	return row{kind: rowDevice, title: title, desc: desc, deviceID: id}
}

// peerRow renders a discovered Connect device, with its client/version and (if
// known) what it's currently playing.
func peerRow(p peerDevice) row {
	desc := deviceTypeLabel(p.dev.Client)
	if p.dev.Version != "" {
		desc += " · v" + p.dev.Version
	}
	if p.nowPlaying != "" {
		desc += " · ▶ " + p.nowPlaying
	}
	desc += " · " + p.dev.Addr
	return row{kind: rowPeer, title: "🔊 " + p.dev.Name, desc: desc, peerAddr: p.dev.Addr}
}

// deviceTypeLabel maps a client id to a friendly device type.
func deviceTypeLabel(client string) string {
	switch client {
	case "tui":
		return "Terminal"
	case "darwin", "macos":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux", "gnome", "kde":
		return "Linux"
	case "":
		return "OpenDeezer"
	default:
		return client
	}
}
