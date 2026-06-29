package fr.cyclooo.opendeezer.ui.screens

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
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
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.WebRemoteInfo

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

            Text("Phone Remote", style = MaterialTheme.typography.titleMedium)
            SettingSwitch(
                "Enable",
                "Serve a remote control page on your local Wi-Fi",
                webRemoteEnabled,
            ) {
                webRemoteEnabled = it
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
