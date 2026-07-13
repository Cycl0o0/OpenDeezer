package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.scrollBy
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.DeleteSweep
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.DragHandle
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.Artwork
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.DraggableItem
import fr.cyclooo.opendeezer.ui.components.dragHandle
import fr.cyclooo.opendeezer.ui.components.rememberDragDropState

/** A queue row with a stable synthetic [uid] so keyed reordering survives
 *  duplicate track ids without the index leaking into the key. */
private data class QueueEntry(val uid: Long, val track: Track)

private fun List<Track>.toEntries(): List<QueueEntry> =
    mapIndexed { i, t -> QueueEntry(i.toLong(), t) }

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QueueScreen(player: PlayerController, onBack: () -> Unit) {
    val state by player.state.collectAsState()
    val offlineIds by player.offlineIds.collectAsState()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.queue_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = stringResource(R.string.action_back))
                    }
                },
                actions = {
                    if (state.queue.isNotEmpty()) {
                        IconButton(onClick = { player.clearQueue() }) {
                            Icon(Icons.Filled.DeleteSweep, contentDescription = stringResource(R.string.action_clear_queue))
                        }
                    }
                },
            )
        },
    ) { padding ->
        if (state.queue.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                CenteredMessage(stringResource(R.string.queue_empty))
            }
            return@Scaffold
        }

        val listState = rememberLazyListState()

        // Local, editable mirror of the engine queue. Reordering shuffles this
        // list live for smooth drag visuals; it re-syncs from engine truth
        // (poll adoption) whenever the queue content changes and no drag is in
        // flight, so remote edits / auto-advance stay reflected.
        var items by remember { mutableStateOf(state.queue.toEntries()) }

        val dragState = rememberDragDropState(
            lazyListState = listState,
            onMoveLive = { from, to ->
                items = items.toMutableList().apply { add(to, removeAt(from)) }
            },
            onMoveCommit = { from, to -> player.moveInQueue(from, to) },
        )

        LaunchedEffect(state.queue) {
            if (dragState.draggingItemIndex == null) items = state.queue.toEntries()
        }

        // Drain overscroll requests emitted while dragging past the edges.
        LaunchedEffect(dragState) {
            while (true) {
                val delta = dragState.scrollChannel.receive()
                listState.scrollBy(delta)
            }
        }

        // Keep the playing row on screen as the queue auto-advances (not while
        // the user is actively dragging).
        LaunchedEffect(state.index) {
            if (dragState.draggingItemIndex == null && state.index in items.indices) {
                listState.animateScrollToItem(state.index)
            }
        }

        LazyColumn(Modifier.fillMaxSize().padding(padding), state = listState) {
            itemsIndexed(items, key = { _, e -> e.uid }) { index, entry ->
                val isCurrent = entry.uid == state.index.toLong()
                DraggableItem(dragState, index) {
                    QueueRow(
                        track = entry.track,
                        isCurrent = isCurrent,
                        offlineDownloaded = offlineIds.contains(entry.track.id),
                        downloadOfflineEnabled = player.premium,
                        onClick = { player.jumpTo(items.indexOfFirst { it.uid == entry.uid }) },
                        onRemove = {
                            val at = items.indexOfFirst { it.uid == entry.uid }
                            if (at >= 0) {
                                items = items.toMutableList().apply { removeAt(at) }
                                player.removeFromQueue(at)
                            }
                        },
                        onDownloadOffline = { player.downloadForOffline(entry.track) },
                        dragHandleModifier = Modifier.dragHandle(dragState, index),
                    )
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun QueueRow(
    track: Track,
    isCurrent: Boolean,
    offlineDownloaded: Boolean,
    downloadOfflineEnabled: Boolean,
    onClick: () -> Unit,
    onRemove: () -> Unit,
    onDownloadOffline: () -> Unit,
    dragHandleModifier: Modifier,
) {
    // Swipe-to-remove — disabled for the playing row (the engine refuses to drop
    // it anyway). Removal fires as soon as the swipe settles past the threshold.
    val dismissState = rememberSwipeToDismissBoxState(
        confirmValueChange = { value -> !isCurrent && value != SwipeToDismissBoxValue.Settled },
    )
    LaunchedEffect(dismissState.currentValue) {
        if (dismissState.currentValue != SwipeToDismissBoxValue.Settled) onRemove()
    }

    SwipeToDismissBox(
        state = dismissState,
        enableDismissFromStartToEnd = !isCurrent,
        enableDismissFromEndToStart = !isCurrent,
        backgroundContent = {
            Box(
                Modifier
                    .fillMaxSize()
                    .background(MaterialTheme.colorScheme.errorContainer)
                    .padding(horizontal = 24.dp),
                contentAlignment = Alignment.CenterEnd,
            ) {
                Icon(
                    Icons.Filled.Delete,
                    contentDescription = stringResource(R.string.action_remove),
                    tint = MaterialTheme.colorScheme.onErrorContainer,
                )
            }
        },
    ) {
        val rowBg = if (isCurrent) Color(0x1AA238FF) else MaterialTheme.colorScheme.background
        Row(
            Modifier
                .fillMaxWidth()
                .background(rowBg)
                .padding(start = 16.dp, top = 8.dp, bottom = 8.dp, end = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            // Tapping the info area jumps to this track; the handle / overflow to
            // the right stay independently operable.
            Row(
                Modifier.weight(1f).clickable(onClick = onClick),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Artwork(track.artworkUrl, Modifier.size(52.dp), corner = 6)
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(
                        track.name.ifBlank { stringResource(R.string.unknown_title) },
                        style = MaterialTheme.typography.bodyLarge,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
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
            }
            if (offlineDownloaded) {
                Icon(
                    Icons.Filled.DownloadDone,
                    contentDescription = stringResource(R.string.cd_downloaded),
                    modifier = Modifier.size(18.dp),
                    tint = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(4.dp))
            }
            if (isCurrent) {
                Icon(
                    Icons.Filled.VolumeUp,
                    contentDescription = stringResource(R.string.cd_now_playing),
                    tint = MaterialTheme.colorScheme.primary,
                )
            }
            QueueRowMenu(
                isCurrent = isCurrent,
                downloadOfflineEnabled = downloadOfflineEnabled,
                onRemove = onRemove,
                onDownloadOffline = onDownloadOffline,
            )
            Box(
                dragHandleModifier.size(40.dp),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    Icons.Filled.DragHandle,
                    contentDescription = stringResource(R.string.cd_reorder),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun QueueRowMenu(
    isCurrent: Boolean,
    downloadOfflineEnabled: Boolean,
    onRemove: () -> Unit,
    onDownloadOffline: () -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) {
            Icon(Icons.Filled.MoreVert, contentDescription = stringResource(R.string.cd_more))
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
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
                    open = false
                    onDownloadOffline()
                },
            )
            if (!isCurrent) {
                DropdownMenuItem(
                    text = { Text(stringResource(R.string.action_remove)) },
                    leadingIcon = { Icon(Icons.Filled.Delete, contentDescription = null) },
                    onClick = {
                        open = false
                        onRemove()
                    },
                )
            }
        }
    }
}
