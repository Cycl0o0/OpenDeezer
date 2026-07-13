package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.QueueMusic
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.CastConnected
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.FavoriteBorder
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.Lyrics
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Repeat
import androidx.compose.material.icons.filled.RepeatOne
import androidx.compose.material.icons.filled.Shuffle
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.SkipPrevious
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.player.PlayerState
import fr.cyclooo.opendeezer.ui.HingeSplit
import fr.cyclooo.opendeezer.ui.LocalFoldState
import fr.cyclooo.opendeezer.ui.components.Artwork
import fr.cyclooo.opendeezer.ui.components.formatDuration

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NowPlayingScreen(
    state: PlayerState,
    player: PlayerController,
    onBack: () -> Unit,
    onLyrics: () -> Unit,
    onQueue: () -> Unit,
    onCast: () -> Unit,
) {
    val track = state.current

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.now_playing_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = stringResource(R.string.action_back))
                    }
                },
                actions = {
                    IconButton(onClick = onCast) {
                        Icon(
                            if (state.connectedDevice.isNotBlank()) Icons.Filled.CastConnected else Icons.Filled.Cast,
                            contentDescription = stringResource(R.string.cd_connect),
                            tint = if (state.connectedDevice.isNotBlank()) MaterialTheme.colorScheme.primary
                            else MaterialTheme.colorScheme.onSurface,
                        )
                    }
                    IconButton(onClick = onQueue) {
                        Icon(Icons.AutoMirrored.Filled.QueueMusic, contentDescription = stringResource(R.string.queue_title))
                    }
                },
            )
        },
    ) { padding ->
        if (track == null) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text(stringResource(R.string.np_nothing_playing), color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            return@Scaffold
        }

        val fold = LocalFoldState.current
        val hinge = fold.hingeBounds
        if (fold.isTableTop && hinge != null) {
            // Tabletop posture (half-opened, horizontal hinge): the upright half
            // shows artwork + live lyrics, the flat half keeps the transport
            // controls under the fingers, split at the physical hinge.
            HingeSplit(
                hingeBounds = hinge,
                horizontalHinge = true,
                modifier = Modifier.fillMaxSize().padding(padding),
                first = {
                    Row(
                        Modifier.fillMaxSize().padding(16.dp),
                        horizontalArrangement = Arrangement.spacedBy(16.dp),
                    ) {
                        // Both panes take weighted halves so the artwork (measured as
                        // the largest square fitting its half) can never starve the
                        // lyrics pane of width on tall, narrow flex-mode panes.
                        Box(Modifier.weight(1f).fillMaxHeight(), contentAlignment = Alignment.Center) {
                            Artwork(track.artworkUrl, Modifier.aspectRatio(1f), corner = 16)
                        }
                        LyricsContent(player, Modifier.weight(1f).fillMaxHeight())
                    }
                },
                second = {
                    Column(
                        Modifier
                            .fillMaxSize()
                            .verticalScroll(rememberScrollState())
                            .padding(horizontal = 24.dp, vertical = 8.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        TrackInfo(track, state)
                        Spacer(Modifier.height(8.dp))
                        PlayerControls(state, player, track, onLyrics)
                    }
                },
            )
        } else {
            Column(
                Modifier
                    .fillMaxSize()
                    .padding(padding)
                    .verticalScroll(rememberScrollState())
                    .padding(24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Artwork(
                    track.artworkUrl,
                    Modifier.widthIn(max = 480.dp).fillMaxWidth().aspectRatio(1f),
                    corner = 16,
                )
                Spacer(Modifier.height(24.dp))
                TrackInfo(track, state)
                Spacer(Modifier.height(16.dp))
                PlayerControls(state, player, track, onLyrics)
            }
        }
    }
}

/** Track title, artist line, remote-device note and the preview chip. */
@Composable
private fun TrackInfo(track: Track, state: PlayerState) {
    Text(
        track.name.ifBlank { stringResource(R.string.unknown_title) },
        style = MaterialTheme.typography.headlineSmall,
        maxLines = 2,
        overflow = TextOverflow.Ellipsis,
        textAlign = TextAlign.Center,
    )
    val sub = track.artistLine.ifBlank { track.albumName }
    if (sub.isNotBlank()) {
        Text(
            sub,
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
    if (state.connectedDevice.isNotBlank()) {
        Text(
            stringResource(R.string.np_playing_on, state.connectedDevice),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.primary,
        )
    }
    // 30s preview (e.g. an unavailable/region-locked track): flag it so the
    // user knows this isn't the full stream.
    if (Engine.isPreview()) {
        Spacer(Modifier.height(8.dp))
        Surface(
            color = MaterialTheme.colorScheme.secondaryContainer,
            shape = RoundedCornerShape(8.dp),
        ) {
            Text(
                stringResource(R.string.preview_chip),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
                modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp),
            )
        }
    }
}

/** Seek bar, transport buttons, shuffle/repeat, volume and the format line. */
@Composable
private fun PlayerControls(
    state: PlayerState,
    player: PlayerController,
    track: Track,
    onLyrics: () -> Unit,
) {
    var scrubbing by remember { mutableStateOf(false) }
    var scrubValue by remember { mutableFloatStateOf(0f) }
    var volDrag by remember { mutableStateOf<Float?>(null) }

    // Truthful heart: driven by the cached liked-id set (engine truth), so an
    // already-liked track shows filled without a per-track lookup. Toggling is
    // optimistic and reverts with a snackbar if the write fails.
    val likedIds by player.likedIds.collectAsState()
    val liked = !track.isEpisode && likedIds.contains(track.id)
    val offlineIds by player.offlineIds.collectAsState()
    val offlineReady = offlineIds.contains(track.id)

    val duration = state.durationMs.coerceAtLeast(1L)
    val livePosFraction = (state.positionMs.toFloat() / duration.toFloat()).coerceIn(0f, 1f)
    val sliderValue = if (scrubbing) scrubValue else livePosFraction

    Slider(
        value = sliderValue,
        onValueChange = { scrubbing = true; scrubValue = it },
        onValueChangeFinished = {
            player.seek((scrubValue * duration).toLong())
            scrubbing = false
        },
    )
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(formatDuration((sliderValue * duration).toLong()), style = MaterialTheme.typography.labelSmall)
        Text(formatDuration(state.durationMs), style = MaterialTheme.typography.labelSmall)
    }

    Spacer(Modifier.height(8.dp))

    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceEvenly,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(
            onClick = { player.toggleFavorite(track) },
            enabled = !track.isEpisode,
        ) {
            Icon(
                if (liked) Icons.Filled.Favorite else Icons.Filled.FavoriteBorder,
                contentDescription = stringResource(R.string.cd_like),
                tint = if (liked) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
            )
        }
        IconButton(onClick = player::prev, enabled = state.hasPrev) {
            Icon(Icons.Filled.SkipPrevious, contentDescription = stringResource(R.string.action_previous), modifier = Modifier.size(36.dp))
        }
        FilledIconButton(
            onClick = player::togglePause,
            modifier = Modifier.size(64.dp),
        ) {
            Icon(
                if (state.state == Engine.PLAYING) Icons.Filled.Pause else Icons.Filled.PlayArrow,
                contentDescription = stringResource(R.string.cd_play_pause),
                modifier = Modifier.size(36.dp),
            )
        }
        IconButton(onClick = player::next, enabled = state.hasNext) {
            Icon(Icons.Filled.SkipNext, contentDescription = stringResource(R.string.action_next), modifier = Modifier.size(36.dp))
        }
        IconButton(onClick = onLyrics, enabled = !track.isEpisode) {
            Icon(Icons.Filled.Lyrics, contentDescription = stringResource(R.string.lyrics_title))
        }
    }

    Spacer(Modifier.height(8.dp))

    // B4: Shuffle and Repeat controls. Clicking them updates local queue
    //     behaviour and forwards the new mode to any connected remote device
    //     via Engine.setRepeat / Engine.setShuffle.
    Row(
        Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceEvenly,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = { player.setShuffle(!state.shuffle) }) {
            Icon(
                Icons.Filled.Shuffle,
                contentDescription = stringResource(R.string.cd_shuffle),
                tint = if (state.shuffle) MaterialTheme.colorScheme.primary
                       else MaterialTheme.colorScheme.onSurface,
            )
        }
        IconButton(onClick = { player.setRepeat((state.repeatMode + 1) % 3) }) {
            Icon(
                if (state.repeatMode == 2) Icons.Filled.RepeatOne else Icons.Filled.Repeat,
                contentDescription = stringResource(R.string.cd_repeat),
                tint = if (state.repeatMode != 0) MaterialTheme.colorScheme.primary
                       else MaterialTheme.colorScheme.onSurface,
            )
        }
        // Download the current track to the configured folder. Premium-only
        // (the engine rejects the request otherwise) and never for episodes;
        // the outcome lands as a snackbar via PlayerController.downloadEvents.
        IconButton(
            onClick = { player.download(track) },
            enabled = player.premium && !track.isEpisode,
        ) {
            Icon(Icons.Filled.Download, contentDescription = stringResource(R.string.action_download))
        }
        // Download for offline: populate the media cache so this track can play
        // with zero network. Premium-only + non-episode; the badge fills once the
        // track is cache-available this session.
        IconButton(
            onClick = { player.downloadForOffline(track) },
            enabled = player.premium && !track.isEpisode,
        ) {
            Icon(
                if (offlineReady) Icons.Filled.DownloadDone else Icons.Filled.DownloadForOffline,
                contentDescription = stringResource(R.string.action_download_offline),
                tint = if (offlineReady) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
            )
        }
    }

    Spacer(Modifier.height(16.dp))

    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Icon(Icons.Filled.VolumeUp, contentDescription = stringResource(R.string.cd_volume), modifier = Modifier.size(20.dp))
        Spacer(Modifier.size(8.dp))
        Slider(
            value = volDrag ?: state.volume.toFloat().coerceIn(0f, 1f),
            onValueChange = {
                volDrag = it
                // Local volume tracks the drag; a Connect remote would do one
                // HTTP round-trip per frame, so it only gets the final value.
                if (state.connectedDevice.isBlank()) player.setVolume(it.toDouble())
            },
            onValueChangeFinished = {
                volDrag?.let { player.setVolume(it.toDouble()) }
                volDrag = null
            },
            modifier = Modifier.weight(1f),
        )
    }

    if (state.format.isNotBlank()) {
        Text(
            state.format,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    Spacer(Modifier.height(16.dp))
}
