package fr.cyclooo.opendeezer.player

import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Track
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

data class PlayerState(
    val current: Track? = null,
    val state: Int = Engine.STOPPED,
    val positionMs: Long = 0L,
    val durationMs: Long = 0L,
    val volume: Double = 1.0,
    val format: String = "",
    val queue: List<Track> = emptyList(),
    val index: Int = -1,
    val connectedDevice: String = "",
    val repeatMode: Int = 0,   // 0=off, 1=all, 2=one
    val shuffle: Boolean = false,
) {
    val isPlaying: Boolean get() = state == Engine.PLAYING

    // B15: next/prev availability honors shuffle + repeat.
    //  - shuffle: there's always another track to jump to (queue > 1).
    //  - repeat-all: "next" wraps past the end, so it's available there too.
    //  - prev restarts the current track at index 0, so it's available whenever
    //    something is loaded.
    val hasNext: Boolean get() = when {
        queue.isEmpty() || index < 0 -> false
        shuffle && queue.size > 1 -> true
        index < queue.lastIndex -> true
        repeatMode == 1 -> true
        else -> false
    }
    val hasPrev: Boolean get() = queue.isNotEmpty() && index >= 0
}

/** One-shot download outcome surfaced to the UI shell (rendered as a snackbar). */
sealed interface DownloadEvent {
    val trackName: String
    data class Started(override val trackName: String) : DownloadEvent
    data class Saved(override val trackName: String, val path: String) : DownloadEvent
    data class Failed(override val trackName: String, val error: String) : DownloadEvent

    // Batch (album/playlist) outcome: [trackName] carries the collection name.
    data class BatchDone(
        override val trackName: String,
        val saved: Int,
        val failed: Int,
        val error: String,
    ) : DownloadEvent
}

/**
 * Owns the in-app play queue and a ~500ms poll loop that mirrors the engine's
 * playback state into a [StateFlow]. It watches `finishedCount` to auto-advance,
 * exactly like the C-archive desktop frontends.
 */
class PlayerController(private val scope: CoroutineScope) {

    private val _state = MutableStateFlow(PlayerState())
    val state: StateFlow<PlayerState> = _state.asStateFlow()

    // Download outcomes are one-shot events, not state, so they ride a SharedFlow
    // that the app shell collects to show a snackbar. Buffered so an emit from the
    // player's own scope never suspends waiting for a collector.
    private val _downloadEvents = MutableSharedFlow<DownloadEvent>(extraBufferCapacity = 8)
    val downloadEvents: SharedFlow<DownloadEvent> = _downloadEvents.asSharedFlow()

    // Set once at login. Downloads are premium-only, so the TrackRow context menu
    // reads this to enable/disable its Download action.
    var premium: Boolean = false

    private var queue: List<Track> = emptyList()
    private var index: Int = -1
    private var lastFinished: Int = Engine.finishedCount()
    private var pollJob: Job? = null
    private var repeatMode: Int = 0       // 0=off, 1=all, 2=one
    private var shuffleEnabled: Boolean = false
    // Serializes track starts: while a start is in flight, poll() must not
    // auto-advance on a finish of the *previous* track (it would double-advance).
    private var startGen: Int = 0
    private var startInFlight: Boolean = false
    // Gapless: id of the queue track armed engine-side via Engine.preload, or
    // null when nothing is armed. When the armed track matches the deterministic
    // next entry at a track boundary, poll() advances the queue pointer without
    // re-issuing a full network resolve (the engine already swapped into it).
    private var preloadedId: String? = null

    // Engine-queue mirror state: the last queue reference + index pushed to the
    // engine, so syncEngineQueue() can cheaply diff on every push() and only send
    // SetQueueJSON when the content changes / SetQueueIndex when the cursor moves.
    private var syncedQueueRef: List<Track>? = null
    private var syncedIndex: Int = Int.MIN_VALUE

