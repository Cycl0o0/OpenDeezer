package fr.cyclooo.opendeezer.ui.components

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.PlaylistAdd
import androidx.compose.material.icons.automirrored.filled.PlaylistPlay
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.Explicit
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.Radio
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil.compose.SubcomposeAsyncImage
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.ConnectDevice
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController

@Composable
fun Artwork(
    url: String?,
    modifier: Modifier = Modifier,
    corner: Int = 8,
) {
    val shape = RoundedCornerShape(corner.dp)
    Box(
        modifier
            .clip(shape)
            .background(MaterialTheme.colorScheme.surfaceVariant),
        contentAlignment = Alignment.Center,
    ) {
        val clean = url?.takeIf { it.isNotBlank() }
        if (clean == null) {
            Icon(
                Icons.Filled.MusicNote,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        } else {
            SubcomposeAsyncImage(
                model = clean,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxWidth().aspectRatio(1f),
                loading = {
                    Icon(
                        Icons.Filled.MusicNote,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                },
                error = {
                    Icon(
                        Icons.Filled.MusicNote,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                },
            )
        }
    }
}

/**
 * Convenience [TrackRow] wired to a [PlayerController]: fills in the standard
 * long-press menu (Play next / Add to queue / Download / Download for offline)
 * and the "downloaded" badge from the player's premium flag + offline-id set, so
 * browse/list screens get the full queue + download affordances with one call.
 * [onStartRadio] and [trailing] stay caller-supplied.
 */
@Composable
fun TrackRow(
    track: Track,
    player: PlayerController,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    trailing: @Composable (() -> Unit)? = null,
    onStartRadio: (() -> Unit)? = null,
) {
    val offlineIds by player.offlineIds.collectAsState()
    TrackRow(
        track = track,
        onClick = onClick,
        modifier = modifier,
        trailing = trailing,
        onDownload = { player.download(track) },
        downloadEnabled = player.premium,
        onStartRadio = onStartRadio,
        onPlayNext = { player.playNext(track) },
        onAddToQueue = { player.addToQueue(track) },
        onDownloadOffline = { player.downloadForOffline(track) },
        downloadOfflineEnabled = player.premium,
        offlineDownloaded = offlineIds.contains(track.id),
    )
}

/**
 * A track list row. Tapping runs [onClick]; when any menu callback
 * ([onPlayNext], [onAddToQueue], [onDownload], [onDownloadOffline],
 * [onStartRadio]) is supplied the row also responds to a long-press with a
 * context menu. The Download items are enabled only when their *_Enabled flag is
 * set (premium), otherwise shown disabled with a hint — downloads are premium-
 * only. [offlineDownloaded] renders a "downloaded" badge for cache-available
 * tracks. [onStartRadio] seeds a "song radio" from this track.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun TrackRow(
    track: Track,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    trailing: @Composable (() -> Unit)? = null,
    onDownload: (() -> Unit)? = null,
    downloadEnabled: Boolean = true,
    onStartRadio: (() -> Unit)? = null,
    onPlayNext: (() -> Unit)? = null,
    onAddToQueue: (() -> Unit)? = null,
    onDownloadOffline: (() -> Unit)? = null,
    downloadOfflineEnabled: Boolean = true,
    offlineDownloaded: Boolean = false,
) {
    val hasMenu = onDownload != null || onStartRadio != null ||
        onPlayNext != null || onAddToQueue != null || onDownloadOffline != null
    var menuOpen by remember { mutableStateOf(false) }
    val clickModifier =
        if (hasMenu) {
            Modifier.combinedClickable(onClick = onClick, onLongClick = { menuOpen = true })
        } else {
            Modifier.clickable(onClick = onClick)
        }
    Box {
        Row(
            modifier
                .fillMaxWidth()
                .then(clickModifier)
                .padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Artwork(track.artworkUrl, Modifier.size(52.dp), corner = 6)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        track.name.ifBlank { stringResource(R.string.unknown_title) },
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (track.explicit) {
                        Spacer(Modifier.width(4.dp))
                        Icon(
                            Icons.Filled.Explicit,
                            contentDescription = stringResource(R.string.cd_explicit),
                            modifier = Modifier.size(16.dp),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                val sub = track.artistLine.ifBlank { track.albumName }
                if (sub.isNotBlank()) {
                    Text(
                        sub,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            if (offlineDownloaded) {
                Spacer(Modifier.width(8.dp))
                Icon(
                    Icons.Filled.DownloadDone,
                    contentDescription = stringResource(R.string.cd_downloaded),
                    modifier = Modifier.size(18.dp),
                    tint = MaterialTheme.colorScheme.primary,
                )
            }
            if (trailing != null) {
                Spacer(Modifier.width(8.dp))
                trailing()
            } else if (track.durationMs > 0) {
                Spacer(Modifier.width(8.dp))
                Text(
                    formatDuration(track.durationMs),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (hasMenu) {
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                if (onPlayNext != null) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.action_play_next)) },
                        leadingIcon = { Icon(Icons.AutoMirrored.Filled.PlaylistPlay, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            onPlayNext()
                        },
                    )
                }
                if (onAddToQueue != null) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.action_add_to_queue)) },
                        leadingIcon = { Icon(Icons.AutoMirrored.Filled.PlaylistAdd, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            onAddToQueue()
                        },
                    )
                }
                if (onStartRadio != null) {
                    DropdownMenuItem(
                        text = { Text(stringResource(R.string.action_start_radio)) },
                        leadingIcon = { Icon(Icons.Filled.Radio, contentDescription = null) },
                        onClick = {
                            menuOpen = false
                            onStartRadio()
                        },
                    )
                }
                if (onDownloadOffline != null) {
                    DropdownMenuItem(
                        text = {
                            if (downloadOfflineEnabled) {
                                Text(stringResource(R.string.action_download_offline))
                            } else {
                                Column {
                                    Text(stringResource(R.string.action_download_offline))
                                    Text(
                                        stringResource(R.string.download_requires_premium),
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        },
                        leadingIcon = { Icon(Icons.Filled.DownloadForOffline, contentDescription = null) },
                        enabled = downloadOfflineEnabled,
                        onClick = {
                            menuOpen = false
                            onDownloadOffline()
                        },
                    )
                }
                if (onDownload != null) {
                    DropdownMenuItem(
                        text = {
                            if (downloadEnabled) {
                                Text(stringResource(R.string.action_download))
                            } else {
                                Column {
                                    Text(stringResource(R.string.action_download))
                                    Text(
                                        stringResource(R.string.download_requires_premium),
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    )
                                }
                            }
                        },
                        leadingIcon = { Icon(Icons.Filled.Download, contentDescription = null) },
                        enabled = downloadEnabled,
                        onClick = {
                            menuOpen = false
                            onDownload()
                        },
                    )
                }
            }
        }
    }
}

@Composable
fun MediaCard(
    title: String,
    subtitle: String,
    artworkUrl: String?,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    round: Boolean = false,
) {
    Column(
        modifier
            .width(150.dp)
            .clickable(onClick = onClick)
            .padding(8.dp),
    ) {
        Artwork(
            artworkUrl,
            Modifier.fillMaxWidth().aspectRatio(1f),
            corner = if (round) 75 else 8,
        )
        Spacer(Modifier.padding(top = 6.dp))
        Text(
            title.ifBlank { stringResource(R.string.unknown_title) },
            style = MaterialTheme.typography.bodyMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (subtitle.isNotBlank()) {
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
fun SectionHeader(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        style = MaterialTheme.typography.titleMedium,
        color = MaterialTheme.colorScheme.onBackground,
        modifier = modifier.padding(horizontal = 16.dp, vertical = 8.dp),
    )
}

@Composable
fun CenteredMessage(text: String, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) {
        Text(
            text,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Localized device-type label for a Connect peer (mirrors the desktop GUIs). */
@Composable
fun deviceTypeLabel(device: ConnectDevice): String = when (device.client.lowercase()) {
    "tui" -> stringResource(R.string.device_type_terminal)
    "darwin", "macos" -> "macOS"
    "windows" -> "Windows"
    "linux", "gnome", "kde" -> "Linux"
    "android" -> "Android"
    "" -> stringResource(R.string.device_type_generic)
    else -> device.client
}

fun formatDuration(ms: Long): String {
    if (ms <= 0) return "0:00"
    val totalSec = ms / 1000
    val m = totalSec / 60
    val s = totalSec % 60
    return "%d:%02d".format(m, s)
}

// Subtle row divider colour used across lists.
val dividerColor: Color get() = Color(0x14FFFFFF)
