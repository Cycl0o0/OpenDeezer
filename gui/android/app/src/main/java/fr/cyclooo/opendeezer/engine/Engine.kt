package fr.cyclooo.opendeezer.engine

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import odmobile.Odmobile

/**
 * Thin Kotlin facade over the gomobile-bound [Odmobile] static methods.
 *
 * Note: gomobile maps Go `int` to Java `long`, so engine getters such as
 * [state]/[quality]/[crossfadeMs]/[finishedCount] return Long and the
 * corresponding setters take Long arguments. Everything that touches the
 * network is exposed as a suspend function pinned to [Dispatchers.IO].
 */
object Engine {

    // Player states (mirror audio.State in the Go core).
    const val STOPPED = 0
    const val LOADING = 1
    const val PLAYING = 2
    const val PAUSED = 3
    const val ERROR = 4

    // ---- lifecycle / account ----

    suspend fun init(arl: String): Boolean = withContext(Dispatchers.IO) {
        try {
            Odmobile.init(arl)
        } catch (_: Throwable) {
            false
        }
    }

    fun loggedIn(): Boolean = runCatching { Odmobile.loggedIn() }.getOrDefault(false)

    suspend fun account(): Account? = withContext(Dispatchers.IO) {
        Json.account(runCatching { Odmobile.account() }.getOrNull())
    }

    fun setClientInfo(client: String, device: String) {
        runCatching { Odmobile.setClientInfo(client, device) }
    }

    // ---- browse (all network; return parsed models) ----

    suspend fun favorites(): List<Track> = io { Json.tracks(Odmobile.favorites()) }
    suspend fun playlists(): List<Playlist> = io { Json.playlists(Odmobile.playlists()) }
    suspend fun playlistTracks(id: String): List<Track> = io { Json.tracks(Odmobile.playlistTracks(id)) }
    suspend fun albumTracks(id: String): List<Track> = io { Json.tracks(Odmobile.albumTracks(id)) }
    suspend fun flow(): List<Track> = io { Json.tracks(Odmobile.flow()) }
    suspend fun artistTop(id: String): List<Track> = io { Json.tracks(Odmobile.artistTop(id)) }
    suspend fun search(q: String): SearchResults = io { Json.search(Odmobile.search(q)) }
    suspend fun charts(): SearchResults = io { Json.search(Odmobile.charts()) }
    suspend fun searchPodcasts(q: String): List<Podcast> = io { Json.podcasts(Odmobile.searchPodcasts(q)) }
    suspend fun podcastEpisodes(id: String): List<Episode> = io { Json.episodes(Odmobile.podcastEpisodes(id)) }
    suspend fun lyrics(id: String): Lyrics = io { Json.lyrics(Odmobile.lyrics(id)) }

    // ---- playback ----

    suspend fun play(trackId: String, durationMs: Long): Boolean =
        io { runCatching { Odmobile.play(trackId, durationMs) }.getOrDefault(false) }

    suspend fun playEpisode(id: String): Boolean =
        io { runCatching { Odmobile.playEpisode(id) }.getOrDefault(false) }

    fun pause() = runCatching { Odmobile.pause() }.let {}
    fun resume() = runCatching { Odmobile.resume() }.let {}
    fun togglePause() = runCatching { Odmobile.togglePause() }.let {}
    fun stop() = runCatching { Odmobile.stop() }.let {}
    fun seek(ms: Long) = runCatching { Odmobile.seek(ms) }.let {}
    fun setVolume(v: Double) = runCatching { Odmobile.setVolume(v) }.let {}

    fun volume(): Double = runCatching { Odmobile.volume() }.getOrDefault(1.0)
    fun state(): Int = runCatching { Odmobile.state().toInt() }.getOrDefault(STOPPED)
    fun positionMs(): Long = runCatching { Odmobile.positionMS() }.getOrDefault(0L)
    fun durationMs(): Long = runCatching { Odmobile.durationMS() }.getOrDefault(0L)
    fun format(): String = runCatching { Odmobile.format() }.getOrDefault("")
    fun finishedCount(): Int = runCatching { Odmobile.finishedCount().toInt() }.getOrDefault(0)
    fun nowPlaying(): Track? = Json.nowPlaying(runCatching { Odmobile.nowPlaying() }.getOrNull())

    // ---- settings ----

    fun setQuality(level: Int) = runCatching { Odmobile.setQuality(level.toLong()) }.let {}
    fun quality(): Int = runCatching { Odmobile.quality().toInt() }.getOrDefault(0)
    fun setReplayGain(on: Boolean) = runCatching { Odmobile.setReplayGain(on) }.let {}
    fun replayGain(): Boolean = runCatching { Odmobile.replayGain() }.getOrDefault(false)
    fun setGapless(on: Boolean) = runCatching { Odmobile.setGapless(on) }.let {}
    fun gapless(): Boolean = runCatching { Odmobile.gapless() }.getOrDefault(true)
    fun setCrossfadeMs(ms: Int) = runCatching { Odmobile.setCrossfadeMS(ms.toLong()) }.let {}
    fun crossfadeMs(): Int = runCatching { Odmobile.crossfadeMS().toInt() }.getOrDefault(0)

    // ---- library writes ----

    suspend fun addFavorite(id: String): Boolean = io { runCatching { Odmobile.addFavorite(id) }.getOrDefault(false) }
    suspend fun removeFavorite(id: String): Boolean = io { runCatching { Odmobile.removeFavorite(id) }.getOrDefault(false) }
    suspend fun addToPlaylist(playlistId: String, trackId: String): Boolean =
        io { runCatching { Odmobile.addToPlaylist(playlistId, trackId) }.getOrDefault(false) }
    suspend fun removeFromPlaylist(playlistId: String, trackId: String): Boolean =
        io { runCatching { Odmobile.removeFromPlaylist(playlistId, trackId) }.getOrDefault(false) }
    suspend fun createPlaylist(title: String): String? = io { Json.playlistId(Odmobile.createPlaylist(title)) }
    suspend fun renamePlaylist(id: String, title: String): Boolean =
        io { runCatching { Odmobile.renamePlaylist(id, title) }.getOrDefault(false) }
    suspend fun deletePlaylist(id: String): Boolean = io { runCatching { Odmobile.deletePlaylist(id) }.getOrDefault(false) }

    // ---- OpenDeezer Connect ----

    suspend fun discoverDevices(timeoutMs: Long = 700L): List<ConnectDevice> =
        io { Json.devices(Odmobile.discoverDevices(timeoutMs)) }
    suspend fun connectDevice(addr: String): Boolean =
        io { runCatching { Odmobile.connectDevice(addr) }.getOrDefault(false) }
    fun disconnectDevice() = runCatching { Odmobile.disconnectDevice() }.let {}
    fun connectedDevice(): String = runCatching { Odmobile.connectedDevice() }.getOrDefault("")

    private suspend inline fun <T> io(crossinline block: () -> T): T =
        withContext(Dispatchers.IO) { block() }
}