    // M1: the last engine QueueVersion we've reconciled with. The poll adopts the
    // engine queue when this moves for a reason other than our own push (a remote
    // controller edited this device's queue). queuePushInFlight blocks adoption
    // while our own SetQueueJSON is landing (it bumps the version).
    private var lastEngineQueueVersion: Long = Engine.queueVersion()
    private var queuePushInFlight: Boolean = false

    // B1: >0 while a local repeat/shuffle change is being forwarded to the
    // engine, so the poll won't briefly revert the UI to the engine's pre-change
    // value before the change lands.
    private var modeSendInFlight: Int = 0

    // Cached liked-track ids (engine truth) so hearts render without a per-track
    // lookup; the Now Playing heart collects this and toggles optimistically.
    private val _likedIds = MutableStateFlow<Set<String>>(emptySet())
    val likedIds: StateFlow<Set<String>> = _likedIds.asStateFlow()

    // One-shot "couldn't update Like" signal (a failed optimistic toggle was
    // reverted); the UI shell shows a snackbar / toast.
    private val _favoriteFailures = MutableSharedFlow<Unit>(extraBufferCapacity = 4)
    val favoriteFailures: SharedFlow<Unit> = _favoriteFailures.asSharedFlow()

    fun start() {
        if (pollJob?.isActive == true) return
        lastFinished = Engine.finishedCount()
        refreshLikedIds()
        pollJob = scope.launch {
            while (isActive) {
                poll()
                delay(POLL_MS)
            }
        }
    }

    fun stop() {
        pollJob?.cancel()
        pollJob = null
    }

    // ---- queue control ----

    fun playQueue(tracks: List<Track>, startIndex: Int) {
        if (tracks.isEmpty()) return
        queue = tracks
        index = startIndex.coerceIn(0, tracks.lastIndex)
        startCurrent()
    }

    fun playSingle(track: Track) = playQueue(listOf(track), 0)

    /** Plays [track], appending it after the current item if a queue exists. */
    fun playNow(track: Track) {
        if (queue.isEmpty()) {
            playSingle(track)
        } else {
            val mutable = queue.toMutableList()
            val at = (index + 1).coerceIn(0, mutable.size)
            mutable.add(at, track)
            queue = mutable
            index = at
            startCurrent()
        }
    }

    /**
     * Saves [track] to the configured download folder off the main thread and
     * reports the outcome via [downloadEvents]. Premium-only — the engine rejects
     * the request for free accounts, which surfaces as a [DownloadEvent.Failed].
     * Episodes ride a different engine path and aren't downloadable here.
     */
    fun download(track: Track) {
        if (track.isEpisode) return
        scope.launch {
            _downloadEvents.emit(DownloadEvent.Started(track.name))
            _downloadEvents.emit(parseDownload(Engine.download(track.id, ""), track.name))
        }
    }

    private fun parseDownload(json: String, trackName: String): DownloadEvent =
        runCatching {
            val o = JSONObject(json)
            val err = o.optString("error")
            if (err.isNotBlank()) DownloadEvent.Failed(trackName, err)
            else DownloadEvent.Saved(trackName, o.optString("path"))
        }.getOrDefault(DownloadEvent.Failed(trackName, ""))

    /** Downloads a whole album to the shared folder; reports a batch summary. */
    fun downloadAlbum(id: String, name: String) = downloadBatch(name) { Engine.downloadAlbum(id) }

    /** Downloads a whole playlist to the shared folder; reports a batch summary. */
    fun downloadPlaylist(id: String, name: String) = downloadBatch(name) { Engine.downloadPlaylist(id) }

    private fun downloadBatch(name: String, call: suspend () -> String) {
        scope.launch {
            _downloadEvents.emit(DownloadEvent.Started(name))
            _downloadEvents.emit(parseBatch(call(), name))
        }
    }

