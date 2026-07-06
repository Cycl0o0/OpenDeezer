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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.AppViewModel
import fr.cyclooo.opendeezer.AuthStage
import fr.cyclooo.opendeezer.R

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
                onArl = { arl, auto -> vm.login(arl, auto = auto) },
            )
            AuthStage.NO_INTERNET -> Centered {
                Column(
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text(stringResource(R.string.no_internet_title), style = MaterialTheme.typography.headlineSmall)
                    Text(
                        stringResource(R.string.no_internet_body),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    // Keeps the saved ARL; re-runs the same launch sign-in path.
                    if (vm.busy) {
                        CircularProgressIndicator()
                    } else {
                        Button(onClick = { vm.retry() }) { Text(stringResource(R.string.action_retry)) }
                    }
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
