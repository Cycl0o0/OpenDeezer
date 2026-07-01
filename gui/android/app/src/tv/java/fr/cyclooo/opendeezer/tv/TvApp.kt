package fr.cyclooo.opendeezer.tv

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.AppViewModel
import fr.cyclooo.opendeezer.AuthStage

/**
 * Android TV root. Switches on the shared [AppViewModel.stage] just like the
 * phone app, but renders 10-foot, D-pad-focusable screens.
 */
@Composable
fun TvApp(vm: AppViewModel) {
    Box(Modifier.fillMaxSize()) {
        when (vm.stage) {
            AuthStage.LOADING -> Centered { CircularProgressIndicator() }
            AuthStage.NEEDS_LOGIN -> TvLoginScreen(
                busy = vm.busy,
                error = vm.loginError,
                onArl = { vm.login(it) },
            )
            AuthStage.NEEDS_PREMIUM -> Centered {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text("Premium required", style = MaterialTheme.typography.headlineSmall)
                    Text(
                        "OpenDeezer streams need a Deezer Premium account.",
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Button(onClick = { vm.logout() }) { Text("Use another account") }
                }
            }
            AuthStage.READY -> TvRootScreen(vm)
        }
    }
}

@Composable
private fun Centered(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { content() }
}
