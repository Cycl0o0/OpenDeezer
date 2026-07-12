package fr.cyclooo.opendeezer

import android.app.Application
import android.webkit.CookieManager
import android.webkit.WebStorage
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.UpdateInfo
import fr.cyclooo.opendeezer.player.PlaybackService
import fr.cyclooo.opendeezer.player.PlayerController
import kotlinx.coroutines.launch

enum class AuthStage { LOADING, NEEDS_LOGIN, NO_INTERNET, READY }

class AppViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = Prefs(app)
    val player = PlayerController(viewModelScope)

    var stage by mutableStateOf(AuthStage.LOADING)
        private set
    var account by mutableStateOf<Account?>(null)
        private set
    var loginError by mutableStateOf<String?>(null)
        private set
    var busy by mutableStateOf(false)
        private set
    var updateInfo by mutableStateOf<UpdateInfo?>(null)
        private set

    // The last ARL that failed Engine.init — the WebView auto-capture must not
    // retry it in a loop (the login page would reset every few seconds).
    private var lastFailedArl: String? = null

    init {
        // Advertise this client to OpenDeezer Connect peers.
        Engine.setClientInfo("android", "OpenDeezer (Android)")
        val saved = prefs.arl
        if (saved.isNullOrBlank()) {
            stage = AuthStage.NEEDS_LOGIN
        } else {
            login(saved, persist = false)
        }
        // Non-intrusive: one background check per launch, never blocks startup.
        checkForUpdate()

        // Foreground playback service: runs while a track is loaded so audio
        // survives backgrounding and lock-screen controls are available.
        PlaybackService.controller = player
        viewModelScope.launch {
            var running = false
            player.state.collect { st ->
                // Keep the service across the brief STOPPED gap between queue
                // tracks — background FGS restarts are rejected on Android 12+.
                val idle = st.queue.isEmpty() && (st.state == Engine.STOPPED || st.state == Engine.ERROR)
                val want = st.current != null && !idle
                if (want && !running) {
                    PlaybackService.start(getApplication())
                    running = true
                } else if (!want && running) {
                    PlaybackService.stop(getApplication())
                    running = false
                }
            }
        }
    }

    /** Silently checks GitHub for a newer release; surfaces it via [updateInfo] if found. */
    fun checkForUpdate() {
        viewModelScope.launch {
            val info = Engine.checkUpdate()
            if (info?.hasUpdate == true) updateInfo = info
        }
    }

    fun dismissUpdate() {
        updateInfo = null
    }

    /** [auto] marks a WebView-captured ARL: a token that already failed is ignored. */
    fun login(arl: String, persist: Boolean = true, auto: Boolean = false) {
        if (auto && arl == lastFailedArl) return
        if (arl.isBlank()) {
            loginError = getApplication<Application>().getString(R.string.login_error_empty_arl)
            stage = AuthStage.NEEDS_LOGIN
            return
        }
        loginError = null
        busy = true
        stage = AuthStage.LOADING
        viewModelScope.launch {
            val ok = Engine.init(arl)
            if (!ok) {
                busy = false
                // Tell "offline" apart from "bad/expired ARL": when the engine
                // reports no internet (kind 2), keep the saved credentials and
                // show the No-Internet screen (with Retry) instead of wiping
                // prefs and bouncing the user back to sign-in.
                if (Engine.loginErrorKind() == 2) {
                    stage = AuthStage.NO_INTERNET
                    return@launch
                }
                lastFailedArl = arl
                loginError = getApplication<Application>().getString(R.string.login_error_failed)
                stage = AuthStage.NEEDS_LOGIN
                if (persist) prefs.clear()
                return@launch
            }
            if (persist) prefs.arl = arl
            val acct = Engine.account()
            account = acct
            busy = false
            when {
                acct == null || !acct.loggedIn -> {
                    lastFailedArl = arl
                    loginError = getApplication<Application>().getString(R.string.login_error_account)
                    stage = AuthStage.NEEDS_LOGIN
                }
                else -> {
                    lastFailedArl = null
                    // Free and premium accounts both reach the app — a Free account
                    // streams full tracks at 128 kbps (not 30s previews). Only the
                    // per-track Download action is premium-only, so record premium
                    // before READY so every screen's TrackRow reads a stable value.
                    player.premium = acct.premium
                    stage = AuthStage.READY
                    player.start()
                    applyRemoteHosts()
                }
            }
        }
    }

    /**
     * Re-attempts sign-in with the saved ARL. Backs the No-Internet screen's
     * Retry button — reuses the exact launch path, so a recovered connection
     * proceeds to READY, a still-offline engine stays on NO_INTERNET, and an
     * expired ARL falls through to NEEDS_LOGIN.
     */
    fun retry() {
        val saved = prefs.arl
        if (saved.isNullOrBlank()) {
            stage = AuthStage.NEEDS_LOGIN
        } else {
            login(saved, persist = false)
        }
    }

    /**
     * Re-apply persisted device + audio settings once logged in. The engine holds
     * these in memory only, so without this they reset on every relaunch. The
     * Connect host also needs the account for same-account auth. Mirrors the iOS
     * RemoteHostStore / AudioPrefs applyOnLaunch().
     */
    private fun applyRemoteHosts() {
        if (prefs.connectHostEnabled) Engine.setConnectHostEnabled(true)
        if (prefs.phoneRemoteEnabled) Engine.setWebRemoteEnabled(true)

        if (prefs.audioQuality >= 0) Engine.setQuality(prefs.audioQuality)
        if (prefs.replayGain >= 0) Engine.setReplayGain(prefs.replayGain == 1)
        if (prefs.gapless >= 0) Engine.setGapless(prefs.gapless == 1)
        if (prefs.crossfadeMs >= 0) Engine.setCrossfadeMs(prefs.crossfadeMs)
        // Re-apply the stream-cache budget so the engine attaches the cache at
        // startup (it can only be set before playback begins).
        if (prefs.mediaCacheMb >= 0) Engine.setMediaCacheMB(prefs.mediaCacheMb)
    }

    fun logout() {
        prefs.clear()
        player.stop()
        player.stopPlayback()
        // Stop serving/advertising for the signed-out account: the Connect host
        // and web remote would otherwise keep accepting commands with its ARL.
        Engine.setConnectHostEnabled(false)
        Engine.setWebRemoteEnabled(false)
        viewModelScope.launch {
            Engine.disconnectDevice()
            // Tear the engine session down too — without this the Go core keeps
            // the old account's client (and its ARL) alive in memory.
            Engine.logout()
        }
        // Drop the WebView session too, or web sign-in silently re-captures the
        // old account's arl cookie and undoes the sign-out.
        runCatching {
            CookieManager.getInstance().removeAllCookies(null)
            CookieManager.getInstance().flush()
            WebStorage.getInstance().deleteAllData()
        }
        account = null
        stage = AuthStage.NEEDS_LOGIN
    }
}
