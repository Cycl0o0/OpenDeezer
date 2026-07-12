package fr.cyclooo.opendeezer.ui

import android.app.Activity
import android.graphics.Rect
import androidx.compose.foundation.layout.Box
import androidx.compose.runtime.Composable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.positionInWindow
import androidx.compose.ui.unit.Constraints
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.window.layout.FoldingFeature
import androidx.window.layout.WindowInfoTracker
import kotlin.math.roundToInt

/**
 * Posture of a foldable device, derived from Jetpack WindowManager's
 * [FoldingFeature]. The default value means "no half-opened fold" — flat
 * phones, fully-opened foldables and cover screens all get it, so every
 * consumer falls back to the classic single-pane rendering.
 */
data class FoldState(
    val isHalfOpened: Boolean = false,
    /** Half-opened around a horizontal hinge (laptop-like, e.g. Z Flip flex mode). */
    val isTableTop: Boolean = false,
    /** Half-opened around a vertical hinge (book-like, e.g. Z Fold inner screen). */
    val isBook: Boolean = false,
    /** The hinge rectangle in window coordinates while half-opened, else null. */
    val hingeBounds: Rect? = null,
)

/**
 * Provided by the mobile launcher activity only; anything else (TV) reads the
 * flat default and renders exactly as before.
 */
val LocalFoldState = compositionLocalOf { FoldState() }

/** Collects [WindowInfoTracker.windowLayoutInfo] lifecycle-aware into a [FoldState]. */
@Composable
fun rememberFoldState(activity: Activity): FoldState {
    val layoutInfo by remember(activity) {
        WindowInfoTracker.getOrCreate(activity).windowLayoutInfo(activity)
    }.collectAsStateWithLifecycle(initialValue = null)
    val fold = layoutInfo?.displayFeatures
        ?.filterIsInstance<FoldingFeature>()
        ?.firstOrNull()
    return if (fold?.state == FoldingFeature.State.HALF_OPENED) {
        FoldState(
            isHalfOpened = true,
            isTableTop = fold.orientation == FoldingFeature.Orientation.HORIZONTAL,
            isBook = fold.orientation == FoldingFeature.Orientation.VERTICAL,
            hingeBounds = fold.bounds,
        )
    } else {
        FoldState()
    }
}

/**
 * Lays out [first] and [second] on either side of the hinge described by
 * [hingeBounds] (window coordinates): above/below it when [horizontalHinge]
 * (tabletop), left/right of it otherwise (book). The hinge strip itself stays
 * empty so no content sits on the crease.
 */
@Composable
fun HingeSplit(
    hingeBounds: Rect,
    horizontalHinge: Boolean,
    modifier: Modifier = Modifier,
    first: @Composable () -> Unit,
    second: @Composable () -> Unit,
) {
    // Hinge bounds are in window coordinates; subtract our own window offset
    // (top app bar, insets, …) to convert them into local layout coordinates.
    var windowOffset by remember { mutableStateOf(Offset.Zero) }
    Layout(
        content = {
            Box { first() }
            Box { second() }
        },
        modifier = modifier.onGloballyPositioned { windowOffset = it.positionInWindow() },
    ) { measurables, constraints ->
        val width = constraints.maxWidth
        val height = constraints.maxHeight
        if (horizontalHinge) {
            val hingeTop = (hingeBounds.top - windowOffset.y.roundToInt()).coerceIn(0, height)
            val hingeBottom = (hingeBounds.bottom - windowOffset.y.roundToInt()).coerceIn(hingeTop, height)
            val top = measurables[0].measure(Constraints.fixed(width, hingeTop))
            val bottom = measurables[1].measure(Constraints.fixed(width, height - hingeBottom))
            layout(width, height) {
                top.place(0, 0)
                bottom.place(0, hingeBottom)
            }
        } else {
            val hingeLeft = (hingeBounds.left - windowOffset.x.roundToInt()).coerceIn(0, width)
            val hingeRight = (hingeBounds.right - windowOffset.x.roundToInt()).coerceIn(hingeLeft, width)
            val left = measurables[0].measure(Constraints.fixed(hingeLeft, height))
            val right = measurables[1].measure(Constraints.fixed(width - hingeRight, height))
            layout(width, height) {
                // Absolute placement: the panes must match the physical hinge
                // sides regardless of the RTL/LTR layout direction.
                left.place(0, 0)
                right.place(hingeRight, 0)
            }
        }
    }
}
