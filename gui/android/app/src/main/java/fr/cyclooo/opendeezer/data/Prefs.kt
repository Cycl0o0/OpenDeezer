package fr.cyclooo.opendeezer.data

import android.content.Context
import android.content.SharedPreferences

/**
 * Persists the Deezer ARL so the app can auto-login on next launch.
 *
 * Plain SharedPreferences is used deliberately (per the engine's threat model the
 * ARL is the only secret and app-private storage is sufficient); this avoids the
 * extra security-crypto dependency.
 */
class Prefs(context: Context) {
    private val sp: SharedPreferences =
        context.applicationContext.getSharedPreferences("opendeezer", Context.MODE_PRIVATE)

    var arl: String?
        get() = sp.getString(KEY_ARL, null)?.takeIf { it.isNotBlank() }
        set(value) {
            sp.edit().apply {
                if (value.isNullOrBlank()) remove(KEY_ARL) else putString(KEY_ARL, value)
            }.apply()
        }

    /**
     * Whether this device advertises itself as an OpenDeezer Connect host, so
     * other same-account apps can discover and control it. Re-applied on launch.
     */
    var connectHostEnabled: Boolean
        get() = sp.getBoolean(KEY_CONNECT_HOST, false)
        set(value) {
            sp.edit().putBoolean(KEY_CONNECT_HOST, value).apply()
        }

    /** Whether the browser-based phone remote is served. Re-applied on launch. */
    var phoneRemoteEnabled: Boolean
        get() = sp.getBoolean(KEY_PHONE_REMOTE, false)
        set(value) {
            sp.edit().putBoolean(KEY_PHONE_REMOTE, value).apply()
        }

    fun clear() {
        sp.edit().remove(KEY_ARL).apply()
    }

    companion object {
        private const val KEY_ARL = "arl"
        private const val KEY_CONNECT_HOST = "connect_host_enabled"
        private const val KEY_PHONE_REMOTE = "phone_remote_enabled"
    }
}
