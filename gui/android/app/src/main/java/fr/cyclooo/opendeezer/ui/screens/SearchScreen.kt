package fr.cyclooo.opendeezer.ui.screens

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import fr.cyclooo.opendeezer.R
import fr.cyclooo.opendeezer.engine.Album
import fr.cyclooo.opendeezer.engine.ArtistInfo
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.engine.Playlist
import fr.cyclooo.opendeezer.engine.SearchResults
import fr.cyclooo.opendeezer.player.PlayerController
import fr.cyclooo.opendeezer.ui.components.CenteredMessage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SearchScreen(
    player: PlayerController,
    onBack: () -> Unit,
    onAlbum: (Album) -> Unit,
    onArtist: (ArtistInfo) -> Unit,
    onPlaylist: (Playlist) -> Unit,
) {
    var query by rememberSaveable { mutableStateOf("") }
    var results by remember { mutableStateOf(SearchResults.EMPTY) }
    var loading by remember { mutableStateOf(false) }
    var completedQuery by remember { mutableStateOf("") }
    var searchFailed by remember { mutableStateOf(false) }
    var retryKey by remember { mutableStateOf(0) }
    val focusRequester = remember { FocusRequester() }
    val focusManager = LocalFocusManager.current
    val keyboardController = LocalSoftwareKeyboardController.current

    // Debounced search as the user types.
    LaunchedEffect(query, retryKey) {
        val q = query.trim()
        if (q.isBlank()) {
            results = SearchResults.EMPTY
            completedQuery = ""
            searchFailed = false
            loading = false
            return@LaunchedEffect
        }
        loading = true
        searchFailed = false
        delay(350)
        try {
            val searchResults = Engine.searchOrNull(q)
            searchFailed = searchResults == null
            results = searchResults ?: SearchResults.EMPTY
            completedQuery = q
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Throwable) {
            results = SearchResults.EMPTY
            completedQuery = q
            searchFailed = true
        }
        loading = false
    }

    LaunchedEffect(Unit) {
        focusRequester.requestFocus()
        keyboardController?.show()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.search_title)) },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = stringResource(R.string.action_back))
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text(stringResource(R.string.search_hint)) },
                leadingIcon = { Icon(Icons.Filled.Search, contentDescription = null) },
                trailingIcon = if (query.isNotEmpty()) {
                    {
                        IconButton(onClick = { query = "" }) {
                            Icon(
                                Icons.Filled.Clear,
                                contentDescription = stringResource(R.string.action_clear),
                            )
                        }
                    }
                } else null,
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 8.dp)
                    .focusRequester(focusRequester),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(
                    onSearch = {
                        focusManager.clearFocus()
                        keyboardController?.hide()
                    },
                ),
            )
            Box(Modifier.fillMaxSize()) {
                val trimmedQuery = query.trim()
                when {
                    loading || (trimmedQuery.isNotEmpty() && completedQuery != trimmedQuery) -> Box(
                        Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center,
                    ) {
                        CircularProgressIndicator()
                    }
                    query.isBlank() -> CenteredMessage(stringResource(R.string.search_empty_prompt))
                    searchFailed -> Column(
                        Modifier.fillMaxSize(),
                        verticalArrangement = Arrangement.Center,
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Text(
                            stringResource(R.string.tv_load_error),
                            modifier = Modifier.padding(horizontal = 32.dp),
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.Center,
                        )
                        TextButton(onClick = { retryKey++ }) {
                            Text(stringResource(R.string.action_retry))
                        }
                    }
                    results.isEmpty -> CenteredMessage(stringResource(R.string.search_no_results))
                    else -> SearchResultsList(
                        results = results,
                        player = player,
                        onAlbum = onAlbum,
                        onArtist = onArtist,
                        onPlaylist = onPlaylist,
                        modifier = Modifier.fillMaxSize(),
                    )
                }
            }
        }
    }
}