    private fun parseBatch(json: String, name: String): DownloadEvent =
        runCatching {
            val o = JSONObject(json)
            DownloadEvent.BatchDone(name, o.optInt("saved"), o.optInt("failed"), o.optString("error"))
        }.getOrDefault(DownloadEvent.Failed(name, ""))

    // ---- radio ("song radio" / "artist radio") ----
    // Seed a mix and play it through the normal queue path (mirrors Flow).

    /** Starts a "song radio" seeded from [track] (the seed stays first) and plays it. */
    fun startTrackRadio(track: Track) {
        scope.launch {
            val tracks = Engine.trackMix(track.id)
            if (tracks.isNotEmpty()) playQueue(tracks, 0)
        }
    }

    /** Starts an "artist radio" seeded from [artistId] and plays it. */
    fun startArtistRadio(artistId: String) {
        scope.launch {
            val tracks = Engine.artistMix(artistId)
            if (tracks.isNotEmpty()) playQueue(tracks, 0)
        }
    }

    // B15: manual skip honors shuffle + repeat, mirroring the poll's natural-
    // completion policy — shuffle jumps to a random other track, repeat-all wraps
    // past the ends. Repeat-one governs auto-advance only, so it doesn't block a
    // deliberate skip. null means "nowhere to go".
    private fun manualNextIndex(): Int? = when {
        queue.isEmpty() -> null
        shuffleEnabled && queue.size > 1 -> queue.indices.filter { it != index }.random()
        index < queue.lastIndex -> index + 1
        repeatMode == 1 -> 0
        else -> null
    }

    // The prev target when it isn't a restart: a random other track under
    // shuffle, the previous row, or (repeat-all) a wrap to the end. null means
    // "restart the current track" (prev at index 0 with no wrap).
    private fun manualPrevIndex(): Int? = when {
        queue.isEmpty() -> null
        shuffleEnabled && queue.size > 1 -> queue.indices.filter { it != index }.random()
        index > 0 -> index - 1
        repeatMode == 1 -> queue.lastIndex
        else -> null
    }

    fun next() {
        val target = manualNextIndex() ?: return
        index = target
        startCurrent()
    }

    fun prev() {
        // Past a few seconds, "prev" restarts the current track (matches every
        // music player); otherwise step back honoring shuffle/repeat, and at the
        // very start with no wrap, restart instead.
        if (Engine.positionMs() > 3000L) {
            control { Engine.seek(0) }
            return
        }
        val target = manualPrevIndex()
        if (target == null) {
            control { Engine.seek(0) }
        } else {
            index = target
            startCurrent()
        }
    }

    fun jumpTo(i: Int) {
        if (i in queue.indices) {
            index = i
            startCurrent()
        }
    }

    fun togglePause() = control { Engine.togglePause() }

    fun pause() = control { Engine.pause() }

    fun resume() = control { Engine.resume() }

    fun seek(ms: Long) = control { Engine.seek(ms) }

    fun setVolume(v: Double) = control { Engine.setVolume(v.coerceIn(0.0, 1.0)) }

    fun stopPlayback() {
        queue = emptyList()
        index = -1
        preloadedId = null
        control { Engine.stop() }
        scope.launch { Engine.clearPreload() }
        pushImmediate()
    }

    // B4: set repeat mode locally and forward to the engine (which forwards to
    // any connected remote). mode: 0=off, 1=all, 2=one.
    fun setRepeat(mode: Int) {
        repeatMode = mode.coerceIn(0, 2)
        sendMode { Engine.setRepeat(repeatMode) }
        reconcilePreload()
    }

    // B4: toggle shuffle locally and forward to the engine / any connected remote.
    fun setShuffle(on: Boolean) {
        shuffleEnabled = on
        sendMode { Engine.setShuffle(if (on) 1 else 0) }
        reconcilePreload()
    }

