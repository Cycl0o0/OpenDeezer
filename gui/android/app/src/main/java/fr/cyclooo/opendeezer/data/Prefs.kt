package fr.cyclooo.opendeezer.data

import android.content.Context
import android.content.SharedPreferences
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Persists the Deezer ARL so the app can auto-login on next launch.
 *
 * The ARL is a bearer credential, so it is encrypted with a non-exportable key
 * from Android Keystore before it enters SharedPreferences. The preference file
 * is also excluded from cloud backup and device transfer in res/xml.
 */
class Prefs(context: Context) {
    private val sp: SharedPreferences =
        context.applicationContext.getSharedPreferences(PREFS_SETTINGS, Context.MODE_PRIVATE)
    private val secrets: SharedPreferences =
        context.applicationContext.getSharedPreferences(PREFS_SECRETS, Context.MODE_PRIVATE)
    private val legacy: SharedPreferences =
        context.applicationContext.getSharedPreferences(PREFS_LEGACY, Context.MODE_PRIVATE)

    init {
        migrateLegacySettings()
    }

    /**
     * Loads the encrypted ARL, migrating the legacy plaintext value when needed.
     * Credential operations share one process-wide lock because several screens
     * create their own [Prefs] instance while all instances use the same
    * SharedPreferences files and Android Keystore alias.
     */
    fun loadArl(): String? = synchronized(CREDENTIAL_LOCK) {
        val encoded = secrets.getString(KEY_ARL_ENCRYPTED, null)
        if (encoded != null) {
            val decrypted = runCatching { decryptArl(encoded) }.getOrNull()
            if (!decrypted.isNullOrBlank()) {
                // A process kill may have interrupted an older migration
                // after the encrypted commit but before plaintext cleanup.
                // Cleanup failure does not invalidate a working ciphertext;
                // leave the legacy value in place and retry on the next load.
                removeLegacyArlBestEffort()
                return@synchronized decrypted
            }
            resetEncryptionState()
        }

        // One-time migration from releases that stored the ARL as plaintext.
        val legacyArl = legacy.getString(KEY_ARL_LEGACY, null)?.takeIf { it.isNotBlank() }
            ?: return@synchronized null
        legacyArl.takeIf { saveArlLocked(it) }
    }

    /**
     * Encrypts and durably stores an ARL. Returns false instead of crashing if
     * Android Keystore is unavailable or rejects the existing key.
     */
    fun saveArl(value: String): Boolean = synchronized(CREDENTIAL_LOCK) {
        saveArlLocked(value)
    }

    private fun saveArlLocked(value: String): Boolean {
        if (value.isBlank()) return false
        return when (persistArl(value)) {
            PersistResult.SUCCESS -> true
            // A SharedPreferences disk failure is not evidence that the key is
            // invalid. Preserve the existing alias/ciphertext instead of turning
            // a transient storage error into destructive credential loss.
            PersistResult.STORAGE_FAILURE -> false
            PersistResult.CRYPTO_FAILURE -> {
                // A restored or invalidated key cannot encrypt new payloads
                // reliably. Delete the unusable alias once and retry fresh.
                resetEncryptionState()
                when (persistArl(value)) {
                    PersistResult.SUCCESS -> true
                    PersistResult.STORAGE_FAILURE -> false
                    PersistResult.CRYPTO_FAILURE -> {
                        resetEncryptionState()
                        false
                    }
                }
            }
        }
    }

    private fun persistArl(value: String): PersistResult {
        val encrypted = runCatching { encryptArl(value) }
            .getOrElse { return PersistResult.CRYPTO_FAILURE }
        if (!secrets.edit().putString(KEY_ARL_ENCRYPTED, encrypted).commit()) {
            return PersistResult.STORAGE_FAILURE
        }
        // The encrypted commit is the security boundary. A failed legacy-file
        // cleanup is retried by loadArl() and must not delete the usable key/blob.
        removeLegacyArlBestEffort()
        return PersistResult.SUCCESS
    }

    private fun removeLegacyArlBestEffort() {
        if (legacy.contains(KEY_ARL_LEGACY)) {
            legacy.edit().remove(KEY_ARL_LEGACY).commit()
        }
    }

    private fun resetEncryptionState() {
        secrets.edit().remove(KEY_ARL_ENCRYPTED).commit()
        runCatching {
            val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
            if (keyStore.containsAlias(KEYSTORE_ALIAS)) keyStore.deleteEntry(KEYSTORE_ALIAS)
        }
    }

