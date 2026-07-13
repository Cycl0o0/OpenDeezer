package fr.cyclooo.opendeezer

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.lifecycle.viewmodel.compose.viewModel
import fr.cyclooo.opendeezer.data.Prefs
import fr.cyclooo.opendeezer.ui.LocalFoldState
import fr.cyclooo.opendeezer.ui.rememberFoldState
import fr.cyclooo.opendeezer.ui.theme.LocalMaterialYou
import fr.cyclooo.opendeezer.ui.theme.MaterialYouState
import fr.cyclooo.opendeezer.ui.theme.OpenDeezerTheme

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Android 13+: the playback service's media notification needs this.
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            registerForActivityResult(ActivityResultContracts.RequestPermission()) {}
                .launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        setContent {
            val prefs = remember { Prefs(this@MainActivity) }
            // Opt-in Material You: read the saved choice, and let the Settings
            // switch flip it live (updates the pref + this state → theme recomposes).
            var dynamicColor by remember { mutableStateOf(prefs.materialYou) }
            OpenDeezerTheme(dynamicColor = dynamicColor) {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    val vm: AppViewModel = viewModel()
                    // Foldable posture (tabletop/book) drives the optional split
                    // layouts; flat devices keep the classic single-pane UI.
                    CompositionLocalProvider(
                        LocalFoldState provides rememberFoldState(this),
                        LocalMaterialYou provides MaterialYouState(enabled = dynamicColor) { v ->
                            prefs.materialYou = v
                            dynamicColor = v
                        },
                    ) {
                        OpenDeezerApp(vm)
                    }
                }
            }
        }
    }
}
