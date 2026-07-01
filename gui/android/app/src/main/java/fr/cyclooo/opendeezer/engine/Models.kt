package fr.cyclooo.opendeezer.engine

import org.json.JSONArray
import org.json.JSONObject

// ---- domain models (mirror the engine's JSON wire shapes) ----

data class Artist(val id: String, val name: String)

data class Track(
    val id: String,
    val name: String,
    val durationMs: Long,
    val artists: List<Artist>,
    val artistLine: String,
    val albumName: String,
    val artworkUrl: String,
    val explicit: Boolean,
    // Episodes ride the same queue but resolve through a different engine call.
    val isEpisode: Boolean = false,
)

data class Album(
    val id: String,
    val name: String,
    val artistLine: String,
    val artworkUrl: String,
)

data class Playlist(
    val id: String,
    val name: String,
    val owner: String,
    val trackCount: Int,
    val artworkUrl: String,
)

data class ArtistInfo(
    val id: String,
    val name: String,
    val artworkUrl: String,
    val nbFans: Int,
)

data class Podcast(
    val id: String,
    val name: String,
    val description: String,
    val artworkUrl: String,
    val episodeCount: Int,
)

data class Episode(
    val id: String,
    val title: String,
    val description: String,
    val releaseDate: String,
    val durationMs: Long,
    val artworkUrl: String,
) {
    fun asTrack(): Track = Track(
        id = id,
        name = title,
        durationMs = durationMs,
        artists = emptyList(),
        artistLine = releaseDate,
        albumName = "",
        artworkUrl = artworkUrl,
        explicit = false,
        isEpisode = true,
    )
}

data class Account(
    val userId: String,
    val name: String,
    val offer: String,
    val canHq: Boolean,
    val canHifi: Boolean,
    val premium: Boolean,
    val loggedIn: Boolean,
)

data class LyricLine(val timeMs: Long, val text: String)

data class Lyrics(val plain: String, val lines: List<LyricLine>) {
    val isSynced: Boolean get() = lines.isNotEmpty()
}

data class SearchResults(
    val tracks: List<Track>,
    val albums: List<Album>,
    val artists: List<ArtistInfo>,
    val playlists: List<Playlist>,
) {
    val isEmpty: Boolean
        get() = tracks.isEmpty() && albums.isEmpty() && artists.isEmpty() && playlists.isEmpty()

    companion object {
        val EMPTY = SearchResults(emptyList(), emptyList(), emptyList(), emptyList())
    }
}

data class ConnectDevice(
    val name: String,
    val addr: String,
    val client: String,
    val version: String,
) {
    // Mirrors the desktop GUIs' client-id -> device-type mapping.
    val typeLabel: String
        get() = when (client.lowercase()) {
            "tui" -> "Terminal"
            "darwin", "macos" -> "macOS"
            "windows" -> "Windows"
            "linux", "gnome", "kde" -> "Linux"
            "android" -> "Android"
            "" -> "Device"
            else -> client
        }
}

data class WebRemoteInfo(
    val enabled: Boolean,
    val code: String,
    val url: String,
    val port: Int,
)

// Mirrors the engine's update.Info JSON: {current, latest, hasUpdate, url, notes}.
data class UpdateInfo(
    val current: String,
    val latest: String,
    val hasUpdate: Boolean,
    val url: String,
    val notes: String,
)

data class HomeData(
    val topTracks: List<Track>,
    val topAlbums: List<Album>,
    val playlists: List<Playlist>,
) {
    companion object {
        val EMPTY = HomeData(emptyList(), emptyList(), emptyList())
    }
}

// ---- parsing (org.json; tolerant of {"error":...} payloads) ----

object Json {

    private fun obj(s: String?): JSONObject? =
        try {
            if (s.isNullOrBlank()) null else JSONObject(s)
        } catch (_: Throwable) {
            null
        }

    private fun JSONObject.artists(key: String): List<Artist> {
        val arr = optJSONArray(key) ?: return emptyList()
        return (0 until arr.length()).mapNotNull { i ->
            arr.optJSONObject(i)?.let { Artist(it.optString("id"), it.optString("name")) }
        }
    }

    private fun JSONObject.toTrack(episode: Boolean = false): Track {
        val arts = artists("artists")
        val line = optString("artistLine").ifBlank { arts.joinToString(", ") { it.name } }
        return Track(
            id = optString("id"),
            name = optString("name"),
            durationMs = optLong("durationMs"),
            artists = arts,
            artistLine = line,
            albumName = optString("albumName"),
            artworkUrl = optString("artworkUrl"),
            explicit = optBoolean("explicit"),
            isEpisode = episode,
        )
    }

    private fun JSONObject.toAlbum(): Album {
        val arts = artists("artists")
        return Album(
            id = optString("id"),
            name = optString("name"),
            artistLine = arts.joinToString(", ") { it.name },
            artworkUrl = optString("artworkUrl"),
        )
    }

    private fun JSONObject.toPlaylist() = Playlist(
        id = optString("id"),
        name = optString("name"),
        owner = optString("owner"),
        trackCount = optInt("trackCount"),
        artworkUrl = optString("artworkUrl"),
    )

    private fun JSONObject.toArtistInfo() = ArtistInfo(
        id = optString("id"),
        name = optString("name"),
        artworkUrl = optString("artworkUrl"),
        nbFans = optInt("nbFans"),
    )

