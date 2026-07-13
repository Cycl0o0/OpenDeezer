package fr.cyclooo.opendeezer.player

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.Bitmap
import android.graphics.drawable.BitmapDrawable
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.os.Build
import android.os.IBinder
import android.support.v4.media.MediaMetadataCompat
import android.support.v4.media.session.MediaSessionCompat
import android.support.v4.media.session.PlaybackStateCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.media.session.MediaButtonReceiver
import coil.Coil
import coil.request.ImageRequest
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Engine
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Foreground media-playback service. The Go engine renders audio in-process;
 * this service keeps the process out of the cached-app freezer while the app
 * is backgrounded, publishes a MediaSession (lock-screen / notification /
 * media-button controls) and arbitrates audio focus so playback pauses for
 * phone calls and other media apps.
 */
class PlaybackService : Service() {

    private var session: MediaSessionCompat? = null
    private var scope: CoroutineScope? = null
    private var wiredController: PlayerController? = null
    private var audioManager: AudioManager? = null
    private var focusRequest: AudioFocusRequest? = null
    private var hasFocus = false
    private var pausedByFocusLoss = false
    private var lastNotifKey = ""

    // Lock-screen / notification artwork. artUrl is the track art currently
    // loaded (or loading); artBitmap is the decoded image once ready. lastState
    // lets a late-arriving bitmap refresh the session/notification.
    private var artUrl: String? = null
    private var artBitmap: Bitmap? = null
    private var lastState: PlayerState = PlayerState()

    private val focusListener = AudioManager.OnAudioFocusChangeListener { change ->
        val c = controller ?: return@OnAudioFocusChangeListener
        when (change) {
            AudioManager.AUDIOFOCUS_LOSS -> {
                hasFocus = false
                pausedByFocusLoss = false
                if (c.state.value.isPlaying) c.pause()
            }
            AudioManager.AUDIOFOCUS_LOSS_TRANSIENT -> {
                if (c.state.value.isPlaying) {
                    pausedByFocusLoss = true
                    c.pause()
                    // Release the OS audio device so the call audio path is free.
                    Engine.setOutputSuspended(true)
                }
            }
            AudioManager.AUDIOFOCUS_GAIN -> {
                hasFocus = true
                Engine.setOutputSuspended(false)
                if (pausedByFocusLoss) {
                    pausedByFocusLoss = false
                    c.resume()
                }
            }
            // TRANSIENT_CAN_DUCK: keep playing; on O+ the system ducks for us.
        }
    }

    override fun onCreate() {
        super.onCreate()
        audioManager = getSystemService(Context.AUDIO_SERVICE) as AudioManager
        createChannel()
        session = MediaSessionCompat(this, "OpenDeezer").apply {
            setCallback(object : MediaSessionCompat.Callback() {
                // NB: must be qualified — inside this apply{} an unqualified
                // `controller` resolves to MediaSessionCompat.controller.
                override fun onPlay() {
                    Companion.controller?.resume()
                }

                override fun onPause() {
                    Companion.controller?.pause()
                }

                override fun onSkipToNext() {
                    Companion.controller?.next()
                }

                override fun onSkipToPrevious() {
                    Companion.controller?.prev()
                }

                override fun onSeekTo(pos: Long) {
                    Companion.controller?.seek(pos)
                }

                override fun onStop() {
                    Companion.controller?.stopPlayback()
                }

                // Lock-screen / Android Auto shuffle + repeat toggles map to the
                // controller (which forwards to the engine / any Connect remote).
                override fun onSetShuffleMode(shuffleMode: Int) {
                    Companion.controller?.setShuffle(shuffleMode != PlaybackStateCompat.SHUFFLE_MODE_NONE)
                }

                override fun onSetRepeatMode(repeatMode: Int) {
                    Companion.controller?.setRepeat(
                        when (repeatMode) {
                            PlaybackStateCompat.REPEAT_MODE_ONE -> 2
                            PlaybackStateCompat.REPEAT_MODE_ALL,
                            PlaybackStateCompat.REPEAT_MODE_GROUP,
                            -> 1
                            else -> 0
                        },
                    )
                }
            })
            isActive = true
        }
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
        wireStateCollection()
    }

