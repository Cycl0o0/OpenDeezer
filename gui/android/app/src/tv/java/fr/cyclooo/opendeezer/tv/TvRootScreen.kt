package fr.cyclooo.opendeezer.tv

import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.DeleteSweep
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DownloadDone
import androidx.compose.material.icons.filled.DownloadForOffline
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Radio
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.SkipPrevious
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import fr.cyclooo.opendeezer.AppViewModel
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.ArtistInfo
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.HistoryEntry
import fr.cyclooo.opendeezer.engine.HistoryStats
import fr.cyclooo.opendeezer.engine.HomeData
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.SearchResults
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.engine.TrackStat
import fr.cyclooo.opendeezer.player.DownloadEvent
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.Artwork
import kotlinx.coroutines.launch

/** A pushed album/playlist detail view, shown as an overlay over the content. */
private data class TvDetailData(
    val title: String,
    val subtitle: String,
    val artworkUrl: String,
    val tracks: List<Track>,
    // Source ids for the detail actions: batch-download (album/playlist) and
    // artist radio. Null when not applicable to this detail's origin.
    val albumId: String? = null,
    val playlistId: String? = null,
    val radioArtistId: String? = null,
)

@Composable
fun TvRootScreen(vm: AppViewModel) {
    var nav by remember { mutableStateOf(TvNav.Home) }
    var detail by remember { mutableStateOf<TvDetailData?>(null) }
    val player = vm.player
    val playerState by player.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()

    // Remote/hardware BACK: close the detail overlay first, then fall back to
    // Home; disabled on Home with no overlay so BACK exits the app as expected.
    BackHandler(enabled = detail != null || nav != TvNav.Home) {
        if (detail != null) detail = null else nav = TvNav.Home
    }

    fun openAlbum(a: Album) = scope.launch {
        val tracks = Engine.albumTracks(a.id)
        if (tracks.isNotEmpty()) detail = TvDetailData(a.name, a.artistLine, a.artworkUrl, tracks, albumId = a.id)
    }
    fun openArtist(a: ArtistInfo) = scope.launch {
        val tracks = Engine.artistTop(a.id)
        if (tracks.isNotEmpty()) detail = TvDetailData(a.name, "", a.artworkUrl, tracks, radioArtistId = a.id)
    }
    fun openPlaylist(p: Playlist) = scope.launch {
        val tracks = Engine.playlistTracks(p.id)
        if (tracks.isNotEmpty()) detail = TvDetailData(p.name, p.owner, p.artworkUrl, tracks, playlistId = p.id)
    }
    val playTracks = { list: List<Track>, i: Int -> player.playQueue(list, i) }

    // Album/playlist downloads report via the shared download-events flow; on TV
    // (no snackbar) surface the outcome as a Toast.
    val context = LocalContext.current
    LaunchedEffect(player) {
        player.downloadEvents.collect { event ->
            val msg = when (event) {
                is DownloadEvent.Started -> context.getString(R.string.download_started, event.trackName)
                is DownloadEvent.Saved ->
                    if (event.path.isBlank()) context.getString(R.string.download_saved_generic, event.trackName)
                    else context.getString(R.string.download_saved, event.path)
                is DownloadEvent.Failed ->
                    if (event.error.isBlank()) context.getString(R.string.download_failed_generic)
                    else context.getString(R.string.download_failed, event.error)
                is DownloadEvent.BatchDone -> when {
                    event.saved == 0 && event.error.isNotBlank() -> context.getString(R.string.download_failed, event.error)
                    event.saved == 0 -> context.getString(R.string.download_failed_generic)
                    event.failed > 0 -> context.getString(R.string.download_batch_partial, event.saved, event.failed)
                    else -> context.getString(R.string.download_batch_saved, event.saved)
                }
                is DownloadEvent.OfflineReady ->
                    if (event.alreadyCached) context.getString(R.string.offline_already, event.trackName)
                    else context.getString(R.string.offline_ready, event.trackName)
                is DownloadEvent.OfflineFailed ->
                    if (event.needsCache) context.getString(R.string.offline_needs_cache)
                    else if (event.error.isBlank()) context.getString(R.string.download_failed_generic)
                    else context.getString(R.string.download_failed, event.error)
            }
            Toast.makeText(context, msg, Toast.LENGTH_SHORT).show()
        }
    }

    // A failed optimistic Like toggle was reverted — surface it as a Toast on TV.
    LaunchedEffect(player) {
        player.favoriteFailures.collect {
            Toast.makeText(context, context.getString(R.string.favorite_update_failed), Toast.LENGTH_SHORT).show()
        }
    }

    Box(Modifier.fillMaxSize().background(TvPalette.screen)) {
        Row(Modifier.fillMaxSize().padding(bottom = if (playerState.current != null) 108.dp else 0.dp)) {
            TvNavRail(
                selected = nav,
                onSelect = { nav = it; detail = null },
            )
            Box(Modifier.weight(1f).fillMaxSize()) {
                // Underlying content — made unfocusable while the overlay is up so
                // D-pad focus can't leak onto hidden cards behind the detail sheet.
                Box(Modifier.fillMaxSize().focusProperties { canFocus = detail == null }) {
                    when (nav) {
                        TvNav.Home -> TvBrowse(
                            onOpenSearch = { nav = TvNav.Search },
                            onPlayTracks = playTracks,
                            onOpenAlbum = { openAlbum(it) },
                            onOpenPlaylist = { openPlaylist(it) },
                        )
                        TvNav.Search -> TvSearch(
                            onPlayTracks = playTracks,
                            onOpenAlbum = { openAlbum(it) },
                            onOpenArtist = { openArtist(it) },
                            onOpenPlaylist = { openPlaylist(it) },
                        )
                        TvNav.Library -> TvLibrary(
                            onPlayTracks = playTracks,
                            onOpenPlaylist = { openPlaylist(it) },
                        )
                        TvNav.History -> TvHistory(onPlay = { track -> player.playQueue(listOf(track), 0) })
                        TvNav.Queue -> TvQueue(player)
                        TvNav.Settings -> TvSettingsScreen(account = vm.account, onLogout = { vm.logout() })
                    }
                }

                detail?.let { d ->
                    Box(Modifier.fillMaxSize().focusGroup()) {
                        TvDetail(
                            title = d.title,
                            subtitle = d.subtitle,
                            artworkUrl = d.artworkUrl,
                            tracks = d.tracks,
                            premium = player.premium,
                            onBack = { detail = null },
                            onPlayAll = { player.playQueue(d.tracks, 0) },
                            onPlay = { i -> player.playQueue(d.tracks, i) },
                            // Radio: seed from the artist when known, else the first track.
                            onRadio = d.radioArtistId?.let { id -> ({ player.startArtistRadio(id) }) }
                                ?: d.tracks.firstOrNull()?.let { t -> ({ player.startTrackRadio(t) }) },
                            // Batch download only for album/playlist detail origins.
                            onDownload = d.albumId?.let { id -> ({ player.downloadAlbum(id, d.title) }) }
                                ?: d.playlistId?.let { id -> ({ player.downloadPlaylist(id, d.title) }) },
                        )
                    }
                }
            }
        }

        playerState.current?.let { cur ->
            TvNowPlayingBar(
                track = cur,
                isPlaying = playerState.isPlaying,
                positionMs = playerState.positionMs,
                durationMs = playerState.durationMs,
                onPlayPause = { player.togglePause() },
                onNext = { player.next() },
                onPrev = { player.prev() },
                modifier = Modifier.align(Alignment.BottomCenter),
            )
        }
    }
}