    // Forwards a local repeat/shuffle change to the engine off the main thread (a
    // routed Connect device makes this a 15s-timeout HTTP call) while suppressing
    // the poll's mode reconciliation until it lands, so the UI can't flicker back
    // to the engine's pre-change value in between.
    private fun sendMode(block: () -> Unit) {
        modeSendInFlight++
        scope.launch {
            withContext(Dispatchers.IO) { block() }
            modeSendInFlight--
            pushImmediate()
        }
    }

    // ---- truthful favourites ----

    /** Refresh the cached liked-id set from engine truth (login + after a toggle). */
    fun refreshLikedIds() {
        scope.launch { _likedIds.value = Engine.favoriteIds() }
    }

    /**
     * Optimistically flips the heart, then reconciles: on success re-pulls the
     * authoritative id set; on failure reverts the cache and signals a snackbar
     * revert via [favoriteFailures]. Episodes aren't favouritable.
     */
    fun toggleFavorite(track: Track) {
        if (track.isEpisode) return
        val id = track.id
        val wasLiked = _likedIds.value.contains(id)
        _likedIds.value = if (wasLiked) _likedIds.value - id else _likedIds.value + id
        scope.launch {
            val ok = if (wasLiked) Engine.removeFavorite(id) else Engine.addFavorite(id)
            if (ok) {
                _likedIds.value = Engine.favoriteIds()
            } else {
                _likedIds.value = if (wasLiked) _likedIds.value + id else _likedIds.value - id
                _favoriteFailures.emit(Unit)
            }
        }
    }

    // When routed to a Connect device these engine calls do synchronous HTTP
    // (15s timeout), so they must never run on the main thread.
    private fun control(block: () -> Unit) {
        scope.launch {
            withContext(Dispatchers.IO) { block() }
            pushImmediate()
        }
    }

    private fun startCurrent() {
        val t = queue.getOrNull(index) ?: return
        val gen = ++startGen
        startInFlight = true
        // A full play discards any armed preload engine-side; forget ours too so
        // armPreload() re-arms (or clears) from scratch after the start lands.
        preloadedId = null
        scope.launch {
            if (t.isEpisode) Engine.playEpisode(t.id, t.durationMs) else Engine.play(t.id, t.durationMs)
            if (gen == startGen) {
                // Baseline after the new track actually replaced the old one, so a
                // natural finish during the async start can't trigger an advance.
                lastFinished = Engine.finishedCount()
                startInFlight = false
                // Gapless: arm the deterministic next track once this one is live.
                armPreload()
            }
            pushImmediate()
        }
        pushImmediate()
    }

    // ---- gapless preload ----
    // Mirrors the desktop GUIs: after a track starts (or the queue's shape
    // changes), resolve the NEXT deterministic queue entry engine-side so the
    // boundary swap is seamless; discard the armed track when the upcoming one
    // stops being knowable. Preload failures just leave the armed id null, so
    // poll() falls back to the plain full-resolve advance.

    // The queue position poll() will advance to when the current track ends
    // naturally, or null when it isn't knowable up front (shuffle, repeat-one,
    // end of queue without repeat-all).
    private fun deterministicNextIndex(): Int? = when {
        queue.isEmpty() || shuffleEnabled || repeatMode == 2 -> null
        index < queue.lastIndex -> index + 1
        repeatMode == 1 -> 0
        else -> null
    }

    // The track worth preloading, or null. Only when the engine performs a
    // seamless boundary swap (gapless or crossfade — it drops the preload
    // otherwise), and never around episodes (they resolve through a different
    // engine path) or while routed to a Connect remote (the remote streams its
    // own audio; a local preload would download a duplicate stream for nothing).
    private fun nextTrackForPreload(): Track? {
        if (!Engine.gapless() && Engine.crossfadeMs() == 0) return null
        if (queue.getOrNull(index)?.isEpisode == true) return null
        if (Engine.connectedDevice().isNotBlank()) return null
        val next = queue.getOrNull(deterministicNextIndex() ?: return null) ?: return null
        return if (next.isEpisode) null else next
    }

