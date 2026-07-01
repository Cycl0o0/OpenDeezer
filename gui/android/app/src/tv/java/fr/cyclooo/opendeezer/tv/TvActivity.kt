package fr.cyclooo.opendeezer.tv

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.lifecycle.viewmodel.compose.viewModel
import fr.cyclooo.opendeezer.AppViewModel
import fr.cyclooo.opendeezer.ui.theme.OpenDeezerTheme

/**
 * Android TV entry point. Reuses the shared [AppViewModel] / engine / player and
 * renders a D-pad-driven, 10-foot Compose UI ([TvApp]) instead of the touch app.
 */
class TvActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            OpenDeezerTheme {
                val vm: AppViewModel = viewModel()
                TvApp(vm)
            }
        }
    }
}
