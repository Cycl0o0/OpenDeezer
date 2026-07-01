package fr.cyclooo.opendeezer.tv

import android.annotation.SuppressLint
import android.webkit.CookieManager
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import fr.cyclooo.opendeezer.ui.theme.DeezerPurple

private const val LOGIN_URL = "https://www.deezer.com/login"

/** Pulls the httpOnly `arl` out of the WebView cookie jar once Deezer sets it. */
private fun readArl(): String? {
    val cm = CookieManager.getInstance()
    val raw = cm.getCookie("https://www.deezer.com") ?: cm.getCookie(".deezer.com") ?: return null
    return raw.split(";")
        .map { it.trim() }
        .firstOrNull { it.startsWith("arl=") }
        ?.substringAfter("arl=")
        ?.takeIf { it.length > 20 }
}

/**
 * Android TV sign-in. Primary path is an embedded Deezer login page — you log in
 * with your real account and the `arl` session cookie is captured automatically,
 * no token to type on a remote. A "paste ARL" fallback stays for headless setups.
 */
@SuppressLint("SetJavaScriptEnabled")
@Composable
fun TvLoginScreen(busy: Boolean, error: String?, onArl: (String) -> Unit) {
    var manual by rememberSaveable { mutableStateOf(false) }

    Column(Modifier.fillMaxSize().padding(40.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("OpenDeezer", style = MaterialTheme.typography.headlineMedium, color = DeezerPurple)
            Text(
                if (manual) "Paste your Deezer ARL" else "Sign in to Deezer — the ARL is captured for you",
                style = MaterialTheme.typography.titleMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            if (busy) CircularProgressIndicator(color = DeezerPurple)
        }
        if (error != null) {
            Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
        }

        if (manual) {
            TvManualArl(busy = busy, onArl = onArl, onBack = { manual = false })
        } else {
            Box(Modifier.fillMaxWidth().weight(1f)) {
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { ctx ->
                        CookieManager.getInstance().setAcceptCookie(true)
                        WebView(ctx).apply {
                            CookieManager.getInstance().setAcceptThirdPartyCookies(this, true)
                            settings.javaScriptEnabled = true
                            settings.domStorageEnabled = true
                            isFocusable = true
                            isFocusableInTouchMode = true
                            webViewClient = object : WebViewClient() {
                                override fun onPageFinished(view: WebView?, url: String?) {
                                    super.onPageFinished(view, url)
                                    CookieManager.getInstance().flush()
                                    readArl()?.let { onArl(it) }
                                }
                            }
                            loadUrl(LOGIN_URL)
                            requestFocus()
                        }
                    },
                )
            }
            TvActionTile(
                label = "Paste ARL instead",
                onClick = { manual = true },
                modifier = Modifier.width(240.dp).height(56.dp),
            )
        }
    }
}

@Composable
private fun TvManualArl(busy: Boolean, onArl: (String) -> Unit, onBack: () -> Unit) {
    var arl by rememberSaveable { mutableStateOf("") }
    Column(
        Modifier.fillMaxWidth().fillMaxHeight(),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        OutlinedTextField(
            value = arl,
            onValueChange = { arl = it },
            label = { Text("ARL") },
            singleLine = true,
            enabled = !busy,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
            modifier = Modifier.width(720.dp),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(16.dp)) {
            TvActionTile(
                label = if (busy) "Signing in…" else "Log in",
                onClick = { if (arl.isNotBlank()) onArl(arl.trim()) },
                modifier = Modifier.width(200.dp).height(64.dp),
            )
            TvActionTile(
                label = "Use web sign-in",
                onClick = onBack,
                modifier = Modifier.width(220.dp).height(64.dp),
            )
        }
        Text(
            "Web sign-in reads the ARL automatically — only paste it here if you already have the token.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Start,
        )
    }
}
