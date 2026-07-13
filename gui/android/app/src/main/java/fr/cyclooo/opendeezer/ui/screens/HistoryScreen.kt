package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.ArtistStat
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.HistoryEntry
import fr.cyclooo.opendeezer.engine.HistoryStats
import fr.cyclooo.opendeezer.engine.Track
import fr.cyclooo.opendeezer.engine.TrackStat
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.SectionHeader
import fr.cyclooo.opendeezer.ui.components.TrackRow

/** A completed-load wrapper so "still loading" (null) reads apart from "empty". */
private data class HistoryLoad(val recent: List<HistoryEntry>, val stats: HistoryStats)

/**
 * Recently played + a 30-day listening summary (top tracks / top artists / total
 * time). Every track row plays by id and can seed a song radio. Backed by the
 * machine-local history the control API also serves.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HistoryScreen(player: PlayerController, onBack: () -> Unit) {
    val load by produceState<HistoryLoad?>(initialValue = null) {
        value = HistoryLoad(Engine.historyRecent(50), Engine.historyStats(30))
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.history_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            val data = load
            when {
                data == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                data.recent.isEmpty() && data.stats.topTracks.isEmpty() ->
                    CenteredMessage(stringResource(R.string.history_empty))
                else -> HistoryContent(data, player)
            }
        }
    }
}

@Composable
private fun HistoryContent(data: HistoryLoad, player: PlayerController) {
    val stats = data.stats
    LazyColumn(Modifier.fillMaxSize()) {
        if (stats.totalSeconds > 0 || stats.topTracks.isNotEmpty() || stats.topArtists.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.history_stats_section)) }
            if (stats.totalSeconds > 0) {
                item {
                    Text(
                        stringResource(R.string.history_total, formatListened(stats.totalSeconds)),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                    )
                }
            }
        }

        if (stats.topTracks.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.section_top_tracks)) }
            itemsIndexed(stats.topTracks, key = { i, t -> "st-$i-${t.trackId}" }) { _, t ->
                val track = t.asTrack()
                TrackRow(
                    track = track,
                    player = player,
                    onClick = { player.playSingle(track) },
                    onStartRadio = { player.startTrackRadio(track) },
                    trailing = {
                        Text(
                            pluralStringResource(R.plurals.n_plays, t.plays, t.plays),
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    },
                )
            }
        }

        if (stats.topArtists.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.history_top_artists)) }
            itemsIndexed(stats.topArtists, key = { i, a -> "sa-$i-${a.artist}" }) { _, a ->
                ArtistStatRow(a)
            }
        }

        if (data.recent.isNotEmpty()) {
            item { SectionHeader(stringResource(R.string.history_title)) }
            itemsIndexed(data.recent, key = { i, e -> "h-$i-${e.trackId}-${e.startedAt}" }) { _, e ->
                val track = e.asTrack()
                TrackRow(
                    track = track,
                    player = player,
                    onClick = { player.playSingle(track) },
                    onStartRadio = { player.startTrackRadio(track) },
                )
            }
        }

        item { Spacer(Modifier.height(88.dp)) }
    }
}

@Composable
private fun ArtistStatRow(a: ArtistStat) {
    androidx.compose.foundation.layout.Row(
        Modifier.padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            a.artist.ifBlank { stringResource(R.string.unknown_title) },
            style = MaterialTheme.typography.bodyLarge,
            modifier = Modifier.weight(1f),
        )
        Text(
            pluralStringResource(R.plurals.n_plays, a.plays, a.plays),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// History carries only id/title/artist, so we synthesise a minimal Track: the
// engine resolves the full stream + metadata when it plays (durationMs unknown = 0).
// B14: episode rows carry kind=="episode" so replay routes to the podcast play
// path (Engine.playEpisode) instead of the track path.
private fun HistoryEntry.asTrack(): Track = Track(
    id = trackId,
    name = title,
    durationMs = 0L,
    artists = emptyList(),
    artistLine = artist,
    albumName = album,
    artworkUrl = "",
    explicit = false,
    isEpisode = kind == "episode",
)

private fun TrackStat.asTrack(): Track = Track(
    id = trackId,
    name = title,
    durationMs = 0L,
    artists = emptyList(),
    artistLine = artist,
    albumName = "",
    artworkUrl = "",
    explicit = false,
)

@Composable
private fun formatListened(totalSeconds: Long): String {
    val minutes = totalSeconds / 60
    val h = minutes / 60
    val m = minutes % 60
    return if (h > 0) stringResource(R.string.dur_hm, h, m) else stringResource(R.string.dur_m, m)
}
