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

    fun clear() {
        sp.edit().remove(KEY_ARL).apply()
    }

    companion object {
        private const val KEY_ARL = "arl"
    }
}
