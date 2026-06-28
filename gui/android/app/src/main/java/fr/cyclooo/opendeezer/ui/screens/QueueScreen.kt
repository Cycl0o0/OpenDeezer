package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.collectAsState
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import fr.cyclooo.opendeezer.ui.components.TrackRow

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun QueueScreen(player: PlayerController, onBack: () -> Unit) {
    val state by player.state.collectAsState()
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Queue") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Box(Modifier.fillMaxSize()) {
            if (state.queue.isEmpty()) {
                CenteredMessage("The queue is empty.")
            } else {
                LazyColumn(Modifier.fillMaxSize().padding(padding)) {
                    itemsIndexed(state.queue, key = { i, t -> "$i-${t.id}" }) { index, track ->
                        val isCurrent = index == state.index
                        TrackRow(
                            track = track,
                            onClick = { player.jumpTo(index) },
                            modifier = if (isCurrent) {
                                Modifier.background(Color(0x1AA238FF))
                            } else {
                                Modifier
                            },
                            trailing = if (isCurrent) {
                                {
                                    Icon(
                                        Icons.Filled.VolumeUp,
                                        contentDescription = "Now playing",
                                        tint = MaterialTheme.colorScheme.primary,
                                    )
                                }
                            } else null,
                        )
                    }
                }
            }
        }
    }
}