/** Shared inline error + retry used by the data-loading screens. */
@Composable
private fun TvLoadError(onRetry: () -> Unit) {
    Column(
        Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(stringResource(R.string.tv_load_error), color = TvPalette.TextDim, style = MaterialTheme.typography.titleMedium)
        TvPill(stringResource(R.string.action_retry), onClick = onRetry)
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
    var failed by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }
    val playFocus = remember { FocusRequester() }

    LaunchedEffect(reload) {
        failed = false
        // The engine reports failures as error payloads (the calls never throw),
        // so use the null-on-error variants; the hero/shelves need home data.
        // Odmobile.Home() additionally swallows the underlying Charts error and
        // returns an empty payload with no "error" key, so an all-empty home is
        // also a failure: global charts are never legitimately empty for a
        // logged-in account.
        val h = Engine.homeOrNull()
        if (h == null || (h.topTracks.isEmpty() && h.topAlbums.isEmpty() && h.playlists.isEmpty())) {
            failed = true
        } else {
            home = h
            charts = Engine.chartsOrNull()
            flow = Engine.flowOrNull().orEmpty()
        }
    }

    if (failed && home == null) { TvLoadError { reload++ }; return }
    val h = home
    if (h == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(color = TvPalette.Purple)
        }
        return
    }

    LaunchedEffect(h.topTracks.isNotEmpty()) {
        if (h.topTracks.isNotEmpty()) runCatching { playFocus.requestFocus() }
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 40.dp, end = 40.dp, top = 40.dp, bottom = 40.dp),
        verticalArrangement = Arrangement.spacedBy(30.dp),
    ) {
        h.topTracks.firstOrNull()?.let { feat ->
            item {
                TvHero(
                    title = feat.name,
                    subtitle = feat.artistLine,
                    artworkUrl = feat.artworkUrl,
                    onPlay = { onPlayTracks(h.topTracks, 0) },
                    onSearch = onOpenSearch,
                    playFocus = playFocus,
                    playIcon = Icons.Filled.PlayArrow,
                )
            }
        }
        if (flow.isNotEmpty()) {
            item {
                TvRow(stringResource(R.string.tv_flow_mix), flow) { i, t ->
                    TvCard(t.name, t.artistLine, t.artworkUrl, onClick = { onPlayTracks(flow, i) })
                }
            }
        }
        item {
            TvRow(stringResource(R.string.tv_made_for_you), h.topTracks) { i, t ->
                TvCard(t.name, t.artistLine, t.artworkUrl, onClick = { onPlayTracks(h.topTracks, i) })
            }
        }
        charts?.let { c ->
            item {
                TvRow(stringResource(R.string.charts_title), c.tracks) { i, t ->
                    TvCard(t.name, t.artistLine, t.artworkUrl, onClick = { onPlayTracks(c.tracks, i) })
                }
            }
        }
        item {
            TvRow(stringResource(R.string.section_albums), h.topAlbums) { _, a ->
                TvCard(a.name, a.artistLine, a.artworkUrl, onClick = { onOpenAlbum(a) })
            }
        }
        item {
            TvRow(stringResource(R.string.section_playlists), h.playlists) { _, p ->
                TvCard(p.name, p.owner, p.artworkUrl, onClick = { onOpenPlaylist(p) })
            }
        }
    }
}

