package fr.cyclooo.opendeezer.tv

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import fr.cyclooo.opendeezer.AppViewModel
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.HomeData
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.SearchResults
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.ui.components.Artwork
import kotlinx.coroutines.launch

/** Internal TV navigation — a tiny back-stackless model driven by the D-pad. */
private sealed interface TvScreen {
    data object Browse : TvScreen
    data object Search : TvScreen
    data class Detail(val title: String, val tracks: List<Track>) : TvScreen
}

@Composable
fun TvRootScreen(vm: AppViewModel) {
    var screen by remember { mutableStateOf<TvScreen>(TvScreen.Browse) }
    val player = vm.player
    val playerState by player.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    fun openAlbum(a: Album) = scope.launch {
        val tracks = Engine.albumTracks(a.id)
        if (tracks.isNotEmpty()) screen = TvScreen.Detail(a.name, tracks)
    }
    fun openPlaylist(p: Playlist) = scope.launch {
        val tracks = Engine.playlistTracks(p.id)
        if (tracks.isNotEmpty()) screen = TvScreen.Detail(p.name, tracks)
    }

    Box(Modifier.fillMaxSize()) {
        Column(Modifier.fillMaxSize().padding(bottom = if (playerState.current != null) 96.dp else 0.dp)) {
            when (val s = screen) {
                TvScreen.Browse -> TvBrowse(
                    onOpenSearch = { screen = TvScreen.Search },
                    onPlayTracks = { list, i -> player.playQueue(list, i) },
                    onOpenAlbum = { openAlbum(it) },
                    onOpenPlaylist = { openPlaylist(it) },
                )
                TvScreen.Search -> TvSearch(
                    onBack = { screen = TvScreen.Browse },
                    onPlayTracks = { list, i -> player.playQueue(list, i) },
                    onOpenAlbum = { openAlbum(it) },
                    onOpenPlaylist = { openPlaylist(it) },
                )
                is TvScreen.Detail -> TvDetail(
                    title = s.title,
                    tracks = s.tracks,
                    onBack = { screen = TvScreen.Browse },
                    onPlay = { i -> player.playQueue(s.tracks, i) },
                )
            }
        }

        playerState.current?.let { cur ->
            TvNowPlayingBar(
                track = cur,
                isPlaying = playerState.isPlaying,
                onPlayPause = { player.togglePause() },
                onNext = { player.next() },
                onPrev = { player.prev() },
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
    }
}

@Composable
private fun TvBrowse(
    onOpenSearch: () -> Unit,
    onPlayTracks: (List<Track>, Int) -> Unit,
    onOpenAlbum: (Album) -> Unit,
    onOpenPlaylist: (Playlist) -> Unit,
) {
    var home by remember { mutableStateOf<HomeData?>(null) }
    var charts by remember { mutableStateOf<SearchResults?>(null) }
    var flow by remember { mutableStateOf<List<Track>>(emptyList()) }

    LaunchedEffect(Unit) {
        home = Engine.home()
        charts = Engine.charts()
        flow = Engine.flow()
    }

    if (home == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        return
    }
    val h = home!!

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(horizontal = 40.dp, vertical = 28.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        item {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("OpenDeezer", style = MaterialTheme.typography.headlineMedium)
                TvActionTile(label = "Search", onClick = onOpenSearch, modifier = Modifier.width(160.dp).height(64.dp))
            }
        }
        item {
            Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                TvActionTile(
                    label = "▶  Flow\nYour personal mix",
                    onClick = { if (flow.isNotEmpty()) onPlayTracks(flow, 0) },
                )
            }
        }
        item {
            TvRow("Made for you", h.topTracks) { t ->
                TvCard(t.name, t.artistLine, t.artworkUrl, onClick = {
                    onPlayTracks(h.topTracks, h.topTracks.indexOf(t))
                })
            }
        }
        charts?.let { c ->
            item {
                TvRow("Charts", c.tracks) { t ->
                    TvCard(t.name, t.artistLine, t.artworkUrl, onClick = {
                        onPlayTracks(c.tracks, c.tracks.indexOf(t))
                    })
                }
            }
        }
        item {
            TvRow("Albums", h.topAlbums) { a ->
                TvCard(a.name, a.artistLine, a.artworkUrl, onClick = { onOpenAlbum(a) })
            }
        }
        item {
            TvRow("Playlists", h.playlists) { p ->
                TvCard(p.name, p.owner, p.artworkUrl, onClick = { onOpenPlaylist(p) })
            }
        }
    }
}