    /** Move non-secret preferences out of the old mixed file. The legacy file
     * remains backup-excluded until its plaintext ARL has also been migrated. */
    private fun migrateLegacySettings() {
        val settingKeys = arrayOf(
            KEY_MATERIAL_YOU,
            KEY_CONNECT_HOST,
            KEY_PHONE_REMOTE,
            KEY_QUALITY,
            KEY_REPLAYGAIN,
            KEY_GAPLESS,
            KEY_CROSSFADE,
            KEY_MEDIA_CACHE,
            KEY_DOWNLOAD_FOLDER,
        )
        val keys = settingKeys.filter { legacy.contains(it) }
        if (keys.isEmpty()) return

        val editor = sp.edit()
        keys.filterNot(sp::contains).forEach { key ->
            when (key) {
                KEY_MATERIAL_YOU, KEY_CONNECT_HOST, KEY_PHONE_REMOTE ->
                    editor.putBoolean(key, legacy.getBoolean(key, false))
                KEY_QUALITY, KEY_REPLAYGAIN, KEY_GAPLESS, KEY_CROSSFADE, KEY_MEDIA_CACHE ->
                    editor.putInt(key, legacy.getInt(key, -1))
                KEY_DOWNLOAD_FOLDER -> editor.putString(key, legacy.getString(key, null))
            }
        }
        if (editor.commit()) {
            legacy.edit().apply { keys.forEach { remove(it) } }.apply()
        }
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

    /**
     * Whether Material You dynamic color (Android 12+) is used instead of the
     * built-in Deezer-purple palette. Opt-in: off by default so the app keeps its
     * own identity unless the user asks it to match the system theme.
     */
    var materialYou: Boolean
        get() = sp.getBoolean(KEY_MATERIAL_YOU, false)
        set(value) { sp.edit().putBoolean(KEY_MATERIAL_YOU, value).apply() }

    fun clear() = synchronized(CREDENTIAL_LOCK) {
        secrets.edit().remove(KEY_ARL_ENCRYPTED).commit()
        legacy.edit().remove(KEY_ARL_LEGACY).commit()
    }

    private fun encryptionKey(): SecretKey {
        val keyStore = KeyStore.getInstance(ANDROID_KEYSTORE).apply { load(null) }
        (keyStore.getKey(KEYSTORE_ALIAS, null) as? SecretKey)?.let { return it }

        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, ANDROID_KEYSTORE).run {
            init(
                KeyGenParameterSpec.Builder(
                    KEYSTORE_ALIAS,
                    KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
                )
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                    .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setRandomizedEncryptionRequired(true)
                    .build(),
            )
            generateKey()
        }
    }

    private fun encryptArl(value: String): String {
        val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, encryptionKey())
        val ciphertext = cipher.doFinal(value.toByteArray(Charsets.UTF_8))
        val payload = byteArrayOf(PAYLOAD_VERSION, cipher.iv.size.toByte()) + cipher.iv + ciphertext
        return Base64.encodeToString(payload, Base64.NO_WRAP)
    }

    private fun decryptArl(encoded: String): String {
        val payload = Base64.decode(encoded, Base64.NO_WRAP)
        require(payload.size >= 2 && payload[0] == PAYLOAD_VERSION) { "unsupported ARL payload" }
        val ivLength = payload[1].toInt() and 0xff
        require(ivLength in 12..32 && payload.size > 2 + ivLength) { "invalid ARL payload" }
        val iv = payload.copyOfRange(2, 2 + ivLength)
        val ciphertext = payload.copyOfRange(2 + ivLength, payload.size)
        val cipher = Cipher.getInstance(CIPHER_TRANSFORMATION)
        cipher.init(Cipher.DECRYPT_MODE, encryptionKey(), GCMParameterSpec(GCM_TAG_BITS, iv))
        return cipher.doFinal(ciphertext).toString(Charsets.UTF_8)
    }

    companion object {
        private val CREDENTIAL_LOCK = Any()
        private const val ANDROID_KEYSTORE = "AndroidKeyStore"
        private const val KEYSTORE_ALIAS = "opendeezer.arl"
        private const val CIPHER_TRANSFORMATION = "AES/GCM/NoPadding"
        private const val GCM_TAG_BITS = 128
        private const val PAYLOAD_VERSION: Byte = 1
        private const val PREFS_LEGACY = "opendeezer"
        private const val PREFS_SETTINGS = "opendeezer_settings"
        private const val PREFS_SECRETS = "opendeezer_secrets"
        private const val KEY_ARL_ENCRYPTED = "arl_encrypted_v1"
        private const val KEY_ARL_LEGACY = "arl"
        private const val KEY_MATERIAL_YOU = "material_you"
        private const val KEY_CONNECT_HOST = "connect_host_enabled"
        private const val KEY_PHONE_REMOTE = "phone_remote_enabled"
        private const val KEY_QUALITY = "audio_quality"
        private const val KEY_REPLAYGAIN = "audio_replaygain"
        private const val KEY_GAPLESS = "audio_gapless"
        private const val KEY_CROSSFADE = "audio_crossfade_ms"
        private const val KEY_MEDIA_CACHE = "media_cache_mb"
        private const val KEY_DOWNLOAD_FOLDER = "download_folder"
    }

    private enum class PersistResult {
        SUCCESS,
        STORAGE_FAILURE,
        CRYPTO_FAILURE,
    }
}
