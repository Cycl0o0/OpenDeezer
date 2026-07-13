// OpenDeezer - native Windows front-end (WinUI 3, C# / .NET 8, Fluent).
//
// The whole engine (login, browse, Blowfish decrypt, MP3/FLAC decode, WASAPI
// playback) is the Go core compiled to a C-ABI shared library
// (lib/libdeezercore.dll) and called in-process via P/Invoke (see DeezerCore.cs).
// This file is UI only: a code-built NavigationView + track ListView + playlist /
// search grids + Charts / Podcasts / Artist / Lyrics pages + a bottom now-playing
// transport bar + About / Settings / login dialogs. It is a 1:1 port of the
// previous C++/WinRT main.cpp; App.xaml (compiled by the XAML markup compiler)
// supplies XamlControlsResources so the Fluent theme actually resolves.
//
// Threading: every blocking DZ* call (DZInit / browse / DZPlay / DZFetch) runs on
// the thread pool via `await Task.Run(...)`; because these handlers start on the
// UI thread (which carries the DispatcherQueueSynchronizationContext), the code
// after each await resumes back on the UI thread automatically. A single 300 ms
// DispatcherQueueTimer polls cheap player state and auto-advances when
// DZFinishedCount() increments.
//
// Login: on startup a saved/env ARL is tried silently; otherwise a chooser offers
// "Log in with Deezer" -- a WebView2 pointed at the Deezer web login whose
// CoreWebView2 cookie store is polled until the HttpOnly "arl" cookie appears,
// then captured and persisted to %APPDATA%\opendeezer\arl.txt -- with manual ARL
// entry kept as a fallback.
//
// OS integration: SystemMediaTransportControls (media overlay / media keys, via
// ISystemMediaTransportControlsInterop::GetForWindow), a Settings dialog persisted
// to %APPDATA%\opendeezer\settings.json, a tray icon (Shell_NotifyIcon) with
// close-to-tray background playback, and OpenDeezer Connect (LAN device transfer).

using System;
using System.Collections.Generic;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Text;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;
using Windows.Foundation;
using Windows.Graphics;
using Windows.Media;
using Windows.Storage.Streams;
using Windows.System;
using Windows.UI;
// Both Microsoft.UI.Dispatching and Windows.System expose DispatcherQueueTimer;
// WinUI 3 uses the Microsoft.UI one.
using DispatcherQueueTimer = Microsoft.UI.Dispatching.DispatcherQueueTimer;

namespace OpenDeezer;

