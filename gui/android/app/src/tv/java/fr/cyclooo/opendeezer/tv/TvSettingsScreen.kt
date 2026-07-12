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
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.ConnectDevice
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.WebRemoteInfo
import fr.cyclooo.opendeezer.ui.components.deviceTypeLabel
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
    var mediaCacheMb by remember { mutableStateOf(Engine.mediaCacheMB()) }
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
                stringResource(R.string.settings_title),
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Black,
                color = Color.White,
            )
        }

        // ---- Audio ----
        item { TvSectionTitle(stringResource(R.string.tv_section_audio)) }
        item {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.tv_quality), color = Color.White, style = MaterialTheme.typography.titleMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    listOf(stringResource(R.string.quality_normal), stringResource(R.string.quality_high), "HiFi").forEachIndexed { i, label ->
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
                        if (account.canHq) stringResource(R.string.tv_hifi_needs_plan) else stringResource(R.string.tv_high_hifi_needs_plan),
                        color = TvPalette.TextDim,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
            }
        }
        item {
            TvToggleRow("ReplayGain", stringResource(R.string.setting_replaygain_sub), replayGain) {
                replayGain = it; Engine.setReplayGain(it); prefs.replayGain = if (it) 1 else 0
            }
        }
        item {
            TvToggleRow(stringResource(R.string.setting_gapless_title), stringResource(R.string.setting_gapless_sub), gapless) {
                gapless = it; Engine.setGapless(it); prefs.gapless = if (it) 1 else 0
            }
        }
        item {
            // Raw-stream disk cache budget (MB); applied at the next launch.
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.settings_stream_cache), color = Color.White, style = MaterialTheme.typography.titleMedium)
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp), verticalAlignment = Alignment.CenterVertically) {
                    TvChoicePill("−128", selected = false, enabled = mediaCacheMb > 0) {
                        val v = (mediaCacheMb - 128).coerceAtLeast(0)
                        mediaCacheMb = v; Engine.setMediaCacheMB(v); prefs.mediaCacheMb = v
                    }
                    Text(
                        if (mediaCacheMb <= 0) stringResource(R.string.common_off) else stringResource(R.string.stream_cache_mb, mediaCacheMb),
                        color = Color.White,
                        style = MaterialTheme.typography.titleMedium,
                    )
                    TvChoicePill("+128", selected = false, enabled = mediaCacheMb < 4096) {
                        val v = (mediaCacheMb + 128).coerceAtMost(4096)
                        mediaCacheMb = v; Engine.setMediaCacheMB(v); prefs.mediaCacheMb = v
                    }
                }
                Text(stringResource(R.string.stream_cache_hint), color = TvPalette.TextDim, style = MaterialTheme.typography.bodySmall)
            }
        }
        item {
            val labels = listOf(stringResource(R.string.common_off), "15", "30", "45", "60", stringResource(R.string.sleep_end))
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(stringResource(R.string.settings_sleep_timer), color = Color.White, style = MaterialTheme.typography.titleMedium)
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
                        0 -> stringResource(R.string.sleep_desc_off)
                        labels.lastIndex -> stringResource(R.string.sleep_desc_end)
                        else -> {
                            val minutes = labels[sleepSel].toIntOrNull() ?: 0
                            pluralStringResource(R.plurals.pauses_in_minutes, minutes, minutes)
                        }
                    },
                    color = TvPalette.TextDim,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }

        // ---- Equalizer ----
        item { TvSectionTitle(stringResource(R.string.equalizer_title)) }
        item {
            TvToggleRow(stringResource(R.string.common_enable), stringResource(R.string.eq_enable_sub), eqEnabled) {
                eqEnabled = it
                Engine.setEqEnabled(it)
            }
        }
        item {
            TvToggleRow(stringResource(R.string.eq_mono_title), stringResource(R.string.eq_mono_sub), eqMono) {
                eqMono = it
                Engine.setEqMono(it)
            }
        }
        eqInit?.takeIf { it.presets.isNotEmpty() }?.let { eq ->
            item {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(R.string.eq_preset), color = Color.White, style = MaterialTheme.typography.titleMedium)
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
                        if (eqPreset == "custom") stringResource(R.string.eq_custom_edited) else stringResource(R.string.eq_adjust_hint),
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
        item { TvSectionTitle(stringResource(R.string.settings_connect)) }
        item {
            TvToggleRow(
                stringResource(R.string.connect_reachable_title),
                if (connectHost && connectAddr.isNotBlank()) stringResource(R.string.reachable_at, connectAddr) else stringResource(R.string.tv_connect_reachable_sub),
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
                stringResource(R.string.tv_phone_remote),
                stringResource(R.string.tv_phone_remote_sub),
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
                                Image(bitmap = bmp, contentDescription = stringResource(R.string.cd_remote_qr), modifier = Modifier.fillMaxSize())
                            }
                        }
                        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                            Text(stringResource(R.string.tv_remote_scan), color = TvPalette.TextDim)
                            Text(info.code, color = Color.White, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.headlineSmall)
                            Text(info.url, color = TvPalette.TextDim, style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
            }
        }
        item {
            TvActionRow(
                stringResource(R.string.tv_play_on_device),
                when {
                    scanning -> stringResource(R.string.tv_searching_network)
                    connected.isNotBlank() -> stringResource(R.string.connected_to, connected)
                    else -> stringResource(R.string.tv_playing_here)
                },
            ) { rescanDevices() }
        }
        if (scanning && devices == null) {
            item { Box(Modifier.padding(start = 12.dp)) { CircularProgressIndicator(color = TvPalette.Purple) } }
        }
        devices?.let { list ->
            item {
                Column(Modifier.padding(start = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    TvDeviceRow(stringResource(R.string.connect_this_device), stringResource(R.string.device_self_tv), connected.isBlank()) {
                        scope.launch { Engine.disconnectDevice(); connected = "" }
                    }
                    if (list.isEmpty()) {
                        Text(stringResource(R.string.tv_no_other_devices), color = TvPalette.TextDim, modifier = Modifier.padding(8.dp))
                    } else list.forEach { d ->
                        TvDeviceRow(d.name.ifBlank { d.addr }, listOfNotNull(deviceTypeLabel(d), d.version.ifBlank { null }?.let { "v$it" }).joinToString(" · "), connected == d.addr) {
                            scope.launch { if (Engine.connectDevice(d.addr)) connected = Engine.connectedDevice() }
                        }
                    }
                    TvDeviceRow(if (scanning) stringResource(R.string.tv_searching) else stringResource(R.string.tv_rescan), stringResource(R.string.tv_rescan_sub), selected = false) { rescanDevices() }
                }
            }
        }

        // ---- About ----
        item { TvSectionTitle(stringResource(R.string.settings_about)) }
        item {
            TvActionRow(stringResource(R.string.update_check_title), updateText.ifBlank { stringResource(R.string.update_check_sub) }) {
                updateText = context.getString(R.string.update_checking)
                scope.launch {
                    val info = Engine.checkUpdate()
                    updateText = when {
                        info == null -> context.getString(R.string.tv_update_failed)
                        info.hasUpdate -> context.getString(R.string.tv_update_available, info.latest)
                        else -> context.getString(R.string.tv_up_to_date, info.current)
                    }
                }
            }
        }

        // ---- Account ----
        if (account != null) {
            item { TvSectionTitle(stringResource(R.string.settings_account)) }
            item {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text(account.name, color = Color.White, style = MaterialTheme.typography.titleLarge)
                    Text(
                        stringResource(R.string.plan_label, account.offer.ifBlank { "—" }) +
                            (if (account.canHifi) " · HiFi" else if (account.canHq) " · HQ" else ""),
                        color = TvPalette.TextDim,
                    )
                }
            }
            item { TvPill(stringResource(R.string.action_sign_out), onClick = onLogout) }
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
                    if (on) stringResource(R.string.tv_toggle_on) else stringResource(R.string.tv_toggle_off),
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
