package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.horizontalScroll
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.layout
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.engine.Engine
import kotlin.math.roundToInt
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.conflate
import kotlinx.coroutines.flow.filterNotNull

/**
 * 10-band graphic equalizer backed by the shared engine DSP. The engine owns
 * all state and persistence (a manual band edit flips the preset to "custom"
 * engine-side, saves are debounced there); this screen only renders controls
 * and forwards changes. State is re-read on every entry so edits from other
 * clients (remote, desktop) show up.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun EqualizerScreen(onBack: () -> Unit) {
    var eq by remember { mutableStateOf(Engine.eqState()) }
    // Local slider positions: updated live while dragging, re-seeded from the
    // engine on preset changes.
    val gains = remember { mutableStateListOf<Float>().apply { eq?.gainsDb?.forEach { add(it.toFloat()) } } }
    var preamp by remember { mutableFloatStateOf(eq?.preampDb?.toFloat() ?: 0f) }

    // Drags fire on every frame; conflate to ~30 engine calls/s. The engine
    // debounces persistence itself, so there is no save logic here.
    val pendingBand = remember { MutableStateFlow<Pair<Int, Float>?>(null) }
    LaunchedEffect(Unit) {
        pendingBand.filterNotNull().conflate().collect { (band, db) ->
            Engine.setEqBand(band, db.toDouble())
            delay(33)
        }
    }

    fun applyPreset(name: String) {
        Engine.setEqPreset(name)
        Engine.eqState()?.let { st ->
            eq = st
            gains.clear()
            st.gainsDb.forEach { gains.add(it.toFloat()) }
            preamp = st.preampDb.toFloat()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Equalizer") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
            )
        },
    ) { padding ->
        val st = eq
        if (st == null) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                Text("Equalizer unavailable", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            return@Scaffold
        }
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            SettingSwitch("Enable", "Apply the 10-band EQ to playback", st.enabled) {
                eq = st.copy(enabled = it)
                Engine.setEqEnabled(it)
            }

            HorizontalDivider()

            Text("Preset", style = MaterialTheme.typography.titleMedium)
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                if (st.preset == "custom") {
                    FilterChip(selected = true, onClick = {}, label = { Text("Custom") })
                }
                st.presets.forEach { name ->
                    FilterChip(
                        selected = st.preset == name,
                        onClick = { applyPreset(name) },
                        label = { Text(presetLabel(name)) },
                    )
                }
            }

            Row(Modifier.fillMaxWidth().height(280.dp)) {
                st.bands.forEachIndexed { i, hz ->
                    Column(
                        Modifier.weight(1f).fillMaxHeight(),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        val gain = gains.getOrElse(i) { 0f }
                        Text(
                            gainLabel(gain),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        VerticalSlider(
                            value = gain,
                            onValueChange = { v ->
                                // 0.5 dB snapping doubles as a soft 0-detent.
                                val snapped = (v * 2).roundToInt() / 2f
                                if (i < gains.size && gains[i] != snapped) {
                                    gains[i] = snapped
                                    pendingBand.value = i to snapped
                                    // The engine flips to "custom" on band
                                    // edits; mirror that immediately.
                                    if (st.preset != "custom") eq = st.copy(preset = "custom")
                                }
                            },
                            modifier = Modifier.weight(1f),
                        )
                        Text(
                            bandLabel(hz),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }

            Column {
                Text("Preamp", style = MaterialTheme.typography.titleMedium)
                Text(
                    "%+.1f dB".format(preamp),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Slider(
                    value = preamp,
                    onValueChange = { preamp = (it * 2).roundToInt() / 2f },
                    onValueChangeFinished = { Engine.setEqPreamp(preamp.toDouble()) },
                    valueRange = -12f..12f,
                )
            }

            HorizontalDivider()

            SettingSwitch("Mono audio", "Downmix stereo to a single channel", st.mono) {
                eq = st.copy(mono = it)
                Engine.setEqMono(it)
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

/**
 * Compose has no vertical Slider: rotate a horizontal one -90° and swap its
 * measurement axes so the parent's height budget becomes the slider's length.
 */
@Composable
private fun VerticalSlider(
    value: Float,
    onValueChange: (Float) -> Unit,
    modifier: Modifier = Modifier,
) {
    Slider(
        value = value,
        onValueChange = onValueChange,
        valueRange = -12f..12f,
        modifier = modifier
            .graphicsLayer { rotationZ = -90f }
            .layout { measurable, constraints ->
                val placeable = measurable.measure(
                    Constraints(
                        minWidth = constraints.minHeight,
                        maxWidth = constraints.maxHeight,
                        minHeight = constraints.minWidth,
                        maxHeight = constraints.maxWidth,
                    ),
                )
                layout(placeable.height, placeable.width) {
                    placeable.place(
                        x = -(placeable.width - placeable.height) / 2,
                        y = (placeable.width - placeable.height) / 2,
                    )
                }
            },
    )
}

// "bass-boost" -> "Bass Boost".
private fun presetLabel(name: String): String =
    name.split('-').joinToString(" ") { part -> part.replaceFirstChar { it.uppercase() } }

private fun bandLabel(hz: Double): String =
    if (hz >= 1000) "${(hz / 1000).toInt()}k" else "${hz.toInt()}"

private fun gainLabel(db: Float): String {
    val v = db.roundToInt()
    return if (v > 0) "+$v" else "$v"
}

// Same shape as SettingsScreen's row; duplicated because that one is file-private.
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
