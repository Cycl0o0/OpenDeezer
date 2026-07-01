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
using System.Threading.Tasks;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Text;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
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

        var updateBar = BuildUpdateBar();
        Grid.SetRow(updateBar, 0);
        RootGrid.Children.Add(updateBar);

        BuildNav();
        BuildPages();
        Grid.SetRow(_nav, 1);
        RootGrid.Children.Add(_nav);

        var bar = BuildTransport();
        Grid.SetRow(bar, 2);
        RootGrid.Children.Add(bar);

        _nav.Content = _homePage; // show the (empty) Home page until login fills it
        _nav.Header = "Home";
    }

    // Small dismissible "a newer version is available" banner above the nav.
    // IsOpen starts false, which collapses the Auto row to zero height, so it
    // never reserves space (and never blocks startup) unless an update is found.
    private InfoBar BuildUpdateBar()
    {
        var downloadBtn = new Button { Content = "Download" };
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
        _updateBar.Title = "OpenDeezer " + info.Latest + " available";
        _updateBar.Message = string.IsNullOrEmpty(info.Notes)
            ? "A newer version is available for download."
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
        _homeItem = NavItem("Home", Symbol.Home, "home");
        _likedItem = NavItem("Liked Songs", Symbol.Audio, "liked");
        _flowItem = NavItem("Flow", Symbol.Play, "flow");
        _playlistsItem = NavItem("Playlists", Symbol.List, "playlists");
        _chartsItem = NavItem("Charts", Symbol.World, "charts");
        _podcastsItem = NavItem("Podcasts", Symbol.Microphone, "podcasts");
        _searchItem = NavItem("Search", Symbol.Find, "search");
        _nav.MenuItems.Add(_homeItem);
        _nav.MenuItems.Add(_likedItem);
        _nav.MenuItems.Add(_flowItem);
        _nav.MenuItems.Add(_playlistsItem);
        _nav.MenuItems.Add(_chartsItem);
        _nav.MenuItems.Add(_podcastsItem);
        _nav.MenuItems.Add(_searchItem);

        // Account: re-open the login chooser to re-auth / switch accounts; handled
        // like Settings/About in OnNav (a modal action, not a page).
        _accountItem = NavItem("Log in / Switch account…", Symbol.Contact, "account");
        _settingsItem = NavItem("Settings", Symbol.Setting, "settings");
        _phoneRemoteItem = NavItem("Phone Remote", Symbol.Phone, "phoneremote");
        _aboutItem = NavItem("About", Symbol.Help, "about");
        _nav.FooterMenuItems.Add(_accountItem);
        _nav.FooterMenuItems.Add(_settingsItem);
        _nav.FooterMenuItems.Add(_phoneRemoteItem);
        _nav.FooterMenuItems.Add(_aboutItem);

        _nav.SelectionChanged += OnNav;
    }

    private void BuildPages()
    {
        // Liked / playlist-detail track list (reused for both)
        _trackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _trackList.ItemClick += OnTrackClick;
        _tracksPage = _trackList;

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
            c.Children.Add(new TextBlock { Text = "New Playlist" });
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
        _searchBox = new TextBox { PlaceholderText = "Search Deezer…" };
        _searchBox.KeyDown += OnSearchKey;
        Grid.SetColumn(_searchBox, 0);
        var searchBtn = new Button { Content = "Search" };
        searchBtn.Click += (_, _) => RunSearch();
        Grid.SetColumn(searchBtn, 1);
        queryRow.Children.Add(_searchBox);
        queryRow.Children.Add(searchBtn);
        Grid.SetRow(queryRow, 0); sp.Children.Add(queryRow);

        var h1 = new TextBlock { Text = "Tracks", FontWeight = FontWeights.SemiBold };
        Grid.SetRow(h1, 1); sp.Children.Add(h1);

        _searchTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _searchTrackList.ItemClick += OnSearchTrackClick;
        Grid.SetRow(_searchTrackList, 2); sp.Children.Add(_searchTrackList);

        var h2 = new TextBlock { Text = "Albums & Playlists", FontWeight = FontWeights.SemiBold };
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

        col.Children.Add(Section("Top Tracks"));
        _chartsTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsTrackList.ItemClick += OnChartsTrackClick;
        NoInnerScroll(_chartsTrackList);
        col.Children.Add(_chartsTrackList);

        col.Children.Add(Section("Albums"));
        _chartsAlbumsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsAlbumsGrid.ItemClick += OnChartsAlbumClick;
        NoInnerScroll(_chartsAlbumsGrid);
        col.Children.Add(_chartsAlbumsGrid);

        col.Children.Add(Section("Artists"));
        _chartsArtistsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _chartsArtistsGrid.ItemClick += OnChartsArtistClick;
        NoInnerScroll(_chartsArtistsGrid);
        col.Children.Add(_chartsArtistsGrid);

        col.Children.Add(Section("Playlists"));
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
        _podcastBox = new TextBox { PlaceholderText = "Search podcasts…" };
        _podcastBox.KeyDown += OnPodcastKey;
        Grid.SetColumn(_podcastBox, 0);
        var pbtn = new Button { Content = "Search" };
        pbtn.Click += (_, _) => RunPodcastSearch();
        Grid.SetColumn(pbtn, 1);
        queryRow.Children.Add(_podcastBox);
        queryRow.Children.Add(pbtn);
        Grid.SetRow(queryRow, 0); pp.Children.Add(queryRow);

        var h = new TextBlock { Text = "Shows", FontWeight = FontWeights.SemiBold };
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
        string greeting = hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
        _homeGreeting = new TextBlock
        {
            Text = greeting,
            FontSize = 32,
            FontWeight = FontWeights.SemiBold,
            Margin = new Thickness(0, 0, 0, 4),
        };
        col.Children.Add(_homeGreeting);

        // Quick-pick cards: tap to navigate to that existing page.
        var quickRow = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 12, Margin = new Thickness(0, 8, 0, 4) };
        quickRow.Children.Add(MakeQuickCard("Liked Songs", Symbol.Audio, () => { _nav.SelectedItem = _likedItem; }));
        quickRow.Children.Add(MakeQuickCard("Flow", Symbol.Play, () => { _nav.SelectedItem = _flowItem; }));
        quickRow.Children.Add(MakeQuickCard("Charts", Symbol.World, () => { _nav.SelectedItem = _chartsItem; }));
        quickRow.Children.Add(MakeQuickCard("Podcasts", Symbol.Microphone, () => { _nav.SelectedItem = _podcastsItem; }));
        col.Children.Add(quickRow);

        // Top Tracks (vertical list; inner scroll disabled so the outer ScrollViewer drives).
        col.Children.Add(Section("Top Tracks"));
        _homeTrackList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _homeTrackList.ItemClick += OnHomeTrackClick;
        NoInnerScroll(_homeTrackList);
        col.Children.Add(_homeTrackList);

        // Your Playlists (horizontal scroll rail of tiles).
        col.Children.Add(Section("Your Playlists"));
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

        col.Children.Add(Section("Top Tracks"));
        _artistTopList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _artistTopList.ItemClick += OnArtistTopClick;
        NoInnerScroll(_artistTopList);
        col.Children.Add(_artistTopList);

        col.Children.Add(Section("Albums"));
        _artistAlbumsGrid = new GridView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true };
        _artistAlbumsGrid.ItemClick += OnArtistAlbumClick;
        NoInnerScroll(_artistAlbumsGrid);
        col.Children.Add(_artistAlbumsGrid);

        col.Children.Add(Section("Related Artists"));
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
        ShowLyricsMessage("Play a track to see its lyrics.");
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
        _nowTitle = new TextBlock { Text = "Logging in…", FontWeight = FontWeights.SemiBold, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        _nowArtist = new TextBlock { Opacity = 0.6, FontSize = 12, TextWrapping = TextWrapping.NoWrap, TextTrimming = TextTrimming.CharacterEllipsis };
        now.Children.Add(_nowTitle); now.Children.Add(_nowArtist);
        left.Children.Add(now);
        _likeBtn = new ToggleButton { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Heart
        ToolTipService.SetToolTip(_likeBtn, "Like / unlike");
        _likeBtn.Click += OnLike;
        _addBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Add to playlist
        ToolTipService.SetToolTip(_addBtn, "Add to playlist");
        _addBtn.Click += OnAddCurrentToPlaylist;
        left.Children.Add(_likeBtn); left.Children.Add(_addBtn);
        Grid.SetColumn(left, 0); bar.Children.Add(left);

        // ---- CENTRE: transport row (shuffle - prev - play - next - repeat) + seek row ----
        var center = new StackPanel { Spacing = 4, VerticalAlignment = VerticalAlignment.Center, HorizontalAlignment = HorizontalAlignment.Center };
        var tr = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6, HorizontalAlignment = HorizontalAlignment.Center, VerticalAlignment = VerticalAlignment.Center };
        _shuffleBtn = new ToggleButton { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Shuffle
        ToolTipService.SetToolTip(_shuffleBtn, "Shuffle");
        _shuffleBtn.Click += OnShuffle;
        var prevBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Previous
        ToolTipService.SetToolTip(prevBtn, "Previous");
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
        ToolTipService.SetToolTip(_playBtn, "Play / pause");
        _playBtn.Click += (_, _) => DeezerCore.DZTogglePause();
        var nextBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 } }; // Next
        ToolTipService.SetToolTip(nextBtn, "Next");
        nextBtn.Click += (_, _) => Next();
        _repeatIcon = new FontIcon { Glyph = "", FontSize = 14 }; // RepeatAll
        _repeatBtn = new Button { Content = _repeatIcon };
        ToolTipService.SetToolTip(_repeatBtn, "Repeat: off");
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
        ToolTipService.SetToolTip(_lyricsBtn, "Lyrics");
        _lyricsBtn.Click += (_, _) => ShowLyrics();
        _artistBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Contact
        ToolTipService.SetToolTip(_artistBtn, "Artist");
        _artistBtn.Click += OnArtist;
        // OpenDeezer Connect: a discreet cast button whose flyout lists LAN devices.
        _connectBtn = new Button { Content = new FontIcon { Glyph = "", FontSize = 14 }, Padding = new Thickness(6, 2, 6, 2) }; // Cast
        ToolTipService.SetToolTip(_connectBtn, "Connect to a device");
        _connectFlyout = new Flyout();
        var cp = new StackPanel { Spacing = 8, MinWidth = 280, Padding = new Thickness(4) };
        cp.Children.Add(new TextBlock { Text = "Connect to a device", FontWeight = FontWeights.SemiBold });
        _connectStatus = new TextBlock { Opacity = 0.7, TextWrapping = TextWrapping.Wrap };
        _connectList = new ListView { SelectionMode = ListViewSelectionMode.None, IsItemClickEnabled = true, MaxHeight = 320 };
        _connectList.ItemClick += OnConnectItemClick;
        cp.Children.Add(_connectStatus); cp.Children.Add(_connectList);
        _connectFlyout.Content = cp;
        _connectFlyout.Opened += OnConnectOpened;
        _connectBtn.Flyout = _connectFlyout;
        var volIcon = new FontIcon { Glyph = "", FontSize = 14, VerticalAlignment = VerticalAlignment.Center }; // Volume
        _volume = new Slider { Minimum = 0, Maximum = 100, Value = 100, Width = 100, VerticalAlignment = VerticalAlignment.Center, Foreground = _accent };
        _volume.ValueChanged += OnVolumeChanged;
        right.Children.Add(_lyricsBtn); right.Children.Add(_artistBtn); right.Children.Add(_connectBtn);
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
        ToolTipService.SetToolTip(b, "Explicit content");
        return b;
    }

    private UIElement MakeTrackRow(Track t, int index)
    {
        var g = new Grid { Tag = index, Height = 56, Padding = new Thickness(6, 4, 6, 4), ColumnSpacing = 12 };
        g.ColumnDefinitions.Add(ColAuto());
        g.ColumnDefinitions.Add(ColStar());
        g.ColumnDefinitions.Add(ColAuto());
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
        var dur = new TextBlock { Text = Wire.TimeText(t.DurationMs), Opacity = 0.6, VerticalAlignment = VerticalAlignment.Center };
        Grid.SetColumn(dur, 2); g.Children.Add(dur);
        if (!string.IsNullOrEmpty(t.ArtworkUrl)) LoadArt(img, t.ArtworkUrl, _artGen, false);
        // Right-click actions (skipped for podcast episodes, which can't be liked /
        // added to a music playlist).
        if (!t.IsEpisode && !string.IsNullOrEmpty(t.Id))
        {
            var mf = new MenuFlyout();
            var like = new MenuFlyoutItem { Text = "Like", Tag = t.Id };
            like.Click += OnRowLike;
            var add = new MenuFlyoutItem { Text = "Add to playlist…", Tag = t.Id };
            add.Click += OnRowAddToPlaylist;
            mf.Items.Add(like); mf.Items.Add(add);
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
            var tile = MakeTile(p.Name, p.TrackCount + " tracks", p.ArtworkUrl, i);
            // Per-tile right-click: rename / delete (Tag carries the _playlists index).
            var mf = new MenuFlyout();
            var rn = new MenuFlyoutItem { Text = "Rename…", Tag = i };
            rn.Click += OnPlaylistRename;
            var del = new MenuFlyoutItem { Text = "Delete…", Tag = i };
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
        _nowTitle.Text = "Logging in…";
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
            _nowTitle.Text = "Login failed";
            await ShowMessage("Login failed", "That ARL is invalid or expired. Try logging in again.");
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

        _nowTitle.Text = "Checking account…";
        var acct = await Task.Run(() => DeezerCore.Account());
        _account = acct;
        if (!_account.Premium) { ShowBlocked(); return; } // Free account -> gate the app

        _lastFinished = DeezerCore.DZFinishedCount();
        _updatingVol = true; _volume.Value = DeezerCore.DZVolume() * 100.0; _updatingVol = false;
        _timer.Start();
        _nowTitle.Text = "Not playing";
        _nowArtist.Text = "";
        _suppressNav = false;
        _nav.SelectedItem = _homeItem; // -> OnNav -> LoadHome
    }

    // Free-account block: replace the ENTIRE window content with a non-dismissible
    // message; the only action is Quit. A Premium subscription is required.
    private void ShowBlocked()
    {
        _blocked = true;
        try { _timer?.Stop(); } catch { }
        try { DeezerCore.DZStop(); } catch { }

        var page = new Grid { Background = new SolidColorBrush(Color.FromArgb(0xFF, 0x14, 0x04, 0x1E)) };
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
            Text = "Premium required",
            FontSize = 26,
            FontWeight = FontWeights.SemiBold,
            TextWrapping = TextWrapping.Wrap,
            TextAlignment = TextAlignment.Center,
            HorizontalAlignment = HorizontalAlignment.Center,
        });
        string offer = string.IsNullOrEmpty(_account.Offer) ? "Deezer Free" : _account.Offer;
        sp.Children.Add(new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            TextAlignment = TextAlignment.Center,
            Opacity = 0.85,
            HorizontalAlignment = HorizontalAlignment.Center,
            Text = "Your account (" + offer + ") isn't Premium. Subscribe at deezer.com, then restart OpenDeezer.",
        });
        var quit = new Button { Content = "Quit", HorizontalAlignment = HorizontalAlignment.Center };
        quit.Click += (_, _) => QuitApp();
        sp.Children.Add(quit);
        page.Children.Add(sp);
        Content = page; // wholesale replace -> the app can no longer be used
    }

    // Login chooser: "Log in with Deezer" opens the embedded webview, "Enter ARL"
    // is the manual fallback. Cancel leaves the app idle (relaunch to retry).
    private async void ShowLoginChoice()
    {
        _nowTitle.Text = "Not signed in";
        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = "Sign in to Deezer",
            Content = new TextBlock
            {
                TextWrapping = TextWrapping.Wrap,
                Text = "Choose how you'd like to sign in.",
            },
            PrimaryButtonText = "Log in with Deezer",
            SecondaryButtonText = "Enter ARL manually",
            CloseButtonText = "Cancel",
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
            string entered = await PromptText("Log in with ARL", "Paste your Deezer ARL", "");
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
            Title = "Log in with Deezer",
            CloseButtonText = "Cancel",
        };
        // Let the dialog grow to fit the web page (defaults cap ~548 px).
        _loginDialog.Resources["ContentDialogMaxWidth"] = 620.0;
        _loginDialog.Resources["ContentDialogMaxHeight"] = 740.0;
        _loginWebView = new WebView2 { Width = 560, Height = 640 };
        _loginDialog.Content = _loginWebView;

        // Do NOT await EnsureCoreWebView2Async here: it only completes after the
        // control loads, which is after ShowAsync -> deadlock. Setting Source kicks
        // off implicit init; the poll waits for CoreWebView2() to become non-null.
        try { _loginWebView.Source = new Uri("https://www.deezer.com/login"); }
        catch { _loginWebView = null; _loginDialog = null; return ""; }

        _arlPollTimer = DispatcherQueue.CreateTimer();
        _arlPollTimer.Interval = TimeSpan.FromMilliseconds(700);
        _arlPollTimer.Tick += OnArlPoll;
        _arlPollTimer.Start();

        await ShowDialog(_loginDialog); // returns when arl captured (Hide) or Cancel

        if (_arlPollTimer != null) { _arlPollTimer.Stop(); _arlPollTimer = null; }
        _loginWebView = null;
        _loginDialog = null;
        return _capturedArl;
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
            case "home": nav.Header = "Home"; nav.Content = _homePage; LoadHome(); break;
            case "liked": nav.Header = "Liked Songs"; nav.Content = _tracksPage; LoadFavorites(); break;
            case "flow": nav.Header = "Flow"; nav.Content = _tracksPage; LoadFlow(); break;
            case "charts": nav.Header = "Charts"; nav.Content = _chartsPage; LoadCharts(); break;
            case "playlists": nav.Header = "Playlists"; nav.Content = _playlistsPage; LoadPlaylists(); break;
            case "podcasts": nav.Header = "Podcasts"; nav.Content = _podcastPage; _podcastBox.Focus(FocusState.Programmatic); break;
            case "search": nav.Header = "Search"; nav.Content = _searchPage; _searchBox.Focus(FocusState.Programmatic); break;
        }
    }

    // ---- browse (heavy work off the UI thread) -------------------------------
    private async void LoadFavorites()
    {
        if (!_loggedIn) return;
        var tracks = await Task.Run(() => DeezerCore.Favorites());
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
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
        var tracks = await Task.Run(() => DeezerCore.Flow());
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
        _homeGreeting.Text = hour < 12 ? "Good morning" : hour < 18 ? "Good afternoon" : "Good evening";
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
        var tracks = await Task.Run(() => DeezerCore.PlaylistTracks(p.Id));
        _tracks = tracks;
        _artGen++;
        FillTrackList(_trackList, _tracks);
    }

    private async void OpenAlbum(Album a)
    {
        _lyricsShown = false;
        _nav.Header = a.Name;
        _nav.Content = _tracksPage;
        var tracks = await Task.Run(() => DeezerCore.AlbumTracks(a.Id));
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
            string sub = p.EpisodeCount > 0 ? p.EpisodeCount + " episodes" : p.Description;
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
        var eps = await Task.Run(() => Wire.ParseEpisodes(DeezerCore.TakeJson(DeezerCore.DZPodcastEpisodesJSON(pod.Id))));
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
        _lyricsShown = false;
        _nav.Header = "Artist";
        _nav.Content = _artistPage;
        _artistHeader.Text = "Loading…"; _artistFans.Text = "";
        _artistTopList.Items.Clear();
        _artistAlbumsGrid.Items.Clear();
        _artistRelatedGrid.Items.Clear();
        var prof = await Task.Run(() => DeezerCore.ArtistProfile(artistId));
        _artistTop = prof.Top;
        _artistAlbums = prof.Albums;
        _artistRelated = prof.Related;
        _artistHeader.Text = string.IsNullOrEmpty(prof.Artist.Name) ? "Artist" : prof.Artist.Name;
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
        if (string.IsNullOrEmpty(aid)) { _ = ShowMessage("No artist", "Start playing a track to view its artist."); return; }
        OpenArtist(aid);
    }

    // ---- lyrics view ---------------------------------------------------------
    private void ShowLyrics()
    {
        if (!_loggedIn) return;
        _lyricsShown = true;
        _nav.Header = "Lyrics";
        _nav.Content = _lyricsPage;
        string id = CurrentTrackId();
        if (string.IsNullOrEmpty(id)) { ShowLyricsMessage("Play a track to see its lyrics."); return; }
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
        ShowLyricsMessage("Loading lyrics…");
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
            ShowLyricsMessage("No lyrics available.");
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
        var (tracks, albums, plists) = await Task.Run(() =>
        {
            string json = DeezerCore.TakeJson(DeezerCore.DZSearchJSON(q));
            return (Wire.ParseTracks(json), Wire.ParseAlbums(json), Wire.ParsePlaylists(json));
        });
        _searchTracks = tracks;
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
        if (!_loggedIn) { _connectStatus.Text = "Sign in to use Connect."; return; }
        _connectStatus.Text = "Searching for devices…";
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
        _connectList.Items.Add(MakeConnectRow("This computer", "Local playback", string.IsNullOrEmpty(connAddr), -1));
        for (int i = 0; i < _connectDevices.Count; i++)
        {
            var d = _connectDevices[i];
            string sub = Wire.ConnectTypeLabel(d.Client);
            if (!string.IsNullOrEmpty(d.Version)) sub = sub + " · OpenDeezer " + d.Version;
            _connectList.Items.Add(MakeConnectRow(string.IsNullOrEmpty(d.Name) ? d.Addr : d.Name, sub, d.Addr == connAddr, i));
        }
        if (_connectDevices.Count == 0) _connectStatus.Text = "No other devices found on your network.";
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
        if (!ok) _ = ShowMessage("Couldn't connect", "That device could not be reached.");
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
        if (_connectBtn == null) return;
        if (!string.IsNullOrEmpty(addr))
        {
            _connectBtn.Foreground = _accent;
            string who = string.IsNullOrEmpty(name) ? addr : name;
            ToolTipService.SetToolTip(_connectBtn, "Playing on " + who);
        }
        else
        {
            _connectBtn.ClearValue(Control.ForegroundProperty);
            ToolTipService.SetToolTip(_connectBtn, "Connect to a device");
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
        await Task.Run(() => { if (like) DeezerCore.DZAddFavorite(id); else DeezerCore.DZRemoveFavorite(id); });
    }
    private void OnRowLike(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id && !string.IsNullOrEmpty(id)) DispatchLike(id, true);
    }
    private void OnRowAddToPlaylist(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string id && !string.IsNullOrEmpty(id)) ShowAddToPlaylist(id);
    }
    private void OnAddCurrentToPlaylist(object s, RoutedEventArgs e)
    {
        string id = CurrentTrackId();
        if (string.IsNullOrEmpty(id)) { _ = ShowMessage("No track", "Start playing a track to add it to a playlist."); return; }
        ShowAddToPlaylist(id);
    }

    // Picker: "New playlist…" + the user's playlists. Selection adds the track;
    // "New playlist…" prompts for a name, creates it, then adds the track.
    private async void ShowAddToPlaylist(string trackId)
    {
        if (!_loggedIn || string.IsNullOrEmpty(trackId)) return;
        var plists = await Task.Run(() => DeezerCore.Playlists());

        var list = new ListView { SelectionMode = ListViewSelectionMode.Single, MaxHeight = 360, MinWidth = 320 };
        list.Items.Add(new TextBlock { Text = "＋  New playlist…" }); // index 0
        foreach (var p in plists) list.Items.Add(new TextBlock { Text = p.Name });
        list.SelectedIndex = plists.Count == 0 ? 0 : 1;

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = "Add to playlist",
            Content = list,
            PrimaryButtonText = "Add",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };
        if (await ShowDialog(dlg) != ContentDialogResult.Primary) return;

        int idx = list.SelectedIndex;
        if (idx < 0) return;
        string playlistId;
        if (idx == 0) // New playlist…
        {
            string name = (await PromptText("New playlist", "Playlist name", "")).Trim();
            if (string.IsNullOrEmpty(name)) return;
            playlistId = await Task.Run(() => Wire.ParseCreatedId(DeezerCore.TakeJson(DeezerCore.DZCreatePlaylist(name))));
            if (string.IsNullOrEmpty(playlistId)) { _ = ShowMessage("Couldn't create playlist", "The playlist could not be created."); return; }
        }
        else
        {
            int pi = idx - 1;
            if (pi < 0 || pi >= plists.Count) return;
            playlistId = plists[pi].Id;
        }
        if (string.IsNullOrEmpty(playlistId)) return;
        bool ok = await Task.Run(() => DeezerCore.DZAddToPlaylist(playlistId, trackId) != 0);
        if (!ok) _ = ShowMessage("Couldn't add to playlist", "The track could not be added.");
    }

    private async void OnNewPlaylist(object s, RoutedEventArgs e)
    {
        if (!_loggedIn) return;
        string name = (await PromptText("New playlist", "Playlist name", "")).Trim();
        if (string.IsNullOrEmpty(name)) return;
        string newId = await Task.Run(() => Wire.ParseCreatedId(DeezerCore.TakeJson(DeezerCore.DZCreatePlaylist(name))));
        if (string.IsNullOrEmpty(newId)) { _ = ShowMessage("Couldn't create playlist", "The playlist could not be created."); return; }
        LoadPlaylists(); // refresh the grid
    }

    private async void OnPlaylistRename(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not int i || i < 0 || i >= _playlists.Count) return;
        var p = _playlists[i];
        string name = (await PromptText("Rename playlist", "Playlist name", p.Name)).Trim();
        if (string.IsNullOrEmpty(name)) return;
        bool ok = await Task.Run(() => DeezerCore.DZRenamePlaylist(p.Id, name) != 0);
        if (!ok) { _ = ShowMessage("Couldn't rename", "The playlist could not be renamed."); return; }
        LoadPlaylists();
    }

    private async void OnPlaylistDelete(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not int i || i < 0 || i >= _playlists.Count) return;
        var p = _playlists[i];
        bool yes = await Confirm("Delete playlist", "Delete “" + p.Name + "”? This can't be undone.", "Delete");
        if (!yes) return;
        bool ok = await Task.Run(() => DeezerCore.DZDeletePlaylist(p.Id) != 0);
        if (!ok) { _ = ShowMessage("Couldn't delete", "The playlist could not be deleted."); return; }
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
            PrimaryButtonText = "OK",
            CloseButtonText = "Cancel",
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
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
        };
        return await ShowDialog(dlg) == ContentDialogResult.Primary;
    }

    // ---- playback ------------------------------------------------------------
    private void PlayFrom(List<Track> list, int index)
    {
        if (_blocked) return; // Free account: playback gated
        _queue = new List<Track>(list);
        _queueIndex = index;
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
    // Blocking play (then optional preload) serialized on one background task.
    private async void DispatchPlay(string id, long dur, bool episode, string nextId, long nextDur)
    {
        await Task.Run(() =>
        {
            if (episode) DeezerCore.DZPlayEpisode(id, dur); // plain, unencrypted stream
            else DeezerCore.DZPlay(id, dur);                // prepares the stream over the network -> blocks
            if (!string.IsNullOrEmpty(nextId)) DeezerCore.DZPreload(nextId, nextDur); // warm next for the gapless swap
        });
    }
    private async void DispatchPreload(string id, long dur)
    {
        await Task.Run(() => DeezerCore.DZPreload(id, dur));
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
        // No "is-liked" query exists; reset the heart to off on every track change.
        // Like / add-to-playlist are library-track only, so disable them for episodes.
        if (_likeBtn != null)
        {
            _suppressLike = true; _likeBtn.IsChecked = false; _suppressLike = false;
            _likeBtn.IsEnabled = !t.IsEpisode;
        }
        if (_addBtn != null) _addBtn.IsEnabled = !t.IsEpisode;
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
    }
    private void Prev()
    {
        if (_queue.Count == 0) return;
        if (_queueIndex > 0) --_queueIndex;
        PlayCurrent();
    }
    private void OnShuffle(object s, RoutedEventArgs e) { _shuffle = _shuffleBtn.IsChecked == true; DeezerCore.DZSetShuffle(_shuffle ? 1 : 0); if (_shuffle) _shuffleBtn.Foreground = _accent; else _shuffleBtn.ClearValue(Control.ForegroundProperty); }
    private void OnRepeat(object s, RoutedEventArgs e)
    {
        _repeat = (_repeat + 1) % 3;
        _repeatIcon.Glyph = _repeat == 2 ? "" : ""; // RepeatOne or RepeatAll
        if (_repeat == 0) _repeatBtn.ClearValue(Control.ForegroundProperty); else _repeatBtn.Foreground = _accent;
        ToolTipService.SetToolTip(_repeatBtn, _repeat == 0 ? "Repeat: off" : _repeat == 1 ? "Repeat: all" : "Repeat: one");
        DeezerCore.DZSetRepeat(_repeat);
    }
    private void OnSeekChanged(object s, RangeBaseValueChangedEventArgs e)
    {
        if (_updatingSeek) return; // programmatic update from the poll tick
        long ms = (long)Math.Round(e.NewValue);
        DeezerCore.DZSeek(ms);
        _posText.Text = Wire.TimeText(ms);
        _lastSeekTick = Environment.TickCount64;
    }
    private void OnVolumeChanged(object s, RangeBaseValueChangedEventArgs e)
    {
        if (_updatingVol) return;
        DeezerCore.DZSetVolume(e.NewValue / 100.0);
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

        // Keep the now-playing bar in sync with the engine's actual track (local
        // control-API plays AND, when routed over Connect, the remote device's
        // track). Adopt only on a genuine engine-side transition to a real track
        // not already shown.
        {
            string json = DeezerCore.NowPlaying();
            try
            {
                using var doc = JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json);
                var obj = doc.RootElement;
                if (obj.ValueKind == JsonValueKind.Object)
                {
                    string npid = obj.Str("id");
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
        }
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
                case SystemMediaTransportControlsButton.Play: DeezerCore.DZResume(); break;
                case SystemMediaTransportControlsButton.Pause: DeezerCore.DZPause(); break;
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
            DeezerCore.DZSeek(ms);
            _lastSeekTick = Environment.TickCount64;
            _updatingSeek = true; _seek.Value = ms; _updatingSeek = false;
            _posText.Text = Wire.TimeText(ms);
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
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_STRING, (IntPtr)NativeMethods.MENU_RESTORE, "Open OpenDeezer");
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_SEPARATOR, IntPtr.Zero, null);
        NativeMethods.AppendMenuW(menu, NativeMethods.MF_STRING, (IntPtr)NativeMethods.MENU_QUIT, "Quit");
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
            CloseButtonText = "OK",
        };
        return ShowDialog(dlg);
    }

    private async void ShowSettings()
    {
        // Output devices + current engine audio state read off the UI thread.
        var (devJson, curDev, curGapless, curCrossfade, ctrlJson) = await Task.Run(() =>
        {
            string dj = DeezerCore.TakeJson(DeezerCore.DZAudioDevicesJSON());
            string cd = DeezerCore.CurrentAudioDevice();
            string cj = DeezerCore.ControlConfig();
            return (dj, cd, DeezerCore.DZGapless() != 0, DeezerCore.DZCrossfadeMS(), cj);
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
        quality.Items.Add("Normal — MP3 128 kbps");
        quality.Items.Add("High — MP3 320 kbps");
        quality.Items.Add("HiFi — FLAC lossless");
        quality.SelectedIndex = _settings.Quality;
        var qsec = new StackPanel { Spacing = 4 };
        qsec.Children.Add(new TextBlock { Text = "Audio quality", FontWeight = FontWeights.SemiBold });
        qsec.Children.Add(quality);
        if (_account.LoggedIn)
        {
            bool exceeds = (_settings.Quality >= 2 && !_account.CanHifi) || (_settings.Quality >= 1 && !_account.CanHq);
            if (exceeds)
                qsec.Children.Add(new TextBlock
                {
                    Text = "Your plan (" + _account.Offer + ") may not support this quality; playback falls back automatically.",
                    TextWrapping = TextWrapping.Wrap,
                    Opacity = 0.8,
                });
        }

        // Output device (id "" = system default).
        var devCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        int selDev = 0;
        for (int i = 0; i < devices.Count; i++)
        {
            string label = string.IsNullOrEmpty(devices[i].Name) ? "System default" : devices[i].Name;
            if (devices[i].IsDefault) label += "  (default)";
            devCombo.Items.Add(label);
            if (devices[i].Id == curDev) selDev = i;
        }
        if (devices.Count > 0) devCombo.SelectedIndex = selDev;
        else { devCombo.IsEnabled = false; devCombo.PlaceholderText = "No output devices"; }
        var asec = new StackPanel { Spacing = 4 };
        asec.Children.Add(new TextBlock { Text = "Output device", FontWeight = FontWeights.SemiBold });
        asec.Children.Add(devCombo);

        // Gapless
        var gapSwitch = new ToggleSwitch
        {
            OnContent = "Play consecutive tracks with no silence",
            OffContent = "Brief gap between tracks",
            IsOn = curGapless,
        };
        var gsec = new StackPanel { Spacing = 4 };
        gsec.Children.Add(new TextBlock { Text = "Gapless playback", FontWeight = FontWeights.SemiBold });
        gsec.Children.Add(gapSwitch);

        // Crossfade (0 / 3 / 6 / 12 s).
        int[] cfVals = { 0, 3000, 6000, 12000 };
        var cfCombo = new ComboBox { HorizontalAlignment = HorizontalAlignment.Stretch };
        cfCombo.Items.Add("Off");
        cfCombo.Items.Add("3 seconds");
        cfCombo.Items.Add("6 seconds");
        cfCombo.Items.Add("12 seconds");
        int cfIdx = 0;
        for (int i = 3; i >= 0; i--) { if (curCrossfade >= cfVals[i]) { cfIdx = i; break; } }
        cfCombo.SelectedIndex = cfIdx;
        var csec = new StackPanel { Spacing = 4 };
        csec.Children.Add(new TextBlock { Text = "Crossfade", FontWeight = FontWeights.SemiBold });
        csec.Children.Add(cfCombo);

        // Volume normalization (ReplayGain) -- bound to engine state.
        var rg = new ToggleSwitch
        {
            OnContent = "Normalize loudness across tracks (ReplayGain)",
            OffContent = "Play tracks at their original loudness",
            IsOn = DeezerCore.DZReplayGain() != 0,
        };
        var rsec = new StackPanel { Spacing = 4 };
        rsec.Children.Add(new TextBlock { Text = "Volume normalization", FontWeight = FontWeights.SemiBold });
        rsec.Children.Add(rg);

        // Background / close-to-tray
        var tray = new ToggleSwitch
        {
            OnContent = "Closing the window keeps playing in the tray",
            OffContent = "Closing the window quits OpenDeezer",
            IsOn = _settings.CloseToTray,
        };
        var tsec = new StackPanel { Spacing = 4 };
        tsec.Children.Add(new TextBlock { Text = "Background playback", FontWeight = FontWeights.SemiBold });
        tsec.Children.Add(tray);

        // Remote control (control API / phone remote): enable, LAN reachability, token.
        // Applies live -- every change is pushed straight to the engine, not gated
        // behind the dialog's Save button.
        var ctrlEnableSwitch = new ToggleSwitch
        {
            OnContent = "Remote control on",
            OffContent = "Remote control off",
            IsOn = ctrlEnabled,
        };
        var ctrlLanSwitch = new ToggleSwitch
        {
            OnContent = "Reachable on the local network",
            OffContent = "This computer only",
            IsOn = ctrlLan,
            IsEnabled = ctrlEnabled,
        };
        var ctrlTokenBox = new TextBox
        {
            PlaceholderText = "Access token (optional)",
            Text = ctrlToken,
            IsEnabled = ctrlEnabled,
        };
        async void ApplyControlConfig()
        {
            bool on = ctrlEnableSwitch.IsOn;
            string addr = ctrlLanSwitch.IsOn ? ":7654" : "";
            string token = ctrlTokenBox.Text ?? "";
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
        rcsec.Children.Add(new TextBlock { Text = "Remote control", FontWeight = FontWeights.SemiBold });
        rcsec.Children.Add(new TextBlock { Text = "Control playback from another device on your network.", Opacity = 0.7, TextWrapping = TextWrapping.Wrap });
        rcsec.Children.Add(ctrlEnableSwitch);
        rcsec.Children.Add(ctrlLanSwitch);
        rcsec.Children.Add(ctrlTokenBox);

        // Updates: on-demand GitHub release check (never downloads/installs anything).
        var updStatus = new TextBlock { Text = "Check GitHub for a newer release.", Opacity = 0.8, TextWrapping = TextWrapping.Wrap };
        var updBtn = new Button { Content = "Check for updates" };
        var updDownloadBtn = new Button { Content = "Download", Visibility = Visibility.Collapsed };
        string updCheckUrl = "";
        updBtn.Click += async (_, _) =>
        {
            updBtn.IsEnabled = false;
            updStatus.Text = "Checking…";
            UpdateInfo info;
            try { info = await Task.Run(() => DeezerCore.CheckUpdate()); }
            catch { info = new UpdateInfo(); }
            if (info.HasUpdate)
            {
                updStatus.Text = "v" + info.Latest + " available (you have v" + info.Current + ").";
                updCheckUrl = info.Url;
                updDownloadBtn.Visibility = Visibility.Visible;
                ShowUpdateNotice(info); // also surface the dismissible banner for after the dialog closes
            }
            else
            {
                updStatus.Text = string.IsNullOrEmpty(info.Latest)
                    ? "Could not check for updates. Try again later."
                    : "You're up to date (v" + info.Current + ").";
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
        usec.Children.Add(new TextBlock { Text = "Updates", FontWeight = FontWeights.SemiBold });
        usec.Children.Add(updStatus);
        usec.Children.Add(updRow);

        sp.Children.Add(qsec);
        sp.Children.Add(asec);
        sp.Children.Add(gsec);
        sp.Children.Add(csec);
        sp.Children.Add(rsec);
        sp.Children.Add(tsec);
        sp.Children.Add(rcsec);
        sp.Children.Add(usec);

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = "Settings",
            Content = sp,
            PrimaryButtonText = "Save",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Primary,
        };
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

            Config.SaveSettings(_settings);
        }
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
            Text = "Scan with your phone (same Wi-Fi), then enter the code.",
            TextWrapping = TextWrapping.Wrap,
            Opacity = 0.8,
        });

        var tog = new ToggleSwitch
        {
            IsOn = initOn,
            OnContent = "Phone Remote active",
            OffContent = "Phone Remote off",
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
            Title = "Phone Remote",
            Content = sp,
            CloseButtonText = "Close",
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
        sp.Children.Add(new TextBlock { Text = "OpenDeezer 1.5.0", FontSize = 22, FontWeight = FontWeights.SemiBold, Foreground = _accent });
        sp.Children.Add(new TextBlock { Text = "An open source reimplementation of Deezer.", TextWrapping = TextWrapping.Wrap });
        sp.Children.Add(new TextBlock
        {
            TextWrapping = TextWrapping.Wrap,
            Text = "Native Windows client (WinUI 3 · C# · Fluent). The engine — login, browse, " +
                   "Blowfish decrypt, MP3 decode, WASAPI playback — is the Go core libdeezercore.dll, linked in-process.",
        });
        if (_account.LoggedIn && !string.IsNullOrEmpty(_account.Name))
            sp.Children.Add(new TextBlock { Text = "Signed in: " + _account.Name + " · " + _account.Offer, TextWrapping = TextWrapping.Wrap, FontWeight = FontWeights.SemiBold });
        sp.Children.Add(new TextBlock { Text = "By Cycl0o0. Licensed under AGPL-3.0.", Opacity = 0.8 });

        var dlg = new ContentDialog
        {
            XamlRoot = Content.XamlRoot,
            Title = "About OpenDeezer",
            Content = sp,
            CloseButtonText = "Close",
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
                               _podcastsItem = null!, _searchItem = null!, _accountItem = null!, _settingsItem = null!,
                               _phoneRemoteItem = null!, _aboutItem = null!;
    private NavigationViewItem? _lastContentItem; // null until the first content page is opened

    private UIElement _tracksPage = null!, _playlistsPage = null!, _searchPage = null!;
    private ListView _trackList = null!, _searchTrackList = null!;
    private GridView _playlistGrid = null!, _searchGrid = null!;
    private TextBox _searchBox = null!;

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
    private ListView _homeTrackList = null!;
    private ScrollViewer _homePlaylistScroll = null!;
    private StackPanel _homePlaylistPanel = null!;
    private List<Track> _homeTracks = new();
    private List<Playlist> _homePlaylists = new();

    private Image _cover = null!;
    private TextBlock _nowTitle = null!, _nowArtist = null!, _posText = null!, _durText = null!;
    private string _curArtist = "";   // base artist line; format badge appended each tick
    private string _nowId = "";             // id shown in the now-playing bar (engine-truth anchor)
    private string _engineNowId = "";       // last id DZNowPlayingJSON reported
    private string _engineNowArtistId = ""; // last artistId DZNowPlayingJSON reported (B3: Connect artist nav)
    private Slider _seek = null!, _volume = null!;
    private Button _playBtn = null!, _repeatBtn = null!, _addBtn = null!, _lyricsBtn = null!, _artistBtn = null!;
    private FontIcon _playIcon = null!, _repeatIcon = null!;
    private ToggleButton _shuffleBtn = null!, _likeBtn = null!;
    private bool _suppressLike;

    // Connect picker
    private Button _connectBtn = null!;
    private Flyout _connectFlyout = null!;
    private TextBlock _connectStatus = null!;
    private ListView _connectList = null!;
    private List<ConnectDevice> _connectDevices = new();
    private string _connectedAddr = "";
    private int _connectGen;

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
    private ListView _artistTopList = null!;
    private GridView _artistAlbumsGrid = null!, _artistRelatedGrid = null!;
    private List<Track> _artistTop = new();
    private List<Album> _artistAlbums = new();
    private List<ArtistInfo> _artistRelated = new();

    private List<Track> _tracks = new(), _searchTracks = new(), _queue = new();
    private List<Playlist> _playlists = new(), _searchPlaylists = new();
    private List<Album> _searchAlbums = new();
    private readonly List<Action> _searchActions = new(); // album/playlist tile -> open

    private bool _loggedIn, _blocked, _shuffle, _updatingSeek, _updatingVol, _suppressNav;
    private int _lastFinished, _artGen, _playGen, _queueIndex = -1, _repeat;
    private long _lastSeekTick;

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
