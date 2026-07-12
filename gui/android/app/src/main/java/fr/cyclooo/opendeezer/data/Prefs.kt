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

    // ---- Audio preferences ----
    // The engine keeps these in memory only, so they'd reset every relaunch.
    // We persist them here and re-apply after login (see AppViewModel). Each is
    // stored with an "unset" sentinel (-1) so we never override the engine
    // default until the user actually picks a value.

    /** Audio quality level (0=Normal, 1=High, 2=HiFi); -1 = unset. */
    var audioQuality: Int
        get() = sp.getInt(KEY_QUALITY, -1)
        set(value) { sp.edit().putInt(KEY_QUALITY, value).apply() }

    /** ReplayGain: 1=on, 0=off, -1=unset. */
    var replayGain: Int
        get() = sp.getInt(KEY_REPLAYGAIN, -1)
        set(value) { sp.edit().putInt(KEY_REPLAYGAIN, value).apply() }

    /** Gapless: 1=on, 0=off, -1=unset. */
    var gapless: Int
        get() = sp.getInt(KEY_GAPLESS, -1)
        set(value) { sp.edit().putInt(KEY_GAPLESS, value).apply() }

    /** Crossfade in milliseconds; -1 = unset. */
    var crossfadeMs: Int
        get() = sp.getInt(KEY_CROSSFADE, -1)
        set(value) { sp.edit().putInt(KEY_CROSSFADE, value).apply() }

    /**
     * Raw-stream on-disk cache budget in MB (0 = off); -1 = unset. The engine
     * attaches the cache once at startup, so this is re-applied on login and only
     * takes effect at the next launch.
     */
    var mediaCacheMb: Int
        get() = sp.getInt(KEY_MEDIA_CACHE, -1)
        set(value) { sp.edit().putInt(KEY_MEDIA_CACHE, value).apply() }

    /**
     * User-chosen download folder (a SAF tree Uri string, or a plain path). Blank
     * when unset — the engine then uses its own shared default. Persisted so the
     * choice (and the taken SAF permission) survive relaunches.
     */
    var downloadFolder: String?
        get() = sp.getString(KEY_DOWNLOAD_FOLDER, null)?.takeIf { it.isNotBlank() }
        set(value) {
            sp.edit().apply {
                if (value.isNullOrBlank()) remove(KEY_DOWNLOAD_FOLDER) else putString(KEY_DOWNLOAD_FOLDER, value)
            }.apply()
        }

    fun clear() {
        sp.edit().remove(KEY_ARL).apply()
    }

    companion object {
        private const val KEY_ARL = "arl"
        private const val KEY_CONNECT_HOST = "connect_host_enabled"
        private const val KEY_PHONE_REMOTE = "phone_remote_enabled"
        private const val KEY_QUALITY = "audio_quality"
        private const val KEY_REPLAYGAIN = "audio_replaygain"
        private const val KEY_GAPLESS = "audio_gapless"
        private const val KEY_CROSSFADE = "audio_crossfade_ms"
        private const val KEY_MEDIA_CACHE = "media_cache_mb"
        private const val KEY_DOWNLOAD_FOLDER = "download_folder"
    }
}
