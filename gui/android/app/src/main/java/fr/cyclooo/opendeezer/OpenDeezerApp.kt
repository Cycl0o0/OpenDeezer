package fr.cyclooo.opendeezer

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Scaffold
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.collectAsState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import fr.cyclooo.opendeezer.engine.Engine
import fr.cyclooo.opendeezer.ui.components.PlayerBar
import fr.cyclooo.opendeezer.ui.screens.ChartsScreen
import fr.cyclooo.opendeezer.ui.screens.ConnectDialog
import fr.cyclooo.opendeezer.ui.screens.EpisodesScreen
import fr.cyclooo.opendeezer.ui.screens.HomeScreen
import fr.cyclooo.opendeezer.ui.screens.LoginScreen
import fr.cyclooo.opendeezer.ui.screens.LyricsScreen
import fr.cyclooo.opendeezer.ui.screens.NowPlayingScreen
import fr.cyclooo.opendeezer.ui.screens.PlaylistsScreen
import fr.cyclooo.opendeezer.ui.screens.PodcastsScreen
import fr.cyclooo.opendeezer.ui.screens.PremiumGateScreen
import fr.cyclooo.opendeezer.ui.screens.QueueScreen
import fr.cyclooo.opendeezer.ui.screens.SearchScreen
import fr.cyclooo.opendeezer.ui.screens.SettingsScreen
import fr.cyclooo.opendeezer.ui.screens.TrackListScreen

@Composable
fun OpenDeezerApp(vm: AppViewModel) {
    when (vm.stage) {
        AuthStage.LOADING -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            CircularProgressIndicator()
        }

        AuthStage.NEEDS_LOGIN -> LoginScreen(
            busy = vm.busy,
            error = vm.loginError,
            onArl = { vm.login(it) },
        )

        AuthStage.NEEDS_PREMIUM -> PremiumGateScreen(
            accountName = vm.account?.name.orEmpty(),
            offer = vm.account?.offer.orEmpty(),
            onLogout = vm::logout,
        )

        AuthStage.READY -> MainScaffold(vm)
    }
}

@Composable
private fun MainScaffold(vm: AppViewModel) {
    val navController = rememberNavController()
    val player = vm.player
    val playerState by player.state.collectAsState()
    var showConnect by remember { mutableStateOf(false) }

    fun nav(route: String) = navController.navigate(route)
    val back: () -> Unit = { navController.popBackStack() }

    val currentRoute = navController.currentBackStackEntryAsState().value?.destination?.route
    val hideBarRoutes = setOf(Routes.NOW_PLAYING, Routes.LYRICS)

    Scaffold(
        // Inner screen Scaffolds manage system-bar insets; this outer one only
        // contributes the bottom player bar so the top inset isn't applied twice.
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        bottomBar = {
            if (playerState.current != null && currentRoute !in hideBarRoutes) {
                PlayerBar(
                    state = playerState,
                    onOpen = { nav(Routes.NOW_PLAYING) },
                    onToggle = player::togglePause,
                    onNext = player::next,
                    onPrev = player::prev,
                )
            }
        },
    ) { padding ->
        NavHost(
            navController = navController,
            startDestination = Routes.HOME,
            modifier = Modifier.fillMaxSize().padding(padding),
        ) {
            composable(Routes.HOME) {
                HomeScreen(
                    accountName = vm.account?.name.orEmpty(),
                    connected = playerState.connectedDevice.isNotBlank(),
                    onNavigate = { nav(it) },
                    onCast = { showConnect = true },
                    onSettings = { nav(Routes.SETTINGS) },
                )
            }
            composable(Routes.LIKED) {
                TrackListScreen("Liked Songs", player, back) { Engine.favorites() }
            }
            composable(Routes.FLOW) {
                TrackListScreen("Flow", player, back) { Engine.flow() }
            }
            composable(Routes.PLAYLISTS) {
                PlaylistsScreen(onBack = back, onOpen = { nav(Routes.playlist(it.id, it.name)) })
            }
            composable(Routes.PLAYLIST) { entry ->
                val id = entry.arguments?.getString("id").orEmpty()
                val name = entry.arguments?.getString("name").orEmpty()
                TrackListScreen(name, player, back) { Engine.playlistTracks(id) }
            }
            composable(Routes.ALBUM) { entry ->
                val id = entry.arguments?.getString("id").orEmpty()
                val name = entry.arguments?.getString("name").orEmpty()
                TrackListScreen(name, player, back) { Engine.albumTracks(id) }
            }
            composable(Routes.ARTIST) { entry ->
                val id = entry.arguments?.getString("id").orEmpty()
                val name = entry.arguments?.getString("name").orEmpty()
                TrackListScreen(name, player, back) { Engine.artistTop(id) }
            }
            composable(Routes.CHARTS) {
                ChartsScreen(
                    player = player,
                    onBack = back,
                    onAlbum = { nav(Routes.album(it.id, it.name)) },
                    onArtist = { nav(Routes.artist(it.id, it.name)) },
                    onPlaylist = { nav(Routes.playlist(it.id, it.name)) },
                )
            }
            composable(Routes.SEARCH) {
                SearchScreen(
                    player = player,
                    onBack = back,
                    onAlbum = { nav(Routes.album(it.id, it.name)) },
                    onArtist = { nav(Routes.artist(it.id, it.name)) },
                    onPlaylist = { nav(Routes.playlist(it.id, it.name)) },
                )
            }
            composable(Routes.PODCASTS) {
                PodcastsScreen(onBack = back, onOpen = { nav(Routes.podcast(it.id, it.name)) })
            }
            composable(Routes.PODCAST) { entry ->
                val id = entry.arguments?.getString("id").orEmpty()
                val name = entry.arguments?.getString("name").orEmpty()
                EpisodesScreen(podcastId = id, podcastName = name, player = player, onBack = back)
            }
            composable(Routes.NOW_PLAYING) {
                NowPlayingScreen(
                    state = playerState,
                    player = player,
                    onBack = back,
                    onLyrics = { nav(Routes.LYRICS) },
                    onQueue = { nav(Routes.QUEUE) },
                    onCast = { showConnect = true },
                )
            }
            composable(Routes.LYRICS) {
                LyricsScreen(player = player, onBack = back)
            }
            composable(Routes.QUEUE) {
                QueueScreen(player = player, onBack = back)
            }
            composable(Routes.SETTINGS) {
                SettingsScreen(account = vm.account, onBack = back, onLogout = vm::logout)
            }
        }
    }

    if (showConnect) {
        ConnectDialog(onDismiss = { showConnect = false })
    }
}
