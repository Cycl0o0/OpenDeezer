package fr.cyclooo.opendeezer

import android.app.Application
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.UpdateInfo
import fr.cyclooo.opendeezer.player.PlayerController
import kotlinx.coroutines.launch

enum class AuthStage { LOADING, NEEDS_LOGIN, NEEDS_PREMIUM, READY }

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

    fun login(arl: String, persist: Boolean = true) {
        if (arl.isBlank()) {
            loginError = "Empty ARL"
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
                loginError = "Login failed — check your ARL and connection."
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
                    loginError = "Could not load account."
                    stage = AuthStage.NEEDS_LOGIN
                }
                !acct.premium -> stage = AuthStage.NEEDS_PREMIUM
                else -> {
                    stage = AuthStage.READY
                    player.start()
                    applyRemoteHosts()
                }
            }
        }
    }

    /**
     * Re-enable whichever "make this device reachable" hosts were on last run.
     * Called once logged in — the Connect host needs the account for same-account
     * auth. Mirrors the iOS RemoteHostStore.applyOnLaunch().
     */
    private fun applyRemoteHosts() {
        if (prefs.connectHostEnabled) Engine.setConnectHostEnabled(true)
        if (prefs.phoneRemoteEnabled) Engine.setWebRemoteEnabled(true)
    }

    fun logout() {
        prefs.clear()
        player.stop()
        player.stopPlayback()
        Engine.disconnectDevice()
        account = null
        stage = AuthStage.NEEDS_LOGIN
    }
}