@Composable
private fun TvSearch(
    onPlayTracks: (List<Track>, Int) -> Unit,
    onOpenAlbum: (Album) -> Unit,
    onOpenArtist: (ArtistInfo) -> Unit,
    onOpenPlaylist: (Playlist) -> Unit,
) {
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<SearchResults?>(null) }
    var searching by remember { mutableStateOf(false) }
    var searchFailed by remember { mutableStateOf(false) }
    var searchGeneration by remember { mutableStateOf(0) }
    val scope = rememberCoroutineScope()
    val fieldFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { fieldFocus.requestFocus() } }

    val runSearch = {
        val q = query.trim()
        if (q.isNotEmpty()) {
            searchGeneration += 1
            val generation = searchGeneration
            searching = true
            searchFailed = false
            scope.launch {
                val searchResults = Engine.searchOrNull(q)
                if (generation == searchGeneration) {
                    results = searchResults
                    searchFailed = searchResults == null
                    searching = false
                }
            }
        }
    }

    Column(
        Modifier.fillMaxSize().padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(28.dp),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            androidx.compose.material3.OutlinedTextField(
                value = query,
                onValueChange = { value ->
                    query = value
                    // An explicit TV search may still be running in the Go
                    // engine. Editing invalidates that response so results for
                    // the previous text never appear under the new query.
                    searchGeneration += 1
                    searching = false
                    searchFailed = false
                    results = null
                },
                singleLine = true,
                label = { Text(stringResource(R.string.tv_search_hint)) },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null, tint = TvPalette.Purple) },
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { runSearch() }),
                modifier = Modifier.weight(1f).focusRequester(fieldFocus),
            )
            TvPill(stringResource(R.string.search_title), onClick = runSearch, leadingIcon = Icons.Filled.Search)
        }

        when {
            searching -> Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(color = TvPalette.Purple)
            }
            searchFailed -> Column(
                Modifier.fillMaxWidth(),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                Text(stringResource(R.string.tv_load_error), color = TvPalette.TextDim)
                TvPill(stringResource(R.string.action_retry), onClick = runSearch)
            }
            results?.isEmpty == true -> Text(
                stringResource(R.string.search_no_results),
                color = TvPalette.TextDim,
                style = MaterialTheme.typography.titleMedium,
            )
            results == null -> Text(
                stringResource(R.string.search_empty_prompt),
                color = TvPalette.TextDim,
                style = MaterialTheme.typography.titleMedium,
            )
            else -> results?.let { r ->
                LazyColumn(verticalArrangement = Arrangement.spacedBy(30.dp)) {
                    if (r.tracks.isNotEmpty()) item {
                        TvRow(stringResource(R.string.section_tracks), r.tracks) { i, t ->
                            TvCard(t.name, t.artistLine, t.artworkUrl, onClick = { onPlayTracks(r.tracks, i) })
                        }
                    }
                    if (r.albums.isNotEmpty()) item {
                        TvRow(stringResource(R.string.section_albums), r.albums) { _, a ->
                            TvCard(a.name, a.artistLine, a.artworkUrl, onClick = { onOpenAlbum(a) })
                        }
                    }
                    if (r.artists.isNotEmpty()) item {
                        TvRow(stringResource(R.string.section_artists), r.artists) { _, a ->
                            TvCard(
                                a.name,
                                if (a.nbFans > 0) {
                                    pluralStringResource(R.plurals.n_fans, a.nbFans, a.nbFans)
                                } else {
                                    ""
                                },
                                a.artworkUrl,
                                onClick = { onOpenArtist(a) },
                            )
                        }
                    }
                    if (r.playlists.isNotEmpty()) item {
                        TvRow(stringResource(R.string.section_playlists), r.playlists) { _, p ->
                            TvCard(p.name, p.owner, p.artworkUrl, onClick = { onOpenPlaylist(p) })
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun TvLibrary(
    onPlayTracks: (List<Track>, Int) -> Unit,
    onOpenPlaylist: (Playlist) -> Unit,
) {
    var liked by remember { mutableStateOf<List<Track>?>(null) }
    var playlists by remember { mutableStateOf<List<Playlist>>(emptyList()) }
    var failed by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }

    LaunchedEffect(reload) {
        failed = false
        val l = Engine.favoritesOrNull()
        val p = Engine.playlistsOrNull()
        if (l == null && p == null) {
            failed = true
        } else {
            liked = l.orEmpty()
            playlists = p.orEmpty()
        }
    }

    if (failed && liked == null) { TvLoadError { reload++ }; return }
    val likedList = liked
    if (likedList == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(color = TvPalette.Purple)
        }
        return
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 40.dp, end = 40.dp, top = 40.dp, bottom = 40.dp),
        verticalArrangement = Arrangement.spacedBy(30.dp),
    ) {
        item {
            Text(stringResource(R.string.tv_your_library), style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Black, color = Color.White)
        }
        item {
            TvRow(stringResource(R.string.tv_liked_songs), likedList) { i, t ->
                TvCard(t.name, t.artistLine, t.artworkUrl, onClick = { onPlayTracks(likedList, i) })
            }
        }
        item {
            TvRow(stringResource(R.string.tv_your_playlists), playlists) { _, p ->
                TvCard(p.name, p.owner, p.artworkUrl, onClick = { onOpenPlaylist(p) })
            }
        }
        if (likedList.isEmpty() && playlists.isEmpty()) {
            item { Text(stringResource(R.string.tv_nothing_saved), color = TvPalette.TextDim, style = MaterialTheme.typography.titleMedium) }
        }
    }
}

@Composable
private fun TvHistory(onPlay: (Track) -> Unit) {
    var recent by remember { mutableStateOf<List<HistoryEntry>?>(null) }
    var stats by remember { mutableStateOf(HistoryStats.EMPTY) }

    LaunchedEffect(Unit) {
        stats = Engine.historyStats(30)
        recent = Engine.historyRecent(50)
    }

    val recentList = recent
    if (recentList == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(color = TvPalette.Purple)
        }
        return
    }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 40.dp, end = 40.dp, top = 40.dp, bottom = 40.dp),
        verticalArrangement = Arrangement.spacedBy(30.dp),
    ) {
        item {
            Text(
                stringResource(R.string.history_title),
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Black,
                color = Color.White,
            )
        }
        if (stats.totalSeconds > 0) {
            item {
                val minutes = stats.totalSeconds / 60
                val h = minutes / 60
                val m = minutes % 60
                val dur = if (h > 0) stringResource(R.string.dur_hm, h, m) else stringResource(R.string.dur_m, m)
                Text(
                    stringResource(R.string.history_stats_section) + " · " + stringResource(R.string.history_total, dur),
                    style = MaterialTheme.typography.titleMedium,
                    color = TvPalette.TextDim,
                )
            }
        }
        if (stats.topTracks.isNotEmpty()) {
            item {
                TvRow(stringResource(R.string.section_top_tracks), stats.topTracks) { _, t ->
                    val track = t.asHistoryTrack()
                    TvCard(track.name, track.artistLine, track.artworkUrl, onClick = { onPlay(track) })
                }
            }
        }
        if (stats.topArtists.isNotEmpty()) {
            item {
                TvRow(stringResource(R.string.history_top_artists), stats.topArtists) { _, a ->
                    TvCard(a.artist, "", "", onClick = {})
                }
            }
        }
        item {
            TvRow(stringResource(R.string.history_title), recentList) { _, e ->
                val track = e.asHistoryTrack()
                TvCard(track.name, track.artistLine, track.artworkUrl, onClick = { onPlay(track) })
            }
        }
        if (recentList.isEmpty() && stats.topTracks.isEmpty()) {
            item { Text(stringResource(R.string.history_empty), color = TvPalette.TextDim, style = MaterialTheme.typography.titleMedium) }
        }
    }
}

