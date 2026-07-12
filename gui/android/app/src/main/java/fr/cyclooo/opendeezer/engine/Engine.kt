package fr.cyclooo.opendeezer.engine

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import odmobile.Odmobile
import org.json.JSONObject

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

    // Why the most recent [init] failed: 0 ok, 1 ARL expired/invalid, 2 no
    // internet, 3 other. Only meaningful right after [init] returned false.
    // gomobile maps Go int -> Java long, hence toInt(); defaults to 3 (other).
    fun loginErrorKind(): Int = runCatching { Odmobile.loginErrorKind().toInt() }.getOrDefault(3)

    suspend fun account(): Account? = withContext(Dispatchers.IO) {
        Json.account(runCatching { Odmobile.account() }.getOrNull())
    }

    fun setClientInfo(client: String, device: String) {
        runCatching { Odmobile.setClientInfo(client, device) }
    }

    // Tears the engine session down: stops playback, closes the control server
    // and Connect-host advertising, and forgets the Deezer client so a later
    // init starts fresh. Suspend — it closes network listeners.
    suspend fun logout() = io { runCatching { Odmobile.logout() }.let {} }

    // Checks GitHub for a newer release. Network-bound and best-effort — a
    // failure (offline, rate-limited, etc.) just yields null, never a crash.
    suspend fun checkUpdate(): UpdateInfo? = io { Json.updateInfo(runCatching { Odmobile.checkUpdate() }.getOrNull()) }

    // ---- browse (all network; return parsed models) ----

    suspend fun favorites(): List<Track> = io { Json.tracks(Odmobile.favorites()) }

    // Client-side favourite check: the engine has no dedicated lookup, so we
    // reconcile against the loaded favourites list. Best-effort — false on error.
    suspend fun isFavorite(id: String): Boolean =
        runCatching { favorites().any { it.id == id } }.getOrDefault(false)
    suspend fun playlists(): List<Playlist> = io { Json.playlists(Odmobile.playlists()) }
    suspend fun playlistTracks(id: String): List<Track> = io { Json.tracks(Odmobile.playlistTracks(id)) }
    suspend fun albumTracks(id: String): List<Track> = io { Json.tracks(Odmobile.albumTracks(id)) }
    suspend fun flow(): List<Track> = io { Json.tracks(Odmobile.flow()) }
    suspend fun artistTop(id: String): List<Track> = io { Json.tracks(Odmobile.artistTop(id)) }

    // Full artist profile (top tracks + albums + related artists) in one call.
    // Null when the engine returned an error payload so the screen can offer a retry.
    suspend fun artistProfile(id: String): ArtistPage? =
        io { Json.artistPage(runCatching { Odmobile.artistProfile(id) }.getOrNull()) }
    suspend fun search(q: String): SearchResults = io { Json.search(Odmobile.search(q)) }
    suspend fun charts(): SearchResults = io { Json.search(Odmobile.charts()) }
    suspend fun home(): HomeData = io { Json.home(runCatching { Odmobile.home() }.getOrNull()) }
    suspend fun searchPodcasts(q: String): List<Podcast> = io { Json.podcasts(Odmobile.searchPodcasts(q)) }
    suspend fun podcastEpisodes(id: String): List<Episode> = io { Json.episodes(Odmobile.podcastEpisodes(id)) }
    suspend fun lyrics(id: String): Lyrics = io { Json.lyrics(Odmobile.lyrics(id)) }

    // Failure-aware variants: null when the engine returned an {"error":...}
    // payload (or nothing), so "couldn't load" is distinguishable from "empty"
    // and retry UIs can actually trigger.
    suspend fun homeOrNull(): HomeData? =
        io { raw(runCatching { Odmobile.home() }.getOrNull())?.let(Json::home) }
    suspend fun searchOrNull(q: String): SearchResults? =
        io { raw(runCatching { Odmobile.search(q) }.getOrNull())?.let(Json::search) }
    suspend fun chartsOrNull(): SearchResults? =
        io { raw(runCatching { Odmobile.charts() }.getOrNull())?.let(Json::search) }
    suspend fun flowOrNull(): List<Track>? =
        io { raw(runCatching { Odmobile.flow() }.getOrNull())?.let(Json::tracks) }
    suspend fun favoritesOrNull(): List<Track>? =
        io { raw(runCatching { Odmobile.favorites() }.getOrNull())?.let(Json::tracks) }
    suspend fun playlistsOrNull(): List<Playlist>? =
        io { raw(runCatching { Odmobile.playlists() }.getOrNull())?.let(Json::playlists) }

    private fun raw(s: String?): String? = if (Json.hasError(s)) null else s

    // ---- playback ----

    suspend fun play(trackId: String, durationMs: Long): Boolean =
        io { runCatching { Odmobile.play(trackId, durationMs) }.getOrDefault(false) }

    suspend fun playEpisode(id: String, durationMs: Long = 0L): Boolean =
        io { runCatching { Odmobile.playEpisodeMS(id, durationMs) }.getOrDefault(false) }

    // ---- gapless preload ----
    // Resolves and buffers the NEXT queue track so the engine swaps into it
    // seamlessly at the track boundary instead of the UI doing a full network
    // re-resolve (mirrors the desktop GUIs' DZPreload). The bound Go func
    // returns an error, which gomobile surfaces as a thrown exception — mapped
    // to false here so callers can fall back to the plain-resolve advance.
    suspend fun preload(id: String): Boolean =
        io { runCatching { Odmobile.preload(id); true }.getOrDefault(false) }

    // Discards a previously armed preload. Call whenever the upcoming track
    // stops being deterministic (shuffle/repeat toggles, queue edits, stop).
    suspend fun clearPreload() = io { runCatching { Odmobile.clearPreload() }.let {} }

    // ---- downloads (premium-only; the engine rejects the request otherwise) ----
    // Returns the engine's raw JSON: {"path":"..."} on success or {"error":"..."}.
    // Pass "" for [dir] to save into the shared default download folder.
    suspend fun download(id: String, dir: String): String =
        io { runCatching { Odmobile.downloadTrack(id, dir) }.getOrDefault("""{"error":"failed"}""") }

    // The shared default download folder (empty when the engine has none yet).
    suspend fun downloadDir(): String = io { runCatching { Odmobile.downloadDir() }.getOrDefault("") }

    // Persists a new shared default download folder; false when the engine can't use it.
    suspend fun setDownloadDir(p: String): Boolean =
        io { runCatching { Odmobile.setDownloadDir(p) }.getOrDefault(false) }

    // Whether the currently-playing stream is a 30s preview rather than the full track.
    fun isPreview(): Boolean = runCatching { Odmobile.isPreview() }.getOrDefault(false)

    // ---- ads (Deezer Free only) ----
    // Whether ad reporting is suppressed. Disabling ads also stops reporting plays,
    // which breaks Deezer's terms — see the disclaimer in SettingsScreen.
    suspend fun adsDisabled(): Boolean = io { runCatching { Odmobile.adsDisabled() }.getOrDefault(false) }
    suspend fun setAdsDisabled(off: Boolean): Boolean =
        io { runCatching { Odmobile.setAdsDisabled(off) }.getOrDefault(false) }

    fun pause() = runCatching { Odmobile.pause() }.let {}
    fun resume() = runCatching { Odmobile.resume() }.let {}
    fun togglePause() = runCatching { Odmobile.togglePause() }.let {}
    fun stop() = runCatching { Odmobile.stop() }.let {}
    fun seek(ms: Long) = runCatching { Odmobile.seek(ms) }.let {}
    fun setVolume(v: Double) = runCatching { Odmobile.setVolume(v) }.let {}

    // Suspends/resumes the local OS audio device without touching playback
    // state — used for audio-focus handling (e.g. release audio during calls).
    fun setOutputSuspended(on: Boolean) = runCatching { Odmobile.setOutputSuspended(on) }.let {}

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

    // ---- equalizer + mono downmix ----
    // The engine owns all EQ state and persistence (manual band edits flip the
    // preset to "custom" engine-side); these only read state / forward changes.

    fun eqState(): EqState? = Json.eqState(runCatching { Odmobile.eqjson() }.getOrNull())
    fun setEqEnabled(on: Boolean) = setEq("""{"enabled":$on}""")
    fun setEqMono(on: Boolean) = setEq("""{"mono":$on}""")
    fun setEqPreset(name: String) = setEq("""{"preset":${JSONObject.quote(name)}}""")
    fun setEqBand(index: Int, gainDb: Double) = setEq("""{"band":{"index":$index,"gainDb":$gainDb}}""")
    fun setEqPreamp(db: Double) = setEq("""{"preampDb":$db}""")

    private fun setEq(js: String) = runCatching { Odmobile.setEQJSON(js) }.let {}

    // ---- sleep timer ----
    // Pause after [minutes] (auto fade-out), or when the current track ends when
    // [endOfTrack] is true. minutes <= 0 with endOfTrack == false cancels the timer.
    fun setSleepTimer(minutes: Int, endOfTrack: Boolean) =
        runCatching { Odmobile.setSleepTimer(minutes.toLong(), if (endOfTrack) 1L else 0L) }.let {}
    fun cancelSleepTimer() = runCatching { Odmobile.cancelSleepTimer() }.let {}
    fun sleepActive(): Boolean = runCatching { Odmobile.sleepActive() != 0L }.getOrDefault(false)
    fun sleepEndOfTrack(): Boolean = runCatching { Odmobile.sleepEndOfTrack() != 0L }.getOrDefault(false)
    fun sleepRemainingMs(): Long = runCatching { Odmobile.sleepRemainingMS() }.getOrDefault(0L)

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
    // Suspend: sends a final Stop to the remote over HTTP, so keep it off main.
    suspend fun disconnectDevice() = io { runCatching { Odmobile.disconnectDevice() }.let {} }
    fun connectedDevice(): String = runCatching { Odmobile.connectedDevice() }.getOrDefault("")

    // ---- repeat / shuffle (forwarded to remote when a Connect device is active) ----

    // mode: 0=off, 1=all, 2=one
    fun setRepeat(mode: Int) = runCatching { Odmobile.setRepeat(mode.toLong()) }.let {}
    // on: 0=off, 1=on
    fun setShuffle(on: Int) = runCatching { Odmobile.setShuffle(on.toLong()) }.let {}

    // ---- phone web remote ----

    fun setWebRemoteEnabled(on: Boolean) =
        runCatching { Odmobile.webRemoteSetEnabled(if (on) 1L else 0L) }.let {}

    fun webRemoteInfo(): WebRemoteInfo? =
        Json.webRemoteInfo(runCatching { Odmobile.webRemoteInfo() }.getOrNull())

    suspend fun webRemoteQRPng(): ByteArray =
        io { runCatching { Odmobile.webRemoteQRPNG() ?: ByteArray(0) }.getOrDefault(ByteArray(0)) }

    // ---- OpenDeezer Connect host ----
    // Advertise this device so other same-account OpenDeezer apps can discover
    // and control it. Mirrors the desktop "make this device reachable" toggle.

    fun setConnectHostEnabled(on: Boolean) =
        runCatching { Odmobile.connectHostSetEnabled(if (on) 1L else 0L) }.let {}

    fun connectHostInfo(): ConnectHostInfo? =
        Json.connectHostInfo(runCatching { Odmobile.connectHostInfo() }.getOrNull())

    private suspend inline fun <T> io(crossinline block: () -> T): T =
        withContext(Dispatchers.IO) { block() }
}
