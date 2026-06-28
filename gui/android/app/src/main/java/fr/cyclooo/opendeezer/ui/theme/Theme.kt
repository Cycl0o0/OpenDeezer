package fr.cyclooo.opendeezer.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

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

@Composable
fun OpenDeezerTheme(content: @Composable () -> Unit) {
    // OpenDeezer's identity is dark-first, so the app theme is always dark.
    MaterialTheme(
        colorScheme = DarkColors,
        typography = Typography(),
        content = content,
    )
}
