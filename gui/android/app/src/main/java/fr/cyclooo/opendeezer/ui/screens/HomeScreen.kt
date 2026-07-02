package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BarChart
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.CastConnected
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.Podcasts
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Stream
import androidx.compose.material3.Card
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.Routes
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.HomeData
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.MediaCard
import fr.cyclooo.opendeezer.ui.components.SectionHeader
import fr.cyclooo.opendeezer.ui.components.TrackRow
import fr.cyclooo.opendeezer.ui.theme.DeezerPurple
import java.util.Calendar

private data class QuickPick(val label: String, val icon: ImageVector, val route: String)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    accountName: String,
    connected: Boolean,
    player: PlayerController,
    onNavigate: (String) -> Unit,
    onPlaylist: (Playlist) -> Unit,
    onCast: () -> Unit,
    onSettings: () -> Unit,
) {
    val homeData by produceState<HomeData?>(initialValue = null) {
        value = Engine.home()
    }

    val hour = Calendar.getInstance().get(Calendar.HOUR_OF_DAY)
    val timeGreeting = when {
        hour < 12 -> stringResource(R.string.greeting_morning)
        hour < 17 -> stringResource(R.string.greeting_afternoon)
        else -> stringResource(R.string.greeting_evening)
    }
    val greeting = if (accountName.isNotBlank()) {
        stringResource(R.string.greeting_named, timeGreeting, accountName)
    } else {
        timeGreeting
    }

    val quickPicks = listOf(
        QuickPick(stringResource(R.string.home_liked), Icons.Filled.Favorite, Routes.LIKED),
        QuickPick("Flow", Icons.Filled.Stream, Routes.FLOW),
        QuickPick(stringResource(R.string.charts_title), Icons.Filled.BarChart, Routes.CHARTS),
        QuickPick(stringResource(R.string.podcasts_title), Icons.Filled.Podcasts, Routes.PODCASTS),
    )

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.app_name), color = DeezerPurple) },
                actions = {
                    IconButton(onClick = onCast) {
                        Icon(
                            if (connected) Icons.Filled.CastConnected else Icons.Filled.Cast,
                            contentDescription = stringResource(R.string.cd_connect),
                            tint = if (connected) DeezerPurple else MaterialTheme.colorScheme.onSurface,
                        )
                    }
                    IconButton(onClick = onSettings) {
                        Icon(Icons.Filled.Settings, contentDescription = stringResource(R.string.settings_title))
                    }
                },
            )
        },
    ) { padding ->
        // Snapshot read in composable scope so LazyColumn recomposes on data arrival.
        val data = homeData

        LazyColumn(
            Modifier.fillMaxSize().padding(padding),
            contentPadding = PaddingValues(bottom = 96.dp),
        ) {
            // 1. Time-based greeting
            item {
                Text(
                    greeting,
                    style = MaterialTheme.typography.headlineMedium,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 20.dp),
                )
            }

            // 2. Quick-pick cards: Liked Songs · Flow · Charts · Podcasts
            item {
                Row(
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    quickPicks.forEach { qp ->
                        Card(
                            Modifier
                                .weight(1f)
                                .clickable { onNavigate(qp.route) },
                        ) {
                            Column(
                                Modifier
                                    .fillMaxWidth()
                                    .padding(vertical = 12.dp, horizontal = 4.dp),
                                horizontalAlignment = Alignment.CenterHorizontally,
                                verticalArrangement = Arrangement.spacedBy(4.dp),
                            ) {
                                Icon(
                                    qp.icon,
                                    contentDescription = null,
                                    tint = DeezerPurple,
                                    modifier = Modifier.size(24.dp),
                                )
                                Text(
                                    qp.label,
                                    style = MaterialTheme.typography.labelSmall,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                    textAlign = TextAlign.Center,
                                )
                            }
                        }
                    }
                }
            }

            item { Spacer(Modifier.height(16.dp)) }

            // 3 & 4. Dynamic sections — show spinner until data arrives
            if (data == null) {
                item {
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .height(120.dp),
                        contentAlignment = Alignment.Center,
                    ) {
                        CircularProgressIndicator()
                    }
                }
            } else {
                // 3. Top Tracks horizontal rail
                if (data.topTracks.isNotEmpty()) {
                    item { SectionHeader(stringResource(R.string.section_top_tracks)) }
                    item {
                        LazyRow(contentPadding = PaddingValues(horizontal = 8.dp)) {
                            itemsIndexed(
                                data.topTracks,
                                key = { i, t -> "ht-$i-${t.id}" },
                            ) { index, track ->
                                // Constrain TrackRow to a fixed width so it works inside LazyRow.
                                Box(Modifier.width(280.dp)) {
                                    TrackRow(
                                        track = track,
                                        onClick = { player.playQueue(data.topTracks, index) },
                                    )
                                }
                            }
                        }
                    }
                }

                // 4. Your Playlists horizontal rail
                if (data.playlists.isNotEmpty()) {
                    item { SectionHeader(stringResource(R.string.section_your_playlists)) }
                    item {
                        LazyRow(contentPadding = PaddingValues(horizontal = 8.dp)) {
                            items(data.playlists, key = { "hp-${it.id}" }) { pl ->
                                MediaCard(
                                    title = pl.name,
                                    subtitle = if (pl.trackCount > 0) {
                                        pluralStringResource(R.plurals.n_tracks, pl.trackCount, pl.trackCount)
                                    } else {
                                        pl.owner
                                    },
                                    artworkUrl = pl.artworkUrl,
                                    onClick = { onPlaylist(pl) },
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