    private suspend fun armPreload() {
        val next = nextTrackForPreload()
        if (next != null) {
            if (next.id == preloadedId) return
            preloadedId = next.id
            // Engine.preload suspends on Dispatchers.IO; a concurrent skip/queue change
            // may re-arm a different id meanwhile. Only clear if we're still the armed one.
            if (!Engine.preload(next.id) && preloadedId == next.id) preloadedId = null
        } else if (preloadedId != null) {
            preloadedId = null
            Engine.clearPreload()
        }
    }

    // A shuffle/repeat change can invalidate an armed linear-next preload:
    // re-arm with the new deterministic next when there is one, otherwise
    // discard the stale preload so the boundary can never swap into a track
    // the queue won't actually play.
    private fun reconcilePreload() {
        scope.launch { armPreload() }
    }

    // B1: drive repeat/shuffle from engine truth so the UI reflects casting and
    // external/remote changes. Suppressed while a local change is in flight (see
    // sendMode) so we don't fight our own not-yet-applied toggle.
    private fun syncModesFromEngine() {
        if (modeSendInFlight > 0) return
        val r = Engine.repeatMode()
        val s = Engine.shuffleOn()
        if (r != repeatMode || s != shuffleEnabled) {
            repeatMode = r
            shuffleEnabled = s
            reconcilePreload()
        }
    }

    // M1: adopt the engine-side queue when a remote controller edits this
    // device's queue (QueueVersion bumps). Replaces the local queue + cursor so
    // now-playing is correct and auto-advance walks the full remote-loaded list
    // instead of stopping after one track. Guards against echoing our own push.
    // Returns true when it took over this poll tick.
    private fun adoptEngineQueueIfChanged(): Boolean {
        if (queuePushInFlight) return false
        val v = Engine.queueVersion()
        if (v == lastEngineQueueVersion) return false
        val snap = Engine.queueSnapshot()
        if (snap == null) {
            lastEngineQueueVersion = v
            return false
        }
        lastEngineQueueVersion = snap.version
        if (snap.tracks.isEmpty()) return false
        val newIndex = snap.index.coerceIn(0, snap.tracks.lastIndex)
        queue = snap.tracks
        index = newIndex
        // Mark adopted content as already-synced so syncEngineQueue() won't echo
        // it straight back to the engine.
        syncedQueueRef = queue
        syncedIndex = index
        preloadedId = null
        // If the engine is already rendering the adopted track (a remote
        // controller both loaded and started it), adopt silently and let the poll
        // auto-advance from here; otherwise start it locally.
        val engineNow = Engine.nowPlaying()
        val adoptedId = snap.tracks.getOrNull(newIndex)?.id
        val engineLive = Engine.state() == Engine.PLAYING || Engine.state() == Engine.LOADING
        if (engineNow != null && engineNow.id == adoptedId && engineLive) {
            lastFinished = Engine.finishedCount()
            scope.launch { armPreload() }
            pushImmediate()
        } else {
            startCurrent()
        }
        return true
    }

    private fun poll() {
        if (startInFlight) {
            push()
            return
        }
        syncModesFromEngine()
        if (adoptEngineQueueIfChanged()) return
        val finished = Engine.finishedCount()
        if (finished > lastFinished) {
            lastFinished = finished
            // Gapless: if the deterministic next entry was preloaded and the
            // engine is still playing past the boundary, it already swapped
            // into that track seamlessly — advance the queue pointer WITHOUT
            // re-issuing a play (which would cut the audio for a re-resolve),
            // then arm the new next. Any other outcome (preload failed, engine
            // stopped, mode changed) falls through to the full-resolve advance.
            val n = deterministicNextIndex()
            if (n != null && preloadedId != null && preloadedId == queue.getOrNull(n)?.id &&
                Engine.state() == Engine.PLAYING
            ) {
                index = n
                preloadedId = null
                scope.launch { armPreload() }
                push()
                return
            }
            if (repeatMode == 2) {
                // Repeat-one: re-start the same track.
                startCurrent()
                return
            }
            if (shuffleEnabled && queue.size > 1) {
                index = queue.indices.filter { it != index }.random()
                startCurrent()
                return
            }
            if (index < queue.lastIndex) {
                // Advance to the next track in the queue.
                index++
                startCurrent()
                return
            }
            if (repeatMode == 1 && queue.isNotEmpty()) {
                // Repeat-all: wrap to the beginning.
                index = 0
                startCurrent()
                return
            }
        }
        push()
    }