// History carries only id/title/artist; synthesise a minimal Track and let the
// engine resolve the full stream on play (durationMs unknown = 0). B14: episode
// rows (kind=="episode") route replay to the podcast play path.
private fun HistoryEntry.asHistoryTrack(): Track = Track(
    id = trackId, name = title, durationMs = 0L, artists = emptyList(),
    artistLine = artist, albumName = album, artworkUrl = "", explicit = false,
    isEpisode = kind == "episode",
)

private fun TrackStat.asHistoryTrack(): Track = Track(
    id = trackId, name = title, durationMs = 0L, artists = emptyList(),
    artistLine = artist, albumName = "", artworkUrl = "", explicit = false,
)

@Composable
private fun TvDetail(
    title: String,
    subtitle: String,
    artworkUrl: String,
    tracks: List<Track>,
    premium: Boolean,
    onBack: () -> Unit,
    onPlayAll: () -> Unit,
    onPlay: (Int) -> Unit,
    onRadio: (() -> Unit)? = null,
    onDownload: (() -> Unit)? = null,
) {
    val playFocus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { playFocus.requestFocus() } }

    Column(
        Modifier.fillMaxSize().background(TvPalette.screen).padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(24.dp),
    ) {
        Row(horizontalArrangement = Arrangement.spacedBy(24.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(180.dp).clip(RoundedCornerShape(16.dp))) {
                Artwork(artworkUrl, Modifier.fillMaxSize(), corner = 16)
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(title, style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold, color = Color.White, maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (subtitle.isNotBlank()) {
                    Text(subtitle, style = MaterialTheme.typography.titleMedium, color = TvPalette.TextDim)
                }
                Text(pluralStringResource(R.plurals.n_tracks, tracks.size, tracks.size), style = MaterialTheme.typography.bodyMedium, color = TvPalette.TextDim)
                Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                    TvPill(stringResource(R.string.action_play_all), onClick = onPlayAll, focusRequester = playFocus, leadingIcon = Icons.Filled.PlayArrow)
                    if (onRadio != null) {
                        TvPill(stringResource(R.string.action_start_radio), onClick = onRadio, leadingIcon = Icons.Filled.Radio)
                    }
                    // Downloads are premium-only, so only show the pill for premium.
                    if (onDownload != null && premium) {
                        TvPill(stringResource(R.string.action_download), onClick = onDownload, leadingIcon = Icons.Filled.Download)
                    }
                    TvPill(stringResource(R.string.action_back), onClick = onBack)
                }
            }
        }
        LazyColumn(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            itemsIndexed(tracks) { i, t ->
                TvTrackRow(t, onClick = { onPlay(i) })
            }
        }
    }
}

