package fr.cyclooo.opendeezer.tv

import android.graphics.BitmapFactory
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.ConnectDevice
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.WebRemoteInfo
import kotlinx.coroutines.launch

/**
 * TV settings: audio, OpenDeezer Connect (make this device reachable + phone
 * remote + play-on other devices), update check and account. Everything is
 * D-pad focusable; toggles flip on the centre button.
 */
@Composable
fun TvSettingsScreen(account: Account?, onLogout: () -> Unit) {
    val context = LocalContext.current
    val prefs = remember(context) { Prefs(context) }
    val scope = rememberCoroutineScope()

    var quality by remember { mutableStateOf(Engine.quality()) }
    var replayGain by remember { mutableStateOf(Engine.replayGain()) }
    var gapless by remember { mutableStateOf(Engine.gapless()) }
    var sleepSel by remember {
        mutableStateOf(
            when {
                Engine.sleepEndOfTrack() -> 5
                Engine.sleepActive() -> when ((Engine.sleepRemainingMs() / 60_000L).toInt()) {
                    in 0 until 15 -> 1
                    in 15 until 30 -> 2
                    in 30 until 45 -> 3
                    else -> 4
                }
                else -> 0
            },
        )
    }
    // EQ state lives engine-side (persisted there, shared with every client);
    // read once on entry, mirror edits locally.
    val eqInit = remember { Engine.eqState() }
    var eqEnabled by remember { mutableStateOf(eqInit?.enabled ?: false) }
    var eqMono by remember { mutableStateOf(eqInit?.mono ?: false) }
    var eqPreset by remember { mutableStateOf(eqInit?.preset ?: "flat") }
    val eqGains = remember { mutableStateListOf<Double>().apply { addAll(eqInit?.gainsDb ?: List(10) { 0.0 }) } }
    var connectHost by remember { mutableStateOf(Engine.connectHostInfo()?.enabled ?: false) }
    var connectAddr by remember { mutableStateOf(Engine.connectHostInfo()?.addr.orEmpty()) }
    var phoneRemote by remember { mutableStateOf(Engine.webRemoteInfo()?.enabled ?: false) }
    var remoteInfo by remember { mutableStateOf<WebRemoteInfo?>(null) }
    var remoteQr by remember { mutableStateOf<ByteArray?>(null) }
    var devices by remember { mutableStateOf<List<ConnectDevice>?>(null) }
    var scanning by remember { mutableStateOf(false) }
    var connected by remember { mutableStateOf(Engine.connectedDevice()) }
    var updateText by remember { mutableStateOf("") }

    fun rescanDevices() {
        if (scanning) return
        scanning = true
        scope.launch {
            try {
                devices = Engine.discoverDevices(700L)
                connected = Engine.connectedDevice()
            } finally {
                scanning = false
            }
        }
    }

    LaunchedEffect(phoneRemote) {
        if (phoneRemote) {
            remoteInfo = Engine.webRemoteInfo()
            remoteQr = Engine.webRemoteQRPng()
        } else {
            remoteInfo = null; remoteQr = null
        }
    }

    LazyColumn(
        Modifier.fillMaxSize().padding(start = 48.dp, end = 48.dp, top = 40.dp, bottom = 40.dp),
        verticalArrangement = Arrangement.spacedBy(26.dp),
    ) {
        item {
            Text(
                "Settings",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Black,
                color = Color.White,
            )
        }

        // ---- Audio ----
        item { TvSectionTitle("Audio") }
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Quality", color = Color.White, style = MaterialTheme.typography.titleMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    listOf("Normal", "High", "HiFi").forEachIndexed { i, label ->
                        val allowed = when (i) {
                            2 -> account?.canHifi ?: true
                            1 -> account?.canHq ?: true
                            else -> true
                        }
                        TvChoicePill(label, selected = quality == i, enabled = allowed) {
                            quality = i; Engine.setQuality(i); prefs.audioQuality = i
                        }
                    }
                }
                if (account != null && !account.canHifi) {
                    Text(
                        if (account.canHq) "HiFi needs a Deezer HiFi plan." else "High/HiFi need a Deezer HQ or HiFi plan.",
                        color = TvPalette.TextDim,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }
        item {
            TvToggleRow("ReplayGain", "Normalise loudness across tracks", replayGain) {
                replayGain = it; Engine.setReplayGain(it); prefs.replayGain = if (it) 1 else 0
            }
        }
        item {
            TvToggleRow("Gapless playback", "No silence between tracks", gapless) {
                gapless = it; Engine.setGapless(it); prefs.gapless = if (it) 1 else 0
            }
        }
        item {
            val labels = listOf("Off", "15", "30", "45", "60", "End")
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Sleep timer", color = Color.White, style = MaterialTheme.typography.titleMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    labels.forEachIndexed { i, label ->
                        TvChoicePill(label, selected = sleepSel == i, enabled = true) {
                            sleepSel = i
                            when (i) {
                                0 -> Engine.cancelSleepTimer()
                                labels.lastIndex -> Engine.setSleepTimer(0, endOfTrack = true)
                                else -> Engine.setSleepTimer(label.toInt(), endOfTrack = false)
                            }
                        }
                    }
                }
                Text(
                    when (sleepSel) {
                        0 -> "Playback won't pause automatically"
                        labels.lastIndex -> "Pauses when the current track ends"
                        else -> "Pauses in ${labels[sleepSel]} minutes"
                    },
                    color = TvPalette.TextDim,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }

        // ---- Equalizer ----
        item { TvSectionTitle("Equalizer") }
        item {
            TvToggleRow("Enable", "Apply the 10-band EQ to playback", eqEnabled) {
                eqEnabled = it
                Engine.setEqEnabled(it)
            }
        }
        item {
            TvToggleRow("Mono audio", "Downmix stereo to a single channel", eqMono) {
                eqMono = it
                Engine.setEqMono(it)
            }
        }
        eqInit?.takeIf { it.presets.isNotEmpty() }?.let { eq ->
            item {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Preset", color = Color.White, style = MaterialTheme.typography.titleMedium)
                    eq.presets.chunked(5).forEach { row ->
                        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            row.forEach { name ->
                                TvChoicePill(eqPresetLabel(name), selected = eqPreset == name, enabled = true) {
                                    Engine.setEqPreset(name)
                                    // Re-read: the preset rewrites all bands.
                                    Engine.eqState()?.let { st ->
                                        eqPreset = st.preset
                                        eqGains.clear()
                                        eqGains.addAll(st.gainsDb)
                                    }
                                }
                            }
                        }
                    }
                    Text(
                        if (eqPreset == "custom") "Custom — bands were edited manually" else "Left/right on a band below adjusts it by 1 dB",
                        color = TvPalette.TextDim,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    eq.bands.forEachIndexed { i, hz ->
                        TvEqBandRow(
                            label = if (hz >= 1000) "${(hz / 1000).toInt()} kHz" else "${hz.toInt()} Hz",
                            gainDb = eqGains.getOrElse(i) { 0.0 },
                        ) { delta ->
                            val v = (eqGains.getOrElse(i) { 0.0 } + delta).coerceIn(-12.0, 12.0)
                            eqGains[i] = v
                            // The engine flips to "custom" on band edits; mirror it.
                            eqPreset = "custom"
                            Engine.setEqBand(i, v)
                        }
                    }
                }
            }
        }

        // ---- OpenDeezer Connect ----
        item { TvSectionTitle("OpenDeezer Connect") }
        item {
            TvToggleRow(
                "Make this device reachable",
                if (connectHost && connectAddr.isNotBlank()) "Reachable at $connectAddr" else "Let your other OpenDeezer apps control this TV",
                connectHost,
            ) {
                connectHost = it
                prefs.connectHostEnabled = it
                Engine.setConnectHostEnabled(it)
                connectAddr = Engine.connectHostInfo()?.addr.orEmpty()
            }
        }
        item {
            TvToggleRow(
                "Phone remote",
                "Serve a browser remote on your Wi-Fi (scan the QR)",
                phoneRemote,
            ) {
                phoneRemote = it
                prefs.phoneRemoteEnabled = it
                Engine.setWebRemoteEnabled(it)
            }
        }
        if (phoneRemote) {
            item {
                val info = remoteInfo
                if (info == null) {
                    CircularProgressIndicator(color = TvPalette.Purple)
                } else {
                    Row(horizontalArrangement = Arrangement.spacedBy(20.dp), verticalAlignment = Alignment.CenterVertically) {
                        val bmp = remember(remoteQr) {
                            remoteQr?.let { b -> BitmapFactory.decodeByteArray(b, 0, b.size)?.asImageBitmap() }
                        }
                        if (bmp != null) {
                            Box(Modifier.size(160.dp).clip(RoundedCornerShape(12.dp)).background(Color.White).padding(8.dp)) {
                                Image(bitmap = bmp, contentDescription = "Remote QR", modifier = Modifier.fillMaxSize())
                            }
                        }
                        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text("Scan on your phone (same Wi-Fi), then enter the code:", color = TvPalette.TextDim)
                            Text(info.code, color = Color.White, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.headlineSmall)
                            Text(info.url, color = TvPalette.TextDim, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
            }
        }
        item {
            TvActionRow(
                "Play on another device",
                when {
                    scanning -> "Searching your network…"
                    connected.isNotBlank() -> "Connected to $connected"
                    else -> "Playing here · open to pick a device"
                },
            ) { rescanDevices() }
        }
        if (scanning && devices == null) {
            item { Box(Modifier.padding(start = 12.dp)) { CircularProgressIndicator(color = TvPalette.Purple) } }
        }
        devices?.let { list ->
            item {
                Column(Modifier.padding(start = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    TvDeviceRow("This device", "OpenDeezer (Android TV)", connected.isBlank()) {
                        scope.launch { Engine.disconnectDevice(); connected = "" }
                    }
                    if (list.isEmpty()) {
                        Text("No other devices on your network.", color = TvPalette.TextDim, modifier = Modifier.padding(8.dp))
                    } else list.forEach { d ->
                        TvDeviceRow(d.name.ifBlank { d.addr }, listOfNotNull(d.typeLabel, d.version.ifBlank { null }?.let { "v$it" }).joinToString(" · "), connected == d.addr) {
                            scope.launch { if (Engine.connectDevice(d.addr)) connected = Engine.connectedDevice() }
                        }
                    }
                    TvDeviceRow(if (scanning) "Searching…" else "Rescan", "Look for devices again", selected = false) { rescanDevices() }
                }
            }
        }

        // ---- About ----
        item { TvSectionTitle("About") }
        item {
            TvActionRow("Check for updates", updateText.ifBlank { "Checks GitHub for a newer release" }) {
                updateText = "Checking…"
                scope.launch {
                    val info = Engine.checkUpdate()
                    updateText = when {
                        info == null -> "Couldn't check — try again"
                        info.hasUpdate -> "Update available: ${info.latest}"
                        else -> "Up to date (v${info.current})"
                    }
                }
            }
        }

        // ---- Account ----
        if (account != null) {
            item { TvSectionTitle("Account") }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(account.name, color = Color.White, style = MaterialTheme.typography.titleLarge)
                    Text(
                        "Plan: ${account.offer.ifBlank { "—" }}" +
                            (if (account.canHifi) " · HiFi" else if (account.canHq) " · HQ" else ""),
                        color = TvPalette.TextDim,
                    )
                }
            }
            item { TvPill("Sign out", onClick = onLogout) }
        }
        item { Spacer(Modifier.height(20.dp)) }
    }
}

