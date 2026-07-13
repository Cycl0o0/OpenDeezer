package fr.cyclooo.opendeezer

import android.app.Application
import fr.cyclooo.opendeezer.player.PlayerController
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob

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
}
