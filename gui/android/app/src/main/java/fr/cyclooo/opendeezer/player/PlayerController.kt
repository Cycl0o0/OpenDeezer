package fr.cyclooo.opendeezer.player

import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Track
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

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
) {
    val isPlaying: Boolean get() = state == Engine.PLAYING
    val hasNext: Boolean get() = index in 0 until queue.lastIndex
    val hasPrev: Boolean get() = index > 0
}

/**
 * Owns the in-app play queue and a ~500ms poll loop that mirrors the engine's
 * playback state into a [StateFlow]. It watches `finishedCount` to auto-advance,
 * exactly like the C-archive desktop frontends.
 */
class PlayerController(private val scope: CoroutineScope) {

    private val _state = MutableStateFlow(PlayerState())
    val state: StateFlow<PlayerState> = _state.asStateFlow()

    private var queue: List<Track> = emptyList()
    private var index: Int = -1
    private var lastFinished: Int = Engine.finishedCount()
    private var pollJob: Job? = null

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

    fun next() {
        if (index < queue.lastIndex) {
            index++
            startCurrent()
        }
    }

    fun prev() {
        // Restart the current track if we're past a few seconds, else go back.
        if (Engine.positionMs() > 3000L || index <= 0) {
            Engine.seek(0)
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

    fun togglePause() {
        Engine.togglePause()
        pushImmediate()
    }

    fun seek(ms: Long) {
        Engine.seek(ms)
        pushImmediate()
    }

    fun setVolume(v: Double) {
        Engine.setVolume(v.coerceIn(0.0, 1.0))
        pushImmediate()
    }

    fun stopPlayback() {
        Engine.stop()
        queue = emptyList()
        index = -1
        pushImmediate()
    }

    private fun startCurrent() {
        val t = queue.getOrNull(index) ?: return
        // Take a baseline so the resulting finish doesn't trigger a spurious advance.
        lastFinished = Engine.finishedCount()
        scope.launch {
            if (t.isEpisode) Engine.playEpisode(t.id) else Engine.play(t.id, t.durationMs)
            pushImmediate()
        }
        pushImmediate()
    }

    private fun poll() {
        val finished = Engine.finishedCount()
        if (finished > lastFinished) {
            lastFinished = finished
            // The current track ended on its own; advance if there's more queue.
            if (index in 0 until queue.lastIndex) {
                index++
                startCurrent()
                return
            }
        }
        push()
    }

    private fun push() {
        val current = queue.getOrNull(index) ?: Engine.nowPlaying()
        _state.value = PlayerState(
            current = current,
            state = Engine.state(),
            positionMs = Engine.positionMs(),
            durationMs = Engine.durationMs(),
            volume = Engine.volume(),
            format = Engine.format(),
            queue = queue,
            index = index,
            connectedDevice = Engine.connectedDevice(),
        )
    }

    // Reflect a user action immediately without waiting for the next poll tick.
    private fun pushImmediate() = push()

    companion object {
        private const val POLL_MS = 500L
    }
}