@Composable
private fun TvSectionTitle(text: String) {
    Text(
        text.uppercase(),
        style = MaterialTheme.typography.titleSmall,
        fontWeight = FontWeight.Bold,
        color = TvPalette.Purple,
        modifier = Modifier.padding(top = 6.dp),
    )
}

@Composable
private fun TvToggleRow(
    title: String,
    subtitle: String,
    checked: Boolean,
    showToggle: Boolean = true,
    onToggle: (Boolean) -> Unit,
) {
    TvFocusRow(onClick = { onToggle(!checked) }) { focused ->
        Column(Modifier.weight(1f)) {
            Text(title, color = if (focused) Color.White else TvPalette.TextDim, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(subtitle, color = TvPalette.TextDim, style = MaterialTheme.typography.bodyMedium)
        }
        if (showToggle) {
            val on = checked
            Box(
                Modifier
                    .width(70.dp).height(34.dp)
                    .clip(RoundedCornerShape(17.dp))
                    .background(if (on) TvPalette.Purple else Color.White.copy(alpha = 0.15f)),
                contentAlignment = if (on) Alignment.CenterEnd else Alignment.CenterStart,
            ) {
                Text(
                    if (on) "ON" else "OFF",
                    color = if (on) Color.White else TvPalette.TextDim,
                    style = MaterialTheme.typography.labelMedium,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.padding(horizontal = 12.dp),
                )
            }
        }
    }
}

@Composable
private fun TvActionRow(title: String, subtitle: String, onClick: () -> Unit) {
    TvFocusRow(onClick = onClick) { focused ->
        Column(Modifier.weight(1f)) {
            Text(title, color = if (focused) Color.White else TvPalette.TextDim, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(subtitle, color = TvPalette.TextDim, style = MaterialTheme.typography.bodyMedium)
        }
    }
}

@Composable
private fun TvDeviceRow(title: String, subtitle: String, selected: Boolean, onClick: () -> Unit) {
    TvFocusRow(onClick = onClick) { focused ->
        Column(Modifier.weight(1f)) {
            Text(title, color = if (focused || selected) Color.White else TvPalette.TextDim, style = MaterialTheme.typography.titleMedium)
            if (subtitle.isNotBlank()) Text(subtitle, color = TvPalette.TextDim, style = MaterialTheme.typography.bodySmall)
        }
        if (selected) Text("✓", color = TvPalette.Purple, style = MaterialTheme.typography.titleLarge)
    }
}

// "bass-boost" -> "Bass Boost".
private fun eqPresetLabel(name: String): String =
    name.split('-').joinToString(" ") { part -> part.replaceFirstChar { it.uppercase() } }

/**
 * One EQ band: a focusable row whose D-pad LEFT/RIGHT adjusts the gain by
 * 1 dB (consumed, so focus stays put; up/down still moves between rows).
 */
@Composable
private fun TvEqBandRow(label: String, gainDb: Double, onAdjust: (Double) -> Unit) {
    var focused by remember { mutableStateOf(false) }
    Row(
        Modifier
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .onKeyEvent { ev ->
                if (ev.type != KeyEventType.KeyDown) return@onKeyEvent false
                when (ev.key) {
                    Key.DirectionLeft -> {
                        onAdjust(-1.0); true
                    }
                    Key.DirectionRight -> {
                        onAdjust(1.0); true
                    }
                    else -> false
                }
            }
            .clip(RoundedCornerShape(8.dp))
            .background(if (focused) TvPalette.Purple.copy(alpha = 0.16f) else Color.Transparent)
            .clickable(onClick = {}) // focus target only; left/right adjusts
            .padding(horizontal = 16.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        Text(
            label,
            color = if (focused) Color.White else TvPalette.TextDim,
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.width(72.dp),
        )
        Box(
            Modifier
                .weight(1f)
                .height(10.dp)
                .clip(RoundedCornerShape(5.dp))
                .background(Color.White.copy(alpha = 0.10f)),
        ) {
            val frac = ((gainDb + 12.0) / 24.0).toFloat().coerceIn(0f, 1f)
            Box(
                Modifier
                    .fillMaxWidth(frac)
                    .fillMaxHeight()
                    .clip(RoundedCornerShape(5.dp))
                    .background(if (focused) TvPalette.Purple else TvPalette.Purple.copy(alpha = 0.5f)),
            )
        }
        Text(
            "%+.0f dB".format(gainDb),
            color = if (focused) Color.White else TvPalette.TextDim,
            fontFamily = FontFamily.Monospace,
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.width(64.dp),
        )
    }
}

/** A focusable settings row: highlights its background on focus. */
@Composable
private fun TvFocusRow(
    onClick: () -> Unit,
    content: @Composable androidx.compose.foundation.layout.RowScope.(focused: Boolean) -> Unit,
) {
    var focused by remember { mutableStateOf(false) }
    Row(
        Modifier
            .fillMaxWidth()
            .onFocusChanged { focused = it.isFocused }
            .clip(RoundedCornerShape(12.dp))
            .background(if (focused) TvPalette.Purple.copy(alpha = 0.16f) else Color.Transparent)
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        content(focused)
    }
}

/** A selectable/greyable choice chip (audio-quality selector). */
@Composable
private fun TvChoicePill(label: String, selected: Boolean, enabled: Boolean, onClick: () -> Unit) {
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(if (focused) 1.06f else 1f, label = "choiceScale")
    val bg = when {
        !enabled -> TvPalette.CardIdle.copy(alpha = 0.4f)
        focused -> TvPalette.Purple
        selected -> TvPalette.Purple.copy(alpha = 0.5f)
        else -> TvPalette.CardIdle
    }
    val fg = when {
        !enabled -> TvPalette.TextDim.copy(alpha = 0.5f)
        focused || selected -> Color.White
        else -> TvPalette.TextDim
    }
    Box(
        Modifier
            .scale(scale)
            .onFocusChanged { focused = it.isFocused }
            .clip(RoundedCornerShape(24.dp))
            .background(bg)
            .border(
                BorderStroke(1.dp, if (focused) TvPalette.Purple else Color.White.copy(alpha = 0.12f)),
                RoundedCornerShape(24.dp),
            )
            .then(if (enabled) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = 22.dp, vertical = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, color = fg, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
    }
}
