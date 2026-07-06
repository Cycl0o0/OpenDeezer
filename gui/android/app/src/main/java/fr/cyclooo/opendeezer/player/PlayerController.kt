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
    val hasNext: Boolean get() = index in 0 until queue.lastIndex
    val hasPrev: Boolean get() = index > 0
}

/** One-shot download outcome surfaced to the UI shell (rendered as a snackbar). */
sealed interface DownloadEvent {
    val trackName: String
    data class Started(override val trackName: String) : DownloadEvent
    data class Saved(override val trackName: String, val path: String) : DownloadEvent
    data class Failed(override val trackName: String, val error: String) : DownloadEvent
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

    fun start() {
        if (pollJob?.isActive == true) return
        lastFinished = Engine.finishedCount()
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

    fun next() {
        if (index < queue.lastIndex) {
            index++
            startCurrent()
        }
    }

    fun prev() {
        // Restart the current track if we're past a few seconds, else go back.
        if (Engine.positionMs() > 3000L || index <= 0) {
            control { Engine.seek(0) }
        } else {
            index--
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
        control { Engine.stop() }
        pushImmediate()
    }

    // B4: set repeat mode locally and forward to any connected remote.
    // mode: 0=off, 1=all, 2=one
    fun setRepeat(mode: Int) {
        repeatMode = mode.coerceIn(0, 2)
        control { Engine.setRepeat(repeatMode) }
    }

    // B4: toggle shuffle locally and forward to any connected remote.
    fun setShuffle(on: Boolean) {
        shuffleEnabled = on
        control { Engine.setShuffle(if (on) 1 else 0) }
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
        scope.launch {
            if (t.isEpisode) Engine.playEpisode(t.id, t.durationMs) else Engine.play(t.id, t.durationMs)
            if (gen == startGen) {
                // Baseline after the new track actually replaced the old one, so a
                // natural finish during the async start can't trigger an advance.
                lastFinished = Engine.finishedCount()
                startInFlight = false
            }
            pushImmediate()
        }
        pushImmediate()
    }

    private fun poll() {
        if (startInFlight) {
            push()
            return
        }
        val finished = Engine.finishedCount()
        if (finished > lastFinished) {
            lastFinished = finished
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
    }

    // Reflect a user action immediately without waiting for the next poll tick.
    private fun pushImmediate() = push()

    companion object {
        private const val POLL_MS = 500L
    }
}
