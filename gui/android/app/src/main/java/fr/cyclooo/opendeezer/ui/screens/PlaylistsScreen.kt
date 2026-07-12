package fr.cyclooo.opendeezer.ui.screens

import android.graphics.Rect
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.HingeSplit
import fr.cyclooo.opendeezer.ui.components.Artwork
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.MediaCard
import fr.cyclooo.opendeezer.ui.components.SectionHeader

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlaylistsScreen(onBack: () -> Unit, onOpen: (Playlist) -> Unit) {
    val playlists by produceState<List<Playlist>?>(initialValue = null) { value = Engine.playlists() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.playlists_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when (val list = playlists) {
                null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                else -> if (list.isEmpty()) {
                    CenteredMessage(stringResource(R.string.playlists_empty))
                } else {
                    LazyVerticalGrid(columns = GridCells.Fixed(2), modifier = Modifier.fillMaxSize()) {
                        items(list, key = { it.id }) { p ->
                            MediaCard(
                                title = p.name,
                                subtitle = if (p.trackCount > 0) {
                                    pluralStringResource(R.plurals.n_tracks, p.trackCount, p.trackCount)
                                } else {
                                    p.owner
                                },
                                artworkUrl = p.artworkUrl,
                                onClick = { onOpen(p) },
                            )
                        }
                    }
                }
            }
        }
    }
}

/**
 * Book posture (half-opened, vertical hinge): the library — Liked Songs plus
 * the playlists — sits on the left page and the selected list's tracks on the
 * right page, split at the physical hinge. Selection is local, so both pages
 * stay visible instead of navigating away.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PlaylistsBookScreen(
    player: PlayerController,
    hingeBounds: Rect,
    onBack: () -> Unit,
) {
    val playlists by produceState<List<Playlist>?>(initialValue = null) { value = Engine.playlists() }
    // null selects the Liked Songs pseudo-entry, listed first like on Home.
    var selected by remember { mutableStateOf<Playlist?>(null) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.playlists_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        HingeSplit(
            hingeBounds = hingeBounds,
            horizontalHinge = false,
            modifier = Modifier.fillMaxSize().padding(padding),
            first = {
                when (val list = playlists) {
                    null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                    else -> LazyColumn(Modifier.fillMaxSize()) {
                        item(key = "liked") {
                            LibraryRow(
                                title = stringResource(R.string.home_liked),
                                subtitle = "",
                                artworkUrl = null,
                                selected = selected == null,
                                onClick = { selected = null },
                            )
                        }
                        items(list, key = { it.id }) { p ->
                            LibraryRow(
                                title = p.name,
                                subtitle = if (p.trackCount > 0) {
                                    pluralStringResource(R.plurals.n_tracks, p.trackCount, p.trackCount)
                                } else {
                                    p.owner
                                },
                                artworkUrl = p.artworkUrl,
                                selected = selected?.id == p.id,
                                onClick = { selected = p },
                            )
                        }
                    }
                }
            },
            second = {
                val sel = selected
                // remember(sel?.id) resets the state to null on selection change so the
                // spinner shows while loading; produceState keeps its previous value
                // across key changes, leaving the old playlist's tracks clickable
                // under the new header.
                var tracks by remember(sel?.id) { mutableStateOf<List<Track>?>(null) }
                LaunchedEffect(sel?.id) {
                    tracks = if (sel == null) Engine.favorites() else Engine.playlistTracks(sel.id)
                }
                Column(Modifier.fillMaxSize()) {
                    SectionHeader(sel?.name ?: stringResource(R.string.home_liked))
                    TrackList(tracks, player, Modifier.fillMaxSize())
                }
            },
        )
    }
}

/** A compact library row for the book layout's left page. */
@Composable
private fun LibraryRow(
    title: String,
    subtitle: String,
    artworkUrl: String?,
    selected: Boolean,
    onClick: () -> Unit,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .background(if (selected) MaterialTheme.colorScheme.surfaceVariant else Color.Transparent)
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Artwork(artworkUrl, Modifier.size(52.dp), corner = 6)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                title.ifBlank { stringResource(R.string.unknown_title) },
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (subtitle.isNotBlank()) {
                Text(
                    subtitle,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}
