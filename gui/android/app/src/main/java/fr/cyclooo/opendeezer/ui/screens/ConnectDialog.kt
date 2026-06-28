package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cast
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Smartphone
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.engine.ConnectDevice
import fr.cyclooo.opendeezer.engine.Engine
import kotlinx.coroutines.launch

@Composable
fun ConnectDialog(onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    var devices by remember { mutableStateOf<List<ConnectDevice>?>(null) }
    var connected by remember { mutableStateOf(Engine.connectedDevice()) }

    fun refresh() {
        devices = null
        scope.launch {
            devices = Engine.discoverDevices(700L)
            connected = Engine.connectedDevice()
        }
    }

    // Discover on first show.
    LaunchedEffect(Unit) {
        devices = Engine.discoverDevices(700L)
        connected = Engine.connectedDevice()
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = { TextButton(onClick = onDismiss) { Text("Close") } },
        dismissButton = { TextButton(onClick = { refresh() }) { Text("Refresh") } },
        title = { Text("OpenDeezer Connect") },
        text = {
            Column {
                // "This device" — selecting it returns playback to local.
                DeviceRow(
                    title = "This device",
                    subtitle = "OpenDeezer (Android)",
                    leading = Icons.Filled.Smartphone,
                    selected = connected.isBlank(),
                    onClick = {
                        Engine.disconnectDevice()
                        connected = ""
                        onDismiss()
                    },
                )
                Spacer(Modifier.size(4.dp))
                when (val list = devices) {
                    null -> Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                    else -> if (list.isEmpty()) {
                        Text(
                            "No devices found on your network.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(8.dp),
                        )
                    } else {
                        LazyColumn(Modifier.heightIn(max = 280.dp)) {
                            items(list, key = { it.addr }) { d ->
                                DeviceRow(
                                    title = d.name.ifBlank { d.addr },
                                    subtitle = listOfNotNull(
                                        d.typeLabel,
                                        d.version.ifBlank { null }?.let { "v$it" },
                                    ).joinToString(" · "),
                                    leading = Icons.Filled.Cast,
                                    selected = connected == d.addr,
                                    onClick = {
                                        scope.launch {
                                            if (Engine.connectDevice(d.addr)) {
                                                connected = Engine.connectedDevice()
                                                onDismiss()
                                            }
                                        }
                                    },
                                )
                            }
                        }
                    }
                }
            }
        },
    )
}

@Composable
private fun DeviceRow(
    title: String,
    subtitle: String,
    leading: androidx.compose.ui.graphics.vector.ImageVector,
    selected: Boolean,
    onClick: () -> Unit,
) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Start,
    ) {
        Icon(
            leading,
            contentDescription = null,
            tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
        )
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            if (subtitle.isNotBlank()) {
                Text(
                    subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (selected) {
            Icon(
                Icons.Filled.CheckCircle,
                contentDescription = "Connected",
                tint = MaterialTheme.colorScheme.primary,
            )
        }
    }
}