    /**
     * Starts collecting the controller's state into the session/notification.
     * Idempotent per controller instance; called from both onCreate and
     * onStartCommand so a controller that appears after onCreate (the service
     * can be cold-started by MediaButtonReceiver) still drives the session.
     */
    private fun wireStateCollection() {
        val c = controller ?: return
        if (c === wiredController) return
        wiredController = c
        scope?.launch { c.state.collect { onState(it) } }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        session?.let { MediaButtonReceiver.handleIntent(it, intent) }
        val notif = buildNotification(controller?.state?.value ?: PlayerState())
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIF_ID, notif, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        } else {
            startForeground(NOTIF_ID, notif)
        }
        if (controller == null) {
            // Cold start with no app (e.g. a media-button press after process
            // death restarted us via MediaButtonReceiver): there is nothing to
            // control, so don't stay foreground with a dead placeholder
            // notification the user can't dismiss.
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf(startId)
            return START_NOT_STICKY
        }
        wireStateCollection()
        return START_NOT_STICKY
    }

    override fun onTaskRemoved(rootIntent: Intent?) {
        // Swiped from recents: stop playback and tear the service down so no
        // zombie audio outlives the visible app. The controller is owned by the
        // Application (not the Activity), so its stopPlayback() still runs here.
        Companion.controller?.stopPlayback()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        super.onTaskRemoved(rootIntent)
    }

    override fun onDestroy() {
        abandonFocus()
        // Never leave the output suspended once focus handling goes away.
        Engine.setOutputSuspended(false)
        scope?.cancel()
        scope = null
        session?.release()
        session = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun onState(st: PlayerState) {
        // Focus is only relevant while this device renders audio locally.
        if (st.isPlaying && st.connectedDevice.isBlank() && !hasFocus) requestFocus()
        lastState = st
        ensureArtwork(st.current?.artworkUrl)
        applyToSession(st)
    }

    /** Publishes [st] to the MediaSession + notification (called on state change and once artwork loads). */
    private fun applyToSession(st: PlayerState) {
        val s = session ?: return
        val t = st.current
        val art = currentArt(t?.artworkUrl)
        val meta = MediaMetadataCompat.Builder()
            .putString(MediaMetadataCompat.METADATA_KEY_TITLE, t?.name ?: getString(R.string.app_name))
            .putString(MediaMetadataCompat.METADATA_KEY_ARTIST, t?.artistLine.orEmpty())
            .putString(MediaMetadataCompat.METADATA_KEY_ALBUM, t?.albumName.orEmpty())
            .putLong(MediaMetadataCompat.METADATA_KEY_DURATION, st.durationMs)
        // Lock-screen artwork: both keys so different OEM lock screens pick it up.
        art?.let {
            meta.putBitmap(MediaMetadataCompat.METADATA_KEY_ALBUM_ART, it)
            meta.putBitmap(MediaMetadataCompat.METADATA_KEY_ART, it)
        }
        s.setMetadata(meta.build())

        // Reflect shuffle/repeat so lock-screen / Auto toggles show the right state.
        s.setShuffleMode(
            if (st.shuffle) PlaybackStateCompat.SHUFFLE_MODE_ALL else PlaybackStateCompat.SHUFFLE_MODE_NONE,
        )
        s.setRepeatMode(
            when (st.repeatMode) {
                2 -> PlaybackStateCompat.REPEAT_MODE_ONE
                1 -> PlaybackStateCompat.REPEAT_MODE_ALL
                else -> PlaybackStateCompat.REPEAT_MODE_NONE
            },
        )

        val playbackState = when (st.state) {
            Engine.PLAYING -> PlaybackStateCompat.STATE_PLAYING
            Engine.PAUSED -> PlaybackStateCompat.STATE_PAUSED
            Engine.LOADING -> PlaybackStateCompat.STATE_BUFFERING
            else -> PlaybackStateCompat.STATE_STOPPED
        }
        s.setPlaybackState(
            PlaybackStateCompat.Builder()
                .setActions(
                    PlaybackStateCompat.ACTION_PLAY or PlaybackStateCompat.ACTION_PAUSE or
                        PlaybackStateCompat.ACTION_PLAY_PAUSE or PlaybackStateCompat.ACTION_STOP or
                        PlaybackStateCompat.ACTION_SKIP_TO_NEXT or PlaybackStateCompat.ACTION_SKIP_TO_PREVIOUS or
                        PlaybackStateCompat.ACTION_SEEK_TO or
                        PlaybackStateCompat.ACTION_SET_SHUFFLE_MODE or PlaybackStateCompat.ACTION_SET_REPEAT_MODE,
                )
                .setState(playbackState, st.positionMs, 1f)
                .build(),
        )

        // Re-notify on track/state/artwork changes; position rides the session state.
        val key = "${t?.id}|${st.state}|${st.durationMs}|${art != null}"
        if (key != lastNotifKey) {
            lastNotifKey = key
            runCatching { NotificationManagerCompat.from(this).notify(NOTIF_ID, buildNotification(st)) }
        }
    }

    /** The decoded artwork bitmap iff it belongs to [url] (guards fast track skips). */
    private fun currentArt(url: String?): Bitmap? =
        if (!url.isNullOrBlank() && url == artUrl) artBitmap else null

    /**
     * Loads the track artwork (via Coil, off the main thread) for the lock-screen
     * / notification image, then refreshes the session once it's ready. Best-effort
     * — a failed/absent load just leaves the small icon. Skips work when the art
     * URL hasn't changed.
     */
    private fun ensureArtwork(url: String?) {
        if (url.isNullOrBlank()) {
            artUrl = null
            artBitmap = null
            return
        }
        if (url == artUrl) return // already loaded / loading for this track
        artUrl = url
        artBitmap = null
        scope?.launch {
            val bmp = withContext(Dispatchers.IO) {
                runCatching {
                    val req = ImageRequest.Builder(applicationContext)
                        .data(url)
                        .allowHardware(false) // MediaSession/notification need a software bitmap
                        .build()
                    (Coil.imageLoader(applicationContext).execute(req).drawable as? BitmapDrawable)?.bitmap
                }.getOrNull()
            }
            // Apply only if this is still the current track's art (a fast skip
            // may have superseded it).
            if (artUrl == url && bmp != null) {
                artBitmap = bmp
                applyToSession(lastState)
            }
        }
    }

    private fun requestFocus() {
        val am = audioManager ?: return
        val granted = if (Build.VERSION.SDK_INT >= 26) {
            val req = focusRequest ?: AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN)
                .setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_MEDIA)
                        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                        .build(),
                )
                .setOnAudioFocusChangeListener(focusListener)
                .build()
                .also { focusRequest = it }
            am.requestAudioFocus(req)
        } else {
            @Suppress("DEPRECATION")
            am.requestAudioFocus(focusListener, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN)
        }
        hasFocus = granted == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
        if (hasFocus) {
            // A synchronous grant after a transient loss (call ends, another app
            // releases focus) may not deliver an async AUDIOFOCUS_GAIN, so the
            // output could stay suspended → silent playback. Un-suspend here and
            // clear any pending focus-loss resume so audio actually resumes.
            pausedByFocusLoss = false
            Engine.setOutputSuspended(false)
        }
    }

    private fun abandonFocus() {
        val am = audioManager ?: return
        if (Build.VERSION.SDK_INT >= 26) {
            focusRequest?.let { am.abandonAudioFocusRequest(it) }
        } else {
            @Suppress("DEPRECATION")
            am.abandonAudioFocus(focusListener)
        }
        hasFocus = false
    }

    private fun createChannel() {
        if (Build.VERSION.SDK_INT < 26) return
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.createNotificationChannel(
            NotificationChannel(CHANNEL_ID, getString(R.string.notif_channel_playback), NotificationManager.IMPORTANCE_LOW),
        )
    }

    private fun buildNotification(st: PlayerState): Notification {
        val t = st.current
        val contentIntent = (packageManager.getLaunchIntentForPackage(packageName)
            ?: packageManager.getLeanbackLaunchIntentForPackage(packageName))?.let {
            PendingIntent.getActivity(this, 0, it, PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        }
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setLargeIcon(currentArt(t?.artworkUrl))
            .setContentTitle(t?.name ?: getString(R.string.app_name))
            .setContentText(t?.artistLine.orEmpty())
            .setContentIntent(contentIntent)
            .setOnlyAlertOnce(true)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .addAction(
                NotificationCompat.Action(
                    android.R.drawable.ic_media_previous, getString(R.string.action_previous),
                    MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_SKIP_TO_PREVIOUS),
                ),
            )
            .addAction(
                if (st.isPlaying) {
                    NotificationCompat.Action(
                        android.R.drawable.ic_media_pause, getString(R.string.action_pause),
                        MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_PAUSE),
                    )
                } else {
                    NotificationCompat.Action(
                        android.R.drawable.ic_media_play, getString(R.string.action_play),
                        MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_PLAY),
                    )
                },
            )
            .addAction(
                NotificationCompat.Action(
                    android.R.drawable.ic_media_next, getString(R.string.action_next),
                    MediaButtonReceiver.buildMediaButtonPendingIntent(this, PlaybackStateCompat.ACTION_SKIP_TO_NEXT),
                ),
            )
            .setStyle(
                androidx.media.app.NotificationCompat.MediaStyle()
                    .setMediaSession(session?.sessionToken)
                    .setShowActionsInCompactView(0, 1, 2),
            )
            .build()
    }

    companion object {
        /** Wired by AppViewModel before the service is started. */
        var controller: PlayerController? = null

        private const val CHANNEL_ID = "playback"
        private const val NOTIF_ID = 1

        fun start(context: Context) {
            // From the background (e.g. playback commanded by a Connect peer)
            // startForegroundService may be rejected on Android 12+ — best-effort.
            runCatching {
                ContextCompat.startForegroundService(context, Intent(context, PlaybackService::class.java))
            }
        }

        fun stop(context: Context) {
            runCatching { context.stopService(Intent(context, PlaybackService::class.java)) }
        }
    }
}
