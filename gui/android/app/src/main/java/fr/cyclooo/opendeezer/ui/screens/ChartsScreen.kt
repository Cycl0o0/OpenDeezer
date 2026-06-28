package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
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
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.ArtistInfo
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.SearchResults
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.CenteredMessage

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChartsScreen(
    player: PlayerController,
    onBack: () -> Unit,
    onAlbum: (Album) -> Unit,
    onArtist: (ArtistInfo) -> Unit,
    onPlaylist: (Playlist) -> Unit,
) {
    val charts by produceState<SearchResults?>(initialValue = null) { value = Engine.charts() }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Charts") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when (val c = charts) {
                null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                else -> if (c.isEmpty) {
                    CenteredMessage("Charts unavailable.")
                } else {
                    SearchResultsList(
                        results = c,
                        player = player,
                        onAlbum = onAlbum,
                        onArtist = onArtist,
                        onPlaylist = onPlaylist,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        }
    }
}