public sealed partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        _settings = Config.LoadSettings();
        Loc.SetLanguage(_settings.Language); // choose the UI language BEFORE any UI is built
        _accent = new SolidColorBrush(Color.FromArgb(0xFF, 0xA2, 0x38, 0xFF)); // Deezer Electric Violet
        BuildUi();
        try { AppWindow.Resize(new SizeInt32(1180, 760)); } catch { }
        // The poll timer + login both need the live DispatcherQueue / XamlRoot, and
        // the HWND (SMTC + tray) only exists once the window is up -- so do that work
        // on first activation, mirroring the C++ Activate().
        Activated += OnFirstActivated;
    }

    private bool _initDone;
    private void OnFirstActivated(object sender, WindowActivatedEventArgs e)
    {
        if (_initDone) return;
        _initDone = true;
        Activated -= OnFirstActivated;
        _appHwnd = WinRT.Interop.WindowNative.GetWindowHandle(this);
        _timer = DispatcherQueue.CreateTimer();
        _timer.Interval = TimeSpan.FromMilliseconds(300);
        _timer.Tick += OnTick;
        SetupSmtc();
        SetupTray();
        AppWindow.Closing += OnClosing;
        StartLogin();
        StartBackgroundUpdateCheck(); // fire-and-forget: never blocks startup
    }

    // ---- grid helpers --------------------------------------------------------
    private static ColumnDefinition ColAuto() => new() { Width = new GridLength(0, GridUnitType.Auto) };
    private static ColumnDefinition ColStar(double w = 1) => new() { Width = new GridLength(w, GridUnitType.Star) };
    private static RowDefinition RowAuto() => new() { Height = new GridLength(0, GridUnitType.Auto) };
    private static RowDefinition RowStar(double w = 1) => new() { Height = new GridLength(w, GridUnitType.Star) };

    // ---- UI construction -----------------------------------------------------
    private void BuildUi()
    {
        // RootGrid is the window content (from MainWindow.xaml): row0 the (normally
        // collapsed) update banner, row1 content, row2 the transport bar.
        RootGrid.RowDefinitions.Add(RowAuto());
        RootGrid.RowDefinitions.Add(RowStar());
        RootGrid.RowDefinitions.Add(RowAuto());

        // Row 0 hosts the (collapsed) update banner and a transient offline-download
        // status InfoBar, stacked so either can show without reserving space.
        var topBars = new StackPanel { Spacing = 0 };
        topBars.Children.Add(BuildUpdateBar());
        _offlineInfoBar = new InfoBar { IsOpen = false, IsClosable = true };
        topBars.Children.Add(_offlineInfoBar);
        Grid.SetRow(topBars, 0);
        RootGrid.Children.Add(topBars);

        BuildNav();
        BuildPages();
        Grid.SetRow(_nav, 1);
        RootGrid.Children.Add(_nav);

        var bar = BuildTransport();
        Grid.SetRow(bar, 2);
        RootGrid.Children.Add(bar);

        // Ctrl+F follows the Windows Find convention from anywhere in the app:
        // reveal Search in the navigation view and put the caret in its field.
        var find = new KeyboardAccelerator
        {
            Key = VirtualKey.F,
            Modifiers = VirtualKeyModifiers.Control,
        };
        find.Invoked += (_, args) =>
        {
            if (!_loggedIn) return;
            _nav.SelectedItem = _searchItem;
            _searchBox.Focus(FocusState.Keyboard);
            args.Handled = true;
        };
        RootGrid.KeyboardAccelerators.Add(find);

        // Arabic (and any RTL UI language): mirror the whole visual tree. ContentDialogs
        // are mirrored separately in ShowDialog(); the Connect flyout in BuildTransport.
        if (Loc.IsRtl) RootGrid.FlowDirection = FlowDirection.RightToLeft;

        _nav.Content = _homePage; // show the (empty) Home page until login fills it
        _nav.Header = Loc.S("Nav_Home");
    }

    // Small dismissible "a newer version is available" banner above the nav.
    // IsOpen starts false, which collapses the Auto row to zero height, so it
    // never reserves space (and never blocks startup) unless an update is found.
    private InfoBar BuildUpdateBar()
    {
        var downloadBtn = new Button { Content = Loc.S("Btn_Download") };
        downloadBtn.Click += async (_, _) =>
        {
            if (string.IsNullOrEmpty(_updateUrl)) return;
            try { await Launcher.LaunchUriAsync(new Uri(_updateUrl)); } catch { }
        };
        _updateBar = new InfoBar
        {
            Severity = InfoBarSeverity.Informational,
            IsOpen = false,
            IsClosable = true,
            ActionButton = downloadBtn,
        };
        return _updateBar;
    }

    // Best-effort, silent, off-thread GitHub release check. Never blocks startup
    // and never surfaces a network/parse failure to the user.
    private async void StartBackgroundUpdateCheck()
    {
        UpdateInfo info;
        try { info = await Task.Run(() => DeezerCore.CheckUpdate()); }
        catch { return; }
        if (info.HasUpdate) ShowUpdateNotice(info);
    }

    private void ShowUpdateNotice(UpdateInfo info)
    {
        _updateUrl = info.Url;
        _updateBar.Title = Loc.Format("Update_TitleFormat", info.Latest);
        _updateBar.Message = string.IsNullOrEmpty(info.Notes)
            ? Loc.S("Update_BodyDefault")
            : (info.Notes.Length > 240 ? info.Notes[..240] + "…" : info.Notes);
        _updateBar.IsOpen = true;
    }

    private NavigationViewItem NavItem(string text, Symbol sym, string tag) =>
        new() { Content = text, Icon = new SymbolIcon(sym), Tag = tag };

    private void BuildNav()
    {
        _nav = new NavigationView
        {
            PaneDisplayMode = NavigationViewPaneDisplayMode.Left,
            IsBackButtonVisible = NavigationViewBackButtonVisible.Collapsed,
            IsSettingsVisible = false,
            PaneTitle = "OpenDeezer",
        };
        _homeItem = NavItem(Loc.S("Nav_Home"), Symbol.Home, "home");
        _likedItem = NavItem(Loc.S("Nav_LikedSongs"), Symbol.Audio, "liked");
        _flowItem = NavItem(Loc.S("Nav_Flow"), Symbol.Play, "flow");
        _playlistsItem = NavItem(Loc.S("Nav_Playlists"), Symbol.List, "playlists");
        _chartsItem = NavItem(Loc.S("Nav_Charts"), Symbol.World, "charts");
        _podcastsItem = NavItem(Loc.S("Nav_Podcasts"), Symbol.Microphone, "podcasts");
        // Recently played + listening stats (machine-local history). Symbol has no
        // "history" member, so use the Segoe MDL2 History glyph directly.
        _recentItem = new NavigationViewItem { Content = Loc.S("Nav_Recent"), Icon = new FontIcon { Glyph = "" }, Tag = "recent" };
        _searchItem = NavItem(Loc.S("Nav_Search"), Symbol.Find, "search");
        _nav.MenuItems.Add(_homeItem);
        _nav.MenuItems.Add(_likedItem);
        _nav.MenuItems.Add(_flowItem);
        _nav.MenuItems.Add(_playlistsItem);
        _nav.MenuItems.Add(_chartsItem);
        _nav.MenuItems.Add(_podcastsItem);
        _nav.MenuItems.Add(_recentItem);
        _nav.MenuItems.Add(_searchItem);

        // Account: re-open the login chooser to re-auth / switch accounts; handled
        // like Settings/About in OnNav (a modal action, not a page).
        _accountItem = NavItem(Loc.S("Nav_Account"), Symbol.Contact, "account");
        _settingsItem = NavItem(Loc.S("Nav_Settings"), Symbol.Setting, "settings");
        _phoneRemoteItem = NavItem(Loc.S("Nav_PhoneRemote"), Symbol.Phone, "phoneremote");
        _aboutItem = NavItem(Loc.S("Nav_About"), Symbol.Help, "about");
        _nav.FooterMenuItems.Add(_accountItem);
        _nav.FooterMenuItems.Add(_settingsItem);
        _nav.FooterMenuItems.Add(_phoneRemoteItem);
        _nav.FooterMenuItems.Add(_aboutItem);

        _nav.SelectionChanged += OnNav;
    }

    private void BuildPages()
    {
        // Liked / playlist-detail track list (reused for liked/flow/album/playlist/
        // podcast/radio). Wrapped in a grid with an optional context action bar that
        // reveals "Download album/playlist" only on an album or playlist view.
        _trackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _trackList.ItemClick += OnTrackClick;
        {
            var tg = new Grid { RowSpacing = 8, Padding = new Thickness(4) };
            tg.RowDefinitions.Add(RowAuto());
            tg.RowDefinitions.Add(RowStar());
            _tracksActionBar = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Visibility = Visibility.Collapsed };
            _tracksDownloadBtn = new Button();
            var dc = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
            dc.Children.Add(new FontIcon { Glyph = "", FontSize = 14 }); // Download
            _tracksDownloadLabel = new TextBlock { VerticalAlignment = VerticalAlignment.Center };
            dc.Children.Add(_tracksDownloadLabel);
            _tracksDownloadBtn.Content = dc;
            _tracksDownloadBtn.Click += OnDownloadCollection;
            _tracksActionBar.Children.Add(_tracksDownloadBtn);
            Grid.SetRow(_tracksActionBar, 0); tg.Children.Add(_tracksActionBar);
            Grid.SetRow(_trackList, 1); tg.Children.Add(_trackList);
            _tracksPage = tg;
        }

        // Playlists page: a "New Playlist" toolbar over the grid. Rename / delete
        // live on each tile's right-click context menu (built in FillPlaylistGrid).
        _playlistGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _playlistGrid.ItemClick += OnPlaylistClick;
        {
            var pg = new Grid { RowSpacing = 8, Padding = new Thickness(4) };
            pg.RowDefinitions.Add(RowAuto());
            pg.RowDefinitions.Add(RowStar());
            var bar = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
            var newBtn = new Button();
            var c = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
            c.Children.Add(new FontIcon { Glyph = "\uE710" }); // Add
            c.Children.Add(new TextBlock { Text = Loc.S("Btn_NewPlaylist") });
            newBtn.Content = c;
            newBtn.Click += OnNewPlaylist;
            bar.Children.Add(newBtn);
            Grid.SetRow(bar, 0); pg.Children.Add(bar);
            Grid.SetRow(_playlistGrid, 1); pg.Children.Add(_playlistGrid);
            _playlistsPage = pg;
        }

        // Search page: query row + track list + album/playlist grid
        var sp = new Grid { Padding = new Thickness(4), RowSpacing = 8 };
        sp.RowDefinitions.Add(RowAuto());
        sp.RowDefinitions.Add(RowAuto());
        sp.RowDefinitions.Add(RowStar(2));
        sp.RowDefinitions.Add(RowAuto());
        sp.RowDefinitions.Add(RowStar(3));

        var queryRow = new Grid { ColumnSpacing = 8 };
        queryRow.ColumnDefinitions.Add(ColStar());
        queryRow.ColumnDefinitions.Add(ColAuto());
        _searchBox = new TextBox { PlaceholderText = Loc.S("Search_Placeholder") };
        _searchBox.KeyDown += OnSearchKey;
        Grid.SetColumn(_searchBox, 0);
        var searchBtn = new Button { Content = Loc.S("Nav_Search") };
        searchBtn.Click += (_, _) => RunSearch();
        Grid.SetColumn(searchBtn, 1);
        queryRow.Children.Add(_searchBox);
        queryRow.Children.Add(searchBtn);
        Grid.SetRow(queryRow, 0); sp.Children.Add(queryRow);

        var h1 = new TextBlock { Text = Loc.S("Search_TracksHeader"), FontWeight = FontWeights.SemiBold };
        Grid.SetRow(h1, 1); sp.Children.Add(h1);

        _searchTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _searchTrackList.ItemClick += OnSearchTrackClick;
        Grid.SetRow(_searchTrackList, 2); sp.Children.Add(_searchTrackList);

        var h2 = new TextBlock { Text = Loc.S("Search_AlbumsPlaylistsHeader"), FontWeight = FontWeights.SemiBold };
        Grid.SetRow(h2, 3); sp.Children.Add(h2);

        _searchGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _searchGrid.ItemClick += OnSearchGridClick;
        Grid.SetRow(_searchGrid, 4); sp.Children.Add(_searchGrid);

        _searchPage = sp;

        BuildHomePage();
        BuildArtistPage();
        BuildLyricsPage();
        BuildChartsPage();
        BuildPodcastPage();
        BuildRecentPage();
    }

    // Recently played + listening stats: a scrolling column of the machine-local
    // history (recent plays + top tracks/artists over 30 days + total time). Inner
    // lists don't scroll; the outer ScrollViewer does (like Charts).
    private void BuildRecentPage()
    {
        _recentScroll = new ScrollViewer
        {
            Padding = new Thickness(16, 12, 16, 16),
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        var col = new StackPanel { Spacing = 8 };

        _recentTotalText = new TextBlock { FontSize = 14, Opacity = 0.8, TextWrapping = TextWrapping.Wrap };
        col.Children.Add(_recentTotalText);

        col.Children.Add(Section(Loc.S("Section_RecentlyPlayed")));
        _recentList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _recentList.ItemClick += OnRecentTrackClick;
        NoInnerScroll(_recentList);
        col.Children.Add(_recentList);

        col.Children.Add(Section(Loc.S("Section_TopTracks")));
        _recentTopTracksList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _recentTopTracksList.ItemClick += OnRecentTopTrackClick;
        NoInnerScroll(_recentTopTracksList);
        col.Children.Add(_recentTopTracksList);

        col.Children.Add(Section(Loc.S("Section_TopArtists")));
        _recentTopArtistsList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = false };
        NoInnerScroll(_recentTopArtistsList);
        col.Children.Add(_recentTopArtistsList);

        _recentEmpty = new TextBlock { Text = Loc.S("Recent_Empty"), Opacity = 0.7, TextWrapping = TextWrapping.Wrap, Visibility = Visibility.Collapsed };
        col.Children.Add(_recentEmpty);

        _recentScroll.Content = col;
        _recentPage = _recentScroll;
    }

    private static TextBlock Section(string text) =>
        new() { Text = text, FontWeight = FontWeights.SemiBold, FontSize = 18, Margin = new Thickness(0, 12, 0, 2) };

    private static void NoInnerScroll(DependencyObject el)
    {
        ScrollViewer.SetVerticalScrollMode(el, ScrollMode.Disabled);
        ScrollViewer.SetVerticalScrollBarVisibility(el, ScrollBarVisibility.Disabled);
    }

    // Charts: a scrolling column of Top Tracks + Albums + Artists + Playlists
    // (inner lists don't scroll; the outer ScrollViewer does).
    private void BuildChartsPage()
    {
        _chartsScroll = new ScrollViewer
        {
            Padding = new Thickness(16, 12, 16, 16),
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        var col = new StackPanel { Spacing = 8 };

        col.Children.Add(Section(Loc.S("Section_TopTracks")));
        _chartsTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsTrackList.ItemClick += OnChartsTrackClick;
        NoInnerScroll(_chartsTrackList);
        col.Children.Add(_chartsTrackList);

        col.Children.Add(Section(Loc.S("Section_Albums")));
        _chartsAlbumsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsAlbumsGrid.ItemClick += OnChartsAlbumClick;
        NoInnerScroll(_chartsAlbumsGrid);
        col.Children.Add(_chartsAlbumsGrid);

        col.Children.Add(Section(Loc.S("Section_Artists")));
        _chartsArtistsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsArtistsGrid.ItemClick += OnChartsArtistClick;
        NoInnerScroll(_chartsArtistsGrid);
        col.Children.Add(_chartsArtistsGrid);

        col.Children.Add(Section(Loc.S("Section_Playlists")));
        _chartsPlaylistsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsPlaylistsGrid.ItemClick += OnChartsPlaylistClick;
        NoInnerScroll(_chartsPlaylistsGrid);
        col.Children.Add(_chartsPlaylistsGrid);

        _chartsScroll.Content = col;
        _chartsPage = _chartsScroll;
    }

    // Podcasts: a search row + a grid of shows. Clicking a show loads its episodes
    // into the shared track list (as IsEpisode tracks) so playback reuses the queue.
    private void BuildPodcastPage()
    {
        var pp = new Grid { Padding = new Thickness(4), RowSpacing = 8 };
        pp.RowDefinitions.Add(RowAuto());
        pp.RowDefinitions.Add(RowAuto());
        pp.RowDefinitions.Add(RowStar());

        var queryRow = new Grid { ColumnSpacing = 8 };
        queryRow.ColumnDefinitions.Add(ColStar());
        queryRow.ColumnDefinitions.Add(ColAuto());
        _podcastBox = new TextBox { PlaceholderText = Loc.S("Podcast_SearchPlaceholder") };
        _podcastBox.KeyDown += OnPodcastKey;
        Grid.SetColumn(_podcastBox, 0);
        var pbtn = new Button { Content = Loc.S("Nav_Search") };
        pbtn.Click += (_, _) => RunPodcastSearch();
        Grid.SetColumn(pbtn, 1);
        queryRow.Children.Add(_podcastBox);
        queryRow.Children.Add(pbtn);
        Grid.SetRow(queryRow, 0); pp.Children.Add(queryRow);

        var h = new TextBlock { Text = Loc.S("Podcast_ShowsHeader"), FontWeight = FontWeights.SemiBold };
        Grid.SetRow(h, 1); pp.Children.Add(h);

        _podcastGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _podcastGrid.ItemClick += OnPodcastClick;
        Grid.SetRow(_podcastGrid, 2); pp.Children.Add(_podcastGrid);

        _podcastPage = pp;
    }

    // Home: time-based greeting + quick-pick cards + Top Tracks list + playlists rail.
    private void BuildHomePage()
    {
        _homeScroll = new ScrollViewer
        {
            Padding = new Thickness(16, 12, 16, 16),
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        var col = new StackPanel { Spacing = 8 };

        // Greeting -- refreshed to the current hour each time LoadHome() runs.
        int hour = DateTime.Now.Hour;
        string greeting = hour < 12 ? Loc.S("Greeting_Morning") : hour < 18 ? Loc.S("Greeting_Afternoon") : Loc.S("Greeting_Evening");
        _homeGreeting = new TextBlock
        {
            Text = greeting,
            FontSize = 32,
            FontWeight = FontWeights.SemiBold,
            Margin = new Thickness(0, 0, 0, 4),
        };
        col.Children.Add(_homeGreeting);

        // Subtle non-Premium hint: Free accounts play full tracks at 128 kbps, so the
        // only difference worth surfacing is the standard quality ceiling. Hidden by
        // default; LoadHome() reveals it once the account tier is known.
        _homeFreeHint = new TextBlock
        {
            Text = Loc.S("Account_FreeQualityHint"),
            FontSize = 12,
            Opacity = 0.6,
            Margin = new Thickness(0, 0, 0, 4),
            Visibility = Visibility.Collapsed,
        };
        col.Children.Add(_homeFreeHint);

        // Quick-pick cards: tap to navigate to that existing page.
        var quickRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12, Margin = new Thickness(0, 8, 0, 4) };
        quickRow.Children.Add(MakeQuickCard(Loc.S("Nav_LikedSongs"), Symbol.Audio, () => { _nav.SelectedItem = _likedItem; }));
        quickRow.Children.Add(MakeQuickCard(Loc.S("Nav_Flow"), Symbol.Play, () => { _nav.SelectedItem = _flowItem; }));
        quickRow.Children.Add(MakeQuickCard(Loc.S("Nav_Charts"), Symbol.World, () => { _nav.SelectedItem = _chartsItem; }));
        quickRow.Children.Add(MakeQuickCard(Loc.S("Nav_Podcasts"), Symbol.Microphone, () => { _nav.SelectedItem = _podcastsItem; }));
        col.Children.Add(quickRow);

        // Top Tracks (vertical list; inner scroll disabled so the outer ScrollViewer drives).
        col.Children.Add(Section(Loc.S("Section_TopTracks")));
        _homeTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _homeTrackList.ItemClick += OnHomeTrackClick;
        NoInnerScroll(_homeTrackList);
        col.Children.Add(_homeTrackList);

        // Your Playlists (horizontal scroll rail of tiles).
        col.Children.Add(Section(Loc.S("Section_YourPlaylists")));
        _homePlaylistScroll = new ScrollViewer
        {
            HorizontalScrollMode = ScrollMode.Auto,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Auto,
            VerticalScrollMode = ScrollMode.Disabled,
            VerticalScrollBarVisibility = ScrollBarVisibility.Disabled,
            Margin = new Thickness(0, 0, 0, 8),
        };
        _homePlaylistPanel = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
        _homePlaylistScroll.Content = _homePlaylistPanel;
        col.Children.Add(_homePlaylistScroll);

        _homeScroll.Content = col;
        _homePage = _homeScroll;
    }

    // A quick-pick navigation card: icon + label inside a standard Button.
    private Button MakeQuickCard(string label, Symbol sym, Action onClick)
    {
        var sp = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, VerticalAlignment = VerticalAlignment.Center };
        sp.Children.Add(new SymbolIcon(sym));
        sp.Children.Add(new TextBlock { Text = label, VerticalAlignment = VerticalAlignment.Center });
        var btn = new Button
        {
            Content = sp,
            MinWidth = 128,
            Padding = new Thickness(12, 10, 12, 10),
        };
        btn.Click += (_, _) => onClick();
        return btn;
    }

    // Artist detail: a scrolling column of name/fans + Top Tracks + Albums +
    // Related Artists (inner lists' own scrolling disabled; outer scrolls).
    private void BuildArtistPage()
    {
        _artistScroll = new ScrollViewer
        {
            Padding = new Thickness(16, 12, 16, 16),
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        var col = new StackPanel { Spacing = 8 };

        _artistHeader = new TextBlock { FontSize = 28, FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.Wrap };
        col.Children.Add(_artistHeader);
        _artistFans = new TextBlock { Opacity = 0.6 };
        col.Children.Add(_artistFans);

        // Start radio: an "artist radio" mix seeded from this artist.
        _artistRadioBtn = new Button { Margin = new Thickness(0, 4, 0, 0) };
        var arc = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        arc.Children.Add(new FontIcon { Glyph = "", FontSize = 14 }); // Play
        arc.Children.Add(new TextBlock { Text = Loc.S("Menu_StartRadio"), VerticalAlignment = VerticalAlignment.Center });
        _artistRadioBtn.Content = arc;
        _artistRadioBtn.Click += (_, _) => StartArtistRadio(_artistId);
        col.Children.Add(_artistRadioBtn);

        col.Children.Add(Section(Loc.S("Section_TopTracks")));
        _artistTopList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _artistTopList.ItemClick += OnArtistTopClick;
        NoInnerScroll(_artistTopList);
        col.Children.Add(_artistTopList);

        col.Children.Add(Section(Loc.S("Section_Albums")));
        _artistAlbumsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _artistAlbumsGrid.ItemClick += OnArtistAlbumClick;
        NoInnerScroll(_artistAlbumsGrid);
        col.Children.Add(_artistAlbumsGrid);

        col.Children.Add(Section(Loc.S("Section_RelatedArtists")));
        _artistRelatedGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _artistRelatedGrid.ItemClick += OnArtistRelatedClick;
        NoInnerScroll(_artistRelatedGrid);
        col.Children.Add(_artistRelatedGrid);

        _artistScroll.Content = col;
        _artistPage = _artistScroll;
    }

    // Lyrics: a scrolling stack of per-line TextBlocks (synced) or one block (plain).
    private void BuildLyricsPage()
    {
        _lyricsScroll = new ScrollViewer
        {
            Padding = new Thickness(24, 16, 24, 24),
            HorizontalScrollMode = ScrollMode.Disabled,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
        };
        _lyricsPanel = new StackPanel { Spacing = 6 };
        _lyricsScroll.Content = _lyricsPanel;
        _lyricsPage = _lyricsScroll;
        ShowLyricsMessage(Loc.S("Lyrics_PlayPrompt"));
    }

    private Grid BuildTransport()
    {
        // Groove-Music-style Fluent bar: three zones - now-playing on the left,
        // the transport CENTERED (play is a filled accent circle) with the seek
        // bar + times directly under it, and secondary actions + volume on the
        // right. [Star, Auto, Star] keeps the centre cluster dead-centre in the
        // bar regardless of the left/right content widths.
        var bar = new Grid
        {
            Padding = new Thickness(16, 8, 16, 10),
            Background = new SolidColorBrush(Color.FromArgb(0x66, 0x14, 0x04, 0x1E)),
        };
        bar.ColumnDefinitions.Add(ColStar()); // left zone (now-playing)
        bar.ColumnDefinitions.Add(ColAuto()); // centre zone (transport + seek)
        bar.ColumnDefinitions.Add(ColStar()); // right zone (actions + volume)

        // ---- LEFT: cover + title/artist + like + add ----
        var left = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12, VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Left };
        _cover = new Image { Width = 48, Height = 48, VerticalAlignment = VerticalAlignment.Center };
        left.Children.Add(_cover);
        var now = new StackPanel { VerticalAlignment = VerticalAlignment.Center, MinWidth = 120, MaxWidth = 240 };
        _nowTitle = new TextBlock { Text = Loc.S("Status_LoggingIn"), FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        _nowArtist = new TextBlock { Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        now.Children.Add(_nowTitle); now.Children.Add(_nowArtist);
        left.Children.Add(now);
        // "Preview" badge: shown only while the current stream is a 30-second sample
        // (surfaced by DZIsPreview and toggled in OnTick). Collapsed by default.
        _previewBadge = new Border
        {
            Background = _accent,
            CornerRadius = new CornerRadius(3),
            Padding = new Thickness(6, 1, 6, 2),
            VerticalAlignment = VerticalAlignment.Center,
            Visibility = Visibility.Collapsed,
            Child = new TextBlock
            {
                Text = Loc.S("Preview_Badge"),
                FontSize = 10,
                FontWeight = FontWeights.Bold,
                Foreground = new SolidColorBrush(Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF)),
            },
        };
        left.Children.Add(_previewBadge);
        _likeBtn = new ToggleButton { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Heart
        ToolTipService.SetToolTip(_likeBtn, Loc.S("Tooltip_Like"));
        AutomationProperties.SetName(_likeBtn, Loc.S("Tooltip_Like")); // icon-only -> give screen readers a name
        _likeBtn.Click += OnLike;
        _addBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Add to playlist
        ToolTipService.SetToolTip(_addBtn, Loc.S("Tooltip_AddToPlaylist"));
        AutomationProperties.SetName(_addBtn, Loc.S("Tooltip_AddToPlaylist"));
        _addBtn.Click += OnAddCurrentToPlaylist;
        // Download for offline (premium-only): caches the current track for offline
        // playback. Enabled/disabled per-track in SetNowPlaying (premium + not an episode).
        _downloadBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Download
        ToolTipService.SetToolTip(_downloadBtn, Loc.S("Tooltip_DownloadOffline"));
        AutomationProperties.SetName(_downloadBtn, Loc.S("Tooltip_DownloadOffline"));
        _downloadBtn.Click += OnDownloadCurrentOffline;
        left.Children.Add(_likeBtn); left.Children.Add(_addBtn); left.Children.Add(_downloadBtn);
        Grid.SetColumn(left, 0); bar.Children.Add(left);

        // ---- CENTRE: transport row (shuffle - prev - play - next - repeat) + seek row ----
        var center = new StackPanel { Spacing = 4, VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Center };
        // Casting chip: a "Playing on <device>" pill with a "Play here" button,
        // shown only while a Connect device owns playback (UpdateConnectIndicator
        // toggles it). "Play here" disconnects, returning playback to this computer.
        var chipWhite = new SolidColorBrush(Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF));
        _castChip = new Border
        {
            Background = _accent,
            CornerRadius = new CornerRadius(12),
            Padding = new Thickness(10, 2, 4, 2),
            Visibility = Visibility.Collapsed,
            HorizontalAlignment = HorizontalAlignment.Center,
        };
        var castRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, VerticalAlignment = VerticalAlignment.Center };
        castRow.Children.Add(new FontIcon { Glyph = "", FontSize = 12, Foreground = chipWhite, VerticalAlignment = VerticalAlignment.Center }); // Cast
        _castChipText = new TextBlock { Foreground = chipWhite, FontSize = 12, VerticalAlignment = VerticalAlignment.Center, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis, MaxWidth = 220 };
        castRow.Children.Add(_castChipText);
        var playHereBtn = new Button
        {
            Content = Loc.S("Connect_PlayHere"),
            FontSize = 12,
            Padding = new Thickness(8, 1, 8, 2),
            Foreground = chipWhite,
            Background = new SolidColorBrush(Color.FromArgb(0x33, 0xFF, 0xFF, 0xFF)),
            BorderThickness = new Thickness(0),
        };
        ToolTipService.SetToolTip(playHereBtn, Loc.S("Connect_PlayHere"));
        AutomationProperties.SetName(playHereBtn, Loc.S("Connect_PlayHere"));
        playHereBtn.Click += (_, _) => { _connectFlyout?.Hide(); DispatchDisconnect(); }; // return playback here
        castRow.Children.Add(playHereBtn);
        _castChip.Child = castRow;
        center.Children.Add(_castChip);
        var tr = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
        _shuffleBtn = new ToggleButton { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Shuffle
        ToolTipService.SetToolTip(_shuffleBtn, Loc.S("Tooltip_Shuffle"));
        AutomationProperties.SetName(_shuffleBtn, Loc.S("Tooltip_Shuffle"));
        _shuffleBtn.Click += OnShuffle;
        var prevBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Previous
        ToolTipService.SetToolTip(prevBtn, Loc.S("Tooltip_Previous"));
        AutomationProperties.SetName(prevBtn, Loc.S("Tooltip_Previous"));
        prevBtn.Click += (_, _) => Prev();
        // Play/pause as a filled accent circle - the Groove signature.
        _playIcon = new FontIcon { Glyph = "", FontSize = 16, Foreground = new SolidColorBrush(Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF)) }; // Play (white on accent)
        _playBtn = new Button
        {
            Content = _playIcon,
            Width = 44, Height = 44, Padding = new Thickness(0),
            CornerRadius = new CornerRadius(22),
            Background = _accent,
            HorizontalContentAlignment = HorizontalAlignment.Center,
            VerticalContentAlignment = VerticalAlignment.Center,
        };
        ToolTipService.SetToolTip(_playBtn, Loc.S("Tooltip_PlayPause"));
        AutomationProperties.SetName(_playBtn, Loc.S("Tooltip_PlayPause"));
        // Off-thread: when routed over Connect this forwards over HTTP (15 s timeout).
        _playBtn.Click += async (_, _) => await Task.Run(DeezerCore.DZTogglePause);
        var nextBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Next
        ToolTipService.SetToolTip(nextBtn, Loc.S("Tooltip_Next"));
        AutomationProperties.SetName(nextBtn, Loc.S("Tooltip_Next"));
        nextBtn.Click += (_, _) => Next();
        _repeatIcon = new FontIcon { Glyph = "", FontSize = 14 }; // RepeatAll
        _repeatBtn = new Button { Content = _repeatIcon };
        ToolTipService.SetToolTip(_repeatBtn, Loc.S("Tooltip_RepeatOff"));
        AutomationProperties.SetName(_repeatBtn, Loc.S("Tooltip_RepeatOff")); // kept in sync in ApplyRepeatDisplay
        _repeatBtn.Click += OnRepeat;
        tr.Children.Add(_shuffleBtn); tr.Children.Add(prevBtn); tr.Children.Add(_playBtn); tr.Children.Add(nextBtn); tr.Children.Add(_repeatBtn);
        center.Children.Add(tr);

        var seekRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
        _posText = new TextBlock { Text = "0:00", Opacity = 0.7, FontSize = 12, VerticalAlignment = VerticalAlignment.Center, MinWidth = 36, TextAlignment = TextAlignment.Right };
        _seek = new Slider { Minimum = 0, Maximum = 1000, Value = 0, Width = 360, VerticalAlignment = VerticalAlignment.Center, Foreground = _accent };
        _seek.ValueChanged += OnSeekChanged;
        _durText = new TextBlock { Text = "0:00", Opacity = 0.7, FontSize = 12, VerticalAlignment = VerticalAlignment.Center, MinWidth = 36 };
        seekRow.Children.Add(_posText); seekRow.Children.Add(_seek); seekRow.Children.Add(_durText);
        center.Children.Add(seekRow);
        Grid.SetColumn(center, 1); bar.Children.Add(center);

        // ---- RIGHT: lyrics - artist - connect (cast) - volume ----
        var right = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Right };
        _lyricsBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // ClosedCaption
        ToolTipService.SetToolTip(_lyricsBtn, Loc.S("Tooltip_Lyrics"));
        AutomationProperties.SetName(_lyricsBtn, Loc.S("Tooltip_Lyrics"));
        _lyricsBtn.Click += (_, _) => ShowLyrics();
        _artistBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Contact
        ToolTipService.SetToolTip(_artistBtn, Loc.S("Tooltip_Artist"));
        AutomationProperties.SetName(_artistBtn, Loc.S("Tooltip_Artist"));
        _artistBtn.Click += OnArtist;
        // OpenDeezer Connect: a discreet cast button whose flyout lists LAN devices.
        _connectBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Cast
        ToolTipService.SetToolTip(_connectBtn, Loc.S("Tooltip_Connect"));
        AutomationProperties.SetName(_connectBtn, Loc.S("Tooltip_Connect"));
        _connectFlyout = new Flyout();
        var cp = new StackPanel { Spacing = 8, MinWidth = 280, Padding = new Thickness(4), FlowDirection = Loc.FlowDirection };
        cp.Children.Add(new TextBlock { Text = Loc.S("Connect_Title"), FontWeight = FontWeights.SemiBold });
        _connectStatus = new TextBlock { Opacity = 0.7, TextWrapping = TextWrapping.Wrap };
        _connectList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true, MaxHeight = 320 };
        _connectList.ItemClick += OnConnectItemClick;
        cp.Children.Add(_connectStatus); cp.Children.Add(_connectList);
        _connectFlyout.Content = cp;
        _connectFlyout.Opened += OnConnectOpened;
        _connectBtn.Flyout = _connectFlyout;
        // Up-Next queue: a flyout listing the current queue (jump / reorder / remove /
        // clear). Rebuilt on open and after each edit (RefreshQueuePanel).
        _queueBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // List
        ToolTipService.SetToolTip(_queueBtn, Loc.S("Tooltip_Queue"));
        AutomationProperties.SetName(_queueBtn, Loc.S("Tooltip_Queue"));
        _queueFlyout = new Flyout();
        var qp = new StackPanel { Spacing = 8, MinWidth = 320, Padding = new Thickness(4), FlowDirection = Loc.FlowDirection };
        var qHeader = new Grid();
        qHeader.ColumnDefinitions.Add(ColStar());
        qHeader.ColumnDefinitions.Add(ColAuto());
        var qTitle = new TextBlock { Text = Loc.S("Queue_Title"), FontWeight = FontWeights.SemiBold, VerticalAlignment = VerticalAlignment.Center };
        Grid.SetColumn(qTitle, 0); qHeader.Children.Add(qTitle);
        _queueClearBtn = new Button { Content = Loc.S("Queue_Clear"), FontSize = 12, Padding = new Thickness(8, 2, 8, 2) };
        _queueClearBtn.Click += OnQueueClear;
        Grid.SetColumn(_queueClearBtn, 1); qHeader.Children.Add(_queueClearBtn);
        qp.Children.Add(qHeader);
        _queueStatus = new TextBlock { Opacity = 0.7, TextWrapping = TextWrapping.Wrap, Visibility = Visibility.Collapsed };
        _queueList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true, MaxHeight = 380 };
        _queueList.ItemClick += OnQueueRowClick;
        qp.Children.Add(_queueStatus); qp.Children.Add(_queueList);
        _queueFlyout.Content = qp;
        _queueFlyout.Opened += OnQueueFlyoutOpened;
        _queueFlyout.Closed += OnQueueFlyoutClosed;
        _queueBtn.Flyout = _queueFlyout;
        var volIcon = new FontIcon { Glyph = "", FontSize = 14, VerticalAlignment = VerticalAlignment.Center }; // Volume
        _volume = new Slider { Minimum = 0, Maximum = 100, Value = 100, Width = 100, VerticalAlignment = VerticalAlignment.Center, Foreground = _accent };
        _volume.ValueChanged += OnVolumeChanged;
        right.Children.Add(_queueBtn); right.Children.Add(_lyricsBtn); right.Children.Add(_artistBtn); right.Children.Add(_connectBtn);
        right.Children.Add(volIcon); right.Children.Add(_volume);
        Grid.SetColumn(right, 2); bar.Children.Add(right);

        return bar;
    }

    // ---- item factories ------------------------------------------------------
    private FrameworkElement MakeExplicitBadge()
    {
        var b = new Border
        {
            Background = new SolidColorBrush(Color.FromArgb(0xFF, 0x9A, 0x9A, 0x9A)),
            CornerRadius = new CornerRadius(3),
            Padding = new Thickness(4, 0, 4, 1),
            VerticalAlignment = VerticalAlignment.Center,
        };
        b.Child = new TextBlock
        {
            Text = "E",
            FontSize = 10,
            FontWeight = FontWeights.Bold,
            LineHeight = 12,
            VerticalAlignment = VerticalAlignment.Center,
            Foreground = new SolidColorBrush(Color.FromArgb(0xFF, 0xFF, 0xFF, 0xFF)),
        };
        ToolTipService.SetToolTip(b, Loc.S("Tooltip_Explicit"));
        return b;
    }

    private UIElement MakeTrackRow(Track t, int index)
    {
        var g = new Grid { Tag = index, Height = 56, Padding = new Thickness(6, 4, 6, 4), ColumnSpacing = 12 };
        g.ColumnDefinitions.Add(ColAuto()); // 0 artwork
        g.ColumnDefinitions.Add(ColStar()); // 1 title/artist
        g.ColumnDefinitions.Add(ColAuto()); // 2 offline "downloaded" glyph
        g.ColumnDefinitions.Add(ColAuto()); // 3 duration
        var img = new Image { Width = 44, Height = 44, VerticalAlignment = VerticalAlignment.Center };
        Grid.SetColumn(img, 0); g.Children.Add(img);
        var sp = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        var title = new TextBlock { Text = t.Name, FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        var artist = new TextBlock { Text = t.ArtistLine, Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        if (t.IsExplicit)
        {
            var titleRow = new Grid { ColumnSpacing = 6 };
            titleRow.ColumnDefinitions.Add(ColAuto());
            titleRow.ColumnDefinitions.Add(ColStar());
            var badge = MakeExplicitBadge();
            Grid.SetColumn(badge, 0); titleRow.Children.Add(badge);
            Grid.SetColumn(title, 1); titleRow.Children.Add(title);
            sp.Children.Add(titleRow);
        }
        else
        {
            sp.Children.Add(title);
        }
        sp.Children.Add(artist);
        Grid.SetColumn(sp, 1); g.Children.Add(sp);
        // "Downloaded" glyph for tracks cached for offline this session (ids from
        // DownloadForOffline's {key}); MarkOffline repaints the list to surface it.
        if (_offlineIds.Contains(t.Id))
        {
            var off = new FontIcon { Glyph = "", FontSize = 12, Foreground = _accent, VerticalAlignment = VerticalAlignment.Center };
            ToolTipService.SetToolTip(off, Loc.S("Tooltip_Downloaded"));
            Grid.SetColumn(off, 2); g.Children.Add(off);
        }
        var dur = new TextBlock { Text = Wire.TimeText(t.DurationMs), Opacity = 0.6, VerticalAlignment = VerticalAlignment.Center };
        Grid.SetColumn(dur, 3); g.Children.Add(dur);
        if (!string.IsNullOrEmpty(t.ArtworkUrl)) LoadArt(img, t.ArtworkUrl, _artGen, false);
        // Right-click actions (skipped for podcast episodes, which can't be liked /
        // added to a music playlist).
        if (!t.IsEpisode && !string.IsNullOrEmpty(t.Id))
        {
            var mf = new MenuFlyout();
            var like = new MenuFlyoutItem { Text = Loc.S("Menu_Like"), Tag = t.Id };
            like.Click += OnRowLike;
            var add = new MenuFlyoutItem { Text = Loc.S("Menu_AddToPlaylist"), Tag = t.Id };
            add.Click += OnRowAddToPlaylist;
            // Up-Next queue: insert right after the playing track, or append to the end.
            // Capture the Track (full metadata is needed to build the queue row / engine
            // insert payload), not just its id.
            var tCap = t;
            var playNext = new MenuFlyoutItem { Text = Loc.S("Menu_PlayNext") };
            playNext.Click += (_, _) => OnRowPlayNext(tCap);
            var addQueue = new MenuFlyoutItem { Text = Loc.S("Menu_AddToQueue") };
            addQueue.Click += (_, _) => OnRowAddToQueue(tCap);
            // Start radio: a "song radio" mix seeded from this track (loads + plays
            // through the same path as Flow).
            var radio = new MenuFlyoutItem { Text = Loc.S("Menu_StartRadio"), Tag = t.Id };
            radio.Click += OnRowStartRadio;
            mf.Items.Add(like); mf.Items.Add(add); mf.Items.Add(playNext); mf.Items.Add(addQueue); mf.Items.Add(radio);
            // Download (premium-only offline export to a folder). Disabled with an
            // explanatory tooltip for Free accounts, which the engine refuses anyway.
            var dl = new MenuFlyoutItem { Text = Loc.S("Menu_Download"), Tag = t.Id };
            if (_account.Premium)
                dl.Click += OnRowDownload;
            else
            {
                dl.IsEnabled = false;
                ToolTipService.SetToolTip(dl, Loc.S("Menu_DownloadRequiresPremium"));
            }
            mf.Items.Add(dl);
            // Download for offline (premium-only): cache for offline playback (stamps
            // the row's "downloaded" glyph on success).
            var dlOff = new MenuFlyoutItem { Text = Loc.S("Menu_DownloadOffline") };
            if (_account.Premium)
            {
                string offId = t.Id;
                dlOff.Click += (_, _) => DownloadForOffline(offId);
            }
            else
            {
                dlOff.IsEnabled = false;
                ToolTipService.SetToolTip(dlOff, Loc.S("Menu_DownloadRequiresPremium"));
            }
            mf.Items.Add(dlOff);
            g.ContextFlyout = mf;
        }
        return g;
    }

    private UIElement MakeTile(string title, string subtitle, string art, int index)
    {
        var sp = new StackPanel { Width = 164, Margin = new Thickness(6), Tag = index };
        var img = new Image { Width = 152, Height = 152 };
        var t1 = new TextBlock { Text = title, FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 6, 0, 0), TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        var t2 = new TextBlock { Text = subtitle, Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        sp.Children.Add(img); sp.Children.Add(t1); sp.Children.Add(t2);
        if (!string.IsNullOrEmpty(art)) LoadArt(img, art, _artGen, false);
        return sp;
    }

    // One Connect picker row: name + "type · version" subtitle, accent check when
    // active. Tag carries the _connectDevices index, or -1 for "This computer".
    private UIElement MakeConnectRow(string name, string subtitle, bool active, int index)
    {
        var g = new Grid { Tag = index, Padding = new Thickness(6), ColumnSpacing = 10, MinWidth = 260 };
        g.ColumnDefinitions.Add(ColStar());
        g.ColumnDefinitions.Add(ColAuto());
        var sp = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        sp.Children.Add(new TextBlock { Text = name, FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis });
        sp.Children.Add(new TextBlock { Text = subtitle, Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis });
        Grid.SetColumn(sp, 0); g.Children.Add(sp);
        if (active)
        {
            var chk = new FontIcon { Glyph = "\uE73E", Foreground = _accent, VerticalAlignment = VerticalAlignment.Center }; // CheckMark
            Grid.SetColumn(chk, 1); g.Children.Add(chk);
        }
        return g;
    }

    private void FillTrackList(ListView lv, List<Track> tracks)
    {
        lv.Items.Clear();
        for (int i = 0; i < tracks.Count; i++) lv.Items.Add(MakeTrackRow(tracks[i], i));
    }

    private void FillPlaylistGrid()
    {
        _playlistGrid.Items.Clear();
        for (int i = 0; i < _playlists.Count; i++)
        {
            var p = _playlists[i];
            var tile = MakeTile(p.Name, Loc.Plural("Tracks", p.TrackCount), p.ArtworkUrl, i);
            // Per-tile right-click: rename / delete (Tag carries the _playlists index).
            var mf = new MenuFlyout();
            var rn = new MenuFlyoutItem { Text = Loc.S("Menu_Rename"), Tag = i };
            rn.Click += OnPlaylistRename;
            var del = new MenuFlyoutItem { Text = Loc.S("Menu_Delete"), Tag = i };
            del.Click += OnPlaylistDelete;
            mf.Items.Add(rn); mf.Items.Add(del);
            if (tile is FrameworkElement fe) fe.ContextFlyout = mf;
            _playlistGrid.Items.Add(tile);
        }
    }

    private void FillSearchGrid()
    {
        _searchGrid.Items.Clear();
        _searchActions.Clear();
        // Artists first (their tile opens the artist page); then albums + playlists.
        foreach (var ar in _searchArtists)
        {
            int idx = _searchActions.Count;
            var arc = ar;
            _searchActions.Add(() => OpenArtist(arc.Id));
            _searchGrid.Items.Add(MakeTile(ar.Name, Wire.FansText(ar.NbFans), ar.ArtworkUrl, idx));
        }
        foreach (var a in _searchAlbums)
        {
            int idx = _searchActions.Count;
            var ac = a;
            _searchActions.Add(() => OpenAlbum(ac));
            _searchGrid.Items.Add(MakeTile(a.Name, a.ArtistLine, a.ArtworkUrl, idx));
        }
        foreach (var p in _searchPlaylists)
        {
            int idx = _searchActions.Count;
            var pc = p;
            _searchActions.Add(() => OpenPlaylist(pc));
            _searchGrid.Items.Add(MakeTile(p.Name, p.Owner, p.ArtworkUrl, idx));
        }
    }

    private void FillTileGrid(GridView grid, List<Album> albums)
    {
        grid.Items.Clear();
        for (int i = 0; i < albums.Count; i++) grid.Items.Add(MakeTile(albums[i].Name, albums[i].ArtistLine, albums[i].ArtworkUrl, i));
    }
    private void FillArtistTiles(GridView grid, List<ArtistInfo> artists)
    {
        grid.Items.Clear();
        for (int i = 0; i < artists.Count; i++) grid.Items.Add(MakeTile(artists[i].Name, Wire.FansText(artists[i].NbFans), artists[i].ArtworkUrl, i));
    }
    private void FillPlaylistTiles(GridView grid, List<Playlist> plists)
    {
        grid.Items.Clear();
        for (int i = 0; i < plists.Count; i++) grid.Items.Add(MakeTile(plists[i].Name, plists[i].Owner, plists[i].ArtworkUrl, i));
    }

    // ---- cover art: fetch bytes off-thread, decode on the UI thread ----------
    private async void LoadArt(Image img, string url, int token, bool isCover)
    {
        var bytes = await Task.Run(() => DeezerCore.Fetch(url));
        if (bytes.Length == 0) return;
        // List reloaded -> drop stale results (matches the C++ generation check).
        if (isCover) { if (token != _playGen) return; }
        else { if (token != _artGen) return; }
        try
        {
            var stream = new InMemoryRandomAccessStream();
            var writer = new DataWriter(stream);
            writer.WriteBytes(bytes);
            await writer.StoreAsync();
            writer.DetachStream();
            stream.Seek(0);
            var bmp = new BitmapImage();
            img.Source = bmp;
            await bmp.SetSourceAsync(stream);
        }
        catch { }
    }

    // ---- login ---------------------------------------------------------------
    private async void StartLogin()
    {
        string arl = Config.LoadArl();
        if (string.IsNullOrEmpty(arl)) { ShowLoginChoice(); return; }
        await TryLogin(arl, persist: false);
    }

    private async Task TryLogin(string arl, bool persist)
    {
        _nowTitle.Text = Loc.S("Status_LoggingIn");
        bool ok = await Task.Run(() =>
        {
            // Identify this instance to OpenDeezer Connect BEFORE DZInit.
            DeezerCore.DZSetClientInfo("windows", Wire.ThisDeviceName());
            return DeezerCore.DZInit(arl) != 0;
        });
        if (ok)
        {
            if (persist) Config.SaveArl(arl); // remember for next launch
            FinishLogin();
        }
        else
        {
            _nowTitle.Text = Loc.S("Status_LoginFailed");
            // No internet is a transient, retryable failure -> a dedicated Retry
            // screen instead of the expired-ARL "sign in again" chooser. Any other
            // kind (1 expired/invalid ARL, 3 other) keeps the existing behavior.
            if (DeezerCore.DZLoginErrorKind() == 2) { ShowNoInternet(); return; }
            await ShowMessage(Loc.S("Dialog_LoginFailedTitle"), Loc.S("Dialog_LoginFailedBody"));
            ShowLoginChoice();
        }
    }

    // Shared success path: apply persisted prefs, fetch the account tier, gate a
    // non-premium (Deezer Free) account behind a block before any browsing/playback.
    private async void FinishLogin()
    {
        _loggedIn = true;
        DeezerCore.DZSetQuality(_settings.Quality);
        DeezerCore.DZSetReplayGain(_settings.ReplayGain ? 1 : 0);
        DeezerCore.DZSetGapless(_settings.Gapless ? 1 : 0);
        DeezerCore.DZSetCrossfadeMS(_settings.CrossfadeMs);
        if (!string.IsNullOrEmpty(_settings.AudioDevice)) DeezerCore.DZSetAudioDevice(_settings.AudioDevice);

        _nowTitle.Text = Loc.S("Status_CheckingAccount");
        var acct = await Task.Run(() => DeezerCore.Account());
        _account = acct;
        SeedLikedIds(); // populate the liked-ids cache so the heart is truthful from the first track
        // A Deezer Free account streams FULL tracks at 128 kbps through the engine
        // (not 30-second previews), so it uses the app exactly like Premium. Premium
        // is still tracked to gate the Download menu item; LoadHome surfaces a subtle
        // "standard quality" hint for Free accounts.

        // Restore the app UI if a takeover screen (the no-internet retry page)
        // replaced the window content during login. In the normal login flow Content
        // is already RootGrid, so this is a no-op; the nav manipulation below needs
        // RootGrid to be the live window content.
        if (!ReferenceEquals(Content, RootGrid)) Content = RootGrid;

        _lastFinished = DeezerCore.DZFinishedCount();
        _updatingVol = true; _volume.Value = DeezerCore.DZVolume() * 100.0; _updatingVol = false;
        _timer.Start();
        _nowTitle.Text = Loc.S("Status_NotPlaying");
        _nowArtist.Text = "";
        _suppressNav = false;
        // Force a SelectionChanged even when Home is ALREADY selected (the switch-
        // account path reverts the nav to _homeItem before login): null it first so
        // reselecting fires OnNav -> LoadHome and Home shows the new account's data.
        _nav.SelectedItem = null;
        _nav.SelectedItem = _homeItem; // -> OnNav -> LoadHome
    }

    // No-internet screen: DZInit failed because the network is unreachable (kind 2),
    // as opposed to an expired ARL. Replace the ENTIRE window content with a
    // retryable page; Retry re-runs the saved-ARL login flow. A full-window takeover
    // that is recoverable -- no engine stop, and Retry restores the app UI.
    private void ShowNoInternet()
    {
        var page = new Grid { Background = new SolidColorBrush(Color.FromArgb(0xFF, 0x14, 0x04, 0x1E)), FlowDirection = Loc.FlowDirection };
        var sp = new StackPanel
        {
            Spacing = 14,
            MaxWidth = 560,
            Padding = new Thickness(24),
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
        };
        sp.Children.Add(new TextBlock
        {
            Text = "OpenDeezer",
            FontSize = 22,
            FontWeight = FontWeights.SemiBold,
            Foreground = _accent,
            HorizontalAlignment = HorizontalAlignment.Center,
        });
        sp.Children.Add(new TextBlock
        {
            Text = Loc.S("NoInternet_Title"),
            FontSize = 26,
            FontWeight = FontWeights.SemiBold,
            TextWrapping = TextWrapping.Wrap,
            TextAlignment = TextAlignment.Center,
            HorizontalAlignment = HorizontalAlignment.Center,
        });
        sp.Children.Add(new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            TextAlignment = TextAlignment.Center,
            Opacity = 0.85,
            HorizontalAlignment = HorizontalAlignment.Center,
            Text = Loc.S("NoInternet_Body"),
        });
        var retry = new Button { Content = Loc.S("Action_Retry"), HorizontalAlignment = HorizontalAlignment.Center };
        retry.Click += (_, _) =>
        {
            // Busy state while the saved-ARL login retries: StartLogin replaces the
            // window content on success, or rebuilds this page (fresh, enabled button)
            // on a repeat no-internet failure.
            retry.IsEnabled = false;
            retry.Content = Loc.S("Status_LoggingIn");
            StartLogin();
        };
        sp.Children.Add(retry);
        page.Children.Add(sp);
        Content = page; // wholesale replace, same takeover as the block screen
    }

    // Login chooser: "Log in with Deezer" opens the embedded webview, "Enter ARL"
    // is the manual fallback. Cancel leaves the app idle (relaunch to retry).
    private async void ShowLoginChoice()
    {
        _nowTitle.Text = Loc.S("Status_NotSignedIn");
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Dialog_SignInTitle"),
            Content = new TextBlock
            {
                TextWrapping = TextWrapping.Wrap,
                Text = Loc.S("Dialog_SignInBody"),
            },
            PrimaryButtonText = Loc.S("Btn_LogInWithDeezer"),
            SecondaryButtonText = Loc.S("Btn_EnterArlManually"),
            CloseButtonText = Loc.S("Btn_Cancel"),
            DefaultButton = ContentDialogButton.Primary,
        };
        var res = await ShowDialog(dlg);
        if (res == ContentDialogResult.Primary)
        {
            string arl = await ShowWebLogin();
            if (!string.IsNullOrEmpty(arl)) await TryLogin(arl, persist: true);
            else ShowLoginChoice();
        }
        else if (res == ContentDialogResult.Secondary)
        {
            string entered = await PromptText(Loc.S("Dialog_LogInWithArlTitle"), Loc.S("Dialog_PasteArlPlaceholder"), "");
            entered = (entered ?? "").Trim();
            if (!string.IsNullOrEmpty(entered)) await TryLogin(entered, persist: true);
            else ShowLoginChoice();
        }
        // Cancel: stay idle; nav stays empty until the user re-triggers login.
    }

    // Embedded Deezer login: host a WebView2 in a modal dialog, then poll the
    // CoreWebView2 cookie store until a non-empty "arl" cookie appears (HttpOnly,
    // so only readable via CookieManager, not document.cookie).
    private async Task<string> ShowWebLogin()
    {
        _capturedArl = "";
        _loginDialog = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Dialog_WebLoginTitle"),
            CloseButtonText = Loc.S("Btn_Cancel"),
        };
        // Let the dialog grow to fit the web page (defaults cap ~548 px).
        _loginDialog.Resources["ContentDialogMaxWidth"] = 620.0;
        _loginDialog.Resources["ContentDialogMaxHeight"] = 740.0;
        _loginWebView = new WebView2 { Width = 560, Height = 640 };
        _loginDialog.Content = _loginWebView;
        try
        {
            // Do NOT await EnsureCoreWebView2Async here: it only completes after the
            // control loads, which is after ShowAsync -> deadlock. Setting Source kicks
            // off implicit init; the poll waits for CoreWebView2() to become non-null.
            _loginWebView.Source = new Uri("https://www.deezer.com/login");

            _arlPollTimer = DispatcherQueue.CreateTimer();
            _arlPollTimer.Interval = TimeSpan.FromMilliseconds(700);
            _arlPollTimer.Tick += OnArlPoll;
            _arlPollTimer.Start();

            await ShowDialog(_loginDialog); // returns when arl captured (Hide) or Cancel
            return _capturedArl;
        }
        catch { return ""; }
        finally
        {
            // Always tear down -- runs on the Source-set exception path too. Detach +
            // stop the poll timer and Close() the WebView2 so its out-of-process
            // msedgewebview2.exe host exits now instead of leaking until GC.
            if (_arlPollTimer != null) { _arlPollTimer.Stop(); _arlPollTimer.Tick -= OnArlPoll; _arlPollTimer = null; }
            _loginWebView?.Close();
            _loginWebView = null;
            _loginDialog = null;
        }
    }

    // Cookie poll (UI thread): once CoreWebView2 is up, read the deezer.com cookie
    // jar and, when a non-empty "arl" appears, stash it and close the dialog.
    private async void OnArlPoll(DispatcherQueueTimer sender, object args)
    {
        if (_arlPollBusy || _loginWebView == null) return;
        var core = _loginWebView.CoreWebView2;
        if (core == null) return; // CoreWebView2 not initialized yet
        _arlPollBusy = true;
        try
        {
            var cookies = await core.CookieManager.GetCookiesAsync("https://www.deezer.com");
            if (_loginWebView != null) // dialog still open after the await
            {
                foreach (var c in cookies)
                {
                    if (c.Name == "arl")
                    {
                        string v = c.Value;
                        if (!string.IsNullOrEmpty(v))
                        {
                            _capturedArl = v;
                            _arlPollTimer?.Stop();
                            _loginDialog?.Hide();
                        }
                        break;
                    }
                }
            }
        }
        catch { }
        _arlPollBusy = false;
    }

    // ---- navigation ----------------------------------------------------------
    private void OnNav(NavigationView nav, NavigationViewSelectionChangedEventArgs args)
    {
        if (_suppressNav) return;
        if (args.SelectedItem is not NavigationViewItem item) return;
        string tag = item.Tag as string ?? "";
        // About / Settings / Account / Phone Remote are modal actions, not pages: open then revert.
        if (tag is "about" or "settings" or "account" or "phoneremote")
        {
            if (tag == "about") ShowAbout();
            else if (tag == "settings") ShowSettings();
            else if (tag == "phoneremote") ShowPhoneRemote();
            else ShowLoginChoice();
            _suppressNav = true;
            nav.SelectedItem = _lastContentItem ?? _homeItem;
            _suppressNav = false;
            return;
        }
        _lastContentItem = item;
        _lyricsShown = false; // leaving the lyrics/artist page for a menu page
        switch (tag)
        {
            case "home": nav.Header = Loc.S("Nav_Home"); nav.Content = _homePage; LoadHome(); break;
            case "liked": nav.Header = Loc.S("Nav_LikedSongs"); nav.Content = _tracksPage; LoadFavorites(); break;
            case "flow": nav.Header = Loc.S("Nav_Flow"); nav.Content = _tracksPage; LoadFlow(); break;
            case "charts": nav.Header = Loc.S("Nav_Charts"); nav.Content = _chartsPage; LoadCharts(); break;
            case "playlists": nav.Header = Loc.S("Nav_Playlists"); nav.Content = _playlistsPage; LoadPlaylists(); break;
            case "podcasts": nav.Header = Loc.S("Nav_Podcasts"); nav.Content = _podcastPage; _podcastBox.Focus(FocusState.Programmatic); break;
            case "recent": nav.Header = Loc.S("Nav_Recent"); nav.Content = _recentPage; LoadRecent(); break;
            case "search": nav.Header = Loc.S("Nav_Search"); nav.Content = _searchPage; _searchBox.Focus(FocusState.Programmatic); break;
        }
    }

    // ---- browse (heavy work off the UI thread) -------------------------------
    private async void LoadFavorites()
    {
        if (!_loggedIn) return;
        SetCollectionContext(CollectionKind.None, "");
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.Favorites());
        if (gen != _browseGen) return; // a newer navigation superseded this one
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
        SeedLikedIds(); // refresh the liked-ids cache from the just-loaded favorites
    }

    private async void LoadCharts()
    {
        if (!_loggedIn) return;
        var (tracks, albums, artists, playlists) = await Task.Run(() =>
        {
            string json = DeezerCore.TakeJson(DeezerCore.DZChartsJSON());
            return (Wire.ParseTracks(json), Wire.ParseAlbums(json), Wire.ParseArtists(json), Wire.ParsePlaylists(json));
        });
        _chartsTracks = tracks;
        _chartsAlbums = albums;
        _chartsArtists = artists;
        _chartsPlaylists = playlists;
        _artGen++;
        FillTrackList(_chartsTrackList, _chartsTracks);
        FillTileGrid(_chartsAlbumsGrid, _chartsAlbums);
        FillArtistTiles(_chartsArtistsGrid, _chartsArtists);
        FillPlaylistTiles(_chartsPlaylistsGrid, _chartsPlaylists);
        try { _chartsScroll.ChangeView(null, 0.0, null); } catch { }
    }

    // Flow: the personalized stream -> the shared track list, then auto-play head.
    private async void LoadFlow()
    {
        if (!_loggedIn) return;
        SetCollectionContext(CollectionKind.None, "");
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.Flow());
        if (gen != _browseGen) return; // a newer navigation superseded this one
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
        if (_tracks.Count > 0) PlayFrom(_tracks, 0);
    }

    private async void LoadPlaylists()
    {
        if (!_loggedIn) return;
        var ps = await Task.Run(() => DeezerCore.Playlists());
        _playlists = ps;
        _artGen++;
        FillPlaylistGrid();
    }

    // Home: fetch top tracks + playlists off-thread, update the greeting, then fill.
    private async void LoadHome()
    {
        if (!_loggedIn) return;
        var home = await Task.Run(() => DeezerCore.Home());
        _homeTracks = home.TopTracks;
        _homePlaylists = home.Playlists;
        _artGen++;
        // Refresh the greeting in case the hour changed since the page was built.
        int hour = DateTime.Now.Hour;
        _homeGreeting.Text = hour < 12 ? Loc.S("Greeting_Morning") : hour < 18 ? Loc.S("Greeting_Afternoon") : Loc.S("Greeting_Evening");
        // Show the "standard quality" hint only for Free accounts.
        _homeFreeHint.Visibility = _account.Premium ? Visibility.Collapsed : Visibility.Visible;
        FillTrackList(_homeTrackList, _homeTracks);
        FillHomePlaylistRail();
        try { _homeScroll.ChangeView(null, 0.0, null); } catch { }
    }

    private void FillHomePlaylistRail()
    {
        _homePlaylistPanel.Children.Clear();
        for (int i = 0; i < _homePlaylists.Count; i++)
        {
            var p = _homePlaylists[i];
            var tile = MakeTile(p.Name, p.Owner, p.ArtworkUrl, i);
            int captured = i;
            if (tile is FrameworkElement fe)
                fe.Tapped += (_, _) => OpenPlaylist(_homePlaylists[captured]);
            _homePlaylistPanel.Children.Add(tile);
        }
    }

    private async void OpenPlaylist(Playlist p)
    {
        _lyricsShown = false;
        _nav.Header = p.Name;
        _nav.Content = _tracksPage;
        SetCollectionContext(CollectionKind.Playlist, p.Id);
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.PlaylistTracks(p.Id));
        if (gen != _browseGen) return; // a newer navigation superseded this one
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
    }

    private async void OpenAlbum(Album a)
    {
        _lyricsShown = false;
        _nav.Header = a.Name;
        _nav.Content = _tracksPage;
        SetCollectionContext(CollectionKind.Album, a.Id);
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.AlbumTracks(a.Id));
        if (gen != _browseGen) return; // a newer navigation superseded this one
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
    }

    private void OnHomeTrackClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0) PlayFrom(_homeTracks, i); }

    // ---- charts activation ---------------------------------------------------
    private void OnChartsTrackClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0) PlayFrom(_chartsTracks, i); }
    private void OnChartsAlbumClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _chartsAlbums.Count) OpenAlbum(_chartsAlbums[i]); }
    private void OnChartsArtistClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _chartsArtists.Count) OpenArtist(_chartsArtists[i].Id); }
    private void OnChartsPlaylistClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _chartsPlaylists.Count) OpenPlaylist(_chartsPlaylists[i]); }

    // ---- podcasts ------------------------------------------------------------
    private void OnPodcastKey(object s, KeyRoutedEventArgs e) { if (e.Key == VirtualKey.Enter) RunPodcastSearch(); }
    private async void RunPodcastSearch()
    {
        if (!_loggedIn) return;
        string q = _podcastBox.Text;
        if (string.IsNullOrEmpty(q)) return;
        var pods = await Task.Run(() => Wire.ParsePodcasts(DeezerCore.TakeJson(DeezerCore.DZSearchPodcastsJSON(q))));
        _podcasts = pods;
        _artGen++;
        _podcastGrid.Items.Clear();
        for (int i = 0; i < _podcasts.Count; i++)
        {
            var p = _podcasts[i];
            string sub = p.EpisodeCount > 0 ? Loc.Plural("Episodes", p.EpisodeCount) : p.Description;
            _podcastGrid.Items.Add(MakeTile(p.Name, sub, p.ArtworkUrl, i));
        }
    }
    private void OnPodcastClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _podcasts.Count) OpenPodcast(_podcasts[i]); }

    // Episodes load into the shared track list as IsEpisode tracks; clicking a row
    // plays through the episode (plain-stream) path via the unified queue.
    private async void OpenPodcast(Podcast pod)
    {
        _lyricsShown = false;
        _nav.Header = pod.Name;
        _nav.Content = _tracksPage;
        SetCollectionContext(CollectionKind.None, ""); // episodes aren't a downloadable collection
        int gen = ++_browseGen;
        var eps = await Task.Run(() => Wire.ParseEpisodes(DeezerCore.TakeJson(DeezerCore.DZPodcastEpisodesJSON(pod.Id))));
        if (gen != _browseGen) return; // a newer navigation superseded this one
        var tracks = new List<Track>(eps.Count);
        foreach (var e in eps)
        {
            tracks.Add(new Track
            {
                Id = e.Id,
                Name = e.Title,
                ArtistLine = pod.Name,
                AlbumName = pod.Name,
                ArtworkUrl = string.IsNullOrEmpty(e.ArtworkUrl) ? pod.ArtworkUrl : e.ArtworkUrl,
                DurationMs = e.DurationMs,
                IsEpisode = true,
            });
        }
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
    }

    // ---- artist view ---------------------------------------------------------
    private async void OpenArtist(string artistId)
    {
        if (!_loggedIn || string.IsNullOrEmpty(artistId)) return;
        _artistId = artistId; // seed for the artist-radio button
        _lyricsShown = false;
        _nav.Header = Loc.S("Nav_Artist");
        _nav.Content = _artistPage;
        _artistHeader.Text = Loc.S("Status_Loading"); _artistFans.Text = "";
        _artistTopList.Items.Clear();
        _artistAlbumsGrid.Items.Clear();
        _artistRelatedGrid.Items.Clear();
        var prof = await Task.Run(() => DeezerCore.ArtistProfile(artistId));
        _artistTop = prof.Top;
        _artistAlbums = prof.Albums;
        _artistRelated = prof.Related;
        _artistHeader.Text = string.IsNullOrEmpty(prof.Artist.Name) ? Loc.S("Nav_Artist") : prof.Artist.Name;
        _artistFans.Text = Wire.FansText(prof.Artist.NbFans);
        _artGen++;
        FillTrackList(_artistTopList, _artistTop); // reuses MakeTrackRow rows
        FillArtistAlbums();
        FillArtistRelated();
        try { _artistScroll.ChangeView(null, 0.0, null); } catch { } // back to top
    }

    private void FillArtistAlbums()
    {
        _artistAlbumsGrid.Items.Clear();
        for (int i = 0; i < _artistAlbums.Count; i++) _artistAlbumsGrid.Items.Add(MakeTile(_artistAlbums[i].Name, _artistAlbums[i].ArtistLine, _artistAlbums[i].ArtworkUrl, i));
    }
    private void FillArtistRelated()
    {
        _artistRelatedGrid.Items.Clear();
        for (int i = 0; i < _artistRelated.Count; i++) _artistRelatedGrid.Items.Add(MakeTile(_artistRelated[i].Name, Wire.FansText(_artistRelated[i].NbFans), _artistRelated[i].ArtworkUrl, i));
    }
    private void OnArtistTopClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0) PlayFrom(_artistTop, i); }
    private void OnArtistAlbumClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _artistAlbums.Count) OpenAlbum(_artistAlbums[i]); }
    private void OnArtistRelatedClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _artistRelated.Count) OpenArtist(_artistRelated[i].Id); }
    private void OnArtist(object s, RoutedEventArgs e)
    {
        string aid = CurrentArtistId();
        if (string.IsNullOrEmpty(aid)) { _ = ShowMessage(Loc.S("Dialog_NoArtistTitle"), Loc.S("Dialog_NoArtistBody")); return; }
        OpenArtist(aid);
    }

    // ---- lyrics view ---------------------------------------------------------
    private void ShowLyrics()
    {
        if (!_loggedIn) return;
        _lyricsShown = true;
        _nav.Header = Loc.S("Nav_Lyrics");
        _nav.Content = _lyricsPage;
        string id = CurrentTrackId();
        if (string.IsNullOrEmpty(id)) { ShowLyricsMessage(Loc.S("Lyrics_PlayPrompt")); return; }
        LoadLyrics(id);
    }

    private async void LoadLyrics(string trackId)
    {
        if (string.IsNullOrEmpty(trackId)) return;
        if (_lyricsCache.TryGetValue(trackId, out var cached))
        {
            _lyricsTrackId = trackId;
            _lyrics = cached;
            RenderLyrics();
            return;
        }
        int gen = ++_lyricsGen;
        _lyricsTrackId = trackId; // optimistic: stops the tick re-triggering
        ShowLyricsMessage(Loc.S("Lyrics_Loading"));
        var ly = await Task.Run(() => DeezerCore.Lyrics(trackId));
        _lyricsCache[trackId] = ly; // cache regardless of staleness
        if (gen != _lyricsGen) return; // a newer request superseded this one
        _lyrics = ly;
        if (_lyricsShown) RenderLyrics();
    }

    private void RenderLyrics()
    {
        _lyricsPanel.Children.Clear();
        _lyricLineBlocks.Clear();
        _lyricActive = -1;
        if (_lyrics.IsSynced && _lyrics.Synced.Count > 0)
        {
            foreach (var l in _lyrics.Synced)
            {
                var tb = new TextBlock
                {
                    Text = string.IsNullOrEmpty(l.Text) ? "♪" : l.Text, // musical note for blank lines
                    TextWrapping = TextWrapping.Wrap,
                    FontSize = 18,
                    Opacity = 0.45,
                };
                _lyricsPanel.Children.Add(tb);
                _lyricLineBlocks.Add(tb);
            }
            UpdateLyricsHighlight(DeezerCore.DZPositionMS());
        }
        else if (!string.IsNullOrEmpty(_lyrics.Plain))
        {
            _lyricsPanel.Children.Add(new TextBlock { Text = _lyrics.Plain, TextWrapping = TextWrapping.Wrap, FontSize = 16 });
        }
        else
        {
            ShowLyricsMessage(Loc.S("Lyrics_None"));
        }
    }

    private void ShowLyricsMessage(string msg)
    {
        _lyricsPanel.Children.Clear();
        _lyricLineBlocks.Clear();
        _lyricActive = -1;
        _lyricsPanel.Children.Add(new TextBlock { Text = msg, Opacity = 0.7, TextWrapping = TextWrapping.Wrap });
    }

    // Active line = last synced line whose timeMs <= pos. Restyle on change only.
    private void UpdateLyricsHighlight(long pos)
    {
        if (_lyricLineBlocks.Count == 0) return;
        int active = -1;
        for (int i = 0; i < _lyrics.Synced.Count; i++)
        {
            if (_lyrics.Synced[i].TimeMs <= pos) active = i; else break;
        }
        if (active == _lyricActive) return;
        if (_lyricActive >= 0 && _lyricActive < _lyricLineBlocks.Count)
        {
            var prev = _lyricLineBlocks[_lyricActive];
            prev.Opacity = 0.45;
            prev.FontWeight = FontWeights.Normal;
            prev.ClearValue(TextBlock.ForegroundProperty); // back to theme default
        }
        _lyricActive = active;
        if (active >= 0 && active < _lyricLineBlocks.Count)
        {
            var cur = _lyricLineBlocks[active];
            cur.Opacity = 1.0;
            cur.FontWeight = FontWeights.SemiBold;
            cur.Foreground = _accent;
            ScrollLyricToActive();
        }
    }

    private void ScrollLyricToActive()
    {
        if (_lyricActive < 0 || _lyricActive >= _lyricLineBlocks.Count) return;
        var block = _lyricLineBlocks[_lyricActive];
        try
        {
            var gt = block.TransformToVisual(_lyricsPanel); // panel == scroll content
            var pt = gt.TransformPoint(new Point(0, 0));
            double target = pt.Y - _lyricsScroll.ViewportHeight / 2.0 + block.ActualHeight / 2.0; // center the active line
            if (target < 0.0) target = 0.0;
            _lyricsScroll.ChangeView(null, target, null);
        }
        catch { }
    }

    // The now-playing track (head of the active queue), used by both views.
    // When routed over Connect the engine's DZNowPlayingJSON is the authoritative
    // source; the local queue may be on a different track entirely.
    private string CurrentTrackId() =>
        !string.IsNullOrEmpty(_connectedAddr)
            ? _engineNowId
            : (_queueIndex >= 0 && _queueIndex < _queue.Count) ? _queue[_queueIndex].Id : "";
    private string CurrentArtistId() =>
        !string.IsNullOrEmpty(_connectedAddr)
            ? _engineNowArtistId
            : (_queueIndex >= 0 && _queueIndex < _queue.Count) ? _queue[_queueIndex].ArtistId : "";

    // ---- search --------------------------------------------------------------
    private void OnSearchKey(object s, KeyRoutedEventArgs e) { if (e.Key == VirtualKey.Enter) RunSearch(); }
    private async void RunSearch()
    {
        if (!_loggedIn) return;
        string q = _searchBox.Text;
        if (string.IsNullOrEmpty(q)) return;
        var (tracks, artists, albums, plists) = await Task.Run(() =>
        {
            string json = DeezerCore.TakeJson(DeezerCore.DZSearchJSON(q));
            return (Wire.ParseTracks(json), Wire.ParseArtists(json), Wire.ParseAlbums(json), Wire.ParsePlaylists(json));
        });
        _searchTracks = tracks;
        _searchArtists = artists;
        _searchAlbums = albums;
        _searchPlaylists = plists;
        _artGen++;
        FillTrackList(_searchTrackList, _searchTracks);
        FillSearchGrid();
    }

    // ---- item activation -----------------------------------------------------
    private static int TagIndex(object clicked) => clicked is FrameworkElement fe && fe.Tag is int i ? i : -1;
    private void OnTrackClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0) PlayFrom(_tracks, i); }
    private void OnSearchTrackClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0) PlayFrom(_searchTracks, i); }
    private void OnPlaylistClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _playlists.Count) OpenPlaylist(_playlists[i]); }
    private void OnSearchGridClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _searchActions.Count) _searchActions[i](); }

    // ---- OpenDeezer Connect (LAN device picker) ------------------------------
    private async void OnConnectOpened(object sender, object args)
    {
        int gen = ++_connectGen; // drops a previous (slower) open
        _connectList.Items.Clear();
        _connectStatus.Visibility = Visibility.Visible;
        if (!_loggedIn) { _connectStatus.Text = Loc.S("Connect_SignIn"); return; }
        _connectStatus.Text = Loc.S("Connect_Searching");
        var (devs, connAddr) = await Task.Run(() =>
        {
            var d = Wire.ParseConnectDevices(DeezerCore.TakeJson(DeezerCore.DZDiscoverDevices(700)));
            return (d, DeezerCore.ConnectedDevice());
        });
        if (gen != _connectGen) return; // a newer open superseded this one
        _connectDevices = devs;
        string connName = "";
        foreach (var d in _connectDevices) if (d.Addr == connAddr) connName = d.Name;
        UpdateConnectIndicator(connAddr, connName);

        _connectList.Items.Clear();
        // "This computer" (local) -> disconnect. Active when no device is connected.
        _connectList.Items.Add(MakeConnectRow(Loc.S("Connect_ThisComputer"), Loc.S("Connect_LocalPlayback"), string.IsNullOrEmpty(connAddr), -1));
        for (int i = 0; i < _connectDevices.Count; i++)
        {
            var d = _connectDevices[i];
            string sub = Wire.ConnectTypeLabel(d.Client);
            if (!string.IsNullOrEmpty(d.Version)) sub = Loc.Format("Connect_VersionFormat", sub, d.Version);
            _connectList.Items.Add(MakeConnectRow(string.IsNullOrEmpty(d.Name) ? d.Addr : d.Name, sub, d.Addr == connAddr, i));
        }
        if (_connectDevices.Count == 0) _connectStatus.Text = Loc.S("Connect_NoDevices");
        else _connectStatus.Visibility = Visibility.Collapsed;
    }

    private void OnConnectItemClick(object s, ItemClickEventArgs e)
    {
        int i = TagIndex(e.ClickedItem);
        _connectFlyout?.Hide();
        if (i < 0) { DispatchDisconnect(); return; } // -1 = "This computer"
        if (i < _connectDevices.Count)
        {
            var d = _connectDevices[i];
            DispatchConnect(d.Addr, string.IsNullOrEmpty(d.Name) ? d.Addr : d.Name);
        }
    }

    private async void DispatchConnect(string addr, string name)
    {
        if (string.IsNullOrEmpty(addr)) return;
        var (ok, connAddr) = await Task.Run(() =>
        {
            int r = DeezerCore.DZConnectDevice(addr);
            return (r != 0, DeezerCore.ConnectedDevice());
        });
        if (!ok) _ = ShowMessage(Loc.S("Dialog_ConnectFailTitle"), Loc.S("Dialog_ConnectFailBody"));
        UpdateConnectIndicator(connAddr, ok ? name : "");
    }

    private async void DispatchDisconnect()
    {
        await Task.Run(() => DeezerCore.DZDisconnectDevice()); // playback returns to this computer
        UpdateConnectIndicator("", "");
    }

    private void UpdateConnectIndicator(string addr, string name)
    {
        _connectedAddr = addr;
        // Casting chip: visible with "Playing on <device>" while a Connect device
        // owns playback; collapsed (zero footprint) otherwise.
        if (_castChip != null)
        {
            if (!string.IsNullOrEmpty(addr))
            {
                _castChipText.Text = Loc.Format("Connect_PlayingOnFormat", string.IsNullOrEmpty(name) ? addr : name);
                _castChip.Visibility = Visibility.Visible;
            }
            else _castChip.Visibility = Visibility.Collapsed;
        }
        if (_connectBtn == null) return;
        if (!string.IsNullOrEmpty(addr))
        {
            _connectBtn.Foreground = _accent;
            string who = string.IsNullOrEmpty(name) ? addr : name;
            ToolTipService.SetToolTip(_connectBtn, Loc.Format("Connect_PlayingOnFormat", who));
        }
        else
        {
            _connectBtn.ClearValue(Control.ForegroundProperty);
            ToolTipService.SetToolTip(_connectBtn, Loc.S("Tooltip_Connect"));
        }
    }

    // ---- library mutations: like / add-to-playlist / playlist CRUD -----------
    // No "is-liked" query exists, so the heart is a local toggle that resets to off
    // on every track change (SetNowPlaying).
    private void OnLike(object s, RoutedEventArgs e)
    {
        if (_suppressLike) return; // programmatic reset, not a user click
        string id = CurrentTrackId();
        bool want = _likeBtn.IsChecked == true;
        if (string.IsNullOrEmpty(id)) { _suppressLike = true; _likeBtn.IsChecked = false; _suppressLike = false; return; }
        DispatchLike(id, want);
    }
    private async void DispatchLike(string id, bool like)
    {
        // Keep the liked-ids cache in step so the heart stays truthful across track
        // changes (and a row-like updates the now-playing heart when it's the same
        // track). Runs on the UI thread before the blocking engine call.
        if (like) _likedIds.Add(id); else _likedIds.Remove(id);
        if (id == CurrentTrackId()) RefreshLikeForCurrent();
        await Task.Run(() => { if (like) DeezerCore.DZAddFavorite(id); else DeezerCore.DZRemoveFavorite(id); });
    }
    // Seed the liked-ids cache from the engine's favorite-id list (off-thread), then
    // refresh the now-playing heart to match. Called at login and on favorites load.
    private async void SeedLikedIds()
    {
        if (!_loggedIn) return;
        var ids = await Task.Run(() => DeezerCore.FavoriteIDs());
        _likedIds = new HashSet<string>(ids);
        RefreshLikeForCurrent();
    }
    // Set the now-playing heart from the liked-ids cache for the current track.
    private void RefreshLikeForCurrent()
    {
        if (_likeBtn == null) return;
        string id = CurrentTrackId();
        bool liked = !string.IsNullOrEmpty(id) && _likedIds.Contains(id);
        _suppressLike = true; _likeBtn.IsChecked = liked; _suppressLike = false;
    }
    private void OnRowLike(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id && !string.IsNullOrEmpty(id)) DispatchLike(id, true);
    }
    private void OnRowAddToPlaylist(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id && !string.IsNullOrEmpty(id)) ShowAddToPlaylist(id);
    }
    // Premium-only offline export. Downloads the track to the engine's shared
    // default folder ("" destDir) off the UI thread, then reports the saved path
    // or the engine's error via the same modal helper the other row actions use.
    private async void OnRowDownload(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not string id || string.IsNullOrEmpty(id)) return;
        if (!_account.Premium) { _ = ShowMessage(Loc.S("Dialog_DownloadFailTitle"), Loc.S("Menu_DownloadRequiresPremium")); return; }
        string json = await Task.Run(() => DeezerCore.Download(id, "")); // "" -> shared default folder
        string path = "", err = "";
        try
        {
            using var doc = JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json);
            path = doc.RootElement.Str("path");
            err = doc.RootElement.Str("error");
        }
        catch { }
        if (!string.IsNullOrEmpty(path))
            _ = ShowMessage(Loc.S("Dialog_DownloadDoneTitle"), Loc.Format("Dialog_DownloadDoneBodyFormat", path));
        else
            _ = ShowMessage(Loc.S("Dialog_DownloadFailTitle"), string.IsNullOrEmpty(err) ? Loc.S("Dialog_DownloadFailBody") : err);
    }

    // ---- radio (song / artist mixes -> the shared Flow path) -----------------
    private void OnRowStartRadio(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id && !string.IsNullOrEmpty(id)) StartTrackRadio(id);
    }
    private async void StartTrackRadio(string id)
    {
        if (!_loggedIn || string.IsNullOrEmpty(id)) return;
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.TrackMix(id));
        if (gen != _browseGen) return; // superseded by a newer navigation
        LoadRadioResult(Loc.S("Radio_SongHeader"), tracks);
    }
    private async void StartArtistRadio(string artistId)
    {
        if (!_loggedIn || string.IsNullOrEmpty(artistId)) return;
        int gen = ++_browseGen;
        var tracks = await Task.Run(() => DeezerCore.ArtistMix(artistId));
        if (gen != _browseGen) return;
        LoadRadioResult(Loc.S("Radio_ArtistHeader"), tracks);
    }
    // Shared: show the mix in the track list and auto-play its head, like Flow.
    private void LoadRadioResult(string header, List<Track> tracks)
    {
        if (tracks.Count == 0) { _ = ShowMessage(Loc.S("Dialog_RadioFailTitle"), Loc.S("Dialog_RadioFailBody")); return; }
        _lyricsShown = false;
        _nav.Header = header;
        _nav.Content = _tracksPage;
        SetCollectionContext(CollectionKind.None, ""); // a mix isn't a downloadable collection
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
        PlayFrom(_tracks, 0);
    }

    // ---- recently played + listening stats (machine-local history) -----------
    private async void LoadRecent()
    {
        if (!_loggedIn) return;
        int gen = ++_browseGen;
        var (recent, stats) = await Task.Run(() => (DeezerCore.HistoryRecent(100), DeezerCore.HistoryStats(30)));
        if (gen != _browseGen) return; // superseded by a newer navigation
        _recentTracks = recent;
        _recentTopTracks = stats.TopTracks;
        _artGen++; // recent/stat rows carry no artwork, but keep the token monotonic

        // Total listening time over the last 30 days (hours + minutes).
        long sec = stats.TotalSeconds < 0 ? 0 : stats.TotalSeconds;
        _recentTotalText.Text = Loc.Format("Recent_ListenTimeFormat", sec / 3600, (sec % 3600) / 60);

        FillTrackList(_recentList, _recentTracks);

        // Top tracks: reuse the track-row factory (id-playable). The play count is
        // folded into the artist line since the row has no dedicated stat column.
        _recentTopTracksList.Items.Clear();
        for (int i = 0; i < _recentTopTracks.Count; i++)
        {
            var ts = _recentTopTracks[i];
            string playsText = Loc.Plural("Plays", ts.Plays);
            _recentTopTracksList.Items.Add(MakeTrackRow(new Track
            {
                Id = ts.TrackId,
                Name = ts.Title,
                ArtistLine = string.IsNullOrEmpty(ts.Artist) ? playsText : ts.Artist + "  ·  " + playsText,
            }, i));
        }

        // Top artists: a simple non-playable label row (artist + play count).
        _recentTopArtistsList.Items.Clear();
        foreach (var a in stats.TopArtists)
        {
            var g = new Grid { Padding = new Thickness(6, 4, 6, 4), ColumnSpacing = 12 };
            g.ColumnDefinitions.Add(ColStar());
            g.ColumnDefinitions.Add(ColAuto());
            var name = new TextBlock { Text = a.Artist, FontWeight = FontWeights.SemiBold, VerticalAlignment = VerticalAlignment.Center, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
            Grid.SetColumn(name, 0); g.Children.Add(name);
            var plays = new TextBlock { Text = Loc.Plural("Plays", a.Plays), Opacity = 0.6, VerticalAlignment = VerticalAlignment.Center };
            Grid.SetColumn(plays, 1); g.Children.Add(plays);
            _recentTopArtistsList.Items.Add(g);
        }

        _recentEmpty.Visibility = (_recentTracks.Count == 0 && _recentTopTracks.Count == 0 && stats.TopArtists.Count == 0)
            ? Visibility.Visible : Visibility.Collapsed;
        try { _recentScroll.ChangeView(null, 0.0, null); } catch { }
    }
    private void OnRecentTrackClick(object s, ItemClickEventArgs e) { int i = TagIndex(e.ClickedItem); if (i >= 0 && i < _recentTracks.Count) PlayFrom(_recentTracks, i); }
    private void OnRecentTopTrackClick(object s, ItemClickEventArgs e)
    {
        int i = TagIndex(e.ClickedItem);
        if (i < 0 || i >= _recentTopTracks.Count) return;
        var ts = _recentTopTracks[i];
        PlayFrom(new List<Track> { new Track { Id = ts.TrackId, Name = ts.Title, ArtistLine = ts.Artist } }, 0);
    }

    // ---- download an album / playlist (PREMIUM-only batch export) ------------
    // The action bar over the shared track list carries the current collection
    // context so "Download album/playlist" knows what to fetch.
    private enum CollectionKind { None, Album, Playlist }
    private void SetCollectionContext(CollectionKind kind, string id)
    {
        _collectionKind = kind;
        _collectionId = id;
        if (_tracksActionBar == null) return;
        if (kind == CollectionKind.None || string.IsNullOrEmpty(id))
        {
            _tracksActionBar.Visibility = Visibility.Collapsed;
            return;
        }
        _tracksActionBar.Visibility = Visibility.Visible;
        _tracksDownloadLabel.Text = kind == CollectionKind.Album ? Loc.S("Btn_DownloadAlbum") : Loc.S("Btn_DownloadPlaylist");
        // Premium-gate exactly like the single-track download.
        _tracksDownloadBtn.IsEnabled = _account.Premium;
        ToolTipService.SetToolTip(_tracksDownloadBtn, _account.Premium ? null : Loc.S("Menu_DownloadRequiresPremium"));
    }
    private async void OnDownloadCollection(object sender, RoutedEventArgs e)
    {
        var kind = _collectionKind;
        string id = _collectionId;
        if (kind == CollectionKind.None || string.IsNullOrEmpty(id)) return;
        if (!_account.Premium) { _ = ShowMessage(Loc.S("Dialog_DownloadFailTitle"), Loc.S("Menu_DownloadRequiresPremium")); return; }
        // Busy state: the batch download blocks (per-track network + decrypt).
        _tracksDownloadBtn.IsEnabled = false;
        string prevText = _tracksDownloadLabel.Text;
        _tracksDownloadLabel.Text = Loc.S("Status_Downloading");
        string json = await Task.Run(() => DeezerCore.TakeJson(
            kind == CollectionKind.Album ? DeezerCore.DZDownloadAlbum(id) : DeezerCore.DZDownloadPlaylist(id)));
        _tracksDownloadLabel.Text = prevText;
        _tracksDownloadBtn.IsEnabled = _account.Premium;
        long saved = 0, failed = 0; string dir = "", err = "";
        try
        {
            using var doc = JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json);
            var o = doc.RootElement;
            saved = o.Num("saved");
            failed = o.Num("failed");
            dir = o.Str("dir");
            err = o.Str("error");
        }
        catch { }
        if (saved > 0)
            _ = ShowMessage(Loc.S("Dialog_DownloadDoneTitle"), Loc.Format("Dialog_DownloadBatchDoneFormat", saved, failed, dir));
        else
            _ = ShowMessage(Loc.S("Dialog_DownloadFailTitle"), string.IsNullOrEmpty(err) ? Loc.S("Dialog_DownloadFailBody") : err);
    }

    private void OnAddCurrentToPlaylist(object s, RoutedEventArgs e)
    {
        string id = CurrentTrackId();
        if (string.IsNullOrEmpty(id)) { _ = ShowMessage(Loc.S("Dialog_NoTrackTitle"), Loc.S("Dialog_NoTrackBody")); return; }
        ShowAddToPlaylist(id);
    }

    // Picker: "New playlist…" + the user's playlists. Selection adds the track;
    // "New playlist…" prompts for a name, creates it, then adds the track.
    private async void ShowAddToPlaylist(string trackId)
    {
        if (!_loggedIn || string.IsNullOrEmpty(trackId)) return;
        var plists = await Task.Run(() => DeezerCore.Playlists());

        var list = new ListView { SelectionMode = ListViewSelectionMode.Single, MaxHeight = 360, MinWidth = 320 };
        list.Items.Add(new TextBlock { Text = Loc.S("Dialog_NewPlaylistItem") }); // index 0
        foreach (var p in plists) list.Items.Add(new TextBlock { Text = p.Name });
        list.SelectedIndex = plists.Count == 0 ? 0 : 1;

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Dialog_AddToPlaylistTitle"),
            Content = list,
            PrimaryButtonText = Loc.S("Btn_Add"),
            CloseButtonText = Loc.S("Btn_Cancel"),
            DefaultButton = ContentDialogButton.Primary,
        };
        if (await ShowDialog(dlg) != ContentDialogResult.Primary) return;

        int idx = list.SelectedIndex;
        if (idx < 0) return;
        string playlistId;
        if (idx == 0) // New playlist…
        {
            string name = (await PromptText(Loc.S("Dialog_NewPlaylistTitle"), Loc.S("Dialog_PlaylistNamePlaceholder"), "")).Trim();
            if (string.IsNullOrEmpty(name)) return;
            playlistId = await Task.Run(() => Wire.ParseCreatedId(DeezerCore.TakeJson(DeezerCore.DZCreatePlaylist(name))));
            if (string.IsNullOrEmpty(playlistId)) { _ = ShowMessage(Loc.S("Dialog_CreateFailTitle"), Loc.S("Dialog_CreateFailBody")); return; }
        }
        else
        {
            int pi = idx - 1;
            if (pi < 0 || pi >= plists.Count) return;
            playlistId = plists[pi].Id;
        }
        if (string.IsNullOrEmpty(playlistId)) return;
        bool ok = await Task.Run(() => DeezerCore.DZAddToPlaylist(playlistId, trackId) != 0);
        if (!ok) _ = ShowMessage(Loc.S("Dialog_AddFailTitle"), Loc.S("Dialog_AddFailBody"));
    }

    private async void OnNewPlaylist(object s, RoutedEventArgs e)
    {
        if (!_loggedIn) return;
        string name = (await PromptText(Loc.S("Dialog_NewPlaylistTitle"), Loc.S("Dialog_PlaylistNamePlaceholder"), "")).Trim();
        if (string.IsNullOrEmpty(name)) return;
        string newId = await Task.Run(() => Wire.ParseCreatedId(DeezerCore.TakeJson(DeezerCore.DZCreatePlaylist(name))));
        if (string.IsNullOrEmpty(newId)) { _ = ShowMessage(Loc.S("Dialog_CreateFailTitle"), Loc.S("Dialog_CreateFailBody")); return; }
        LoadPlaylists(); // refresh the grid
    }

    private async void OnPlaylistRename(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not int i || i < 0 || i >= _playlists.Count) return;
        var p = _playlists[i];
        string name = (await PromptText(Loc.S("Dialog_RenamePlaylistTitle"), Loc.S("Dialog_PlaylistNamePlaceholder"), p.Name)).Trim();
        if (string.IsNullOrEmpty(name)) return;
        bool ok = await Task.Run(() => DeezerCore.DZRenamePlaylist(p.Id, name) != 0);
        if (!ok) { _ = ShowMessage(Loc.S("Dialog_RenameFailTitle"), Loc.S("Dialog_RenameFailBody")); return; }
        LoadPlaylists();
    }

    private async void OnPlaylistDelete(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not int i || i < 0 || i >= _playlists.Count) return;
        var p = _playlists[i];
        bool yes = await Confirm(Loc.S("Dialog_DeletePlaylistTitle"), Loc.Format("Dialog_DeletePlaylistBodyFormat", p.Name), Loc.S("Btn_Delete"));
        if (!yes) return;
        bool ok = await Task.Run(() => DeezerCore.DZDeletePlaylist(p.Id) != 0);
        if (!ok) { _ = ShowMessage(Loc.S("Dialog_DeleteFailTitle"), Loc.S("Dialog_DeleteFailBody")); return; }
        LoadPlaylists();
    }

    // Small reusable modal helpers (single-line text entry + yes/no confirm).
    private async Task<string> PromptText(string title, string placeholder, string initial)
    {
        var tb = new TextBox { PlaceholderText = placeholder, Text = initial, AcceptsReturn = false };
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = title,
            Content = tb,
            PrimaryButtonText = Loc.S("Btn_OK"),
            CloseButtonText = Loc.S("Btn_Cancel"),
            DefaultButton = ContentDialogButton.Primary,
        };
        return await ShowDialog(dlg) == ContentDialogResult.Primary ? tb.Text : "";
    }
    private async Task<bool> Confirm(string title, string body, string okText)
    {
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = title,
            Content = new TextBlock { Text = body, TextWrapping = TextWrapping.Wrap },
            PrimaryButtonText = okText,
            CloseButtonText = Loc.S("Btn_Cancel"),
            DefaultButton = ContentDialogButton.Close,
        };
        return await ShowDialog(dlg) == ContentDialogResult.Primary;
    }

    // ---- playback ------------------------------------------------------------
    private void PlayFrom(List<Track> list, int index)
    {
        _queue = new List<Track>(list);
        _queueIndex = index;
        SyncEngineQueueSet(); // mirror the new queue + cursor into the engine
        PlayCurrent();
    }
    private void PlayCurrent()
    {
        if (_queueIndex < 0 || _queueIndex >= _queue.Count) return;
        var t = _queue[_queueIndex];
        SetNowPlaying(t);
        _updatingSeek = true;
        _seek.Maximum = t.DurationMs > 0 ? t.DurationMs : 1;
        _seek.Value = 0;
        _updatingSeek = false;
        _posText.Text = Wire.TimeText(0);
        _durText.Text = Wire.TimeText(t.DurationMs);
        // Gapless: warm the deterministic next track (real tracks only, never
        // shuffle / repeat-one).
        string nextId = ""; long nextDur = 0;
        if (_settings.Gapless && !t.IsEpisode && HasDeterministicNext(out int n))
        {
            nextId = _queue[n].Id; nextDur = _queue[n].DurationMs;
        }
        DispatchPlay(t.Id, t.DurationMs, t.IsEpisode, nextId, nextDur);
    }
    // Blocking play (then optional preload) chained onto one serialized background
    // task so DZPlay calls reach the engine in click order, plus a generation
    // counter so a play superseded by a faster follow-up click is skipped entirely
    // (otherwise the slower network response wins and arms a stale gapless preload).
    private void DispatchPlay(string id, long dur, bool episode, string nextId, long nextDur)
    {
        int gen = ++_playDispatchGen;
        _playChain = _playChain.ContinueWith(_ =>
        {
            if (gen != Volatile.Read(ref _playDispatchGen)) return; // superseded while queued
            if (episode) DeezerCore.DZPlayEpisode(id, dur); // plain, unencrypted stream
            else DeezerCore.DZPlay(id, dur);                // prepares the stream over the network -> blocks
            if (gen != Volatile.Read(ref _playDispatchGen)) return; // superseded mid-play -> its preload is stale
            if (!string.IsNullOrEmpty(nextId)) DeezerCore.DZPreload(nextId, nextDur); // warm next for the gapless swap
        }, TaskScheduler.Default);
    }
    private void DispatchPreload(string id, long dur)
    {
        int gen = _playDispatchGen; // no new play: preload only while still current
        _playChain = _playChain.ContinueWith(_ =>
        {
            if (gen != Volatile.Read(ref _playDispatchGen)) return;
            DeezerCore.DZPreload(id, dur);
        }, TaskScheduler.Default);
    }

    // ---- engine queue sync (GUI queue -> engine) -----------------------------
    // Mirror the GUI queue into the engine so remote controllers see it on /status
    // and the engine can own natural-finish auto-advance (its AdvanceAuto honors
    // DZSetRepeat / DZSetShuffle, which the transport already pushes). Once the
    // cursor is aligned the engine drives finishes itself and DZFinishedCount stops
    // bumping for them -- OnTick polls DZQueueIndex to keep _queueIndex aligned.
    // Podcast-episode queues are NOT mirrored: the engine's advance resolves
    // through the music-stream path, not DZPlayEpisode, so an episode queue clears
    // the engine queue and keeps the GUI's own DZFinishedCount-driven advance.
    private static bool IsEpisodeQueue(List<Track> q)
    {
        foreach (var t in q) if (t.IsEpisode) return true;
        return false;
    }
    // One Track -> the engine's queue-element JSON object (the shape DZQueueSet /
    // DZQueueInsertNext parse; only "id" is required, the rest keep remote /status
    // rows and a re-synced queue fully labelled).
    private static JsonObject TrackToJsonObject(Track t)
    {
        var artists = new JsonArray();
        if (!string.IsNullOrEmpty(t.ArtistId) || !string.IsNullOrEmpty(t.ArtistLine))
            artists.Add(new JsonObject { ["id"] = t.ArtistId, ["name"] = t.ArtistLine });
        return new JsonObject
        {
            ["id"] = t.Id,
            ["name"] = t.Name,
            ["durationMs"] = t.DurationMs,
            ["artistLine"] = t.ArtistLine,
            ["artistId"] = t.ArtistId,
            ["artists"] = artists,
            ["albumName"] = t.AlbumName,
            ["artworkUrl"] = t.ArtworkUrl,
            ["explicit"] = t.IsExplicit,
        };
    }
    private static string BuildQueueJson(List<Track> q)
    {
        var arr = new JsonArray();
        foreach (var t in q) arr.Add(TrackToJsonObject(t));
        return arr.ToJsonString();
    }
    // (Re)build: replace the engine queue + align the cursor. The index push is
    // gen-guarded so a later cursor move wins even if the tasks finish out of order.
    private async void SyncEngineQueueSet()
    {
        bool episode = IsEpisodeQueue(_queue);
        _queueSynced = !episode && _queue.Count > 0;
        string json = episode ? "[]" : BuildQueueJson(_queue);
        int idx = _queueIndex;
        int gen = ++_queueSyncIndexGen;
        _queueSyncPending++;
        try
        {
            await Task.Run(() =>
            {
                DeezerCore.DZQueueSet(json); // always: replace the engine queue content
                if (!episode && idx >= 0 && gen == Volatile.Read(ref _queueSyncIndexGen))
                    DeezerCore.DZQueueSetIndex(idx);
            });
        }
        finally { _queueSyncPending--; }
    }
    // Cursor-only: align the engine cursor to the GUI's playing row (music only).
    private async void SyncEngineQueueIndex()
    {
        if (!_queueSynced || _queueIndex < 0) return;
        int idx = _queueIndex;
        int gen = ++_queueSyncIndexGen;
        _queueSyncPending++;
        try
        {
            await Task.Run(() =>
            {
                if (gen != Volatile.Read(ref _queueSyncIndexGen)) return; // superseded by a newer move
                DeezerCore.DZQueueSetIndex(idx);
            });
        }
        finally { _queueSyncPending--; }
    }
    // The next queue index when advance is deterministic (mirrors Next()'s ordering).
    private bool HasDeterministicNext(out int outIndex)
    {
        outIndex = -1;
        if (_shuffle || _repeat == 2 || _queue.Count == 0) return false;
        int n;
        if (_queueIndex + 1 < _queue.Count) n = _queueIndex + 1;
        else if (_repeat == 1) n = 0;
        else return false;
        if (n < 0 || n >= _queue.Count) return false;
        if (_queue[n].IsEpisode) return false; // episodes don't use the preload swap
        outIndex = n;
        return true;
    }
    // Engine already gaplessly swapped to the preloaded next: advance the UI's
    // queue pointer + now-playing WITHOUT re-issuing play, then warm the new next.
    private void AdvanceUiToPreloaded(int n)
    {
        _queueIndex = n;
        var t = _queue[_queueIndex];
        SetNowPlaying(t);
        _updatingSeek = true;
        _seek.Maximum = t.DurationMs > 0 ? t.DurationMs : 1;
        _seek.Value = 0;
        _updatingSeek = false;
        _posText.Text = Wire.TimeText(0);
        _durText.Text = Wire.TimeText(t.DurationMs);
        if (_settings.Gapless && !t.IsEpisode && HasDeterministicNext(out int n2))
            DispatchPreload(_queue[n2].Id, _queue[n2].DurationMs);
        SyncEngineQueueIndex(); // keep the engine cursor on the promoted row
    }
    private void SetNowPlaying(Track t)
    {
        _nowId = t.Id; // anchor for the engine-truth poll in OnTick
        _nowTitle.Text = t.IsExplicit ? "\U0001F174 " + t.Name : t.Name; // enclosed-E for explicit
        _curArtist = t.ArtistLine;
        _nowArtist.Text = t.ArtistLine;
        _cover.Source = null;
        int token = ++_playGen;
        if (!string.IsNullOrEmpty(t.ArtworkUrl)) LoadArt(_cover, t.ArtworkUrl, token, true);
        // Seed the heart from the liked-ids cache (populated at login / favorites
        // load) so it reflects real library state per track, not a blanket off.
        // Like / add-to-playlist are library-track only, so disable them for episodes.
        if (_likeBtn != null)
        {
            bool liked = !t.IsEpisode && _likedIds.Contains(t.Id);
            _suppressLike = true; _likeBtn.IsChecked = liked; _suppressLike = false;
            _likeBtn.IsEnabled = !t.IsEpisode;
        }
        if (_addBtn != null) _addBtn.IsEnabled = !t.IsEpisode;
        // Download for offline: premium-only and never for podcast episodes. Point the
        // tooltip at the premium requirement when the account can't use it.
        if (_downloadBtn != null)
        {
            _downloadBtn.IsEnabled = _account.Premium && !t.IsEpisode;
            ToolTipService.SetToolTip(_downloadBtn, _account.Premium ? Loc.S("Tooltip_DownloadOffline") : Loc.S("Menu_DownloadRequiresPremium"));
        }
        UpdateSmtcMetadata(t); // push to the OS media overlay / lock screen
    }
    private void Next()
    {
        if (_queue.Count == 0) return;
        if (_shuffle && _queue.Count > 1)
        {
            int n = _queueIndex;
            while (n == _queueIndex) n = _rng.Next(_queue.Count);
            _queueIndex = n;
        }
        else if (_queueIndex + 1 < _queue.Count) { ++_queueIndex; }
        else if (_repeat == 1) { _queueIndex = 0; }
        else { return; }
        PlayCurrent();
        SyncEngineQueueIndex(); // realign the engine cursor to the new row
    }
    private void Prev()
    {
        if (_queue.Count == 0) return;
        if (_queueIndex > 0) --_queueIndex;
        PlayCurrent();
        SyncEngineQueueIndex();
    }

    // ---- Up-Next queue panel (transport flyout) ------------------------------
    // Backed by the GUI's authoritative _queue / _queueIndex. Structural edits
    // mutate that model first, then apply the SAME granular edit to the engine
    // queue (only while _queueSynced -- an episode queue is engine-unsynced) and
    // re-assert the cursor, reusing the newest-wins _queueSyncIndexGen guard the
    // engine-sync path already uses. The panel is rebuilt on open and after every
    // edit; OnTick refreshes the highlight live while it is open.
    private void OnQueueFlyoutOpened(object sender, object args) { _queueOpen = true; RefreshQueuePanel(); }
    private void OnQueueFlyoutClosed(object sender, object args) { _queueOpen = false; }

    private void RefreshQueuePanel()
    {
        if (_queueList == null) return;
        _queueRenderedIndex = _queueIndex;
        _queueList.Items.Clear();
        if (_queue.Count == 0)
        {
            _queueStatus.Text = Loc.S("Queue_Empty");
            _queueStatus.Visibility = Visibility.Visible;
            if (_queueClearBtn != null) _queueClearBtn.IsEnabled = false;
            return;
        }
        _queueStatus.Visibility = Visibility.Collapsed;
        if (_queueClearBtn != null) _queueClearBtn.IsEnabled = true;
        for (int i = 0; i < _queue.Count; i++)
            _queueList.Items.Add(MakeQueueRow(_queue[i], i));
    }

    // One Up-Next row: play-indicator (current) + title/artist + move up/down +
    // remove. Tag carries the queue index (row click jumps to it).
    private UIElement MakeQueueRow(Track t, int index)
    {
        bool current = index == _queueIndex;
        var g = new Grid { Tag = index, Padding = new Thickness(4, 2, 2, 2), ColumnSpacing = 8, MinWidth = 300 };
        g.ColumnDefinitions.Add(ColAuto());   // 0 play indicator
        g.ColumnDefinitions.Add(ColStar());   // 1 title/artist
        g.ColumnDefinitions.Add(ColAuto());   // 2 move up
        g.ColumnDefinitions.Add(ColAuto());   // 3 move down
        g.ColumnDefinitions.Add(ColAuto());   // 4 remove
        if (current)
        {
            var nowIcon = new FontIcon { Glyph = "", FontSize = 12, Foreground = _accent, VerticalAlignment = VerticalAlignment.Center }; // Play
            Grid.SetColumn(nowIcon, 0); g.Children.Add(nowIcon);
        }
        var sp = new StackPanel { VerticalAlignment = VerticalAlignment.Center };
        sp.Children.Add(new TextBlock
        {
            Text = t.Name,
            FontWeight = current ? FontWeights.SemiBold : FontWeights.Normal,
            Foreground = current ? _accent : null,
            TextWrapping = TextWrapping.NoWrap,
            TextTrimming = TextTrimming.CharacterEllipsis,
        });
        sp.Children.Add(new TextBlock { Text = t.ArtistLine, Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis });
        Grid.SetColumn(sp, 1); g.Children.Add(sp);
        var up = QueueIconButton("", Loc.S("Queue_MoveUp"), index);   // ChevronUp
        up.IsEnabled = index > 0;
        up.Click += OnQueueMoveUp;
        Grid.SetColumn(up, 2); g.Children.Add(up);
        var down = QueueIconButton("", Loc.S("Queue_MoveDown"), index); // ChevronDown
        down.IsEnabled = index < _queue.Count - 1;
        down.Click += OnQueueMoveDown;
        Grid.SetColumn(down, 3); g.Children.Add(down);
        var rm = QueueIconButton("", Loc.S("Queue_Remove"), index);    // Delete
        rm.Click += OnQueueRemove;
        Grid.SetColumn(rm, 4); g.Children.Add(rm);
        return g;
    }
    private static Button QueueIconButton(string glyph, string tip, int index)
    {
        var b = new Button
        {
            Content = new FontIcon { Glyph = glyph, FontSize = 12 },
            Tag = index,
            Padding = new Thickness(6, 2, 6, 2),
            Background = new SolidColorBrush(Colors.Transparent),
            BorderThickness = new Thickness(0),
            VerticalAlignment = VerticalAlignment.Center,
        };
        ToolTipService.SetToolTip(b, tip);
        AutomationProperties.SetName(b, tip);
        return b;
    }

    private void OnQueueRowClick(object s, ItemClickEventArgs e)
    {
        int i = TagIndex(e.ClickedItem);
        if (i < 0 || i >= _queue.Count || i == _queueIndex) return;
        _queueIndex = i;
        PlayCurrent();
        SyncEngineQueueIndex();  // realign the engine cursor to the jumped row
        RefreshQueuePanel();
    }
    private void OnQueueMoveUp(object s, RoutedEventArgs e)
    {
        int i = TagIndex(s); if (i > 0) MoveQueueItem(i, i - 1);
    }
    private void OnQueueMoveDown(object s, RoutedEventArgs e)
    {
        int i = TagIndex(s); if (i >= 0 && i < _queue.Count - 1) MoveQueueItem(i, i + 1);
    }
    private void OnQueueRemove(object s, RoutedEventArgs e)
    {
        int i = TagIndex(s); if (i >= 0) RemoveQueueItem(i);
    }

    // Relocate a queue row, adjust the play cursor the way a list-move does, mirror
    // the edit into the engine, and repaint the panel.
    private void MoveQueueItem(int from, int to)
    {
        if (from < 0 || from >= _queue.Count || to < 0 || to >= _queue.Count || from == to) return;
        var t = _queue[from];
        _queue.RemoveAt(from);
        _queue.Insert(to, t);
        if (from == _queueIndex) _queueIndex = to;
        else if (from < _queueIndex && to >= _queueIndex) _queueIndex--;
        else if (from > _queueIndex && to <= _queueIndex) _queueIndex++;
        ApplyEngineQueueEdit(() => DeezerCore.DZQueueMove(from, to));
        RearmPreloadAfterQueueEdit();
        RefreshQueuePanel();
    }
    // Remove a queue row. Removing the playing row starts the track that slides into
    // its slot (or stops when the queue empties); otherwise playback is untouched.
    // The play cursor is adjusted FIRST so the single engine task (remove + cursor
    // re-assert) carries the final index -- no separate, racy cursor push.
    private void RemoveQueueItem(int i)
    {
        if (i < 0 || i >= _queue.Count) return;
        bool wasCurrent = i == _queueIndex;
        _queue.RemoveAt(i);
        if (_queue.Count == 0) _queueIndex = -1;
        else if (wasCurrent) { if (_queueIndex >= _queue.Count) _queueIndex = _queue.Count - 1; }
        else if (i < _queueIndex) _queueIndex--; // cursor slid down under the removed row
        ApplyEngineQueueEdit(() => DeezerCore.DZQueueRemove(i));
        if (wasCurrent && _queueIndex >= 0) PlayCurrent(); // start the track now in this slot (arms its own preload)
        else RearmPreloadAfterQueueEdit();                 // a plain edit may change the deterministic next
        RefreshQueuePanel();
    }
    // "Clear queue": drop everything except the currently playing track (keeps
    // playback going), then re-push the trimmed queue to the engine.
    private void OnQueueClear(object s, RoutedEventArgs e)
    {
        if (_queue.Count == 0) return;
        if (_queueIndex >= 0 && _queueIndex < _queue.Count)
        {
            _queue = new List<Track> { _queue[_queueIndex] };
            _queueIndex = 0;
        }
        else { _queue = new List<Track>(); _queueIndex = -1; }
        SyncEngineQueueSet();
        RearmPreloadAfterQueueEdit();
        RefreshQueuePanel();
    }

    // Track-menu "Play next": insert right after the current row (via the engine's
    // insert-next export); an empty/idle queue just starts the track.
    private void QueuePlayNext(Track t)
    {
        if (string.IsNullOrEmpty(t.Id)) return;
        if (_queue.Count == 0 || _queueIndex < 0) { PlayFrom(new List<Track> { t }, 0); return; }
        int at = Math.Min(_queueIndex + 1, _queue.Count);
        _queue.Insert(at, t);
        string js = TrackToJsonObject(t).ToJsonString();
        ApplyEngineQueueEdit(() => DeezerCore.DZQueueInsertNext(js));
        RearmPreloadAfterQueueEdit();
        RefreshQueuePanel();
    }
    // Track-menu "Add to queue": append at the end (no dedicated engine export, so
    // re-push the whole queue); an empty/idle queue just starts the track.
    private void QueueAppend(Track t)
    {
        if (string.IsNullOrEmpty(t.Id)) return;
        if (_queue.Count == 0 || _queueIndex < 0) { PlayFrom(new List<Track> { t }, 0); return; }
        _queue.Add(t);
        SyncEngineQueueSet();
        RearmPreloadAfterQueueEdit();
        RefreshQueuePanel();
    }
    private void OnRowPlayNext(Track t) => QueuePlayNext(t);
    private void OnRowAddToQueue(Track t) => QueueAppend(t);

    // Apply a granular structural edit to the engine queue, then re-assert the
    // cursor from the (already-updated) GUI model so the engine matches regardless
    // of how the export handles the cursor. Skipped when the queue is not mirrored
    // (episode / empty). Off-thread like every blocking DZ* call, and gen-guarded
    // (shared counter) so a later edit's cursor wins on out-of-order completion.
    private async void ApplyEngineQueueEdit(Action engineEdit)
    {
        if (!_queueSynced) return;
        int idx = _queueIndex;
        int gen = ++_queueSyncIndexGen;
        _queueSyncPending++;
        try
        {
            await Task.Run(() =>
            {
                engineEdit();
                if (idx >= 0 && gen == Volatile.Read(ref _queueSyncIndexGen))
                    DeezerCore.DZQueueSetIndex(idx);
            });
        }
        finally { _queueSyncPending--; }
    }
    // A structural edit can change the deterministic next track: re-arm the gapless
    // preload onto the new next, or drop a now-stale one (DZClearPreload's case).
    private void RearmPreloadAfterQueueEdit()
    {
        if (!_settings.Gapless) return;
        if (HasDeterministicNext(out int n)) DispatchPreload(_queue[n].Id, _queue[n].DurationMs);
        else
        {
            int gen = _playDispatchGen;
            _playChain = _playChain.ContinueWith(_ =>
            {
                if (gen == Volatile.Read(ref _playDispatchGen)) DeezerCore.DZClearPreload();
            }, TaskScheduler.Default);
        }
    }

    // ---- offline caching (Download for offline; PREMIUM-ONLY) ----------------
    // Cache a track for offline playback off the UI thread (blocking, forwards over
    // HTTP when casting), then flash a transient InfoBar and stamp the "downloaded"
    // glyph on its row (id echoed back as {key}).
    private async void DownloadForOffline(string id)
    {
        if (string.IsNullOrEmpty(id)) return;
        if (!_account.Premium)
        {
            ShowOfflineInfo(false, Loc.S("Offline_FailTitle"), Loc.S("Menu_DownloadRequiresPremium"));
            return;
        }
        string json = await Task.Run(() => DeezerCore.DownloadForOffline(id));
        string key = "", err = "";
        try
        {
            using var doc = JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json);
            key = doc.RootElement.Str("key");
            err = doc.RootElement.Str("error");
        }
        catch { }
        if (string.IsNullOrEmpty(err))
        {
            MarkOffline(string.IsNullOrEmpty(key) ? id : key);
            ShowOfflineInfo(true, Loc.S("Offline_DoneTitle"), Loc.S("Offline_DoneBody"));
        }
        else ShowOfflineInfo(false, Loc.S("Offline_FailTitle"), err);
    }
    // Record an offline track id and, if it is visible in the current list, repaint
    // so its "downloaded" glyph appears immediately.
    private void MarkOffline(string id)
    {
        if (string.IsNullOrEmpty(id) || !_offlineIds.Add(id)) return;
        if (_trackList != null && _tracks.Exists(t => t.Id == id)) FillTrackList(_trackList, _tracks);
    }
    // Transient status banner (auto-dismisses); reuses the top-of-window InfoBar row.
    private void ShowOfflineInfo(bool success, string title, string message)
    {
        if (_offlineInfoBar == null) return;
        _offlineInfoBar.Severity = success ? InfoBarSeverity.Success : InfoBarSeverity.Error;
        _offlineInfoBar.Title = title;
        _offlineInfoBar.Message = message;
        _offlineInfoBar.IsOpen = true;
        _offlineInfoTimer ??= CreateOfflineInfoTimer();
        _offlineInfoTimer.Stop();
        _offlineInfoTimer.Start();
    }
    private DispatcherQueueTimer CreateOfflineInfoTimer()
    {
        var t = DispatcherQueue.CreateTimer();
        t.Interval = TimeSpan.FromSeconds(5);
        t.IsRepeating = false;
        t.Tick += (s, _) => { s.Stop(); if (_offlineInfoBar != null) _offlineInfoBar.IsOpen = false; };
        return t;
    }
    private void OnDownloadCurrentOffline(object s, RoutedEventArgs e) => DownloadForOffline(CurrentTrackId());

    // Off-thread like every other blocking DZ* call: when routed over Connect these
    // forward over HTTP (15 s timeout), so they must never run on the dispatcher.
    private async void OnShuffle(object s, RoutedEventArgs e)
    {
        ApplyShuffleDisplay(_shuffleBtn.IsChecked == true);
        _lastTransportTick = Environment.TickCount64; // hold off the engine-truth reconcile briefly
        int on = _shuffle ? 1 : 0;
        // Next is no longer deterministic -> drop any armed gapless preload so a
        // stale next track can't be swapped in (DZClearPreload's documented case).
        bool clearPreload = !HasDeterministicNext(out _);
        await Task.Run(() =>
        {
            DeezerCore.DZSetShuffle(on);
            if (clearPreload) DeezerCore.DZClearPreload();
        });
    }
    private async void OnRepeat(object s, RoutedEventArgs e)
    {
        _repeat = (_repeat + 1) % 3;
        _repeatIcon.Glyph = _repeat == 2 ? "" : ""; // RepeatOne or RepeatAll
        if (_repeat == 0) _repeatBtn.ClearValue(Control.ForegroundProperty); else _repeatBtn.Foreground = _accent;
        ToolTipService.SetToolTip(_repeatBtn, _repeat == 0 ? Loc.S("Tooltip_RepeatOff") : _repeat == 1 ? Loc.S("Tooltip_RepeatAll") : Loc.S("Tooltip_RepeatOne"));
        AutomationProperties.SetName(_repeatBtn, _repeat == 0 ? Loc.S("Tooltip_RepeatOff") : _repeat == 1 ? Loc.S("Tooltip_RepeatAll") : Loc.S("Tooltip_RepeatOne"));
        _lastTransportTick = Environment.TickCount64; // hold off the engine-truth reconcile briefly
        int mode = _repeat;
        bool clearPreload = !HasDeterministicNext(out _); // repeat-one never preloads; drop a stale one
        await Task.Run(() =>
        {
            DeezerCore.DZSetRepeat(mode);
            if (clearPreload) DeezerCore.DZClearPreload();
        });
    }
    // Reflect a repeat mode (0 off / 1 all / 2 one) on the transport WITHOUT
    // sending a command -- used by the OnTick engine-truth reconcile (and casting).
    // Glyphs are the Segoe MDL2 RepeatOne / RepeatAll (as \u so the source stays
    // ASCII); they match the literal glyphs OnRepeat sets on a local toggle.
    private void ApplyRepeatDisplay(int mode)
    {
        _repeat = mode;
        _repeatIcon.Glyph = mode == 2 ? "" : ""; // RepeatOne / RepeatAll
        if (mode == 0) _repeatBtn.ClearValue(Control.ForegroundProperty); else _repeatBtn.Foreground = _accent;
        string name = mode == 0 ? Loc.S("Tooltip_RepeatOff") : mode == 1 ? Loc.S("Tooltip_RepeatAll") : Loc.S("Tooltip_RepeatOne");
        ToolTipService.SetToolTip(_repeatBtn, name);
        AutomationProperties.SetName(_repeatBtn, name);
    }
    // Reflect a shuffle state on the toggle without firing OnShuffle (Click is a
    // user-only event; a programmatic IsChecked change never re-invokes it).
    private void ApplyShuffleDisplay(bool on)
    {
        _shuffle = on;
        if (_shuffleBtn.IsChecked != on) _shuffleBtn.IsChecked = on;
        if (on) _shuffleBtn.Foreground = _accent; else _shuffleBtn.ClearValue(Control.ForegroundProperty);
    }
    private void OnSeekChanged(object s, RangeBaseValueChangedEventArgs e)
    {
        if (_updatingSeek) return; // programmatic update from the poll tick
        long ms = (long)Math.Round(e.NewValue);
        _posText.Text = Wire.TimeText(ms);
        _lastSeekTick = Environment.TickCount64;
        _pendingSeekMs = ms;
        PumpSeek();
    }
    // Coalescing pump (UI-thread state only): one DZSeek in flight at a time; drag
    // ticks arriving mid-flight collapse into a single trailing call with the
    // latest value, so a slider drag never queues blocking round-trips.
    private async void PumpSeek()
    {
        if (_seekInFlight) { _seekDirty = true; return; }
        _seekInFlight = true;
        do
        {
            _seekDirty = false;
            long ms = _pendingSeekMs;
            await Task.Run(() => DeezerCore.DZSeek(ms));
        } while (_seekDirty);
        _seekInFlight = false;
        _lastSeekTick = Environment.TickCount64; // hold the poll off until after the last commit
    }
    private void OnVolumeChanged(object s, RangeBaseValueChangedEventArgs e)
    {
        if (_updatingVol) return;
        _pendingVolume = e.NewValue / 100.0;
        PumpVolume();
    }
    private async void PumpVolume()
    {
        if (_volInFlight) { _volDirty = true; return; }
        _volInFlight = true;
        do
        {
            _volDirty = false;
            double v = _pendingVolume;
            await Task.Run(() => DeezerCore.DZSetVolume(v));
        } while (_volDirty);
        _volInFlight = false;
    }

    // ---- 300 ms poll: cheap state reads + auto-advance + SMTC push -----------
    private void OnTick(DispatcherQueueTimer sender, object args)
    {
        if (!_loggedIn) return;
        int st = DeezerCore.DZState();
        long pos = DeezerCore.DZPositionMS(), dur = DeezerCore.DZDurationMS();
        if (dur > 0)
        {
            if (_seek.Maximum != dur) { _updatingSeek = true; _seek.Maximum = dur; _updatingSeek = false; }
            _durText.Text = Wire.TimeText(dur);
        }
        if (Environment.TickCount64 - _lastSeekTick > 400) // don't fight a live drag
        {
            _updatingSeek = true;
            double v = pos;
            if (dur > 0 && v > dur) v = dur;
            _seek.Value = v;
            _updatingSeek = false;
        }
        _posText.Text = Wire.TimeText(pos);
        _playIcon.Glyph = st == 2 ? "\uE769" : "\uE768"; // pause glyph while playing

        // Show the actual output format next to the artist.
        if (!string.IsNullOrEmpty(_curArtist))
        {
            string f = DeezerCore.Format();
            _nowArtist.Text = string.IsNullOrEmpty(f) ? _curArtist : _curArtist + "   ·   " + f;
        }

        // Preview badge: visible while the current stream is a 30-second sample
        // (loading / playing / paused). Only touch the tree when it actually flips.
        if (_previewBadge != null)
        {
            bool prev = (st == 1 || st == 2 || st == 3) && DeezerCore.IsPreview();
            if (prev != _lastPreview)
            {
                _previewBadge.Visibility = prev ? Visibility.Visible : Visibility.Collapsed;
                _lastPreview = prev;
            }
        }

        // Lyrics page (when open): drive the synced highlight off the same tick,
        // and refetch when the track changes.
        if (_lyricsShown)
        {
            if (_lyrics.IsSynced && _lyricLineBlocks.Count > 0) UpdateLyricsHighlight(pos);
            string cur = CurrentTrackId();
            if (!string.IsNullOrEmpty(cur) && cur != _lyricsTrackId) LoadLyrics(cur);
        }

        // Mirror state to the OS overlay: status on change, timeline ~every 5 s.
        if (_smtc != null)
        {
            MediaPlaybackStatus ps =
                st == 2 ? MediaPlaybackStatus.Playing :
                st == 3 ? MediaPlaybackStatus.Paused :
                st == 1 ? MediaPlaybackStatus.Changing :
                          MediaPlaybackStatus.Stopped;
            if (ps != _lastSmtcStatus) { try { _smtc.PlaybackStatus = ps; } catch { } _lastSmtcStatus = ps; }
            if (++_smtcTimelineTick >= 16) { _smtcTimelineTick = 0; UpdateSmtcTimeline(pos, dur); }
        }

        int fin = DeezerCore.DZFinishedCount();
        if (fin != _lastFinished)
        {
            _lastFinished = fin;
            if (_repeat == 2)
            {
                PlayCurrent(); // repeat-one
            }
            else if (_settings.Gapless && st == 2 && HasDeterministicNext(out int n))
            {
                // The engine kept playing -> it already swapped to the preloaded next.
                // Advance the UI pointer only (no second DZPlay).
                AdvanceUiToPreloaded(n);
            }
            else
            {
                Next(); // normal advance / restart
            }
        }

        // Keep the now-playing bar + queue cursor in sync with the engine's actual
        // track. (a) local control-API plays and, over Connect, the remote device's
        // track refresh the bar; (b) when the GUI synced its queue, the engine owns
        // natural-finish advance (DZFinishedCount does NOT bump for those) -> follow
        // its cursor so manual Next/Prev + gapless preload arming stay aligned.
        {
            string json = DeezerCore.NowPlaying();
            string npid = "";
            try
            {
                using var doc = JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json);
                var obj = doc.RootElement;
                if (obj.ValueKind == JsonValueKind.Object)
                {
                    npid = obj.Str("id");
                    if (!string.IsNullOrEmpty(npid))
                    {
                        bool changed = npid != _engineNowId;
                        _engineNowId = npid;
                        _engineNowArtistId = obj.Str("artistId");
                        if (changed && npid != _nowId) SetNowPlaying(Wire.TrackFromObj(obj));
                    }
                }
            }
            catch { }

            // Engine-owned queue advance: adopt the engine cursor when it moved on
            // its own (a natural finish, or a remote controller's next/prev). Skipped
            // while a local queue push is in flight (its not-yet-applied cursor would
            // misfire), and only when the engine's current track matches that queue
            // row (so a remote content edit can't point the pointer at a stale slot).
            if (_queueSynced && _queueSyncPending == 0 && !string.IsNullOrEmpty(npid))
            {
                int eidx = DeezerCore.DZQueueIndex();
                if (eidx >= 0 && eidx < _queue.Count && eidx != _queueIndex && _queue[eidx].Id == npid)
                {
                    _queueIndex = eidx;
                    if (_settings.Gapless && HasDeterministicNext(out int gn))
                        DispatchPreload(_queue[gn].Id, _queue[gn].DurationMs);
                }
            }
        }

        // Engine-truth transport reconcile: adopt the engine's repeat/shuffle so the
        // display never drifts and, when casting, mirrors the remote device. Held
        // off briefly after a local toggle so an in-flight (possibly HTTP) command
        // isn't fought by a stale read. OnShuffle/OnRepeat send the command; this
        // reconciles the displayed state.
        if (Environment.TickCount64 - _lastTransportTick > 1200)
        {
            int rep = DeezerCore.GetRepeat();
            if (rep != _repeat) ApplyRepeatDisplay(rep);
            bool shuf = DeezerCore.GetShuffle();
            if (shuf != _shuffle) ApplyShuffleDisplay(shuf);
        }

        // Keep the open Up-Next panel's current-track highlight live as playback
        // advances (queue content only changes via explicit edits, which repaint).
        if (_queueOpen && _queueIndex != _queueRenderedIndex) RefreshQueuePanel();
    }

    // ---- SystemMediaTransportControls (OS media overlay / media keys) --------
    private void SetupSmtc()
    {
        try
        {
            _smtc = Smtc.GetForWindow(_appHwnd);
            if (_smtc == null) return;
            _smtc.IsEnabled = true;
            _smtc.IsPlayEnabled = true; _smtc.IsPauseEnabled = true;
            _smtc.IsNextEnabled = true; _smtc.IsPreviousEnabled = true;
            _smtc.DisplayUpdater.Type = MediaPlaybackType.Music;
            // Handlers run on a threadpool thread -> marshal to the UI thread, then
            // route into the EXISTING transport logic.
            _smtc.ButtonPressed += OnSmtcButton;
            _smtc.PlaybackPositionChangeRequested += OnSmtcSeek;
        }
        catch { _smtc = null; }
    }

    private void OnSmtcButton(SystemMediaTransportControls s, SystemMediaTransportControlsButtonPressedEventArgs a)
    {
        var btn = a.Button;
        DispatcherQueue.TryEnqueue(() =>
        {
            switch (btn)
            {
                case SystemMediaTransportControlsButton.Play: _ = Task.Run(DeezerCore.DZResume); break;
                case SystemMediaTransportControlsButton.Pause: _ = Task.Run(DeezerCore.DZPause); break;
                case SystemMediaTransportControlsButton.Next: Next(); break;
                case SystemMediaTransportControlsButton.Previous: Prev(); break;
            }
        });
    }

    private void OnSmtcSeek(SystemMediaTransportControls s, PlaybackPositionChangeRequestedEventArgs a)
    {
        long ms = (long)a.RequestedPlaybackPosition.TotalMilliseconds;
        DispatcherQueue.TryEnqueue(() =>
        {
            _lastSeekTick = Environment.TickCount64;
            _updatingSeek = true; _seek.Value = ms; _updatingSeek = false;
            _posText.Text = Wire.TimeText(ms);
            _pendingSeekMs = ms;
            PumpSeek(); // off-thread + coalesced with any slider drag
            UpdateSmtcTimeline(ms, (long)_seek.Maximum);
        });
    }

    private void UpdateSmtcMetadata(Track t)
    {
        if (_smtc == null) return;
        try
        {
            var du = _smtc.DisplayUpdater;
            du.Type = MediaPlaybackType.Music;
            var mp = du.MusicProperties;
            mp.Title = t.Name;
            mp.Artist = t.ArtistLine;
            mp.AlbumTitle = t.AlbumName;
            if (!string.IsNullOrEmpty(t.ArtworkUrl))
            {
                try { du.Thumbnail = RandomAccessStreamReference.CreateFromUri(new Uri(t.ArtworkUrl)); } catch { }
            }
            du.Update();
            _smtc.PlaybackStatus = MediaPlaybackStatus.Playing;
            _lastSmtcStatus = MediaPlaybackStatus.Playing;
            UpdateSmtcTimeline(0, t.DurationMs);
            _smtcTimelineTick = 0;
        }
        catch { }
    }

    private void UpdateSmtcTimeline(long posMs, long durMs)
    {
        if (_smtc == null) return;
        try
        {
            var tl = new SystemMediaTransportControlsTimelineProperties();
            var end = TimeSpan.FromMilliseconds(durMs > 0 ? durMs : 0);
            tl.StartTime = TimeSpan.Zero;
            tl.EndTime = end;
            tl.Position = TimeSpan.FromMilliseconds(posMs < 0 ? 0 : posMs);
            tl.MinSeekTime = TimeSpan.Zero;
            tl.MaxSeekTime = end;
            _smtc.UpdateTimelineProperties(tl);
        }
        catch { }
    }

    // ---- tray icon + close-to-tray (background playback) ---------------------
    private void SetupTray()
    {
        try
        {
            s_instance = this;
            s_trayProc = TrayWndProc; // keep the delegate alive for the lifetime of the window class
            var wc = new NativeMethods.WNDCLASSEXW
            {
                cbSize = (uint)System.Runtime.InteropServices.Marshal.SizeOf<NativeMethods.WNDCLASSEXW>(),
                lpfnWndProc = System.Runtime.InteropServices.Marshal.GetFunctionPointerForDelegate(s_trayProc),
                hInstance = NativeMethods.GetModuleHandleW(null),
                lpszClassName = "OpenDeezerTrayWnd",
            };
            NativeMethods.RegisterClassExW(ref wc); // harmless if already registered

            _msgHwnd = NativeMethods.CreateWindowExW(0, "OpenDeezerTrayWnd", "OpenDeezerTray", 0,
                0, 0, 0, 0, NativeMethods.HWND_MESSAGE, IntPtr.Zero, wc.hInstance, IntPtr.Zero);

            IntPtr hIcon = LoadAppIcon();
            _nid = new NativeMethods.NOTIFYICONDATAW
            {
                cbSize = (uint)System.Runtime.InteropServices.Marshal.SizeOf<NativeMethods.NOTIFYICONDATAW>(),
                hWnd = _msgHwnd,
                uID = NativeMethods.TRAY_UID,
                uFlags = NativeMethods.NIF_ICON | NativeMethods.NIF_MESSAGE | NativeMethods.NIF_TIP,
                uCallbackMessage = NativeMethods.WM_TRAYCALLBACK,
                hIcon = hIcon,
                szTip = "OpenDeezer",
                szInfo = "",       // ByValTStr fields can't marshal null
                szInfoTitle = "",
            };
            NativeMethods.Shell_NotifyIconW(NativeMethods.NIM_ADD, ref _nid);
            _trayAdded = true;
        }
        catch { }
    }

    private static IntPtr LoadAppIcon()
    {
        try
        {
            var exe = Environment.ProcessPath;
            if (!string.IsNullOrEmpty(exe))
            {
                IntPtr h = NativeMethods.ExtractIconW(NativeMethods.GetModuleHandleW(null), exe, 0);
                if (h != IntPtr.Zero && h.ToInt64() != 1) return h; // 1 => file has no icons
            }
        }
        catch { }
        return NativeMethods.LoadIconW(IntPtr.Zero, NativeMethods.IDI_APPLICATION);
    }

    private void RemoveTray()
    {
        if (_trayAdded) { try { NativeMethods.Shell_NotifyIconW(NativeMethods.NIM_DELETE, ref _nid); } catch { } _trayAdded = false; }
    }

    private void RestoreWindow()
    {
        try { AppWindow.Show(); Activate(); NativeMethods.SetForegroundWindow(_appHwnd); } catch { }
    }

    private void ShowTrayMenu()
    {
        IntPtr menu = NativeMethods.CreatePopupMenu();
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_STRING, (IntPtr)NativeMethods.MENU_RESTORE, Loc.S("Tray_Open"));
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_SEPARATOR, IntPtr.Zero, null);
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_STRING, (IntPtr)NativeMethods.MENU_QUIT, Loc.S("Tray_Quit"));
        NativeMethods.GetCursorPos(out var p);
        NativeMethods.SetForegroundWindow(_msgHwnd); // so the menu dismisses on focus loss
        NativeMethods.TrackPopupMenu(menu, NativeMethods.TPM_RIGHTBUTTON, p.X, p.Y, 0, _msgHwnd, IntPtr.Zero);
        NativeMethods.DestroyMenu(menu);
    }

    private void QuitApp()
    {
        _quitting = true;
        RemoveTray();
        if (_smtc != null) { try { _smtc.IsEnabled = false; } catch { } }
        try { Application.Current.Exit(); } catch { }
    }

    // Close button: honor close-to-tray (keep engine playing in the background).
    private void OnClosing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (_quitting) return;
        if (_settings.CloseToTray)
        {
            args.Cancel = true;
            try { AppWindow.Hide(); } catch { }
        }
        else
        {
            RemoveTray(); // real close -> let the process exit
        }
    }

    // Single-instance routing: the app has exactly one MainWindow, so the static
    // WndProc forwards to it (no GWLP_USERDATA / GCHandle juggling required).
    private static MainWindow? s_instance;
    private static NativeMethods.WndProcDelegate? s_trayProc;
    private static IntPtr TrayWndProc(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam)
    {
        var self = s_instance;
        if (self != null && msg == NativeMethods.WM_TRAYCALLBACK)
        {
            int evt = (int)(lParam.ToInt64() & 0xFFFF); // LOWORD(lParam)
            switch (evt)
            {
                case NativeMethods.WM_LBUTTONDBLCLK: self.RestoreWindow(); break;
                case NativeMethods.WM_RBUTTONUP:
                case NativeMethods.WM_CONTEXTMENU: self.ShowTrayMenu(); break;
            }
            return IntPtr.Zero;
        }
        if (self != null && msg == NativeMethods.WM_COMMAND)
        {
            int cmd = (int)(wParam.ToInt64() & 0xFFFF); // LOWORD(wParam)
            switch (cmd)
            {
                case NativeMethods.MENU_RESTORE: self.RestoreWindow(); break;
                case NativeMethods.MENU_QUIT: self.QuitApp(); break;
            }
            return IntPtr.Zero;
        }
        return NativeMethods.DefWindowProcW(hWnd, msg, wParam, lParam);
    }

    // ---- dialogs -------------------------------------------------------------
    // Guard ShowAsync (WinUI permits only one ContentDialog open at a time).
    private static async Task<ContentDialogResult> ShowDialog(ContentDialog dlg)
    {
        dlg.FlowDirection = Loc.FlowDirection; // mirror every dialog for RTL languages (Arabic)
        try { return await dlg.ShowAsync(); }
        catch { return ContentDialogResult.None; }
    }

    private Task ShowMessage(string title, string body)
    {
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = title,
            Content = new TextBlock { Text = body, TextWrapping = TextWrapping.Wrap },
            CloseButtonText = Loc.S("Btn_OK"),
        };
        return ShowDialog(dlg);
    }

    private async void ShowSettings()
    {
        // Output devices + current engine audio state read off the UI thread.
        var (devJson, curDev, curGapless, curCrossfade, ctrlJson, slpActive, slpEot, slpRemMs, ddir, curCacheMB) = await Task.Run(() =>
        {
            string dj = DeezerCore.TakeJson(DeezerCore.DZAudioDevicesJSON());
            string cd = DeezerCore.CurrentAudioDevice();
            string cj = DeezerCore.ControlConfig();
            return (dj, cd, DeezerCore.DZGapless() != 0, DeezerCore.DZCrossfadeMS(), cj,
                    DeezerCore.DZSleepTimerActive() != 0, DeezerCore.DZSleepTimerEndOfTrack() != 0, DeezerCore.DZSleepTimerRemainingMS(),
                    DeezerCore.DownloadDir(), DeezerCore.MediaCacheMB());
        });
        var devices = Wire.ParseDevices(devJson);

        bool ctrlEnabled = false, ctrlLan = false;
        string ctrlToken = "";
        try
        {
            using var ctrlDoc = JsonDocument.Parse(string.IsNullOrEmpty(ctrlJson) ? "{}" : ctrlJson);
            var co = ctrlDoc.RootElement;
            ctrlEnabled = co.Bool("enabled");
            ctrlLan = co.Bool("lan");
            ctrlToken = co.Str("token");
        }
        catch { }

        var sp = new StackPanel { Spacing = 18, MinWidth = 360 };

        // Audio quality
        var quality = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        quality.Items.Add(Loc.S("Quality_Normal"));
        quality.Items.Add(Loc.S("Quality_High"));
        quality.Items.Add(Loc.S("Quality_HiFi"));
        quality.SelectedIndex = _settings.Quality;
        var qsec = new StackPanel { Spacing = 4 };
        qsec.Children.Add(new TextBlock { Text = Loc.S("Settings_AudioQuality"), FontWeight = FontWeights.SemiBold });
        qsec.Children.Add(quality);
        if (_account.LoggedIn)
        {
            bool exceeds = (_settings.Quality >= 2 && !_account.CanHifi) || (_settings.Quality >= 1 && !_account.CanHq);
            if (exceeds)
                qsec.Children.Add(new TextBlock
                {
                    Text = Loc.Format("Settings_QualityWarnFormat", _account.Offer),
                    TextWrapping = TextWrapping.Wrap,
                    Opacity = 0.8,
                });
        }

        // Output device (id "" = system default).
        var devCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        int selDev = 0;
        for (int i = 0; i < devices.Count; i++)
        {
            string label = string.IsNullOrEmpty(devices[i].Name) ? Loc.S("Settings_SystemDefault") : devices[i].Name;
            if (devices[i].IsDefault) label = Loc.Format("Settings_DeviceDefaultFormat", label);
            devCombo.Items.Add(label);
            if (devices[i].Id == curDev) selDev = i;
        }
        if (devices.Count > 0) devCombo.SelectedIndex = selDev;
        else { devCombo.IsEnabled = false; devCombo.PlaceholderText = Loc.S("Settings_NoOutputDevices"); }
        var asec = new StackPanel { Spacing = 4 };
        asec.Children.Add(new TextBlock { Text = Loc.S("Settings_OutputDevice"), FontWeight = FontWeights.SemiBold });
        asec.Children.Add(devCombo);

        // Gapless
        var gapSwitch = new ToggleSwitch
        {
            OnContent = Loc.S("Settings_GaplessOn"),
            OffContent = Loc.S("Settings_GaplessOff"),
            IsOn = curGapless,
        };
        var gsec = new StackPanel { Spacing = 4 };
        gsec.Children.Add(new TextBlock { Text = Loc.S("Settings_GaplessPlayback"), FontWeight = FontWeights.SemiBold });
        gsec.Children.Add(gapSwitch);

        // Crossfade (0 / 3 / 6 / 12 s).
        int[] cfVals = { 0, 3000, 6000, 12000 };
        var cfCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        cfCombo.Items.Add(Loc.S("Common_Off"));
        cfCombo.Items.Add(Loc.Plural("Seconds", 3));
        cfCombo.Items.Add(Loc.Plural("Seconds", 6));
        cfCombo.Items.Add(Loc.Plural("Seconds", 12));
        int cfIdx = 0;
        for (int i = 3; i >= 0; i--) { if (curCrossfade >= cfVals[i]) { cfIdx = i; break; } }
        cfCombo.SelectedIndex = cfIdx;
        var csec = new StackPanel { Spacing = 4 };
        csec.Children.Add(new TextBlock { Text = Loc.S("Settings_Crossfade"), FontWeight = FontWeights.SemiBold });
        csec.Children.Add(cfCombo);

        // Equalizer: opens its own dialog (10 vertical band sliders need the
        // width, and this list already scrolls). WinUI allows only one open
        // ContentDialog at a time, so the button hides Settings first; the EQ
        // applies live and is engine-persisted, so it never needed Save anyway.
        var eqBtn = new Button { Content = Loc.S("Settings_EqualizerBtn") };
        var eqsec = new StackPanel { Spacing = 4 };
        eqsec.Children.Add(new TextBlock { Text = Loc.S("Settings_Equalizer"), FontWeight = FontWeights.SemiBold });
        eqsec.Children.Add(new TextBlock { Text = Loc.S("Settings_EqualizerDesc"), Opacity = 0.7, TextWrapping = TextWrapping.Wrap });
        eqsec.Children.Add(eqBtn);

        // Sleep timer (engine state; applied on Save via DZSetSleepTimer / DZCancelSleepTimer).
        // Off / 15 / 30 / 45 / 60 min, or pause when the current track ends.
        int[] slpMins = { 0, 15, 30, 45, 60 }; // combo index -> minutes; index 5 = End of track
        var slpCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        slpCombo.Items.Add(Loc.S("Common_Off"));
        slpCombo.Items.Add(Loc.Plural("Minutes", 15));
        slpCombo.Items.Add(Loc.Plural("Minutes", 30));
        slpCombo.Items.Add(Loc.Plural("Minutes", 45));
        slpCombo.Items.Add(Loc.Plural("Minutes", 60));
        slpCombo.Items.Add(Loc.S("Settings_EndOfTrack"));
        int slpIdx = 0;
        if (slpActive)
        {
            if (slpEot) slpIdx = 5;
            else
            {
                // Snap the remaining time up to the nearest preset for display.
                int remMin = (int)((slpRemMs + 59999) / 60000);
                slpIdx = 4; // default to 60 if it exceeds every preset
                for (int i = 1; i <= 4; i++) { if (remMin <= slpMins[i]) { slpIdx = i; break; } }
            }
        }
        slpCombo.SelectedIndex = slpIdx;
        var slsec = new StackPanel { Spacing = 4 };
        slsec.Children.Add(new TextBlock { Text = Loc.S("Settings_SleepTimer"), FontWeight = FontWeights.SemiBold });
        slsec.Children.Add(slpCombo);

        // Volume normalization (ReplayGain) -- bound to engine state.
        var rg = new ToggleSwitch
        {
            OnContent = Loc.S("Settings_ReplayGainOn"),
            OffContent = Loc.S("Settings_ReplayGainOff"),
            IsOn = DeezerCore.DZReplayGain() != 0,
        };
        var rsec = new StackPanel { Spacing = 4 };
        rsec.Children.Add(new TextBlock { Text = Loc.S("Settings_VolumeNormalization"), FontWeight = FontWeights.SemiBold });
        rsec.Children.Add(rg);

        // Background / close-to-tray
        var tray = new ToggleSwitch
        {
            OnContent = Loc.S("Settings_TrayOn"),
            OffContent = Loc.S("Settings_TrayOff"),
            IsOn = _settings.CloseToTray,
        };
        var tsec = new StackPanel { Spacing = 4 };
        tsec.Children.Add(new TextBlock { Text = Loc.S("Settings_BackgroundPlayback"), FontWeight = FontWeights.SemiBold });
        tsec.Children.Add(tray);

        // Download folder: the engine-side default target for premium track exports
        // ("" = a built-in default). Editable, plus a Browse… picker. Applied on Save,
        // or immediately when a folder is chosen through Browse.
        string curDownloadDir = ddir;
        var dlBox = new TextBox { Text = curDownloadDir, HorizontalAlignment = HorizontalAlignment.Stretch };
        var browseBtn = new Button { Content = Loc.S("Btn_Browse") };
        browseBtn.Click += async (_, _) =>
        {
            try
            {
                var picker = new Windows.Storage.Pickers.FolderPicker();
                picker.SuggestedStartLocation = Windows.Storage.Pickers.PickerLocationId.Downloads;
                picker.FileTypeFilter.Add("*"); // required, else PickSingleFolderAsync throws
                // WinUI 3 pickers need the owning HWND (no CoreWindow); reuse the app handle.
                WinRT.Interop.InitializeWithWindow.Initialize(picker, _appHwnd);
                var folder = await picker.PickSingleFolderAsync();
                if (folder != null && !string.IsNullOrEmpty(folder.Path))
                {
                    dlBox.Text = folder.Path;
                    curDownloadDir = folder.Path;
                    await Task.Run(() => DeezerCore.SetDownloadDir(folder.Path));
                }
            }
            catch { }
        };
        var dlGrid = new Grid { ColumnSpacing = 8 };
        dlGrid.ColumnDefinitions.Add(ColStar());
        dlGrid.ColumnDefinitions.Add(ColAuto());
        Grid.SetColumn(dlBox, 0); dlGrid.Children.Add(dlBox);
        Grid.SetColumn(browseBtn, 1); dlGrid.Children.Add(browseBtn);
        var dlsec = new StackPanel { Spacing = 4 };
        dlsec.Children.Add(new TextBlock { Text = Loc.S("Settings_DownloadFolder"), FontWeight = FontWeights.SemiBold });
        dlsec.Children.Add(dlGrid);

        // Stream cache: on-disk raw-stream cache budget in MB (0 = off). Engine state
        // (media.json); the cache attaches to the player at startup, so a change only
        // takes effect on the NEXT launch. Applied on Save.
        var cacheBox = new NumberBox
        {
            Minimum = 0,
            Maximum = 100000,
            SmallChange = 50,
            LargeChange = 500,
            SpinButtonPlacementMode = NumberBoxSpinButtonPlacementMode.Inline,
            Value = curCacheMB < 0 ? 0 : curCacheMB,
            HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        var mcsec = new StackPanel { Spacing = 4 };
        mcsec.Children.Add(new TextBlock { Text = Loc.S("Settings_StreamCache"), FontWeight = FontWeights.SemiBold });
        mcsec.Children.Add(new TextBlock { Text = Loc.S("Settings_StreamCacheDesc"), Opacity = 0.7, TextWrapping = TextWrapping.Wrap });
        mcsec.Children.Add(cacheBox);

        // Disable ads: Deezer Free ONLY (Premium has no ads). Engine-persisted state,
        // applied on Save. The disclaimer beneath spells out that this suppresses the
        // play reporting that credits artists and breaches Deezer's terms of use.
        CheckBox? adsCheck = null;
        StackPanel? adsec = null;
        if (!_account.Premium)
        {
            adsCheck = new CheckBox { Content = Loc.S("Settings_DisableAds"), IsChecked = DeezerCore.AdsDisabled() };
            adsec = new StackPanel { Spacing = 4 };
            adsec.Children.Add(adsCheck);
            adsec.Children.Add(new TextBlock
            {
                Text = Loc.S("Settings_DisableAdsDisclaimer"),
                Opacity = 0.7,
                TextWrapping = TextWrapping.Wrap,
            });
        }

        // Remote control (control API / phone remote): enable, LAN reachability, token.
        // Applies live -- every change is pushed straight to the engine, not gated
        // behind the dialog's Save button.
        var ctrlEnableSwitch = new ToggleSwitch
        {
            OnContent = Loc.S("Settings_RemoteOn"),
            OffContent = Loc.S("Settings_RemoteOff"),
            IsOn = ctrlEnabled,
        };
        var ctrlLanSwitch = new ToggleSwitch
        {
            OnContent = Loc.S("Settings_RemoteLanOn"),
            OffContent = Loc.S("Settings_RemoteLanOff"),
            IsOn = ctrlLan,
            IsEnabled = ctrlEnabled,
        };
        var ctrlTokenBox = new TextBox
        {
            PlaceholderText = Loc.S("Settings_AccessToken"),
            Text = ctrlToken,
            IsEnabled = ctrlEnabled,
        };
        // Skip no-op applies: DZSetControlConfig restarts the control server, which
        // would kill an active Phone Remote's pairing code + phone sessions even
        // when the user merely tabbed through the token box.
        bool appliedOn = ctrlEnabled;
        string appliedAddr = ctrlLan ? ":7654" : "";
        string appliedToken = ctrlToken;
        async void ApplyControlConfig()
        {
            bool on = ctrlEnableSwitch.IsOn;
            string addr = ctrlLanSwitch.IsOn ? ":7654" : "";
            string token = ctrlTokenBox.Text ?? "";
            if (on == appliedOn && addr == appliedAddr && token == appliedToken) return;
            appliedOn = on; appliedAddr = addr; appliedToken = token;
            await Task.Run(() => DeezerCore.DZSetControlConfig(on ? 1 : 0, addr, token));
        }
        ctrlEnableSwitch.Toggled += (_, _) =>
        {
            ctrlLanSwitch.IsEnabled = ctrlEnableSwitch.IsOn;
            ctrlTokenBox.IsEnabled = ctrlEnableSwitch.IsOn;
            ApplyControlConfig();
        };
        ctrlLanSwitch.Toggled += (_, _) => ApplyControlConfig();
        ctrlTokenBox.LostFocus += (_, _) => ApplyControlConfig();
        var rcsec = new StackPanel { Spacing = 4 };
        rcsec.Children.Add(new TextBlock { Text = Loc.S("Settings_RemoteControl"), FontWeight = FontWeights.SemiBold });
        rcsec.Children.Add(new TextBlock { Text = Loc.S("Settings_RemoteDesc"), Opacity = 0.7, TextWrapping = TextWrapping.Wrap });
        rcsec.Children.Add(ctrlEnableSwitch);
        rcsec.Children.Add(ctrlLanSwitch);
        rcsec.Children.Add(ctrlTokenBox);

        // Updates: on-demand GitHub release check (never downloads/installs anything).
        var updStatus = new TextBlock { Text = Loc.S("Settings_UpdateCheckDesc"), Opacity = 0.8, TextWrapping = TextWrapping.Wrap };
        var updBtn = new Button { Content = Loc.S("Settings_CheckForUpdates") };
        var updDownloadBtn = new Button { Content = Loc.S("Btn_Download"), Visibility = Visibility.Collapsed };
        string updCheckUrl = "";
        updBtn.Click += async (_, _) =>
        {
            updBtn.IsEnabled = false;
            updStatus.Text = Loc.S("Settings_Checking");
            UpdateInfo info;
            try { info = await Task.Run(() => DeezerCore.CheckUpdate()); }
            catch { info = new UpdateInfo(); }
            if (info.HasUpdate)
            {
                updStatus.Text = Loc.Format("Settings_UpdateAvailFormat", info.Latest, info.Current);
                updCheckUrl = info.Url;
                updDownloadBtn.Visibility = Visibility.Visible;
                ShowUpdateNotice(info); // also surface the dismissible banner for after the dialog closes
            }
            else
            {
                updStatus.Text = string.IsNullOrEmpty(info.Latest)
                    ? Loc.S("Settings_UpdateFail")
                    : Loc.Format("Settings_UpToDateFormat", info.Current);
                updDownloadBtn.Visibility = Visibility.Collapsed;
            }
            updBtn.IsEnabled = true;
        };
        updDownloadBtn.Click += async (_, _) =>
        {
            if (string.IsNullOrEmpty(updCheckUrl)) return;
            try { await Launcher.LaunchUriAsync(new Uri(updCheckUrl)); } catch { }
        };
        var updRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8 };
        updRow.Children.Add(updBtn);
        updRow.Children.Add(updDownloadBtn);
        var usec = new StackPanel { Spacing = 4 };
        usec.Children.Add(new TextBlock { Text = Loc.S("Settings_Updates"), FontWeight = FontWeights.SemiBold });
        usec.Children.Add(updStatus);
        usec.Children.Add(updRow);

        // Language: choose the UI language (or follow Windows). The already-built
        // code-behind UI cannot re-text itself, so a change takes effect on the next
        // launch; on Save we re-point Loc and prompt for a restart.
        var langCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        int selLang = 0;
        for (int i = 0; i < Loc.Languages.Length; i++)
        {
            var lang = Loc.Languages[i];
            langCombo.Items.Add(string.IsNullOrEmpty(lang.Tag) ? Loc.S("Settings_LanguageSystem") : lang.Native);
            if (lang.Tag == _settings.Language) selLang = i;
        }
        langCombo.SelectedIndex = selLang;
        var lsec = new StackPanel { Spacing = 4 };
        lsec.Children.Add(new TextBlock { Text = Loc.S("Settings_Language"), FontWeight = FontWeights.SemiBold });
        lsec.Children.Add(langCombo);

        sp.Children.Add(qsec);
        sp.Children.Add(asec);
        sp.Children.Add(gsec);
        sp.Children.Add(csec);
        sp.Children.Add(eqsec);
        sp.Children.Add(slsec);
        sp.Children.Add(rsec);
        sp.Children.Add(tsec);
        sp.Children.Add(dlsec);
        sp.Children.Add(mcsec);
        if (adsec != null) sp.Children.Add(adsec); // Free accounts only
        sp.Children.Add(rcsec);
        sp.Children.Add(lsec);
        sp.Children.Add(usec);

        // Wrap in a scroll viewer — the settings list (audio + remote control +
        // updates) is taller than the dialog, so it must scroll.
        var settingsScroll = new ScrollViewer
        {
            Content = sp,
            VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
            MaxHeight = 520,
        };
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Settings_Title"),
            Content = settingsScroll,
            PrimaryButtonText = Loc.S("Btn_Save"),
            CloseButtonText = Loc.S("Btn_Cancel"),
            DefaultButton = ContentDialogButton.Primary,
        };
        eqBtn.Click += (_, _) => { dlg.Hide(); ShowEqualizer(); };
        if (await ShowDialog(dlg) == ContentDialogResult.Primary)
        {
            int lvl = quality.SelectedIndex;
            _settings.Quality = lvl < 0 ? 0 : (lvl > 2 ? 2 : lvl);
            _settings.CloseToTray = tray.IsOn;
            _settings.ReplayGain = rg.IsOn;
            DeezerCore.DZSetQuality(_settings.Quality); // applies to the NEXT track
            DeezerCore.DZSetReplayGain(_settings.ReplayGain ? 1 : 0);

            int di = devCombo.SelectedIndex;
            if (di >= 0 && di < devices.Count)
            {
                _settings.AudioDevice = devices[di].Id;
                DeezerCore.DZSetAudioDevice(devices[di].Id);
            }
            _settings.Gapless = gapSwitch.IsOn;
            DeezerCore.DZSetGapless(_settings.Gapless ? 1 : 0);
            int ci = cfCombo.SelectedIndex; if (ci < 0 || ci > 3) ci = 0;
            _settings.CrossfadeMs = cfVals[ci];
            DeezerCore.DZSetCrossfadeMS(_settings.CrossfadeMs);

            // Download folder: engine-side state (not persisted to settings.json).
            // Skip when Browse already applied the same path.
            string dd = (dlBox.Text ?? "").Trim();
            if (dd != curDownloadDir) DeezerCore.SetDownloadDir(dd);

            // Stream cache (MB): engine-side state (media.json). NaN -> 0. Applies on
            // the next launch (the cache attaches to the player at startup).
            double cacheVal = cacheBox.Value;
            int cacheMB = double.IsNaN(cacheVal) ? 0 : (int)Math.Round(cacheVal);
            if (cacheMB < 0) cacheMB = 0;
            if (cacheMB != curCacheMB) DeezerCore.SetMediaCacheMB(cacheMB);

            // Disable-ads opt-out (Free accounts only; engine-persisted).
            if (adsCheck != null) DeezerCore.SetAdsDisabled(adsCheck.IsChecked == true);

            // Sleep timer: only touch the engine when the user changed the selection,
            // so re-saving other settings never resets a running timer. Transient
            // engine state -- not persisted to _settings.
            int si = slpCombo.SelectedIndex;
            if (si != slpIdx)
            {
                if (si <= 0) DeezerCore.DZCancelSleepTimer();
                else if (si == 5) DeezerCore.DZSetSleepTimer(0, 1);   // End of track
                else DeezerCore.DZSetSleepTimer(slpMins[si], 0);       // minutes
            }

            // UI language: persisted, but only takes effect on the next launch because
            // the code-built tree is already texted. Re-point Loc and prompt a restart.
            int li = langCombo.SelectedIndex;
            string newLang = (li >= 0 && li < Loc.Languages.Length) ? Loc.Languages[li].Tag : "";
            bool langChanged = newLang != _settings.Language;
            _settings.Language = newLang;

            Config.SaveSettings(_settings);

            if (langChanged)
            {
                Loc.SetLanguage(newLang);
                _ = ShowMessage(Loc.S("Settings_Language"), Loc.S("Settings_LanguageRestart"));
            }
        }
    }

    // 10-band equalizer + mono downmix. Everything applies LIVE via DZSetEQJSON
    // partial updates and the ENGINE owns state + persistence (eq.json debounced
    // engine-side), so the dialog has no Save button and this app stores nothing.
    private async void ShowEqualizer()
    {
        // Fresh engine state off the UI thread -- another client (remote, phone)
        // may have changed the EQ since this app last looked.
        var eq = await Task.Run(() => DeezerCore.EQ());

        bool updatingEq = false; // programmatic slider/combo writes (mirrors _updatingSeek)

        // Coalescing pump like PumpSeek: one DZSetEQJSON in flight at a time; drag
        // ticks arriving mid-flight collapse into a single trailing call per band
        // (+ preamp), so a slider drag never queues blocking round-trips.
        var pendingBand = new double?[10];
        double? pendingPreamp = null;
        bool eqInFlight = false;
        async void PumpEq()
        {
            if (eqInFlight) return;
            eqInFlight = true;
            while (true)
            {
                string payload = "";
                for (int i = 0; i < pendingBand.Length; i++)
                {
                    if (pendingBand[i] is double g)
                    {
                        pendingBand[i] = null;
                        payload = new JsonObject { ["band"] = new JsonObject { ["index"] = i, ["gainDb"] = g } }.ToJsonString();
                        break;
                    }
                }
                if (payload.Length == 0 && pendingPreamp is double pa)
                {
                    pendingPreamp = null;
                    payload = new JsonObject { ["preampDb"] = pa }.ToJsonString();
                }
                if (payload.Length == 0) break;
                await Task.Run(() => DeezerCore.DZSetEQJSON(payload));
            }
            eqInFlight = false;
        }

        var sp = new StackPanel { Spacing = 16, MinWidth = 420 };

        // Preset picker: engine preset ids + a display-only "Custom" entry the
        // combo snaps to when any band is edited by hand (the engine does the
        // same flip on its side automatically).
        var presetCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch, IsEnabled = eq.Enabled };
        foreach (var name in eq.Presets) presetCombo.Items.Add(Wire.PresetLabel(name));
        int customIdx = presetCombo.Items.Count;
        presetCombo.Items.Add(Loc.S("EQ_Custom"));
        int selPreset = eq.Presets.IndexOf(eq.Preset);
        presetCombo.SelectedIndex = selPreset >= 0 ? selPreset : customIdx;

        // 10 vertical band sliders (-12..+12 dB, 0.5 dB steps) over Hz labels.
        // Grid is a Panel (no IsEnabled); wrap it in a ContentControl — a Control —
        // so the whole band strip can be greyed out when the EQ is off.
        var bandGrid = new Grid();
        var bandGroup = new ContentControl
        {
            Content = bandGrid,
            IsEnabled = eq.Enabled,
            HorizontalContentAlignment = HorizontalAlignment.Stretch,
        };
        var bandSliders = new Slider[10];
        for (int i = 0; i < bandSliders.Length; i++)
        {
            bandGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var s = new Slider
            {
                Orientation = Orientation.Vertical,
                Minimum = -12,
                Maximum = 12,
                StepFrequency = 0.5,
                Height = 140,
                Value = i < eq.GainsDb.Length ? eq.GainsDb[i] : 0,
                HorizontalAlignment = HorizontalAlignment.Center,
                Foreground = _accent,
            };
            int idx = i;
            s.ValueChanged += (_, e) =>
            {
                if (updatingEq) return;
                pendingBand[idx] = e.NewValue;
                // A manual band edit flips the engine preset to "custom"; mirror it.
                updatingEq = true;
                presetCombo.SelectedIndex = customIdx;
                updatingEq = false;
                PumpEq();
            };
            bandSliders[i] = s;
            var col = new StackPanel { Spacing = 4, HorizontalAlignment = HorizontalAlignment.Center };
            col.Children.Add(s);
            col.Children.Add(new TextBlock
            {
                Text = Wire.BandText(i < eq.Bands.Length ? eq.Bands[i] : 0),
                FontSize = 11,
                Opacity = 0.7,
                HorizontalAlignment = HorizontalAlignment.Center,
            });
            Grid.SetColumn(col, i);
            bandGrid.Children.Add(col);
        }

        // Preamp: overall output trim under the bands (same -12..+12 range).
        var preampSlider = new Slider
        {
            Minimum = -12,
            Maximum = 12,
            StepFrequency = 0.5,
            Value = eq.PreampDb,
            Foreground = _accent,
            IsEnabled = eq.Enabled,
        };
        preampSlider.ValueChanged += (_, e) =>
        {
            if (updatingEq) return;
            pendingPreamp = e.NewValue;
            PumpEq();
        };

        var enable = new ToggleSwitch
        {
            OnContent = Loc.S("EQ_EnableOn"),
            OffContent = Loc.S("EQ_EnableOff"),
            IsOn = eq.Enabled,
        };
        enable.Toggled += async (_, _) =>
        {
            bool on = enable.IsOn;
            presetCombo.IsEnabled = on;
            bandGroup.IsEnabled = on;
            preampSlider.IsEnabled = on;
            string payload = new JsonObject { ["enabled"] = on }.ToJsonString();
            await Task.Run(() => DeezerCore.DZSetEQJSON(payload));
        };

        // Mono downmix is independent of the EQ enable (never greyed with it).
        var mono = new ToggleSwitch
        {
            OnContent = Loc.S("EQ_MonoOn"),
            OffContent = Loc.S("EQ_MonoOff"),
            IsOn = eq.Mono,
        };
        mono.Toggled += async (_, _) =>
        {
            string payload = new JsonObject { ["mono"] = mono.IsOn }.ToJsonString();
            await Task.Run(() => DeezerCore.DZSetEQJSON(payload));
        };

        presetCombo.SelectionChanged += async (_, _) =>
        {
            if (updatingEq) return;
            int pi = presetCombo.SelectedIndex;
            if (pi < 0 || pi >= eq.Presets.Count) return; // "Custom" is a state, not a choice
            string payload = new JsonObject { ["preset"] = eq.Presets[pi] }.ToJsonString();
            // A preset rewrites every band, so re-read the state and move the sliders.
            var st = await Task.Run(() =>
            {
                DeezerCore.DZSetEQJSON(payload);
                return DeezerCore.EQ();
            });
            updatingEq = true;
            for (int i = 0; i < bandSliders.Length && i < st.GainsDb.Length; i++) bandSliders[i].Value = st.GainsDb[i];
            updatingEq = false;
        };

        var psec = new StackPanel { Spacing = 4 };
        psec.Children.Add(new TextBlock { Text = Loc.S("EQ_Preset"), FontWeight = FontWeights.SemiBold });
        psec.Children.Add(presetCombo);

        var pasec = new StackPanel { Spacing = 4 };
        pasec.Children.Add(new TextBlock { Text = Loc.S("EQ_Preamp"), FontWeight = FontWeights.SemiBold });
        pasec.Children.Add(preampSlider);

        var msec = new StackPanel { Spacing = 4 };
        msec.Children.Add(new TextBlock { Text = Loc.S("EQ_MonoAudio"), FontWeight = FontWeights.SemiBold });
        msec.Children.Add(mono);

        sp.Children.Add(enable);
        sp.Children.Add(psec);
        sp.Children.Add(bandGroup);
        sp.Children.Add(pasec);
        sp.Children.Add(msec);

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Settings_Equalizer"),
            Content = sp,
            CloseButtonText = Loc.S("Btn_Close"),
        };
        await ShowDialog(dlg);
    }

    private async void ShowPhoneRemote()
    {
        // Read current enabled state off-thread so the dialog opens without blocking.
        string initInfo = await Task.Run(() => DeezerCore.WebRemoteInfo());
        bool initOn = false;
        try
        {
            using var initDoc = JsonDocument.Parse(string.IsNullOrEmpty(initInfo) ? "{}" : initInfo);
            initOn = initDoc.RootElement.Bool("enabled");
        }
        catch { }

        var sp = new StackPanel { Spacing = 16, MinWidth = 360 };

        sp.Children.Add(new TextBlock
        {
            Text = Loc.S("PhoneRemote_Instructions"),
            TextWrapping = TextWrapping.Wrap,
            Opacity = 0.8,
        });

        var tog = new ToggleSwitch
        {
            IsOn = initOn,
            OnContent = Loc.S("PhoneRemote_On"),
            OffContent = Loc.S("PhoneRemote_Off"),
        };
        sp.Children.Add(tog);

        // QR code image (512x512 PNG from the engine, displayed at 220x220).
        var qrImg = new Image
        {
            Width = 220,
            Height = 220,
            HorizontalAlignment = HorizontalAlignment.Center,
            Margin = new Thickness(0, 8, 0, 0),
        };
        // 6-digit pairing code: large, monospace, spaced for legibility.
        var codeBlock = new TextBlock
        {
            FontFamily = new FontFamily("Consolas"),
            FontSize = 40,
            FontWeight = FontWeights.Bold,
            HorizontalAlignment = HorizontalAlignment.Center,
            CharacterSpacing = 400,
        };
        var urlBlock = new TextBlock
        {
            FontSize = 12,
            Opacity = 0.7,
            HorizontalAlignment = HorizontalAlignment.Center,
            TextWrapping = TextWrapping.Wrap,
            TextAlignment = TextAlignment.Center,
        };
        var infoPanel = new StackPanel
        {
            Spacing = 6,
            Visibility = initOn ? Visibility.Visible : Visibility.Collapsed,
        };
        infoPanel.Children.Add(qrImg);
        infoPanel.Children.Add(codeBlock);
        infoPanel.Children.Add(urlBlock);
        sp.Children.Add(infoPanel);

        // Populate immediately when the server is already running.
        if (initOn) await LoadPhoneRemoteInfo(qrImg, codeBlock, urlBlock);

        tog.Toggled += async (_, _) =>
        {
            bool on = tog.IsOn;
            await Task.Run(() => DeezerCore.DZWebRemoteSetEnabled(on ? 1 : 0));
            if (on)
            {
                await LoadPhoneRemoteInfo(qrImg, codeBlock, urlBlock);
                infoPanel.Visibility = Visibility.Visible;
            }
            else
            {
                infoPanel.Visibility = Visibility.Collapsed;
            }
        };

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("Nav_PhoneRemote"),
            Content = sp,
            CloseButtonText = Loc.S("Btn_Close"),
        };
        await ShowDialog(dlg);
    }

    // Fetch QR PNG + info JSON off-thread, then populate the Phone Remote dialog
    // controls on the UI thread. Mirrors the InMemoryRandomAccessStream path in LoadArt.
    private async Task LoadPhoneRemoteInfo(Image qrImg, TextBlock codeBlock, TextBlock urlBlock)
    {
        var (info, qrBytes) = await Task.Run(() => (DeezerCore.WebRemoteInfo(), DeezerCore.WebRemoteQRPng()));
        string code = "", url = "";
        try
        {
            using var doc = JsonDocument.Parse(string.IsNullOrEmpty(info) ? "{}" : info);
            var o = doc.RootElement;
            code = o.Str("code");
            url = o.Str("url");
        }
        catch { }
        codeBlock.Text = code;
        urlBlock.Text = url;
        if (qrBytes.Length > 0)
        {
            try
            {
                var stream = new InMemoryRandomAccessStream();
                var writer = new DataWriter(stream);
                writer.WriteBytes(qrBytes);
                await writer.StoreAsync();
                writer.DetachStream();
                stream.Seek(0);
                var bmp = new BitmapImage();
                qrImg.Source = bmp;
                await bmp.SetSourceAsync(stream);
            }
            catch { }
        }
    }

    private async void ShowAbout()
    {
        var sp = new StackPanel { Spacing = 8 };
        sp.Children.Add(new TextBlock { Text = "OpenDeezer 3.0.0", FontSize = 22, FontWeight = FontWeights.SemiBold, Foreground = _accent }); // brand + version: not localized
        sp.Children.Add(new TextBlock { Text = Loc.S("About_Tagline"), TextWrapping = TextWrapping.Wrap });
        sp.Children.Add(new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            Text = Loc.S("About_Description"),
        });
        if (_account.LoggedIn && !string.IsNullOrEmpty(_account.Name))
            sp.Children.Add(new TextBlock { Text = Loc.Format("About_SignedInFormat", _account.Name, _account.Offer), TextWrapping = TextWrapping.Wrap, FontWeight = FontWeights.SemiBold });
        sp.Children.Add(new TextBlock { Text = Loc.S("About_Credits"), Opacity = 0.8 });

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = Loc.S("About_Title"),
            Content = sp,
            CloseButtonText = Loc.S("Btn_Close"),
        };
        await ShowDialog(dlg);
    }

    // ---- members -------------------------------------------------------------
    private readonly SolidColorBrush _accent;
    private readonly Random _rng = new();
    private DispatcherQueueTimer _timer = null!;

    // Update check (see BuildUpdateBar / StartBackgroundUpdateCheck / ShowSettings).
    private InfoBar _updateBar = null!;
    private string _updateUrl = "";

    private NavigationView _nav = null!;
    private NavigationViewItem _homeItem = null!, _likedItem = null!, _flowItem = null!, _playlistsItem = null!, _chartsItem = null!,
                               _podcastsItem = null!, _recentItem = null!, _searchItem = null!, _accountItem = null!, _settingsItem = null!,
                               _phoneRemoteItem = null!, _aboutItem = null!;
    private NavigationViewItem? _lastContentItem; // null until the first content page is opened

    private UIElement _tracksPage = null!, _playlistsPage = null!, _searchPage = null!;
    private ListView _trackList = null!, _searchTrackList = null!;
    private GridView _playlistGrid = null!, _searchGrid = null!;
    private TextBox _searchBox = null!;

    // tracks-page context action bar (Download album/playlist over the shared list)
    private StackPanel _tracksActionBar = null!;
    private Button _tracksDownloadBtn = null!;
    private TextBlock _tracksDownloadLabel = null!;
    private CollectionKind _collectionKind = CollectionKind.None;
    private string _collectionId = "";

    // recent / listening-stats page
    private UIElement _recentPage = null!;
    private ScrollViewer _recentScroll = null!;
    private TextBlock _recentTotalText = null!, _recentEmpty = null!;
    private ListView _recentList = null!, _recentTopTracksList = null!, _recentTopArtistsList = null!;
    private List<Track> _recentTracks = new();
    private List<TrackStat> _recentTopTracks = new();

    // charts page
    private UIElement _chartsPage = null!;
    private ScrollViewer _chartsScroll = null!;
    private ListView _chartsTrackList = null!;
    private GridView _chartsAlbumsGrid = null!, _chartsArtistsGrid = null!, _chartsPlaylistsGrid = null!;
    private List<Track> _chartsTracks = new();
    private List<Album> _chartsAlbums = new();
    private List<ArtistInfo> _chartsArtists = new();
    private List<Playlist> _chartsPlaylists = new();

    // podcasts page
    private UIElement _podcastPage = null!;
    private TextBox _podcastBox = null!;
    private GridView _podcastGrid = null!;
    private List<Podcast> _podcasts = new();

    // home page
    private UIElement _homePage = null!;
    private ScrollViewer _homeScroll = null!;
    private TextBlock _homeGreeting = null!;
    private TextBlock _homeFreeHint = null!;   // "Free account · standard quality (128 kbps)" (Free tier only)
    private ListView _homeTrackList = null!;
    private ScrollViewer _homePlaylistScroll = null!;
    private StackPanel _homePlaylistPanel = null!;
    private List<Track> _homeTracks = new();
    private List<Playlist> _homePlaylists = new();

    private Image _cover = null!;
    private TextBlock _nowTitle = null!, _nowArtist = null!, _posText = null!, _durText = null!;
    private Border _previewBadge = null!;   // "Preview" chip shown for 30-second sample streams
    private bool _lastPreview;              // last DZIsPreview() state (avoid churning the tree)
    private string _curArtist = "";   // base artist line; format badge appended each tick
    private string _nowId = "";             // id shown in the now-playing bar (engine-truth anchor)
    private string _engineNowId = "";       // last id DZNowPlayingJSON reported
    private string _engineNowArtistId = ""; // last artistId DZNowPlayingJSON reported (B3: Connect artist nav)
    private Slider _seek = null!, _volume = null!;
    private Button _playBtn = null!, _repeatBtn = null!, _addBtn = null!, _lyricsBtn = null!, _artistBtn = null!, _downloadBtn = null!;
    private FontIcon _playIcon = null!, _repeatIcon = null!;
    private ToggleButton _shuffleBtn = null!, _likeBtn = null!;
    private bool _suppressLike;
    // liked-track id cache (seeded from DZFavoriteIDsJSON) -> a truthful now-playing heart
    private HashSet<string> _likedIds = new();

    // Up-Next queue panel (transport flyout; backed by _queue / _queueIndex).
    private Button _queueBtn = null!, _queueClearBtn = null!;
    private Flyout _queueFlyout = null!;
    private ListView _queueList = null!;
    private TextBlock _queueStatus = null!;
    private bool _queueOpen;            // flyout open (drives the live-highlight refresh)
    private int _queueRenderedIndex = -1; // _queueIndex last painted into the panel

    // Offline caching (Download for offline). _offlineIds collects ids cached this
    // session (from DZDownloadForOffline's {key}) to stamp the "downloaded" glyph;
    // _offlineInfoBar + its auto-dismiss timer flash the result.
    private HashSet<string> _offlineIds = new();
    private InfoBar _offlineInfoBar = null!;
    private DispatcherQueueTimer? _offlineInfoTimer;

    // Connect picker
    private Button _connectBtn = null!;
    private Flyout _connectFlyout = null!;
    private TextBlock _connectStatus = null!;
    private ListView _connectList = null!;
    private List<ConnectDevice> _connectDevices = new();
    private string _connectedAddr = "";
    private int _connectGen;
    // Casting chip in the now-playing bar ("Playing on <device>" + "Play here").
    private Border _castChip = null!;
    private TextBlock _castChipText = null!;

    // lyrics view
    private UIElement _lyricsPage = null!;
    private ScrollViewer _lyricsScroll = null!;
    private StackPanel _lyricsPanel = null!;
    private readonly List<TextBlock> _lyricLineBlocks = new();
    private Lyrics _lyrics = new();
    private string _lyricsTrackId = "";
    private readonly Dictionary<string, Lyrics> _lyricsCache = new();
    private int _lyricsGen, _lyricActive = -1;
    private bool _lyricsShown;

    // artist view
    private UIElement _artistPage = null!;
    private ScrollViewer _artistScroll = null!;
    private TextBlock _artistHeader = null!, _artistFans = null!;
    private Button _artistRadioBtn = null!;
    private string _artistId = ""; // current artist (seeds the artist-radio button)
    private ListView _artistTopList = null!;
    private GridView _artistAlbumsGrid = null!, _artistRelatedGrid = null!;
    private List<Track> _artistTop = new();
    private List<Album> _artistAlbums = new();
    private List<ArtistInfo> _artistRelated = new();

    private List<Track> _tracks = new(), _searchTracks = new(), _queue = new();
    private List<Playlist> _playlists = new(), _searchPlaylists = new();
    private List<Album> _searchAlbums = new();
    private List<ArtistInfo> _searchArtists = new();
    private readonly List<Action> _searchActions = new(); // artist/album/playlist tile -> open

    private bool _loggedIn, _shuffle, _updatingSeek, _updatingVol, _suppressNav;
    private int _lastFinished, _artGen, _playGen, _queueIndex = -1, _repeat;
    private long _lastSeekTick;
    private long _lastTransportTick; // debounce window for the OnTick repeat/shuffle reconcile
    private int _browseGen; // drops stale track-list fetches (mirrors _lyricsGen/_connectGen)

    // play dispatch serialization (DispatchPlay/DispatchPreload)
    private Task _playChain = Task.CompletedTask;
    private int _playDispatchGen;

    // engine queue sync (SyncEngineQueueSet/Index + the OnTick DZQueueIndex adopt).
    // _queueSynced: the engine queue currently mirrors a non-episode _queue.
    // _queueSyncPending: >0 while a set/index push is in flight (suppresses adopt).
    // _queueSyncIndexGen: newest-cursor-wins guard for out-of-order index pushes.
    private bool _queueSynced;
    private int _queueSyncPending;
    private int _queueSyncIndexGen;

    // seek/volume coalescing pumps (all flags touched on the UI thread only)
    private long _pendingSeekMs;
    private bool _seekInFlight, _seekDirty;
    private double _pendingVolume;
    private bool _volInFlight, _volDirty;

    // login (embedded Deezer webview + automatic arl-cookie capture)
    private WebView2? _loginWebView;
    private ContentDialog? _loginDialog;
    private DispatcherQueueTimer? _arlPollTimer;
    private string _capturedArl = "";
    private bool _arlPollBusy;

    // OS integration state
    private Settings _settings = new();
    private Account _account = new();
    private IntPtr _appHwnd, _msgHwnd;
    private NativeMethods.NOTIFYICONDATAW _nid;
    private bool _trayAdded, _quitting;

    private SystemMediaTransportControls? _smtc;
    private MediaPlaybackStatus _lastSmtcStatus = MediaPlaybackStatus.Closed;
    private int _smtcTimelineTick;
}
