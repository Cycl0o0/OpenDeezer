package fr.cyclooo.opendeezer.tv

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import fr.cyclooo.opendeezer.ui.components.Artwork

/** Shared 10-foot palette. Deezer purple over a deep, warm-black gradient. */
object TvPalette {
    val Purple = Color(0xFFA238FF)
    val PurpleDeep = Color(0xFF2A1840)
    val Ink = Color(0xFF0B0B10)
    val CardIdle = Color(0xFF1B1B24)
    val TextDim = Color(0xFFB6B0C2)

    val screen = Brush.verticalGradient(listOf(PurpleDeep, Ink))
}

/**
 * A focusable poster card for the browse shelves. Lifts, scales and gains a
 * purple ring while focused, and its label brightens — so the selection reads
 * from across a room.
 */
@Composable
fun TvCard(
    title: String,
    subtitle: String,
    artworkUrl: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    size: Int = 168,
) {
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(if (focused) 1.12f else 1f, label = "cardScale")
    val labelColor by animateColorAsState(
        if (focused) Color.White else TvPalette.TextDim, label = "cardLabel",
    )

    Column(
        modifier
            .width(size.dp)
            .scale(scale)
            .onFocusChanged { focused = it.isFocused }
            .clickable(onClick = onClick),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(
            Modifier
                .size(size.dp)
                .clip(RoundedCornerShape(14.dp))
                .then(
                    if (focused) Modifier.border(BorderStroke(3.dp, TvPalette.Purple), RoundedCornerShape(14.dp))
                    else Modifier
                ),
        ) {
            Artwork(artworkUrl, Modifier.fillMaxSize(), corner = 14)
        }
        Text(
            title,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.SemiBold,
            color = labelColor,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (subtitle.isNotBlank()) {
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = TvPalette.TextDim,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A titled horizontal shelf of cards; empty shelves render nothing. */
@Composable
fun <T> TvRow(
    title: String,
    entries: List<T>,
    modifier: Modifier = Modifier,
    card: @Composable (T) -> Unit,
) {
    if (entries.isEmpty()) return
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(
            title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = Color.White,
            modifier = Modifier.padding(start = 4.dp),
        )
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(18.dp),
            contentPadding = PaddingValues(horizontal = 4.dp, vertical = 8.dp),
        ) {
            items(entries) { card(it) }
        }
    }
}

/**
 * A focusable pill button (Play, Search, transport…). Fills with Deezer purple
 * and scales while focused; an idle pill is a soft translucent chip. Pass a
 * [focusRequester] to grab initial focus.
 */
@Composable
fun TvPill(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    focusRequester: FocusRequester? = null,
) {
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(if (focused) 1.06f else 1f, label = "pillScale")
    val bg by animateColorAsState(if (focused) TvPalette.Purple else TvPalette.CardIdle, label = "pillBg")
    val fg = if (focused) Color.White else TvPalette.TextDim

    Box(
        modifier
            .then(if (focusRequester != null) Modifier.focusRequester(focusRequester) else Modifier)
            .scale(scale)
            .onFocusChanged { focused = it.isFocused }
            .clip(RoundedCornerShape(28.dp))
            .background(bg)
            .border(
                BorderStroke(1.dp, if (focused) TvPalette.Purple else Color.White.copy(alpha = 0.12f)),
                RoundedCornerShape(28.dp),
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 22.dp, vertical = 12.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, color = fg, fontWeight = FontWeight.SemiBold, fontSize = 16.sp)
    }
}

/**
 * The featured hero at the top of the browse screen: big art, a title, and a
 * primary "Play" pill (which takes initial focus) plus a "Search" pill.
 */
@Composable
fun TvHero(
    title: String,
    subtitle: String,
    artworkUrl: String,
    onPlay: () -> Unit,
    onSearch: () -> Unit,
    playFocus: FocusRequester,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier.fillMaxWidth().height(300.dp),
        horizontalArrangement = Arrangement.spacedBy(28.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier
                .size(300.dp)
                .clip(RoundedCornerShape(20.dp))
                .border(BorderStroke(1.dp, Color.White.copy(alpha = 0.1f)), RoundedCornerShape(20.dp)),
        ) {
            Artwork(artworkUrl, Modifier.fillMaxSize(), corner = 20)
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Text(
                "FEATURED",
                style = MaterialTheme.typography.labelLarge,
                color = TvPalette.Purple,
                fontWeight = FontWeight.Bold,
            )
            Text(
                title,
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                color = Color.White,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (subtitle.isNotBlank()) {
                Text(
                    subtitle,
                    style = MaterialTheme.typography.titleMedium,
                    color = TvPalette.TextDim,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.height(6.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                TvPill("▶  Play", onPlay, focusRequester = playFocus)
                TvPill("Search", onSearch)
            }
        }
    }
}
