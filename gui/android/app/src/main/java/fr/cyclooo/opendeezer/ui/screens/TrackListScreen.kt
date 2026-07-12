package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.TrackRow

/**
 * A scrollable track list. When [onBulkDownload] + [bulkDownloadLabel] are set an
 * overflow action offers to download the whole collection (album/playlist),
 * premium-gated like the per-track download.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrackListScreen(
    title: String,
    player: PlayerController,
    onBack: () -> Unit,
    onBulkDownload: (() -> Unit)? = null,
    bulkDownloadLabel: String? = null,
    load: suspend () -> List<Track>,
) {
    val tracks by produceState<List<Track>?>(initialValue = null, key1 = title) {
        value = load()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(title) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
                actions = {
                    if (onBulkDownload != null && bulkDownloadLabel != null) {
                        BulkDownloadMenu(
                            label = bulkDownloadLabel,
                            premium = player.premium,
                            onDownload = onBulkDownload,
                        )
                    }
                },
            )
        },
        floatingActionButton = {
            val list = tracks
            if (!list.isNullOrEmpty()) {
                ExtendedFloatingActionButton(
                    onClick = { player.playQueue(list, 0) },
                    icon = { Icon(Icons.Filled.PlayArrow, contentDescription = null) },
                    text = { Text(stringResource(R.string.action_play_all)) },
                )
            }
        },
    ) { padding ->
        TrackList(tracks, player, Modifier.fillMaxSize().padding(padding))
    }
}

/** Track list body (spinner / empty message / rows), reusable in fold-aware layouts. */
@Composable
fun TrackList(tracks: List<Track>?, player: PlayerController, modifier: Modifier = Modifier) {
    Box(modifier) {
        when (val list = tracks) {
            null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            else -> if (list.isEmpty()) {
                CenteredMessage(stringResource(R.string.tracklist_empty))
            } else {
                LazyColumn(Modifier.fillMaxSize()) {
                    itemsIndexed(list, key = { i, t -> "$i-${t.id}" }) { index, track ->
                        TrackRow(
                            track = track,
                            onClick = { player.playQueue(list, index) },
                            onDownload = { player.download(track) },
                            downloadEnabled = player.premium,
                            onStartRadio = if (track.isEpisode) null else { { player.startTrackRadio(track) } },
                        )
                    }
                    item { Spacer(Modifier.height(88.dp)) }
                }
            }
        }
    }
}

/** Overflow action for a whole-collection download, premium-gated with a hint. */
@Composable
private fun BulkDownloadMenu(label: String, premium: Boolean, onDownload: () -> Unit) {
    var menuOpen by remember { mutableStateOf(false) }
    IconButton(onClick = { menuOpen = true }) {
        Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.cd_more))
    }
    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
        DropdownMenuItem(
            text = {
                if (premium) {
                    Text(label)
                } else {
                    Column {
                        Text(label)
                        Text(
                            stringResource(R.string.download_requires_premium),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            },
            leadingIcon = { Icon(Icons.Filled.Download, contentDescription = null) },
            enabled = premium,
            onClick = {
                menuOpen = false
                onDownload()
            },
        )
    }
}