@Composable
private fun TvSearch(
    onBack: () -> Unit,
    onPlayTracks: (List<Track>, Int) -> Unit,
    onOpenAlbum: (Album) -> Unit,
    onOpenPlaylist: (Playlist) -> Unit,
) {
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<SearchResults?>(null) }
    var searching by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Column(
        Modifier.fillMaxSize().padding(40.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            androidx.compose.material3.OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                singleLine = true,
                label = { Text("Search tracks, albums, playlists") },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                modifier = Modifier.width(560.dp),
            )
            TvActionTile(
                label = "Go",
                onClick = {
                    val q = query.trim()
                    if (q.isNotEmpty()) {
                        searching = true
                        scope.launch {
                            results = Engine.search(q)
                            searching = false
                        }
                    }
                },
                modifier = Modifier.width(120.dp).height(64.dp),
            )
            TvActionTile(label = "Back", onClick = onBack, modifier = Modifier.width(120.dp).height(64.dp))
        }

        if (searching) {
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        }
        results?.let { r ->
            LazyColumn(verticalArrangement = Arrangement.spacedBy(28.dp)) {
                item {
                    TvRow("Tracks", r.tracks) { t ->
                        TvCard(t.name, t.artistLine, t.artworkUrl, onClick = {
                            onPlayTracks(r.tracks, r.tracks.indexOf(t))
                        })
                    }
                }
                item {
                    TvRow("Albums", r.albums) { a ->
                        TvCard(a.name, a.artistLine, a.artworkUrl, onClick = { onOpenAlbum(a) })
                    }
                }
                item {
                    TvRow("Playlists", r.playlists) { p ->
                        TvCard(p.name, p.owner, p.artworkUrl, onClick = { onOpenPlaylist(p) })
                    }
                }
            }
        }
    }
}

@Composable
private fun TvDetail(
    title: String,
    tracks: List<Track>,
    onBack: () -> Unit,
    onPlay: (Int) -> Unit,
) {
    Column(Modifier.fillMaxSize().padding(40.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(20.dp)) {
            TvActionTile(label = "Back", onClick = onBack, modifier = Modifier.width(120.dp).height(56.dp))
            Text(title, style = MaterialTheme.typography.headlineMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        LazyColumn(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            items(tracks) { t ->
                TvTrackRow(t, onClick = { onPlay(tracks.indexOf(t)) })
            }
        }
    }
}

@Composable
private fun TvTrackRow(track: Track, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bg = if (focused) MaterialTheme.colorScheme.surfaceVariant else MaterialTheme.colorScheme.surface
    Surface(color = bg, modifier = Modifier.fillMaxWidth()) {
        Row(
            Modifier
                .fillMaxWidth()
                .androidxOnFocus { focused = it }
                .clickable(onClick = onClick)
                .padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(14.dp),
        ) {
            Artwork(track.artworkUrl, Modifier.width(48.dp).height(48.dp), corner = 6)
            Column(Modifier.weight(1f)) {
                Text(track.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    track.artistLine,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (focused) Icon(Icons.Filled.PlayArrow, contentDescription = "Play")
        }
    }
}

@Composable
private fun TvNowPlayingBar(
    track: Track,
    isPlaying: Boolean,
    onPlayPause: () -> Unit,
    onNext: () -> Unit,
    onPrev: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        modifier = modifier.fillMaxWidth().height(88.dp),
    ) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 40.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Artwork(track.artworkUrl, Modifier.width(56.dp).height(56.dp), corner = 6)
            Column(Modifier.weight(1f)) {
                Text(track.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    track.artistLine,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            TvActionTile(label = "◀◀", onClick = onPrev, modifier = Modifier.width(84.dp).height(56.dp))
            TvActionTile(label = if (isPlaying) "❚❚" else "▶", onClick = onPlayPause, modifier = Modifier.width(84.dp).height(56.dp))
            TvActionTile(label = "▶▶", onClick = onNext, modifier = Modifier.width(84.dp).height(56.dp))
        }
    }
}

/** Small helper mirroring onFocusChanged so track rows highlight on D-pad focus. */
private fun Modifier.androidxOnFocus(onChange: (Boolean) -> Unit): Modifier =
    this.then(androidx.compose.ui.focus.onFocusChanged { onChange(it.isFocused) })
