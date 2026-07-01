package fr.cyclooo.opendeezer.tv

import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
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

@Composable
private fun TvLoginScreen(busy: Boolean, error: String?, onArl: (String) -> Unit) {
    var arl by remember { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }

    Centered {
        Column(
            Modifier.width(560.dp).padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Text("OpenDeezer", style = MaterialTheme.typography.headlineMedium)
            Text(
                "Sign in with your Deezer ARL cookie. On a computer, copy it from " +
                    "your browser and enter it here, or use the phone/desktop app to log in.",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            OutlinedTextField(
                value = arl,
                onValueChange = { arl = it },
                label = { Text("ARL") },
                singleLine = true,
                enabled = !busy,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
            if (error != null) {
                Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            Button(
                onClick = { onArl(arl.trim()) },
                enabled = !busy && arl.isNotBlank(),
                modifier = Modifier.focusable(),
            ) {
                Text(if (busy) "Signing in…" else "Sign in")
            }
        }
    }
}
