package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.QueueMusic
import androidx.compose.material.icons.filled.BarChart
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.CastConnected
import androidx.compose.material.icons.filled.Favorite
import androidx.compose.material.icons.filled.LibraryMusic
import androidx.compose.material.icons.filled.Podcasts
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Stream
import androidx.compose.material3.Card
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.Routes
import fr.cyclooo.opendeezer.ui.theme.DeezerPurple

private data class HomeEntry(val label: String, val icon: ImageVector, val route: String)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    accountName: String,
    connected: Boolean,
    onNavigate: (String) -> Unit,
    onCast: () -> Unit,
    onSettings: () -> Unit,
) {
    val entries = listOf(
        HomeEntry("Liked Songs", Icons.Filled.Favorite, Routes.LIKED),
        HomeEntry("My Playlists", Icons.Filled.LibraryMusic, Routes.PLAYLISTS),
        HomeEntry("Flow", Icons.Filled.Stream, Routes.FLOW),
        HomeEntry("Charts", Icons.Filled.BarChart, Routes.CHARTS),
        HomeEntry("Podcasts", Icons.Filled.Podcasts, Routes.PODCASTS),
        HomeEntry("Search", Icons.Filled.Search, Routes.SEARCH),
        HomeEntry("Queue", Icons.AutoMirrored.Filled.QueueMusic, Routes.QUEUE),
    )
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("OpenDeezer", color = DeezerPurple) },
                actions = {
                    IconButton(onClick = onCast) {
                        Icon(
                            if (connected) Icons.Filled.CastConnected else Icons.Filled.Cast,
                            contentDescription = "Connect",
                            tint = if (connected) DeezerPurple else MaterialTheme.colorScheme.onSurface,
                        )
                    }
                    IconButton(onClick = onSettings) {
                        Icon(Icons.Filled.Settings, contentDescription = "Settings")
                    }
                },
            )
        },
    ) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding)) {
            item {
                Text(
                    if (accountName.isNotBlank()) "Hi, $accountName" else "Welcome",
                    style = MaterialTheme.typography.headlineSmall,
                    modifier = Modifier.padding(16.dp),
                )
            }
            items(entries, key = { it.route }) { e ->
                Card(
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 6.dp)
                        .clickable { onNavigate(e.route) },
                ) {
                    Row(
                        Modifier.fillMaxWidth().padding(16.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(e.icon, contentDescription = null, tint = DeezerPurple, modifier = Modifier.size(28.dp))
                        Spacer(Modifier.width(16.dp))
                        Text(e.label, style = MaterialTheme.typography.titleMedium)
                    }
                }
            }
            item { Spacer(Modifier.height(96.dp)) }
        }
    }
}