    private fun tracksOf(arr: JSONArray?): List<Track> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).mapNotNull { arr.optJSONObject(it)?.toTrack() }
    }

    private fun albumsOf(arr: JSONArray?): List<Album> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).mapNotNull { arr.optJSONObject(it)?.toAlbum() }
    }

    private fun artistsOf(arr: JSONArray?): List<ArtistInfo> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).mapNotNull { arr.optJSONObject(it)?.toArtistInfo() }
    }

    private fun playlistsOf(arr: JSONArray?): List<Playlist> {
        if (arr == null) return emptyList()
        return (0 until arr.length()).mapNotNull { arr.optJSONObject(it)?.toPlaylist() }
    }

    fun tracks(s: String?): List<Track> = tracksOf(obj(s)?.optJSONArray("tracks"))

    fun playlists(s: String?): List<Playlist> = playlistsOf(obj(s)?.optJSONArray("playlists"))

    fun search(s: String?): SearchResults {
        val o = obj(s) ?: return SearchResults.EMPTY
        return SearchResults(
            tracks = tracksOf(o.optJSONArray("tracks")),
            albums = albumsOf(o.optJSONArray("albums")),
            artists = artistsOf(o.optJSONArray("artists")),
            playlists = playlistsOf(o.optJSONArray("playlists")),
        )
    }

    fun podcasts(s: String?): List<Podcast> {
        val arr = obj(s)?.optJSONArray("podcasts") ?: return emptyList()
        return (0 until arr.length()).mapNotNull { i ->
            arr.optJSONObject(i)?.let {
                Podcast(
                    id = it.optString("id"),
                    name = it.optString("name"),
                    description = it.optString("description"),
                    artworkUrl = it.optString("artworkUrl"),
                    episodeCount = it.optInt("episodeCount"),
                )
            }
        }
    }

    fun episodes(s: String?): List<Episode> {
        val arr = obj(s)?.optJSONArray("episodes") ?: return emptyList()
        return (0 until arr.length()).mapNotNull { i ->
            arr.optJSONObject(i)?.let {
                Episode(
                    id = it.optString("id"),
                    title = it.optString("title"),
                    description = it.optString("description"),
                    releaseDate = it.optString("releaseDate"),
                    durationMs = it.optLong("durationMs"),
                    artworkUrl = it.optString("artworkUrl"),
                )
            }
        }
    }

    fun account(s: String?): Account? {
        val o = obj(s) ?: return null
        if (o.has("error")) return null
        return Account(
            userId = o.optString("userId"),
            name = o.optString("name"),
            offer = o.optString("offer"),
            canHq = o.optBoolean("canHq"),
            canHifi = o.optBoolean("canHifi"),
            premium = o.optBoolean("premium"),
            loggedIn = o.optBoolean("loggedIn"),
        )
    }

    fun nowPlaying(s: String?): Track? {
        val o = obj(s) ?: return null
        if (o.optString("id").isBlank()) return null
        return o.toTrack()
    }

    // Lyrics marshal the raw Go struct: {"Plain": "...", "Synced": [{"TimeMS":..,"Text":..}]}.
    fun lyrics(s: String?): Lyrics {
        val o = obj(s) ?: return Lyrics("", emptyList())
        val plain = o.optString("Plain")
        val synced = o.optJSONArray("Synced")
        val lines = if (synced == null) emptyList() else (0 until synced.length()).mapNotNull { i ->
            synced.optJSONObject(i)?.let { LyricLine(it.optLong("TimeMS"), it.optString("Text")) }
        }
        return Lyrics(plain, lines)
    }

    fun devices(s: String?): List<ConnectDevice> {
        val arr = try {
            if (s.isNullOrBlank()) null else JSONArray(s)
        } catch (_: Throwable) {
            null
        } ?: return emptyList()
        return (0 until arr.length()).mapNotNull { i ->
            arr.optJSONObject(i)?.let {
                ConnectDevice(
                    name = it.optString("name"),
                    addr = it.optString("addr"),
                    client = it.optString("client"),
                    version = it.optString("version"),
                )
            }
        }
    }

    fun playlistId(s: String?): String? {
        val o = obj(s) ?: return null
        val id = o.optString("id")
        return id.ifBlank { null }
    }

    fun webRemoteInfo(s: String?): WebRemoteInfo? {
        val o = obj(s) ?: return null
        return WebRemoteInfo(
            enabled = o.optBoolean("enabled"),
            code = o.optString("code"),
            url = o.optString("url"),
            port = o.optInt("port"),
        )
    }

    fun home(s: String?): HomeData {
        val o = obj(s) ?: return HomeData.EMPTY
        return HomeData(
            topTracks = tracksOf(o.optJSONArray("topTracks")),
            topAlbums = albumsOf(o.optJSONArray("topAlbums")),
            playlists = playlistsOf(o.optJSONArray("playlists")),
        )
    }

    fun updateInfo(s: String?): UpdateInfo? {
        val o = obj(s) ?: return null
        if (o.has("error")) return null
        return UpdateInfo(
            current = o.optString("current"),
            latest = o.optString("latest"),
            hasUpdate = o.optBoolean("hasUpdate"),
            url = o.optString("url"),
            notes = o.optString("notes"),
        )
    }
}
