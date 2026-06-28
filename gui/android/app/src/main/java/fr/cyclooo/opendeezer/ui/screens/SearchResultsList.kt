package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.ArtistInfo
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.SearchResults
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.MediaCard
import fr.cyclooo.opendeezer.ui.components.SectionHeader
import fr.cyclooo.opendeezer.ui.components.TrackRow

/**
 * Renders a [SearchResults] (also reused for Charts): track rows plus horizontal
 * carousels of albums, artists and playlists.
 */
@Composable
fun SearchResultsList(
    results: SearchResults,
    player: PlayerController,
    onAlbum: (Album) -> Unit,
    onArtist: (ArtistInfo) -> Unit,
    onPlaylist: (Playlist) -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyColumn(modifier) {
        if (results.tracks.isNotEmpty()) {
            item { SectionHeader("Tracks") }
            itemsIndexed(results.tracks, key = { i, t -> "t-$i-${t.id}" }) { index, track ->
                TrackRow(track = track, onClick = { player.playQueue(results.tracks, index) })
            }
        }
        if (results.albums.isNotEmpty()) {
            item { SectionHeader("Albums") }
            item {
                LazyRow {
                    items(results.albums, key = { "a-${it.id}" }) { album ->
                        MediaCard(album.name, album.artistLine, album.artworkUrl, { onAlbum(album) })
                    }
                }
            }
        }
        if (results.artists.isNotEmpty()) {
            item { SectionHeader("Artists") }
            item {
                LazyRow {
                    items(results.artists, key = { "ar-${it.id}" }) { artist ->
                        MediaCard(
                            artist.name,
                            if (artist.nbFans > 0) "${artist.nbFans} fans" else "",
                            artist.artworkUrl,
                            { onArtist(artist) },
                            round = true,
                        )
                    }
                }
            }
        }
        if (results.playlists.isNotEmpty()) {
            item { SectionHeader("Playlists") }
            item {
                LazyRow {
                    items(results.playlists, key = { "p-${it.id}" }) { pl ->
                        MediaCard(pl.name, pl.owner, pl.artworkUrl, { onPlaylist(pl) })
                    }
                }
            }
        }
        item { Spacer(Modifier.height(96.dp)) }
    }
}