    private fun push() {
        val connectedDevice = Engine.connectedDevice()
        val queueTrack = queue.getOrNull(index)
        // B3: when routed to a remote device, always reflect the remote's now-playing
        //     track (Engine.nowPlaying() carries the correct artistId and title).
        // B1: for local podcast episodes the engine enriches metadata asynchronously
        //     via fetchEpisodeMeta; prefer Engine.nowPlaying() so we get the updated info.
        val current = when {
            connectedDevice.isNotBlank() -> Engine.nowPlaying()
            queueTrack?.isEpisode == true -> Engine.nowPlaying() ?: queueTrack
            else -> queueTrack ?: Engine.nowPlaying()
        }
        _state.value = PlayerState(
            current = current,
            state = Engine.state(),
            positionMs = Engine.positionMs(),
            durationMs = Engine.durationMs(),
            volume = Engine.volume(),
            format = Engine.format(),
            queue = queue,
            index = index,
            connectedDevice = connectedDevice,
            repeatMode = repeatMode,
            shuffle = shuffleEnabled,
        )
        syncEngineQueue()
    }

    // ---- engine queue sync ----
    // Mirror the app-owned queue into the engine on every push(): when the queue
    // CONTENT changes (new reference) push the full list + cursor; when only the
    // playing row moves push just the cursor. Lets remote /status show the real
    // queue and lets the engine own gapless-promote over the synced queue. Cheap
    // when nothing changed (reference + int compare), so it's safe on the poll.
    private fun syncEngineQueue() {
        val q = queue
        val i = index
        when {
            q !== syncedQueueRef -> {
                syncedQueueRef = q
                syncedIndex = i
                val json = queueJson(q)
                queuePushInFlight = true
                scope.launch {
                    Engine.setQueueJson(json)
                    Engine.setQueueIndex(i)
                    // Our own SetQueueJSON bumped QueueVersion; record it so the
                    // poll's adoption doesn't mistake our push for a remote edit.
                    lastEngineQueueVersion = Engine.queueVersion()
                    queuePushInFlight = false
                }
            }
            i != syncedIndex -> {
                syncedIndex = i
                scope.launch { Engine.setQueueIndex(i) }
            }
        }
    }

    // Serialises the queue into the engine's list wire shape (only id is strictly
    // required, but durationMs feeds remote end-of-track detection).
    private fun queueJson(tracks: List<Track>): String {
        val arr = JSONArray()
        for (t in tracks) {
            val artists = JSONArray()
            for (a in t.artists) artists.put(JSONObject().put("id", a.id).put("name", a.name))
            arr.put(
                JSONObject()
                    .put("id", t.id)
                    .put("name", t.name)
                    .put("durationMs", t.durationMs)
                    .put("artistLine", t.artistLine)
                    .put("artistId", t.artists.firstOrNull()?.id ?: "")
                    .put("artists", artists)
                    .put("albumName", t.albumName)
                    .put("artworkUrl", t.artworkUrl)
                    .put("explicit", t.explicit),
            )
        }
        return arr.toString()
    }

    // Reflect a user action immediately without waiting for the next poll tick.
    private fun pushImmediate() = push()

    companion object {
        private const val POLL_MS = 500L
    }
}
