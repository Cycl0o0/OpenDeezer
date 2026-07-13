package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.KeyboardArrowDown
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
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.LyricLine
import fr.cyclooo.opendeezer.engine.Lyrics
import fr.cyclooo.opendeezer.player.PlayerController
import androidx.compose.runtime.collectAsState

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LyricsScreen(player: PlayerController, onBack: () -> Unit) {
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.lyrics_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        LyricsContent(player, Modifier.fillMaxSize().padding(padding))
    }
}

/** Lyrics body without the app-bar chrome, reusable inside fold-aware layouts. */
@Composable
fun LyricsContent(player: PlayerController, modifier: Modifier = Modifier) {
    val playerState by player.state.collectAsState()
    // B3: when routed to a Connect device, the local queue track may differ from
    //     what is actually playing on the remote. Use Engine.nowPlaying() (which
    //     carries the correct track id from DZNowPlayingJSON) in that case.
    //     If nothing is playing on the remote, nowPlaying() returns null and
    //     the LaunchedEffect below will show "No lyrics available." gracefully.
    val trackId = if (playerState.connectedDevice.isNotBlank()) {
        Engine.nowPlaying()?.id
    } else {
        playerState.current?.id
    }
    val position = playerState.positionMs

    var lyrics by remember(trackId) { mutableStateOf<Lyrics?>(null) }
    LaunchedEffect(trackId) {
        lyrics = if (trackId.isNullOrBlank()) Lyrics("", emptyList()) else Engine.lyrics(trackId)
    }

    // Deezer's synced array carries timed blank/separator entries (instrumental
    // breaks, noise). Drop them so the active line is always a real lyric — the
    // other platforms filter these; not doing so here highlighted a blank line as
    // the "current" lyric.
    val syncedLines = remember(lyrics) { lyrics?.lines?.filter { it.text.isNotBlank() }.orEmpty() }

    Box(modifier) {
        val lyr = lyrics
        when {
            lyr == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                CircularProgressIndicator()
            }
            syncedLines.isNotEmpty() -> SyncedLyrics(syncedLines, position)
            lyr.plain.isNotBlank() -> Text(
                lyr.plain,
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(20.dp),
                style = MaterialTheme.typography.bodyLarge,
            )
            else -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text(stringResource(R.string.lyrics_none), color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
private fun SyncedLyrics(lines: List<LyricLine>, positionMs: Long) {
    // The active line is the last one whose timestamp is <= current position.
    val activeIndex = remember(positionMs, lines) {
        lines.indexOfLast { it.timeMs <= positionMs }.coerceAtLeast(0)
    }
    val listState = rememberLazyListState()
    LaunchedEffect(activeIndex) {
        if (activeIndex >= 0 && lines.isNotEmpty()) {
            listState.animateScrollToItem(activeIndex.coerceIn(0, lines.lastIndex))
        }
    }
    LazyColumn(
        state = listState,
        modifier = Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 12.dp),
    ) {
        itemsIndexed(lines, key = { i, _ -> i }) { index, line ->
            val active = index == activeIndex
            Text(
                line.text,
                modifier = Modifier.fillMaxWidth().padding(vertical = 6.dp),
                color = if (active) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
                fontWeight = if (active) FontWeight.Bold else FontWeight.Normal,
                fontSize = if (active) 20.sp else 16.sp,
            )
        }
    }
}
