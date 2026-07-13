package fr.cyclooo.opendeezer

import android.app.Application
import fr.cyclooo.opendeezer.player.PlayerController
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import odmobile.Odmobile

/**
 * Owns the single [PlayerController] on an application-lifetime scope.
 *
 * A controller tied to the Activity's viewModelScope dies the moment the
 * ViewModel is cleared (e.g. the task is swiped from recents) while the
 * foreground [fr.cyclooo.opendeezer.player.PlaybackService] keeps the in-process
 * Go engine rendering audio — leaving zombie playback that nothing drives and
 * that a later stopPlayback() (launched on the dead scope) can't reach. Owning
 * the controller here keeps one instance whose poll loop always matches the
 * engine and whose stop always runs, for the life of the process.
 */
class OpenDeezerApplication : Application() {

    // Main.immediate mirrors the service scope; the controller drives Compose
    // StateFlows and only hops to Dispatchers.IO for engine calls.
    private val appScope: CoroutineScope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    val player: PlayerController by lazy { PlayerController(appScope) }

    override fun onCreate() {
        super.onCreate()
        // Point the Go engine at this app's writable, persistent private directory
        // BEFORE any engine call (Init reads media.json + attaches the on-disk
        // stream cache from here). Without it those land outside the sandbox and
        // don't survive a relaunch, so the stream cache neither persists across
        // reboots nor reads back into Settings.
        runCatching { Odmobile.setDataDir(filesDir.absolutePath) }
    }
}