@Composable
private fun TvTrackRow(track: Track, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val bg = if (focused) TvPalette.Purple.copy(alpha = 0.18f) else Color.Transparent
    Row(
        Modifier
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .clickable(onClick = onClick)
            .clip(RoundedCornerShape(10.dp))
            .background(bg)
            .padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Box(Modifier.size(48.dp).clip(RoundedCornerShape(8.dp))) {
            Artwork(track.artworkUrl, Modifier.fillMaxSize(), corner = 8)
        }
        Column(Modifier.weight(1f)) {
            Text(
                track.name,
                style = MaterialTheme.typography.bodyLarge,
                color = Color.White,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                track.artistLine,
                style = MaterialTheme.typography.bodySmall,
                color = TvPalette.TextDim,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (focused) Icon(Icons.Filled.PlayArrow, contentDescription = stringResource(R.string.action_play), tint = TvPalette.Purple)
    }
}

@Composable
private fun TvNowPlayingBar(
    track: Track,
    isPlaying: Boolean,
    positionMs: Long,
    durationMs: Long,
    onPlayPause: () -> Unit,
    onNext: () -> Unit,
    onPrev: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(topStart = 20.dp, topEnd = 20.dp))
            .background(TvPalette.CardIdle),
    ) {
        LinearProgressIndicator(
            progress = { if (durationMs > 0) (positionMs.toFloat() / durationMs).coerceIn(0f, 1f) else 0f },
            modifier = Modifier.fillMaxWidth().height(3.dp),
            color = TvPalette.Purple,
            trackColor = Color.White.copy(alpha = 0.12f),
        )
        Row(
            Modifier.fillMaxWidth().height(92.dp).padding(horizontal = 48.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Box(Modifier.size(60.dp).clip(RoundedCornerShape(10.dp))) {
                Artwork(track.artworkUrl, Modifier.fillMaxSize(), corner = 10)
            }
            Column(Modifier.weight(1f)) {
                Text(track.name, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(track.artistLine, style = MaterialTheme.typography.bodyMedium, color = TvPalette.TextDim, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            TvPill("", onClick = onPrev, leadingIcon = Icons.Filled.SkipPrevious)
            TvPill("", onClick = onPlayPause, leadingIcon = if (isPlaying) Icons.Filled.Pause else Icons.Filled.PlayArrow)
            TvPill("", onClick = onNext, leadingIcon = Icons.Filled.SkipNext)
        }
    }
}

/**
 * The 10-foot queue editor. D-pad reorder is impractical, so each row exposes
 * move up/down, remove and (premium) download-for-offline as focusable pills;
 * "Clear" empties the queue. All edits route through the engine edit exports, so
 * the poll re-adopts and the list stays in lockstep with playback.
 */
@Composable
private fun TvQueue(player: PlayerController) {
    val state by player.state.collectAsStateWithLifecycle()
    val offlineIds by player.offlineIds.collectAsStateWithLifecycle()
    val queue = state.queue

    Column(
        Modifier.fillMaxSize().padding(48.dp),
        verticalArrangement = Arrangement.spacedBy(20.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(20.dp)) {
            Text(
                stringResource(R.string.queue_title),
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
            )
            Spacer(Modifier.weight(1f))
            if (queue.isNotEmpty()) {
                TvPill(
                    stringResource(R.string.action_clear_queue),
                    onClick = { player.clearQueue() },
                    leadingIcon = Icons.Filled.DeleteSweep,
                )
            }
        }
        if (queue.isEmpty()) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(stringResource(R.string.queue_empty), color = TvPalette.TextDim, style = MaterialTheme.typography.titleMedium)
            }
        } else {
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                itemsIndexed(queue, key = { i, t -> "q-$i-${t.id}" }) { i, t ->
                    TvQueueRow(
                        track = t,
                        isCurrent = i == state.index,
                        offlineDownloaded = offlineIds.contains(t.id),
                        premium = player.premium,
                        canUp = i > 0,
                        canDown = i < queue.lastIndex,
                        onPlay = { player.jumpTo(i) },
                        onUp = { player.moveInQueue(i, i - 1) },
                        onDown = { player.moveInQueue(i, i + 1) },
                        onRemove = { player.removeFromQueue(i) },
                        onDownloadOffline = { player.downloadForOffline(t) },
                    )
                }
            }
        }
    }
}

@Composable
private fun TvQueueRow(
    track: Track,
    isCurrent: Boolean,
    offlineDownloaded: Boolean,
    premium: Boolean,
    canUp: Boolean,
    canDown: Boolean,
    onPlay: () -> Unit,
    onUp: () -> Unit,
    onDown: () -> Unit,
    onRemove: () -> Unit,
    onDownloadOffline: () -> Unit,
) {
    val bg = if (isCurrent) TvPalette.Purple.copy(alpha = 0.18f) else Color.Transparent
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .background(bg)
            .padding(horizontal = 14.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Box(Modifier.size(56.dp).clip(RoundedCornerShape(8.dp))) {
            Artwork(track.artworkUrl, Modifier.fillMaxSize(), corner = 8)
        }
        Column(Modifier.weight(1f)) {
            Text(
                track.name,
                style = MaterialTheme.typography.bodyLarge,
                color = Color.White,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                track.artistLine,
                style = MaterialTheme.typography.bodySmall,
                color = TvPalette.TextDim,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (offlineDownloaded) {
            Icon(Icons.Filled.DownloadDone, contentDescription = stringResource(R.string.cd_downloaded), tint = TvPalette.Purple)
        }
        if (isCurrent) {
            Text(
                stringResource(R.string.cd_now_playing),
                style = MaterialTheme.typography.labelMedium,
                color = TvPalette.Purple,
                fontWeight = FontWeight.Bold,
            )
        } else {
            TvPill("", onClick = onPlay, leadingIcon = Icons.Filled.PlayArrow)
        }
        if (canUp) TvPill("", onClick = onUp, leadingIcon = Icons.Filled.KeyboardArrowUp)
        if (canDown) TvPill("", onClick = onDown, leadingIcon = Icons.Filled.KeyboardArrowDown)
        if (premium) {
            TvPill(
                "",
                onClick = onDownloadOffline,
                leadingIcon = if (offlineDownloaded) Icons.Filled.DownloadDone else Icons.Filled.DownloadForOffline,
            )
        }
        if (!isCurrent) TvPill("", onClick = onRemove, leadingIcon = Icons.Filled.Delete)
    }
}
