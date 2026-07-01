package fr.cyclooo.opendeezer.ui.components

import android.content.Intent
import android.net.Uri
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.SystemUpdate
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.engine.UpdateInfo

/**
 * Small, dismissible "an update is available" notice. Purely informational —
 * Download just opens the GitHub release page in the browser; OpenDeezer
 * never downloads or installs anything itself.
 */
@Composable
fun UpdateBanner(info: UpdateInfo, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    var showNotes by remember { mutableStateOf(false) }

    fun openRelease() {
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(info.url))) }
    }

    Surface(
        // Sits outside any screen's own Scaffold, so it fits itself below the
        // status bar (a no-op where the system already reserves that space).
        modifier = modifier.fillMaxWidth().statusBarsPadding(),
        color = MaterialTheme.colorScheme.primaryContainer,
    ) {
        Row(
            Modifier.fillMaxWidth().padding(start = 16.dp, end = 4.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                Icons.Filled.SystemUpdate,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimaryContainer,
            )
            Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                Text(
                    "OpenDeezer ${info.latest} available",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onPrimaryContainer,
                )
                if (info.notes.isNotBlank()) {
                    TextButton(onClick = { showNotes = true }, contentPadding = PaddingValues(0.dp)) {
                        Text("Release notes", style = MaterialTheme.typography.labelMedium)
                    }
                }
            }
            TextButton(onClick = { openRelease() }) { Text("Download") }
            IconButton(onClick = onDismiss) {
                Icon(
                    Icons.Filled.Close,
                    contentDescription = "Dismiss",
                    tint = MaterialTheme.colorScheme.onPrimaryContainer,
                )
            }
        }
    }

    if (showNotes) {
        AlertDialog(
            onDismissRequest = { showNotes = false },
            confirmButton = {
                TextButton(onClick = { showNotes = false; openRelease() }) { Text("Download") }
            },
            dismissButton = { TextButton(onClick = { showNotes = false }) { Text("Close") } },
            title = { Text("OpenDeezer ${info.latest}") },
            text = {
                Text(
                    info.notes,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.verticalScroll(rememberScrollState()),
                )
            },
        )
    }
}
