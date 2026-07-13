package fr.cyclooo.opendeezer.ui.screens

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Remove
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
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.engine.Account
import fr.cyclooo.opendeezer.engine.ConnectHostInfo
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.UpdateInfo
import fr.cyclooo.opendeezer.engine.WebRemoteInfo
import fr.cyclooo.opendeezer.ui.theme.LocalMaterialYou
import fr.cyclooo.opendeezer.ui.theme.materialYouSupported
import kotlin.math.roundToInt
import kotlinx.coroutines.launch

// Stream-cache stepper bounds (MB): step in 128 MB increments up to 4 GB.
private const val MEDIA_CACHE_STEP = 128
private const val MEDIA_CACHE_MAX = 4096

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(account: Account?, onBack: () -> Unit, onEqualizer: () -> Unit, onLogout: () -> Unit) {
    var quality by remember { mutableIntStateOf(Engine.quality()) }
    var replayGain by remember { mutableStateOf(Engine.replayGain()) }
    var gapless by remember { mutableStateOf(Engine.gapless()) }
    var crossfadeSec by remember { mutableFloatStateOf((Engine.crossfadeMs() / 1000f)) }
    var mediaCacheMb by remember { mutableIntStateOf(Engine.mediaCacheMB()) }
    var sleepSelection by remember {
        mutableIntStateOf(
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

    // Free-only ad-reporting toggle. Seeded from the engine; only loaded/shown for
    // free accounts (premium has no ads). See the disclaimer rendered under it.
    var adsDisabled by remember { mutableStateOf(false) }
    LaunchedEffect(account?.premium) {
        if (account?.premium == false) adsDisabled = Engine.adsDisabled()
    }

    // Download folder: seed from the persisted choice, else the engine's default.
    var downloadFolder by remember { mutableStateOf(prefs.downloadFolder.orEmpty()) }
    LaunchedEffect(Unit) {
        if (downloadFolder.isBlank()) downloadFolder = Engine.downloadDir()
    }
    // Storage Access Framework tree picker: the correct way to reach user-visible
    // storage under scoped storage. We persist the grant and hand the Uri to the
    // engine (best-effort — see setDownloadDir).
    val folderPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.OpenDocumentTree(),
    ) { uri ->
        if (uri != null) {
            runCatching {
                context.contentResolver.takePersistableUriPermission(
                    uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION,
                )
            }
            val target = uri.toString()
            prefs.downloadFolder = target
            downloadFolder = target
            scope.launch {
                // The engine expects a filesystem path, so it may reject a SAF tree
                // Uri and keep its own default — reflect whatever it settles on.
                if (!Engine.setDownloadDir(target)) {
                    downloadFolder = Engine.downloadDir().ifBlank { target }
                }
            }
        }
    }

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

    val qualityLabels = listOf(stringResource(R.string.quality_normal), stringResource(R.string.quality_high), "HiFi")
    val sleepLabels = listOf(stringResource(R.string.common_off), "15", "30", "45", "60", stringResource(R.string.sleep_end))

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.settings_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            Text(stringResource(R.string.settings_audio_quality), style = MaterialTheme.typography.titleMedium)
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                qualityLabels.forEachIndexed { index, label ->
                    SegmentedButton(
                        selected = quality == index,
                        onClick = {
                            quality = index
                            Engine.setQuality(index)
                            prefs.audioQuality = index
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

            SettingSwitch("ReplayGain", stringResource(R.string.setting_replaygain_sub), replayGain) {
                replayGain = it
                Engine.setReplayGain(it)
                prefs.replayGain = if (it) 1 else 0
            }
            SettingSwitch(
                stringResource(R.string.setting_gapless_title),
                stringResource(R.string.setting_gapless_sub),
                gapless,
            ) {
                gapless = it
                Engine.setGapless(it)
                prefs.gapless = if (it) 1 else 0
            }

            HorizontalDivider()

            Column {
                Text(stringResource(R.string.settings_crossfade), style = MaterialTheme.typography.titleMedium)
                Text(
                    if (crossfadeSec <= 0f) stringResource(R.string.common_off)
                    else stringResource(R.string.crossfade_seconds, crossfadeSec.roundToInt()),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Slider(
                    value = crossfadeSec,
                    onValueChange = { crossfadeSec = it },
                    onValueChangeFinished = {
                        val ms = (crossfadeSec * 1000).toInt()
                        Engine.setCrossfadeMs(ms)
                        prefs.crossfadeMs = ms
                    },
                    valueRange = 0f..12f,
                    steps = 11,
                )
            }

            HorizontalDivider()

            // Raw-stream disk cache budget. Applied at the next launch (the engine
            // attaches the cache once at startup), so persist + note that.
            Column {
                Text(stringResource(R.string.settings_stream_cache), style = MaterialTheme.typography.titleMedium)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        if (mediaCacheMb <= 0) stringResource(R.string.common_off)
                        else stringResource(R.string.stream_cache_mb, mediaCacheMb),
                        style = MaterialTheme.typography.bodyLarge,
                        modifier = Modifier.weight(1f),
                    )
                    IconButton(
                        enabled = mediaCacheMb > 0,
                        onClick = {
                            val v = (mediaCacheMb - MEDIA_CACHE_STEP).coerceAtLeast(0)
                            mediaCacheMb = v
                            Engine.setMediaCacheMB(v)
                            prefs.mediaCacheMb = v
                        },
                    ) { Icon(Icons.Filled.Remove, contentDescription = stringResource(R.string.cd_decrease)) }
                    IconButton(
                        enabled = mediaCacheMb < MEDIA_CACHE_MAX,
                        onClick = {
                            val v = (mediaCacheMb + MEDIA_CACHE_STEP).coerceAtMost(MEDIA_CACHE_MAX)
                            mediaCacheMb = v
                            Engine.setMediaCacheMB(v)
                            prefs.mediaCacheMb = v
                        },
                    ) { Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.cd_increase)) }
                }
                Text(
                    stringResource(R.string.stream_cache_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            // Material You (dynamic color) — opt-in, Android 12+ only. Flips the app
            // theme live via the provided setter (also persisted in Prefs).
            if (materialYouSupported) {
                HorizontalDivider()
                val materialYou = LocalMaterialYou.current
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(stringResource(R.string.settings_material_you), style = MaterialTheme.typography.titleMedium)
                        Text(
                            stringResource(R.string.settings_material_you_hint),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    Switch(checked = materialYou.enabled, onCheckedChange = { materialYou.set(it) })
                }
            }

            HorizontalDivider()

            SettingAction(
                title = stringResource(R.string.equalizer_title),
                subtitle = stringResource(R.string.settings_eq_sub),
                onClick = onEqualizer,
            )

            HorizontalDivider()

            Text(stringResource(R.string.settings_downloads), style = MaterialTheme.typography.titleMedium)
            SettingAction(
                title = stringResource(R.string.download_folder_title),
                subtitle = downloadFolder.ifBlank { stringResource(R.string.download_folder_default) },
                onClick = { folderPicker.launch(null) },
            )

            if (account?.premium == false) {
                HorizontalDivider()
                Text(stringResource(R.string.settings_ads), style = MaterialTheme.typography.titleMedium)
                SettingSwitch(
                    stringResource(R.string.setting_disable_ads_title),
                    stringResource(R.string.setting_disable_ads_disclaimer),
                    adsDisabled,
                ) {
                    adsDisabled = it
                    scope.launch { Engine.setAdsDisabled(it) }
                }
            }

            HorizontalDivider()

            Text(stringResource(R.string.settings_sleep_timer), style = MaterialTheme.typography.titleMedium)
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                sleepLabels.forEachIndexed { index, label ->
                    SegmentedButton(
                        selected = sleepSelection == index,
                        onClick = {
                            sleepSelection = index
                            when (index) {
                                0 -> Engine.cancelSleepTimer()
                                sleepLabels.lastIndex -> Engine.setSleepTimer(0, endOfTrack = true)
                                else -> Engine.setSleepTimer(label.toInt(), endOfTrack = false)
                            }
                        },
                        shape = SegmentedButtonDefaults.itemShape(index, sleepLabels.size),
                    ) { Text(label) }
                }
            }
            Text(
                sleepDescription(sleepSelection, sleepLabels),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            HorizontalDivider()

            Text(stringResource(R.string.settings_connect), style = MaterialTheme.typography.titleMedium)
            SettingSwitch(
                stringResource(R.string.connect_reachable_title),
                stringResource(R.string.connect_reachable_sub),
                connectHostEnabled,
            ) {
                connectHostEnabled = it
                prefs.connectHostEnabled = it
                Engine.setConnectHostEnabled(it)
            }
            connectHostInfo?.takeIf { it.enabled && it.addr.isNotBlank() }?.let { info ->
                Text(
                    stringResource(R.string.reachable_at, info.addr) +
                        (info.name.takeIf { it.isNotBlank() }?.let { " · $it" } ?: ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            HorizontalDivider()

            Text(stringResource(R.string.settings_phone_remote), style = MaterialTheme.typography.titleMedium)
            SettingSwitch(
                stringResource(R.string.common_enable),
                stringResource(R.string.phone_remote_sub),
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
                            stringResource(R.string.phone_remote_scan),
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
                                contentDescription = stringResource(R.string.cd_qr_code),
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

            Text(stringResource(R.string.settings_about), style = MaterialTheme.typography.titleMedium)
            SettingAction(
                title = stringResource(R.string.update_check_title),
                subtitle = if (checkingUpdate) stringResource(R.string.update_checking) else stringResource(R.string.update_check_sub),
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
                Text(stringResource(R.string.settings_account), style = MaterialTheme.typography.titleMedium)
                Text(account.name, style = MaterialTheme.typography.bodyLarge)
                Text(
                    stringResource(R.string.plan_label, account.offer.ifBlank { "—" }) +
                        (if (account.canHifi) " · HiFi" else if (account.canHq) " · HQ" else ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                // Free accounts stream full tracks at 128 kbps — surface that, and
                // it also explains why the per-track Download action is disabled.
                if (!account.premium) {
                    Text(
                        stringResource(R.string.account_free_hint),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            OutlinedButton(onClick = onLogout, modifier = Modifier.fillMaxWidth()) {
                Text(stringResource(R.string.action_sign_out))
            }
            Spacer(Modifier.height(24.dp))
        }
    }

    when (val result = updateResult) {
        is UpdateCheckResult.UpToDate -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = { TextButton(onClick = { updateResult = null }) { Text(stringResource(R.string.action_ok)) } },
            title = { Text(stringResource(R.string.update_uptodate_title)) },
            text = {
                Text(
                    if (result.current.isBlank()) stringResource(R.string.update_latest_generic)
                    else stringResource(R.string.update_latest_named, result.current),
                )
            },
        )

        is UpdateCheckResult.Available -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = {
                TextButton(onClick = {
                    updateResult = null
                    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(result.info.url))) }
                }) { Text(stringResource(R.string.action_download)) }
            },
            dismissButton = { TextButton(onClick = { updateResult = null }) { Text(stringResource(R.string.action_close)) } },
            title = { Text(stringResource(R.string.update_available_title, result.info.latest)) },
            text = {
                Text(
                    result.info.notes.ifBlank { stringResource(R.string.update_available_generic) },
                    modifier = Modifier.verticalScroll(rememberScrollState()),
                )
            },
        )

        UpdateCheckResult.Failed -> AlertDialog(
            onDismissRequest = { updateResult = null },
            confirmButton = { TextButton(onClick = { updateResult = null }) { Text(stringResource(R.string.action_ok)) } },
            title = { Text(stringResource(R.string.update_failed_title)) },
            text = { Text(stringResource(R.string.update_failed_body)) },
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

@Composable
private fun qualityDescription(level: Int): String = when (level) {
    2 -> stringResource(R.string.quality_desc_flac)
    1 -> stringResource(R.string.quality_desc_high)
    else -> stringResource(R.string.quality_desc_normal)
}

@Composable
private fun sleepDescription(index: Int, labels: List<String>): String = when (index) {
    0 -> stringResource(R.string.sleep_desc_off)
    labels.lastIndex -> stringResource(R.string.sleep_desc_end)
    else -> {
        val minutes = labels[index].toIntOrNull() ?: 0
        pluralStringResource(R.plurals.pauses_in_minutes, minutes, minutes)
    }
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
