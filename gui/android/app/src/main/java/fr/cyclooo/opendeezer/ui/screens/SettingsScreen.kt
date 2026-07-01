package fr.cyclooo.opendeezer.ui.screens

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.ConnectHostInfo
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.UpdateInfo
import fr.cyclooo.opendeezer.engine.WebRemoteInfo
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(account: Account?, onBack: () -> Unit, onLogout: () -> Unit) {
    var quality by remember { mutableIntStateOf(Engine.quality()) }
    var replayGain by remember { mutableStateOf(Engine.replayGain()) }
    var gapless by remember { mutableStateOf(Engine.gapless()) }
    var crossfadeSec by remember { mutableFloatStateOf((Engine.crossfadeMs() / 1000f)) }
    var webRemoteEnabled by remember { mutableStateOf(Engine.webRemoteInfo()?.enabled ?: false) }
    var remoteInfo by remember { mutableStateOf<WebRemoteInfo?>(null) }
    var remoteQR by remember { mutableStateOf<ByteArray?>(null) }
    var connectHostEnabled by remember { mutableStateOf(Engine.connectHostInfo()?.enabled ?: false) }
    var connectHostInfo by remember { mutableStateOf<ConnectHostInfo?>(null) }
    var checkingUpdate by remember { mutableStateOf(false) }
    var updateResult by remember { mutableStateOf<UpdateCheckResult?>(null) }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val prefs = remember(context) { Prefs(context) }

    LaunchedEffect(connectHostEnabled) {
        connectHostInfo = if (connectHostEnabled) Engine.connectHostInfo() else null
    }

    LaunchedEffect(webRemoteEnabled) {
        if (webRemoteEnabled) {
            remoteInfo = Engine.webRemoteInfo()
            remoteQR = Engine.webRemoteQRPng()
        } else {
            remoteInfo = null
            remoteQR = null
        }
    }

    val qualityLabels = listOf("Normal", "High", "HiFi")

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            Text("Audio quality", style = MaterialTheme.typography.titleMedium)
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                qualityLabels.forEachIndexed { index, label ->
                    SegmentedButton(
                        selected = quality == index,
                        onClick = {
                            quality = index
                            Engine.setQuality(index)
                        },
                        shape = SegmentedButtonDefaults.itemShape(index, qualityLabels.size),
                        enabled = canSelectQuality(account, index),
                    ) { Text(label) }
                }
            }
            Text(
                qualityDescription(quality),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            HorizontalDivider()

            SettingSwitch("ReplayGain", "Normalise loudness across tracks", replayGain) {
                replayGain = it
                Engine.setReplayGain(it)
            }
            SettingSwitch("Gapless playback", "No silence between tracks", gapless) {
                gapless = it
                Engine.setGapless(it)
            }

            HorizontalDivider()

            Column {
                Text("Crossfade", style = MaterialTheme.typography.titleMedium)
                Text(
                    if (crossfadeSec <= 0f) "Off" else "%.0f s".format(crossfadeSec),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Slider(
                    value = crossfadeSec,
                    onValueChange = { crossfadeSec = it },
                    onValueChangeFinished = { Engine.setCrossfadeMs((crossfadeSec * 1000).toInt()) },
                    valueRange = 0f..12f,
                    steps = 11,
                )
            }

            HorizontalDivider()

            Text("OpenDeezer Connect", style = MaterialTheme.typography.titleMedium)
            SettingSwitch(
                "Make this device reachable",
                "Let your other OpenDeezer apps find and control this device",
                connectHostEnabled,
            ) {
                connectHostEnabled = it
                prefs.connectHostEnabled = it
                Engine.setConnectHostEnabled(it)
            }
            connectHostInfo?.takeIf { it.enabled && it.addr.isNotBlank() }?.let { info ->
                Text(
                    "Reachable at ${info.addr}" + (info.name.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            HorizontalDivider()

            Text("Phone Remote", style = MaterialTheme.typography.titleMedium)
            SettingSwitch(
                "Enable",
                "Serve a remote control page on your local Wi-Fi",
                webRemoteEnabled,
            ) {
                webRemoteEnabled = it
                prefs.phoneRemoteEnabled = it
                Engine.setWebRemoteEnabled(it)
            }
            if (webRemoteEnabled) {
                val info = remoteInfo
                if (info == null) {
                    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                } else {
                    Column(
                        Modifier.fillMaxWidth(),
                        horizontalAlignment = Alignment.CenterHorizontally,
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        Text(
                            "Scan with your phone (same Wi-Fi), then enter the code.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.Center,
                        )
                        val imageBitmap = remember(remoteQR) {
                            remoteQR?.let { bytes ->
                                BitmapFactory.decodeByteArray(bytes, 0, bytes.size)?.asImageBitmap()
                            }
                        }
                        if (imageBitmap != null) {
                            Image(
                                bitmap = imageBitmap,
                                contentDescription = "QR code",
                                modifier = Modifier.size(200.dp),
                            )
                        }
                        Text(
                            info.code,
                            style = MaterialTheme.typography.displaySmall.copy(
                                fontFamily = FontFamily.Monospace,
                            ),
                            textAlign = TextAlign.Center,
                        )
                        Text(
                            info.url,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.Center,
                        )
                    }
                }
            }

            HorizontalDivider()

            Text("About", style = MaterialTheme.typography.titleMedium)
            SettingAction(
                title = "Check for updates",
                subtitle = if (checkingUpdate) "Checking…" else "Checks GitHub for a newer release",
                enabled = !checkingUpdate,
                trailing = {
                    if (checkingUpdate) {
                        CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                    }
                },
                onClick = {
                    checkingUpdate = true
                    scope.launch {
                        val info = Engine.checkUpdate()
                        checkingUpdate = false
                        updateResult = when {
                            info == null -> UpdateCheckResult.Failed
                            info.hasUpdate -> UpdateCheckResult.Available(info)
                            else -> UpdateCheckResult.UpToDate(info.current)
                        }
                    }
                },
            )

            HorizontalDivider()

            if (account != null) {
                Text("Account", style = MaterialTheme.typography.titleMedium)
                Text(account.name, style = MaterialTheme.typography.bodyLarge)
                Text(
                    "Plan: ${account.offer.ifBlank { "—" }}" +
                        (if (account.canHifi) " · HiFi" else if (account.canHq) " · HQ" else ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            OutlinedButton(onClick = onLogout, modifier = Modifier.fillMaxWidth()) {
                Text("Sign out")
            }
            Spacer(Modifier.height(24.dp))
        }
    }

    when (val result = updateResult) {
        is UpdateCheckResult.UpToDate -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = { TextButton(onClick = { updateResult = null }) { Text("OK") } },
            title = { Text("You're up to date") },
            text = {
                Text(
                    if (result.current.isBlank()) "This is the latest version."
                    else "OpenDeezer v${result.current} is the latest version.",
                )
            },
        )

        is UpdateCheckResult.Available -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = {
                TextButton(onClick = {
                    updateResult = null
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(result.info.url))) }
                }) { Text("Download") }
            },
            dismissButton = { TextButton(onClick = { updateResult = null }) { Text("Close") } },
            title = { Text("OpenDeezer ${result.info.latest} available") },
            text = {
                Text(
                    result.info.notes.ifBlank { "A new version is available on GitHub." },
                    modifier = Modifier.verticalScroll(rememberScrollState()),
                )
            },
        )

        UpdateCheckResult.Failed -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = { TextButton(onClick = { updateResult = null }) { Text("OK") } },
            title = { Text("Couldn't check for updates") },
            text = { Text("Check your connection and try again.") },
        )

        null -> {}
    }
}

private sealed interface UpdateCheckResult {
    data class UpToDate(val current: String) : UpdateCheckResult
    data class Available(val info: UpdateInfo) : UpdateCheckResult
    data object Failed : UpdateCheckResult
}

@Composable
private fun SettingAction(
    title: String,
    subtitle: String,
    enabled: Boolean = true,
    trailing: @Composable (() -> Unit)? = null,
    onClick: () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().clickable(enabled = enabled, onClick = onClick),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (trailing != null) {
            Spacer(Modifier.width(8.dp))
            trailing()
        }
    }
}

private fun canSelectQuality(account: Account?, index: Int): Boolean = when (index) {
    2 -> account?.canHifi ?: true
    1 -> account?.canHq ?: true
    else -> true
}

private fun qualityDescription(level: Int): String = when (level) {
    2 -> "FLAC · lossless"
    1 -> "MP3 · 320 kbps"
    else -> "MP3 · 128 kbps"
}

@Composable
private fun SettingSwitch(title: String, subtitle: String, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(checked = checked, onCheckedChange = onChange)
    }
}
