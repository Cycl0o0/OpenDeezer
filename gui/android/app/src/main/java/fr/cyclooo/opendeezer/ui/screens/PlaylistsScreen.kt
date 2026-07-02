package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.MediaCard

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
