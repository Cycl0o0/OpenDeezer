package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.ArtistInfo
import fr.cyclooo.opendeezer.engine.ArtistPage
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.Artwork
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.MediaCard
import fr.cyclooo.opendeezer.ui.components.SectionHeader
import fr.cyclooo.opendeezer.ui.components.TrackRow

/**
 * Full artist profile: header (round portrait + fan count), top tracks, the
 * discography as an album carousel and related artists — one Engine call
 * ([Engine.artistProfile]) mirroring the desktop GUIs' artist view.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArtistProfileScreen(
    artistId: String,
    artistName: String,
    player: PlayerController,
    onBack: () -> Unit,
    onAlbum: (Album) -> Unit,
    onArtist: (ArtistInfo) -> Unit,
) {
    // retry bumps a key to restart the producer; the value is reset to null
    // (spinner) at the start of each load so a retry visibly reloads.
    val retry = remember { mutableIntStateOf(0) }
    val load by produceState<ArtistLoad?>(initialValue = null, key1 = artistId, key2 = retry.intValue) {
        value = null
        value = ArtistLoad(Engine.artistProfile(artistId))
    }
    val page = load?.page

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(page?.artist?.name?.ifBlank { artistName } ?: artistName) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
        floatingActionButton = {
            val top = page?.top
            if (!top.isNullOrEmpty()) {
                ExtendedFloatingActionButton(
                    onClick = { player.playQueue(top, 0) },
                    icon = { Icon(Icons.Filled.PlayArrow, contentDescription = null) },
                    text = { Text(stringResource(R.string.action_play_all)) },
                )
            }
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                load == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                page == null -> Column(
                    Modifier.fillMaxSize().padding(32.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.Center,
                ) {
                    Text(
                        stringResource(R.string.artist_load_error),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(8.dp))
                    TextButton(onClick = { retry.intValue++ }) {
                        Text(stringResource(R.string.action_retry))
                    }
                }
                else -> ArtistProfileContent(page, player, onAlbum, onArtist)
            }
        }
    }
}

/** Completed-load wrapper: distinguishes "still loading" (no value yet) from "failed" (null page). */
private data class ArtistLoad(val page: ArtistPage?)

@Composable
private fun ArtistProfileContent(
    page: ArtistPage,
    player: PlayerController,
    onAlbum: (Album) -> Unit,
    onArtist: (ArtistInfo) -> Unit,
) {
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 88.dp)) {
        item {
            Column(
                Modifier.fillMaxWidth().padding(top = 8.dp, bottom = 8.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Artwork(page.artist.artworkUrl, Modifier.size(140.dp), corner = 70)
                Spacer(Modifier.height(12.dp))
                Text(
                    page.artist.name.ifBlank { stringResource(R.string.unknown_title) },
                    style = MaterialTheme.typography.headlineSmall,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
                if (page.artist.nbFans > 0) {
                    Text(
                        pluralStringResource(R.plurals.n_fans, page.artist.nbFans, page.artist.nbFans),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        if (page.top.isEmpty() && page.albums.isEmpty() && page.related.isEmpty()) {
            item { CenteredMessage(stringResource(R.string.tracklist_empty)) }
        }
        if (page.top.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.section_top_tracks)) }
            itemsIndexed(page.top, key = { i, t -> "t-$i-${t.id}" }) { index, track ->
                TrackRow(
                    track = track,
                    onClick = { player.playQueue(page.top, index) },
                    onDownload = { player.download(track) },
                    downloadEnabled = player.premium,
                )
            }
        }
        if (page.albums.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.section_albums)) }
            item {
                LazyRow(contentPadding = PaddingValues(horizontal = 8.dp)) {
                    items(page.albums, key = { "a-${it.id}" }) { album ->
                        MediaCard(album.name, album.artistLine, album.artworkUrl, { onAlbum(album) })
                    }
                }
            }
        }
        if (page.related.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.section_related_artists)) }
            item {
                LazyRow(contentPadding = PaddingValues(horizontal = 8.dp)) {
                    items(page.related, key = { "ar-${it.id}" }) { artist ->
                        MediaCard(
                            artist.name,
                            if (artist.nbFans > 0) pluralStringResource(R.plurals.n_fans, artist.nbFans, artist.nbFans) else "",
                            artist.artworkUrl,
                            { onArtist(artist) },
                            round = true,
                        )
                    }
                }
            }
        }
    }
}
