package fr.cyclooo.opendeezer.ui.theme

import android.os.Build
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

// Deezer brand purple.
val DeezerPurple = Color(0xFFA238FF)
private val PurpleDark = Color(0xFF8A1FE6)
private val Surface = Color(0xFF121216)
private val SurfaceVariant = Color(0xFF1E1E26)

private val DarkColors = darkColorScheme(
    primary = DeezerPurple,
    onPrimary = Color.White,
    secondary = DeezerPurple,
    background = Color(0xFF0B0B0F),
    onBackground = Color(0xFFEDEDF2),
    surface = Surface,
    onSurface = Color(0xFFEDEDF2),
    surfaceVariant = SurfaceVariant,
    onSurfaceVariant = Color(0xFFB6B6C2),
    primaryContainer = PurpleDark,
)

/** Whether Material You dynamic color is even possible on this device (API 31+). */
val materialYouSupported: Boolean get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.S

/**
 * Material You availability + current toggle, provided from the Activity so a
 * Settings switch can flip the theme live. [enabled] has no effect when
 * [materialYouSupported] is false.
 */
data class MaterialYouState(
    val enabled: Boolean = false,
    val set: (Boolean) -> Unit = {},
)

val LocalMaterialYou = staticCompositionLocalOf { MaterialYouState() }

@Composable
fun OpenDeezerTheme(dynamicColor: Boolean = false, content: @Composable () -> Unit) {
    val context = LocalContext.current
    // Dynamic color (Monet) needs Android 12+. Use the DARK dynamic scheme so the
    // app keeps its dark-first identity while adopting the user's system accent —
    // opt-in only, so the branded palette stays the default.
    val colorScheme = if (dynamicColor && materialYouSupported) {
        dynamicDarkColorScheme(context)
    } else {
        DarkColors
    }
    MaterialTheme(
        colorScheme = colorScheme,
        typography = Typography(),
        content = content,
    )
}
