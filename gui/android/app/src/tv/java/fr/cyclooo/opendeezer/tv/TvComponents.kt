package fr.cyclooo.opendeezer.tv

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
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
import androidx.compose.ui.draw.scale
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.ui.components.Artwork

/**
 * A focusable poster card for the TV browse rows. Grows and gains an accent
 * border while focused so it reads clearly from across a room, and fires
 * [onClick] on the D-pad centre button.
 */
@Composable
fun TvCard(
    title: String,
    subtitle: String,
    artworkUrl: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    size: Int = 150,
) {
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(if (focused) 1.12f else 1f, label = "cardScale")
    val accent = MaterialTheme.colorScheme.primary

    Column(
        modifier
            .width(size.dp)
            .scale(scale)
            .onFocusChanged { focused = it.isFocused }
            .clickable(onClick = onClick),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Artwork(
            artworkUrl,
            Modifier
                .size(size.dp)
                .then(
                    if (focused) Modifier.border(BorderStroke(3.dp, accent), RoundedCornerShape(8.dp))
                    else Modifier
                ),
        )
        Text(
            title,
            style = MaterialTheme.typography.bodyMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (subtitle.isNotBlank()) {
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** A titled horizontal shelf of cards; empty rows render nothing. */
@Composable
fun <T> TvRow(
    title: String,
    entries: List<T>,
    modifier: Modifier = Modifier,
    card: @Composable (T) -> Unit,
) {
    if (entries.isEmpty()) return
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text(
            title,
            style = MaterialTheme.typography.titleLarge,
            modifier = Modifier.padding(start = 4.dp),
        )
        LazyRow(
            horizontalArrangement = Arrangement.spacedBy(16.dp),
            contentPadding = PaddingValues(horizontal = 4.dp, vertical = 6.dp),
        ) {
            items(entries) { card(it) }
        }
    }
}

/**
 * A focusable action tile (Flow, Search, transport buttons…). The caller sizes
 * it via [modifier]; a default is used when none is given.
 */
@Composable
fun TvActionTile(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier.width(200.dp).height(110.dp),
) {
    var focused by remember { mutableStateOf(false) }
    val scale by animateFloatAsState(if (focused) 1.08f else 1f, label = "tileScale")
    val accent = MaterialTheme.colorScheme.primary

    Box(
        modifier
            .scale(scale)
            .onFocusChanged { focused = it.isFocused }
            .border(
                BorderStroke(if (focused) 3.dp else 1.dp, if (focused) accent else MaterialTheme.colorScheme.outline),
                RoundedCornerShape(12.dp),
            )
            .clickable(onClick = onClick)
            .padding(12.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            label,
            style = MaterialTheme.typography.titleMedium,
            textAlign = TextAlign.Center,
        )
    }
}
