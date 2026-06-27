// OpenDeezer - native Windows front-end (WinUI 3, C++/WinRT, Fluent).
//
// The whole engine (login, browse, Blowfish decrypt, MP3 decode, WASAPI
// playback) is the Go core compiled to a C-ABI shared library
// (lib/libdeezercore.dll) and called in-process over extern "C". This file is
// UI only: an entirely code-built NavigationView + track ListView + playlist /
// search grids + a bottom now-playing transport bar + About/Settings dialogs.
// No XAML markup, no .idl, no markup compiler -- the App subclass implements
// IXamlMetadataProvider so default control themes resolve.
//
// Every blocking DZ* call (DZInit / browse / DZPlay / DZFetch) runs on a
// background thread via winrt::resume_background and is marshalled back to the
// UI thread with winrt::resume_foreground(DispatcherQueue). A single 300 ms
// DispatcherQueueTimer polls cheap player state and auto-advances when
// DZFinishedCount() increments.
//
// OS integration (added):
//   * SystemMediaTransportControls (SMTC) -- the system media overlay / lock
//     screen / media keys. Acquired via ISystemMediaTransportControlsInterop::
//     GetForWindow(hwnd) and wired straight to the EXISTING transport handlers;
//     DisplayUpdater + Timeline are pushed on track change and from the poll.
//   * Settings ContentDialog -- audio quality (MP3_128 / MP3_320 -> DZSetQuality)
//     and a close-to-tray toggle, persisted to %APPDATA%\opendeezer\settings.json.
//   * Tray icon (Shell_NotifyIcon) -- close-to-tray keeps the engine playing in
//     the background; the tray menu restores the window or quits.

#include <windows.h>
#include <shellapi.h>
#include <unknwn.h>   // must precede C++/WinRT headers for classic-COM interop
                      // (IWindowNative / ISystemMediaTransportControlsInterop)
#undef GetCurrentTime
#undef GetMessage

#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.UI.h>
#include <winrt/Windows.UI.Text.h>
#include <winrt/Windows.System.h>
#include <winrt/Windows.System.Threading.h>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.Storage.Streams.h>
#include <winrt/Windows.Graphics.h>
#include <winrt/Windows.Media.h>
#include <winrt/Windows.UI.Xaml.Interop.h>

#include <winrt/Microsoft.UI.h>
#include <winrt/Microsoft.UI.Dispatching.h>
#include <winrt/Microsoft.UI.Windowing.h>
#include <winrt/Microsoft.UI.Xaml.h>
#include <winrt/Microsoft.UI.Xaml.Controls.h>
#include <winrt/Microsoft.UI.Xaml.Controls.Primitives.h>
#include <winrt/Microsoft.UI.Xaml.Markup.h>
#include <winrt/Microsoft.UI.Xaml.Media.h>
#include <winrt/Microsoft.UI.Xaml.Media.Imaging.h>
#include <winrt/Microsoft.UI.Xaml.Input.h>

#include <microsoft.ui.xaml.window.h>             // IWindowNative (HWND from Window)
#include <systemmediatransportcontrolsinterop.h>  // ISystemMediaTransportControlsInterop

#include <string>
#include <vector>
#include <functional>
#include <map>
#include <chrono>
#include <cmath>
#include <fstream>
#include <iterator>
#include <coroutine>

// ---- The Go engine's C ABI (libdeezercore.dll). cdecl, all POD params. -------
extern "C" {
    int            DZInit(char* arl);                 // 1 ok, 0 fail (blocks)
    char*          DZUserID(void);                    // free with DZFree
    char*          DZFavoritesJSON(void);             // {"tracks":[...]}  (blocks)
    char*          DZPlaylistsJSON(void);             // {"playlists":[...]}
    char*          DZPlaylistTracksJSON(char* id);    // {"tracks":[...]}
    char*          DZAlbumTracksJSON(char* id);       // {"tracks":[...]}
    char*          DZSearchJSON(char* q);             // {tracks,albums,playlists}
    int            DZPlay(char* trackID, long long durationMS); // 1 ok (blocks)
    void           DZPause(void);
    void           DZResume(void);
    void           DZTogglePause(void);
    void           DZStop(void);
    void           DZSeek(long long ms);              // cheap / non-blocking
    int            DZState(void);                     // 0 stop 1 load 2 play 3 pause 4 err
    long long      DZPositionMS(void);
    long long      DZDurationMS(void);
    void           DZSetVolume(double v);             // 0..1
    double         DZVolume(void);
    int            DZFinishedCount(void);             // monotonic; poll to auto-advance
    char*          DZLastErrorJSON(void);
    void           DZFree(char* s);
    unsigned char* DZFetch(char* url, int* outLen);   // raw bytes (cover art); blocks
    void           DZFreeBytes(unsigned char* p);
    void           DZSetQuality(int level);           // 0=MP3_128,1=MP3_320,2=FLAC
    int            DZHighQuality(void);               // 1 if at least MP3_320
    int            DZQuality(void);                   // current level 0..2
    char*          DZFormat(void);                    // human label of current stream
    // ---- v0.3 additions -----------------------------------------------------
    char*          DZAccountJSON(void);               // {userId,name,offer,canHq,canHifi,loggedIn}
    char*          DZChartsJSON(void);                // {tracks,albums,artists,playlists}
    char*          DZArtistTopJSON(char* id);         // {tracks:[...]}
    char*          DZArtistProfileJSON(char* id);     // {artist,top,albums,related}
    char*          DZLyricsJSON(char* id);            // {plain,synced:[{timeMs,text}],isSynced}
    void           DZSetReplayGain(int on);           // 0 off / 1 on
    int            DZReplayGain(void);                // current state 0/1
}

// ---- namespace aliases ------------------------------------------------------
namespace mux   = winrt::Microsoft::UI::Xaml;
namespace muxc  = winrt::Microsoft::UI::Xaml::Controls;
namespace muxp  = winrt::Microsoft::UI::Xaml::Controls::Primitives;
namespace muxm  = winrt::Microsoft::UI::Xaml::Media;
namespace muxi  = winrt::Microsoft::UI::Xaml::Media::Imaging;
namespace muxk  = winrt::Microsoft::UI::Xaml::Markup;
namespace muxin = winrt::Microsoft::UI::Xaml::Input;
namespace mud   = winrt::Microsoft::UI::Dispatching;
namespace muw   = winrt::Microsoft::UI::Windowing;
namespace wdj   = winrt::Windows::Data::Json;
namespace wss   = winrt::Windows::Storage::Streams;
namespace wut   = winrt::Windows::UI::Text;
namespace wsys  = winrt::Windows::System;
namespace wm    = winrt::Windows::Media;
namespace wf    = winrt::Windows::Foundation;
using winrt::box_value;
using winrt::unbox_value_or;
using winrt::hstring;
using winrt::to_hstring;
using winrt::to_string;
using winrt::fire_and_forget;

// winrt::resume_foreground only accepts Windows::System::DispatcherQueue; add an
// awaiter for WinUI 3's Microsoft::UI::Dispatching::DispatcherQueue so coroutines
// can hop back onto the UI thread. Found unqualified at the call sites via ADL.
inline auto resume_foreground(mud::DispatcherQueue const& dq) {
    struct awaiter {
        mud::DispatcherQueue dq;
        bool await_ready() const noexcept { return false; }
        bool await_suspend(std::coroutine_handle<> h) const {
            return dq.TryEnqueue([h] { h.resume(); }); // false => resume inline
        }
        void await_resume() const noexcept {}
    };
    return awaiter{ dq };
}

// ---- wire models (mirror corelib jTrack/jAlbum/jPlaylist) -------------------
struct Track    { hstring id, name, artistId, artistLine, albumName, artworkUrl; int64_t durationMs = 0; };
struct Album    { hstring id, name, artistLine, artworkUrl; };
struct Playlist { hstring id, name, owner, artworkUrl; int trackCount = 0; };
struct Account  { hstring userId, name, offer; bool canHq = false, canHifi = false, loggedIn = false; };

// jArtistInfo: {id,name,artworkUrl,nbFans}  (related artists + artist header)
struct ArtistInfo { hstring id, name, artworkUrl; int64_t nbFans = 0; };
// DZLyricsJSON: {plain, synced:[{timeMs,text}], isSynced}
struct LyricLine  { int64_t timeMs = 0; hstring text; };
struct Lyrics     { hstring plain; std::vector<LyricLine> synced; bool isSynced = false; };
// DZArtistProfileJSON: {artist, top:[T], albums:[A], related:[Ar]}
struct ArtistProfile { ArtistInfo artist; std::vector<Track> top; std::vector<Album> albums; std::vector<ArtistInfo> related; };

// ---- persisted settings -----------------------------------------------------
struct Settings { int quality = 1; bool closeToTray = true; bool replayGain = false; }; // quality: 0 Normal,1 High,2 HiFi

// ---- small helpers ----------------------------------------------------------
static hstring TakeJson(char* p) {              // own a DZ*JSON result, copy, release
    if (!p) return L"";
    hstring h = to_hstring(std::string(p));     // Go strings are UTF-8
    DZFree(p);
    return h;
}

static hstring TimeText(int64_t ms) {
    if (ms < 0) ms = 0;
    int64_t s = ms / 1000;
    wchar_t buf[32];
    swprintf_s(buf, L"%lld:%02lld", s / 60, s % 60);
    return hstring(buf);
}

static muxc::ColumnDefinition ColAuto() { muxc::ColumnDefinition c; c.Width(mux::GridLength{ 0, mux::GridUnitType::Auto }); return c; }
static muxc::ColumnDefinition ColStar() { muxc::ColumnDefinition c; c.Width(mux::GridLength{ 1, mux::GridUnitType::Star }); return c; }

// ScrollViewer::ChangeView wants IReference<double>; box a plain double into one.
static wf::IReference<double> Ref(double v) { return winrt::box_value(v).try_as<wf::IReference<double>>(); }

// "1,234,567 fans" (thousands-grouped); empty when unknown.
static hstring FansText(int64_t n) {
    if (n <= 0) return L"";
    std::wstring s = std::to_wstring(n), out;
    int len = static_cast<int>(s.size());
    for (int i = 0; i < len; ++i) {
        if (i && (len - i) % 3 == 0) out.push_back(L',');
        out.push_back(s[i]);
    }
    return hstring(out) + L" fans";
}

// One jTrack object -> Track. Pulls artists[0].id so a row can open its artist.
static Track TrackFromObj(wdj::JsonObject const& o) {
    Track t;
    t.id         = o.GetNamedString(L"id", L"");
    t.name       = o.GetNamedString(L"name", L"");
    t.durationMs = static_cast<int64_t>(o.GetNamedNumber(L"durationMs", 0));
    t.artistLine = o.GetNamedString(L"artistLine", L"");
    t.albumName  = o.GetNamedString(L"albumName", L"");
    t.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
    auto artists = o.GetNamedArray(L"artists", wdj::JsonArray{});
    if (artists.Size() > 0) t.artistId = artists.GetObjectAt(0).GetNamedString(L"id", L"");
    return t;
}

static std::vector<Track> ParseTracks(hstring const& json) {
    std::vector<Track> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"tracks", wdj::JsonArray{}))
        out.push_back(TrackFromObj(v.GetObject()));
    return out;
}

static std::vector<Playlist> ParsePlaylists(hstring const& json) {
    std::vector<Playlist> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"playlists", wdj::JsonArray{})) {
        auto o = v.GetObject();
        Playlist p;
        p.id         = o.GetNamedString(L"id", L"");
        p.name       = o.GetNamedString(L"name", L"");
        p.owner      = o.GetNamedString(L"owner", L"");
        p.trackCount = static_cast<int>(o.GetNamedNumber(L"trackCount", 0));
        p.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
        out.push_back(std::move(p));
    }
    return out;
}

static Album AlbumFromObj(wdj::JsonObject const& o) {
    Album a;
    a.id         = o.GetNamedString(L"id", L"");
    a.name       = o.GetNamedString(L"name", L"");
    a.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
    auto artists = o.GetNamedArray(L"artists", wdj::JsonArray{});
    if (artists.Size() > 0) a.artistLine = artists.GetObjectAt(0).GetNamedString(L"name", L"");
    return a;
}

static ArtistInfo ArtistFromObj(wdj::JsonObject const& o) {
    ArtistInfo a;
    a.id         = o.GetNamedString(L"id", L"");
    a.name       = o.GetNamedString(L"name", L"");
    a.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
    a.nbFans     = static_cast<int64_t>(o.GetNamedNumber(L"nbFans", 0));
    return a;
}

static std::vector<Album> ParseAlbums(hstring const& json) {
    std::vector<Album> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"albums", wdj::JsonArray{}))
        out.push_back(AlbumFromObj(v.GetObject()));
    return out;
}

static Account ParseAccount(hstring const& json) {     // DZAccountJSON -> single object
    Account a;
    wdj::JsonObject o{ nullptr };
    if (!wdj::JsonObject::TryParse(json, o)) return a;
    a.userId   = o.GetNamedString(L"userId", L"");
    a.name     = o.GetNamedString(L"name", L"");
    a.offer    = o.GetNamedString(L"offer", L"");
    a.canHq    = o.GetNamedBoolean(L"canHq", false);
    a.canHifi  = o.GetNamedBoolean(L"canHifi", false);
    a.loggedIn = o.GetNamedBoolean(L"loggedIn", false);
    return a;
}

static Lyrics ParseLyrics(hstring const& json) {            // DZLyricsJSON -> single object
    Lyrics ly;
    wdj::JsonObject o{ nullptr };
    if (!wdj::JsonObject::TryParse(json, o)) return ly;
    ly.plain    = o.GetNamedString(L"plain", L"");
    ly.isSynced = o.GetNamedBoolean(L"isSynced", false);
    for (auto const& v : o.GetNamedArray(L"synced", wdj::JsonArray{})) {
        auto so = v.GetObject();
        LyricLine l;
        l.timeMs = static_cast<int64_t>(so.GetNamedNumber(L"timeMs", 0));
        l.text   = so.GetNamedString(L"text", L"");
        ly.synced.push_back(std::move(l));
    }
    return ly;
}

static ArtistProfile ParseArtistProfile(hstring const& json) {  // {artist, top, albums, related}
    ArtistProfile p;
    wdj::JsonObject o{ nullptr };
    if (!wdj::JsonObject::TryParse(json, o)) return p;
    if (o.HasKey(L"artist")) p.artist = ArtistFromObj(o.GetNamedObject(L"artist", wdj::JsonObject{}));
    for (auto const& v : o.GetNamedArray(L"top",     wdj::JsonArray{})) p.top.push_back(TrackFromObj(v.GetObject()));
    for (auto const& v : o.GetNamedArray(L"albums",  wdj::JsonArray{})) p.albums.push_back(AlbumFromObj(v.GetObject()));
    for (auto const& v : o.GetNamedArray(L"related", wdj::JsonArray{})) p.related.push_back(ArtistFromObj(v.GetObject()));
    return p;
}

static void Trim(std::wstring& s) {
    auto issp = [](wchar_t c) { return c == L' ' || c == L'\t' || c == L'\r' || c == L'\n'; };
    size_t b = 0, e = s.size();
    while (b < e && issp(s[b])) ++b;
    while (e > b && issp(s[e - 1])) --e;
    s = s.substr(b, e - b);
}

// %APPDATA%\opendeezer  -- the app config dir (holds arl.txt + settings.json).
static std::wstring ConfigDir() {
    wchar_t buf[8192];
    DWORD m = GetEnvironmentVariableW(L"APPDATA", buf, 8192);
    if (m > 0 && m < 8192) { std::wstring p(buf, m); p += L"\\opendeezer"; return p; }
    return L"";
}
static std::wstring SettingsPath() { auto d = ConfigDir(); return d.empty() ? L"" : d + L"\\settings.json"; }

// ARL: %DEEZER_ARL% first, then %APPDATA%\opendeezer\arl.txt.
static std::wstring LoadArl() {
    wchar_t buf[8192];
    DWORD n = GetEnvironmentVariableW(L"DEEZER_ARL", buf, 8192);
    if (n > 0 && n < 8192) { std::wstring v(buf, n); Trim(v); if (!v.empty()) return v; }
    DWORD m = GetEnvironmentVariableW(L"APPDATA", buf, 8192);
    if (m > 0 && m < 8192) {
        std::wstring path(buf, m);
        path += L"\\opendeezer\\arl.txt";
        std::ifstream f(path.c_str(), std::ios::binary);
        if (f) {
            std::string s((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
            std::wstring w(to_hstring(s).c_str());
            Trim(w);
            return w;
        }
    }
    return L"";
}

// =============================================================================
//  MainWindow -- a code-built Window. Implemented as a winrt::implements type so
//  coroutines can hold get_strong() and events can bind get_weak()/member fns.
// =============================================================================
struct MainWindow : winrt::implements<MainWindow, wf::IInspectable> {
    MainWindow() { LoadSettings(); BuildUi(); }

    void Activate() {
        m_win.Activate();
        // The poll timer + login both need the live DispatcherQueue / XamlRoot,
        // so they start after the window is up. The HWND only exists post-Activate,
        // so SMTC + tray are set up here too.
        m_timer = m_win.DispatcherQueue().CreateTimer();
        m_timer.Interval(std::chrono::milliseconds(300));
        m_timer.Tick({ get_weak(), &MainWindow::OnTick });
        m_win.try_as<::IWindowNative>()->get_WindowHandle(&m_appHwnd);
        SetupSMTC();
        SetupTray();
        // Close-to-tray: intercept the window's close button.
        m_win.AppWindow().Closing({ get_weak(), &MainWindow::OnClosing });
        StartLogin();
    }

private:
    // ---- UI construction ----------------------------------------------------
    void BuildUi() {
        m_win = mux::Window();
        m_win.Title(L"OpenDeezer");
        m_win.SystemBackdrop(muxm::MicaBackdrop{});
        try { m_win.AppWindow().Resize({ 1180, 760 }); } catch (...) {}

        winrt::Windows::UI::Color accent{ 0xFF, 0xA2, 0x38, 0xFF }; // Deezer Electric Violet
        m_accent = muxm::SolidColorBrush(accent);

        // root: row0 content, row1 now-playing bar
        muxc::Grid root;
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 1, mux::GridUnitType::Star }); root.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); root.RowDefinitions().Append(r); }

        BuildNav();
        BuildPages();
        muxc::Grid::SetRow(m_nav, 0);
        root.Children().Append(m_nav);

        auto bar = BuildTransport();
        muxc::Grid::SetRow(bar, 1);
        root.Children().Append(bar);

        m_nav.Content(m_tracksPage); // show the (empty) Liked page until login fills it
        m_nav.Header(box_value(L"Liked Songs"));
        m_win.Content(root);
    }

    muxc::NavigationViewItem NavItem(hstring text, muxc::Symbol sym, hstring tag) {
        muxc::NavigationViewItem i;
        i.Content(box_value(text));
        muxc::SymbolIcon ic; ic.Symbol(sym); i.Icon(ic);
        i.Tag(box_value(tag));
        return i;
    }

    void BuildNav() {
        m_nav = muxc::NavigationView();
        m_nav.PaneDisplayMode(muxc::NavigationViewPaneDisplayMode::Left);
        m_nav.IsBackButtonVisible(muxc::NavigationViewBackButtonVisible::Collapsed);
        m_nav.IsSettingsVisible(false);
        m_nav.PaneTitle(L"OpenDeezer");

        m_likedItem     = NavItem(L"Liked Songs", muxc::Symbol::Audio, L"liked");
        m_playlistsItem = NavItem(L"Playlists",   muxc::Symbol::List,  L"playlists");
        m_chartsItem    = NavItem(L"Charts",      muxc::Symbol::World, L"charts");
        m_searchItem    = NavItem(L"Search",      muxc::Symbol::Find,  L"search");
        m_nav.MenuItems().Append(m_likedItem);
        m_nav.MenuItems().Append(m_playlistsItem);
        m_nav.MenuItems().Append(m_chartsItem);
        m_nav.MenuItems().Append(m_searchItem);

        m_settingsItem = NavItem(L"Settings", muxc::Symbol::Setting, L"settings");
        m_aboutItem    = NavItem(L"About",    muxc::Symbol::Help,    L"about");
        m_nav.FooterMenuItems().Append(m_settingsItem);
        m_nav.FooterMenuItems().Append(m_aboutItem);

        m_nav.SelectionChanged({ get_weak(), &MainWindow::OnNav });
    }

    void BuildPages() {
        // Liked / playlist-detail track list (reused for both)
        m_trackList = muxc::ListView();
        m_trackList.SelectionMode(muxc::ListViewSelectionMode::None);
        m_trackList.IsItemClickEnabled(true);
        m_trackList.ItemClick({ get_weak(), &MainWindow::OnTrackClick });
        m_tracksPage = m_trackList;

        // Playlists grid
        m_playlistGrid = muxc::GridView();
        m_playlistGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_playlistGrid.IsItemClickEnabled(true);
        m_playlistGrid.ItemClick({ get_weak(), &MainWindow::OnPlaylistClick });
        m_playlistsPage = m_playlistGrid;

        // Search page: query row + track list + album/playlist grid
        muxc::Grid sp;
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); sp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); sp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 2, mux::GridUnitType::Star }); sp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); sp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 3, mux::GridUnitType::Star }); sp.RowDefinitions().Append(r); }
        sp.Padding({ 4, 4, 4, 4 });
        sp.RowSpacing(8);

        muxc::Grid queryRow; queryRow.ColumnSpacing(8);
        queryRow.ColumnDefinitions().Append(ColStar());
        queryRow.ColumnDefinitions().Append(ColAuto());
        m_searchBox = muxc::TextBox();
        m_searchBox.PlaceholderText(L"Search Deezer…");
        m_searchBox.KeyDown({ get_weak(), &MainWindow::OnSearchKey });
        muxc::Grid::SetColumn(m_searchBox, 0);
        muxc::Button searchBtn; searchBtn.Content(box_value(L"Search"));
        searchBtn.Click({ get_weak(), &MainWindow::OnSearchClick });
        muxc::Grid::SetColumn(searchBtn, 1);
        queryRow.Children().Append(m_searchBox);
        queryRow.Children().Append(searchBtn);
        muxc::Grid::SetRow(queryRow, 0); sp.Children().Append(queryRow);

        muxc::TextBlock h1; h1.Text(L"Tracks"); h1.FontWeight(wut::FontWeights::SemiBold());
        muxc::Grid::SetRow(h1, 1); sp.Children().Append(h1);

        m_searchTrackList = muxc::ListView();
        m_searchTrackList.SelectionMode(muxc::ListViewSelectionMode::None);
        m_searchTrackList.IsItemClickEnabled(true);
        m_searchTrackList.ItemClick({ get_weak(), &MainWindow::OnSearchTrackClick });
        muxc::Grid::SetRow(m_searchTrackList, 2); sp.Children().Append(m_searchTrackList);

        muxc::TextBlock h2; h2.Text(L"Albums & Playlists"); h2.FontWeight(wut::FontWeights::SemiBold());
        muxc::Grid::SetRow(h2, 3); sp.Children().Append(h2);

        m_searchGrid = muxc::GridView();
        m_searchGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_searchGrid.IsItemClickEnabled(true);
        m_searchGrid.ItemClick({ get_weak(), &MainWindow::OnSearchGridClick });
        muxc::Grid::SetRow(m_searchGrid, 4); sp.Children().Append(m_searchGrid);

        m_searchPage = sp;

        BuildArtistPage();
        BuildLyricsPage();
    }

    // Artist detail: a scrolling column of name/fans + Top Tracks + Albums +
    // Related Artists. The inner ListView/GridView have their own scrolling
    // disabled so they size to content and the outer ScrollViewer scrolls.
    void BuildArtistPage() {
        m_artistScroll = muxc::ScrollViewer();
        m_artistScroll.Padding({ 16, 12, 16, 16 });
        m_artistScroll.HorizontalScrollMode(muxc::ScrollMode::Disabled);
        m_artistScroll.HorizontalScrollBarVisibility(muxc::ScrollBarVisibility::Disabled);

        muxc::StackPanel col; col.Spacing(8);

        m_artistHeader = muxc::TextBlock();
        m_artistHeader.FontSize(28); m_artistHeader.FontWeight(wut::FontWeights::SemiBold());
        m_artistHeader.TextWrapping(mux::TextWrapping::Wrap);
        col.Children().Append(m_artistHeader);

        m_artistFans = muxc::TextBlock(); m_artistFans.Opacity(0.6);
        col.Children().Append(m_artistFans);

        auto section = [](hstring text) {
            muxc::TextBlock h; h.Text(text); h.FontWeight(wut::FontWeights::SemiBold());
            h.FontSize(18); h.Margin({ 0, 12, 0, 2 });
            return h;
        };
        auto noInnerScroll = [](mux::DependencyObject const& el) {
            muxc::ScrollViewer::SetVerticalScrollMode(el, muxc::ScrollMode::Disabled);
            muxc::ScrollViewer::SetVerticalScrollBarVisibility(el, muxc::ScrollBarVisibility::Disabled);
        };

        col.Children().Append(section(L"Top Tracks"));
        m_artistTopList = muxc::ListView();
        m_artistTopList.SelectionMode(muxc::ListViewSelectionMode::None);
        m_artistTopList.IsItemClickEnabled(true);
        m_artistTopList.ItemClick({ get_weak(), &MainWindow::OnArtistTopClick });
        noInnerScroll(m_artistTopList);
        col.Children().Append(m_artistTopList);

        col.Children().Append(section(L"Albums"));
        m_artistAlbumsGrid = muxc::GridView();
        m_artistAlbumsGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_artistAlbumsGrid.IsItemClickEnabled(true);
        m_artistAlbumsGrid.ItemClick({ get_weak(), &MainWindow::OnArtistAlbumClick });
        noInnerScroll(m_artistAlbumsGrid);
        col.Children().Append(m_artistAlbumsGrid);

        col.Children().Append(section(L"Related Artists"));
        m_artistRelatedGrid = muxc::GridView();
        m_artistRelatedGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_artistRelatedGrid.IsItemClickEnabled(true);
        m_artistRelatedGrid.ItemClick({ get_weak(), &MainWindow::OnArtistRelatedClick });
        noInnerScroll(m_artistRelatedGrid);
        col.Children().Append(m_artistRelatedGrid);

        m_artistScroll.Content(col);
        m_artistPage = m_artistScroll;
    }

    // Lyrics: a scrolling stack of per-line TextBlocks (synced) or one block
    // (plain). The active line is restyled + scrolled into view from OnTick.
    void BuildLyricsPage() {
        m_lyricsScroll = muxc::ScrollViewer();
        m_lyricsScroll.Padding({ 24, 16, 24, 24 });
        m_lyricsScroll.HorizontalScrollMode(muxc::ScrollMode::Disabled);
        m_lyricsScroll.HorizontalScrollBarVisibility(muxc::ScrollBarVisibility::Disabled);
        m_lyricsPanel = muxc::StackPanel(); m_lyricsPanel.Spacing(6);
        m_lyricsScroll.Content(m_lyricsPanel);
        m_lyricsPage = m_lyricsScroll;
        ShowLyricsMessage(L"Play a track to see its lyrics.");
    }

    muxc::Grid BuildTransport() {
        muxc::Grid bar;
        bar.Padding({ 14, 10, 14, 10 });
        bar.ColumnSpacing(12);
        winrt::Windows::UI::Color barbg{ 0x66, 0x14, 0x04, 0x1E };
        bar.Background(muxm::SolidColorBrush(barbg));
        bar.ColumnDefinitions().Append(ColAuto()); // cover
        bar.ColumnDefinitions().Append(ColAuto()); // now text
        bar.ColumnDefinitions().Append(ColAuto()); // transport buttons
        bar.ColumnDefinitions().Append(ColAuto()); // pos
        bar.ColumnDefinitions().Append(ColStar()); // seek
        bar.ColumnDefinitions().Append(ColAuto()); // dur
        bar.ColumnDefinitions().Append(ColAuto()); // shuffle/repeat
        bar.ColumnDefinitions().Append(ColAuto()); // volume

        m_cover = muxc::Image(); m_cover.Width(48); m_cover.Height(48); m_cover.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(m_cover, 0); bar.Children().Append(m_cover);

        muxc::StackPanel now; now.VerticalAlignment(mux::VerticalAlignment::Center); now.MinWidth(170); now.MaxWidth(240);
        m_nowTitle  = muxc::TextBlock(); m_nowTitle.Text(L"Logging in…"); m_nowTitle.FontWeight(wut::FontWeights::SemiBold());
        m_nowTitle.TextWrapping(mux::TextWrapping::NoWrap); m_nowTitle.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        m_nowArtist = muxc::TextBlock(); m_nowArtist.Opacity(0.6); m_nowArtist.FontSize(12);
        m_nowArtist.TextWrapping(mux::TextWrapping::NoWrap); m_nowArtist.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        now.Children().Append(m_nowTitle); now.Children().Append(m_nowArtist);
        // Quick links to the Lyrics + Artist views for the current track.
        muxc::StackPanel meta; meta.Orientation(muxc::Orientation::Horizontal); meta.Spacing(10);
        m_lyricsBtn = muxc::HyperlinkButton(); m_lyricsBtn.Content(box_value(L"Lyrics"));
        m_lyricsBtn.Padding({ 0, 0, 0, 0 }); m_lyricsBtn.Click({ get_weak(), &MainWindow::OnLyrics });
        m_artistBtn = muxc::HyperlinkButton(); m_artistBtn.Content(box_value(L"Artist"));
        m_artistBtn.Padding({ 0, 0, 0, 0 }); m_artistBtn.Click({ get_weak(), &MainWindow::OnArtist });
        meta.Children().Append(m_lyricsBtn); meta.Children().Append(m_artistBtn);
        now.Children().Append(meta);
        muxc::Grid::SetColumn(now, 1); bar.Children().Append(now);

        muxc::StackPanel tr; tr.Orientation(muxc::Orientation::Horizontal); tr.Spacing(4); tr.VerticalAlignment(mux::VerticalAlignment::Center);
        auto glyphBtn = [](hstring g) { muxc::Button b; muxc::FontIcon fi; fi.Glyph(g); b.Content(fi); return b; };
        auto prevBtn = glyphBtn(L""); prevBtn.Click({ get_weak(), &MainWindow::OnPrev });
        m_playBtn = muxc::Button(); m_playIcon = muxc::FontIcon(); m_playIcon.Glyph(L""); m_playBtn.Content(m_playIcon);
        m_playBtn.Foreground(m_accent); m_playBtn.Click({ get_weak(), &MainWindow::OnPlayPause });
        auto nextBtn = glyphBtn(L""); nextBtn.Click({ get_weak(), &MainWindow::OnNext });
        tr.Children().Append(prevBtn); tr.Children().Append(m_playBtn); tr.Children().Append(nextBtn);
        muxc::Grid::SetColumn(tr, 2); bar.Children().Append(tr);

        m_posText = muxc::TextBlock(); m_posText.Text(L"0:00"); m_posText.Opacity(0.7); m_posText.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(m_posText, 3); bar.Children().Append(m_posText);

        m_seek = muxc::Slider(); m_seek.Minimum(0); m_seek.Maximum(1000); m_seek.Value(0);
        m_seek.VerticalAlignment(mux::VerticalAlignment::Center); m_seek.Foreground(m_accent);
        m_seek.ValueChanged({ get_weak(), &MainWindow::OnSeekChanged });
        muxc::Grid::SetColumn(m_seek, 4); bar.Children().Append(m_seek);

        m_durText = muxc::TextBlock(); m_durText.Text(L"0:00"); m_durText.Opacity(0.7); m_durText.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(m_durText, 5); bar.Children().Append(m_durText);

        muxc::StackPanel modes; modes.Orientation(muxc::Orientation::Horizontal); modes.Spacing(4); modes.VerticalAlignment(mux::VerticalAlignment::Center);
        m_shuffleBtn = muxp::ToggleButton(); { muxc::FontIcon fi; fi.Glyph(L""); m_shuffleBtn.Content(fi); }
        m_shuffleBtn.Click({ get_weak(), &MainWindow::OnShuffle });
        m_repeatBtn = muxc::Button(); m_repeatBtn.Content(box_value(L"Repeat: Off"));
        m_repeatBtn.Click({ get_weak(), &MainWindow::OnRepeat });
        modes.Children().Append(m_shuffleBtn); modes.Children().Append(m_repeatBtn);
        muxc::Grid::SetColumn(modes, 6); bar.Children().Append(modes);

        muxc::StackPanel vol; vol.Orientation(muxc::Orientation::Horizontal); vol.Spacing(6); vol.VerticalAlignment(mux::VerticalAlignment::Center);
        { muxc::FontIcon sp; sp.Glyph(L""); sp.VerticalAlignment(mux::VerticalAlignment::Center); vol.Children().Append(sp); }
        m_volume = muxc::Slider(); m_volume.Minimum(0); m_volume.Maximum(100); m_volume.Value(100); m_volume.Width(120);
        m_volume.VerticalAlignment(mux::VerticalAlignment::Center); m_volume.Foreground(m_accent);
        m_volume.ValueChanged({ get_weak(), &MainWindow::OnVolumeChanged });
        vol.Children().Append(m_volume);
        muxc::Grid::SetColumn(vol, 7); bar.Children().Append(vol);

        return bar;
    }

    // ---- item factories -----------------------------------------------------
    mux::UIElement MakeTrackRow(Track const& t, int index) {
        muxc::Grid g; g.Tag(box_value(index)); g.Height(56); g.Padding({ 6, 4, 6, 4 }); g.ColumnSpacing(12);
        g.ColumnDefinitions().Append(ColAuto());
        g.ColumnDefinitions().Append(ColStar());
        g.ColumnDefinitions().Append(ColAuto());
        muxc::Image img; img.Width(44); img.Height(44); img.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(img, 0); g.Children().Append(img);
        muxc::StackPanel sp; sp.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::TextBlock title; title.Text(t.name); title.FontWeight(wut::FontWeights::SemiBold());
        title.TextWrapping(mux::TextWrapping::NoWrap); title.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        muxc::TextBlock artist; artist.Text(t.artistLine); artist.Opacity(0.6); artist.FontSize(12);
        artist.TextWrapping(mux::TextWrapping::NoWrap); artist.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        sp.Children().Append(title); sp.Children().Append(artist);
        muxc::Grid::SetColumn(sp, 1); g.Children().Append(sp);
        muxc::TextBlock dur; dur.Text(TimeText(t.durationMs)); dur.Opacity(0.6); dur.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(dur, 2); g.Children().Append(dur);
        if (!t.artworkUrl.empty()) LoadArt(img, t.artworkUrl, m_artGen, false);
        return g;
    }

    mux::UIElement MakeTile(hstring title, hstring subtitle, hstring art, int index) {
        muxc::StackPanel sp; sp.Width(164); sp.Margin({ 6, 6, 6, 6 }); sp.Tag(box_value(index));
        muxc::Image img; img.Width(152); img.Height(152);
        muxc::TextBlock t1; t1.Text(title); t1.FontWeight(wut::FontWeights::SemiBold()); t1.Margin({ 0, 6, 0, 0 });
        t1.TextWrapping(mux::TextWrapping::NoWrap); t1.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        muxc::TextBlock t2; t2.Text(subtitle); t2.Opacity(0.6); t2.FontSize(12);
        t2.TextWrapping(mux::TextWrapping::NoWrap); t2.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        sp.Children().Append(img); sp.Children().Append(t1); sp.Children().Append(t2);
        if (!art.empty()) LoadArt(img, art, m_artGen, false);
        return sp;
    }

    void FillTrackList(muxc::ListView const& lv, std::vector<Track> const& tracks) {
        lv.Items().Clear();
        int i = 0;
        for (auto const& t : tracks) { lv.Items().Append(MakeTrackRow(t, i)); ++i; }
    }

    void FillPlaylistGrid() {
        m_playlistGrid.Items().Clear();
        int i = 0;
        for (auto const& p : m_playlists) {
            hstring sub = to_hstring(p.trackCount) + L" tracks";
            m_playlistGrid.Items().Append(MakeTile(p.name, sub, p.artworkUrl, i));
            ++i;
        }
    }

    void FillSearchGrid() {
        m_searchGrid.Items().Clear();
        m_searchActions.clear();
        for (auto const& a : m_searchAlbums) {
            int idx = static_cast<int>(m_searchActions.size());
            Album ac = a;
            m_searchActions.push_back([this, ac] { OpenAlbum(ac); });
            m_searchGrid.Items().Append(MakeTile(a.name, a.artistLine, a.artworkUrl, idx));
        }
        for (auto const& p : m_searchPlaylists) {
            int idx = static_cast<int>(m_searchActions.size());
            Playlist pc = p;
            m_searchActions.push_back([this, pc] { OpenPlaylist(pc); });
            m_searchGrid.Items().Append(MakeTile(p.name, p.owner, p.artworkUrl, idx));
        }
    }

    // ---- cover art: fetch bytes off-thread, decode on UI thread -------------
    static std::vector<uint8_t> FetchBytes(hstring const& url) {
        std::string s = to_string(url);
        int len = 0;
        unsigned char* p = DZFetch(s.data(), &len);
        std::vector<uint8_t> out;
        if (p) { if (len > 0) out.assign(p, p + len); DZFreeBytes(p); }
        return out;
    }

    fire_and_forget LoadArt(muxc::Image img, hstring url, int token, bool isCover) {
        auto strong = get_strong();
        co_await winrt::resume_background();
        auto bytes = FetchBytes(url);
        if (bytes.empty()) co_return;
        co_await resume_foreground(m_win.DispatcherQueue());
        if (isCover) { if (token != m_playGen) co_return; }
        else         { if (token != m_artGen)  co_return; } // list reloaded -> drop stale
        try {
            wss::InMemoryRandomAccessStream stream;
            wss::DataWriter writer{ stream };
            writer.WriteBytes(bytes);
            co_await writer.StoreAsync();
            writer.DetachStream();
            stream.Seek(0);
            muxi::BitmapImage bmp;
            // Assign the source while still on the UI thread; SetSourceAsync's
            // continuation may resume off-thread, so do no UI work after it
            // (avoids an intermittent RPC_E_WRONG_THREAD that silently drops art).
            img.Source(bmp);
            co_await bmp.SetSourceAsync(stream);
        } catch (...) {}
    }

    // ---- login --------------------------------------------------------------
    fire_and_forget StartLogin() {
        auto strong = get_strong();
        std::wstring arlW = LoadArl();
        if (arlW.empty()) {
            ShowMessage(L"No ARL found",
                L"Set %DEEZER_ARL% or write your ARL to %APPDATA%\\opendeezer\\arl.txt, then relaunch.");
            m_nowTitle.Text(L"No ARL");
            co_return;
        }
        hstring arl{ arlW };
        co_await winrt::resume_background();
        std::string s = to_string(arl);
        int ok = DZInit(s.data());
        co_await resume_foreground(m_win.DispatcherQueue());
        if (ok) {
            m_loggedIn = true;
            DZSetQuality(m_settings.quality); // apply persisted quality on startup
            DZSetReplayGain(m_settings.replayGain ? 1 : 0); // apply persisted normalization
            LoadAccount(); // fetch tier (name / offer / hq-hifi caps) for About + Settings
            m_lastFinished = DZFinishedCount();
            m_updatingVol = true; m_volume.Value(DZVolume() * 100.0); m_updatingVol = false;
            m_timer.Start();
            m_nowTitle.Text(L"Not playing");
            m_nowArtist.Text(L"");
            m_suppressNav = false;
            m_nav.SelectedItem(m_likedItem); // -> OnNav -> LoadFavorites
        } else {
            m_nowTitle.Text(L"Login failed");
            ShowMessage(L"Login failed", L"Invalid or expired ARL.");
        }
    }

    // ---- navigation ---------------------------------------------------------
    void OnNav(muxc::NavigationView const& nav, muxc::NavigationViewSelectionChangedEventArgs const& args) {
        if (m_suppressNav) return;
        auto item = args.SelectedItem().try_as<muxc::NavigationViewItem>();
        if (!item) return;
        auto tag = unbox_value_or<hstring>(item.Tag(), L"");
        // About / Settings are modal dialogs, not pages: open then revert selection.
        if (tag == L"about" || tag == L"settings") {
            if (tag == L"about") ShowAbout(); else ShowSettings();
            m_suppressNav = true;
            nav.SelectedItem(m_lastContentItem ? m_lastContentItem : m_likedItem);
            m_suppressNav = false;
            return;
        }
        m_lastContentItem = item;
        m_lyricsShown = false; // leaving the lyrics/artist page for a menu page
        if (tag == L"liked") {
            nav.Header(box_value(L"Liked Songs")); nav.Content(m_tracksPage); LoadFavorites();
        } else if (tag == L"charts") {
            nav.Header(box_value(L"Charts")); nav.Content(m_tracksPage); LoadCharts();
        } else if (tag == L"playlists") {
            nav.Header(box_value(L"Playlists")); nav.Content(m_playlistsPage); LoadPlaylists();
        } else if (tag == L"search") {
            nav.Header(box_value(L"Search")); nav.Content(m_searchPage);
            m_searchBox.Focus(mux::FocusState::Programmatic);
        }
    }

    // ---- browse (heavy work off the UI thread) ------------------------------
    fire_and_forget LoadFavorites() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto tracks = ParseTracks(TakeJson(DZFavoritesJSON()));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    // Charts reuse the shared track list/page, exactly like Liked / playlist detail.
    fire_and_forget LoadCharts() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto tracks = ParseTracks(TakeJson(DZChartsJSON())); // {"tracks":[...], ...}
        co_await resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    // Cache the signed-in account tier for the About / Settings surfaces.
    fire_and_forget LoadAccount() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto acct = ParseAccount(TakeJson(DZAccountJSON()));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_account = std::move(acct);
    }

    fire_and_forget LoadPlaylists() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto ps = ParsePlaylists(TakeJson(DZPlaylistsJSON()));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_playlists = std::move(ps);
        ++m_artGen;
        FillPlaylistGrid();
    }

    fire_and_forget OpenPlaylist(Playlist p) {
        auto strong = get_strong();
        m_lyricsShown = false;
        m_nav.Header(box_value(p.name));
        m_nav.Content(m_tracksPage);
        co_await winrt::resume_background();
        std::string s = to_string(p.id);
        auto tracks = ParseTracks(TakeJson(DZPlaylistTracksJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    fire_and_forget OpenAlbum(Album a) {
        auto strong = get_strong();
        m_lyricsShown = false;
        m_nav.Header(box_value(a.name));
        m_nav.Content(m_tracksPage);
        co_await winrt::resume_background();
        std::string s = to_string(a.id);
        auto tracks = ParseTracks(TakeJson(DZAlbumTracksJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    // ---- artist view --------------------------------------------------------
    // artistID from a jTrack.artists[0].id (transport "Artist" button uses the
    // now-playing track; Related tiles pass another artist's id).
    fire_and_forget OpenArtist(hstring artistId) {
        auto strong = get_strong();
        if (!m_loggedIn || artistId.empty()) co_return;
        m_lyricsShown = false;
        m_nav.Header(box_value(L"Artist"));
        m_nav.Content(m_artistPage);
        m_artistHeader.Text(L"Loading…"); m_artistFans.Text(L"");
        m_artistTopList.Items().Clear();
        m_artistAlbumsGrid.Items().Clear();
        m_artistRelatedGrid.Items().Clear();
        co_await winrt::resume_background();
        std::string s = to_string(artistId);
        auto prof = ParseArtistProfile(TakeJson(DZArtistProfileJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_artistTop     = std::move(prof.top);
        m_artistAlbums  = std::move(prof.albums);
        m_artistRelated = std::move(prof.related);
        m_artistHeader.Text(prof.artist.name.empty() ? hstring(L"Artist") : prof.artist.name);
        m_artistFans.Text(FansText(prof.artist.nbFans));
        ++m_artGen;
        FillTrackList(m_artistTopList, m_artistTop); // reuses MakeTrackRow rows
        FillArtistAlbums();
        FillArtistRelated();
        try { m_artistScroll.ChangeView(nullptr, Ref(0.0), nullptr); } catch (...) {} // back to top
    }

    void FillArtistAlbums() {
        m_artistAlbumsGrid.Items().Clear();
        int i = 0;
        for (auto const& a : m_artistAlbums) { m_artistAlbumsGrid.Items().Append(MakeTile(a.name, a.artistLine, a.artworkUrl, i)); ++i; }
    }
    void FillArtistRelated() {
        m_artistRelatedGrid.Items().Clear();
        int i = 0;
        for (auto const& r : m_artistRelated) { m_artistRelatedGrid.Items().Append(MakeTile(r.name, FansText(r.nbFans), r.artworkUrl, i)); ++i; }
    }
    void OnArtistTopClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_artistTop, i);
    }
    void OnArtistAlbumClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_artistAlbums.size())) OpenAlbum(m_artistAlbums[i]);
    }
    void OnArtistRelatedClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_artistRelated.size())) OpenArtist(m_artistRelated[i].id);
    }
    void OnArtist(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        hstring aid = CurrentArtistId();
        if (aid.empty()) { ShowMessage(L"No artist", L"Start playing a track to view its artist."); return; }
        OpenArtist(aid);
    }

    // ---- lyrics view --------------------------------------------------------
    void OnLyrics(wf::IInspectable const&, mux::RoutedEventArgs const&) { ShowLyrics(); }

    void ShowLyrics() {
        if (!m_loggedIn) return;
        m_lyricsShown = true;
        m_nav.Header(box_value(L"Lyrics"));
        m_nav.Content(m_lyricsPage);
        hstring id = CurrentTrackId();
        if (id.empty()) { ShowLyricsMessage(L"Play a track to see its lyrics."); return; }
        LoadLyrics(id);
    }

    // Fetch off-thread, cache per track id; a generation token drops results
    // that a track change superseded.
    fire_and_forget LoadLyrics(hstring trackId) {
        auto strong = get_strong();
        if (trackId.empty()) co_return;
        std::wstring key{ trackId.c_str() };
        auto it = m_lyricsCache.find(key);
        if (it != m_lyricsCache.end()) {
            m_lyricsTrackId = trackId;
            m_lyrics = it->second;
            RenderLyrics();
            co_return;
        }
        int gen = ++m_lyricsGen;
        m_lyricsTrackId = trackId;             // optimistic: stops the tick re-triggering
        ShowLyricsMessage(L"Loading lyrics…");
        co_await winrt::resume_background();
        std::string s = to_string(trackId);
        auto ly = ParseLyrics(TakeJson(DZLyricsJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_lyricsCache[key] = ly;               // cache regardless of staleness
        if (gen != m_lyricsGen) co_return;     // a newer request superseded this one
        m_lyrics = std::move(ly);
        if (m_lyricsShown) RenderLyrics();
    }

    void RenderLyrics() {
        m_lyricsPanel.Children().Clear();
        m_lyricLineBlocks.clear();
        m_lyricActive = -1;
        if (m_lyrics.isSynced && !m_lyrics.synced.empty()) {
            for (auto const& l : m_lyrics.synced) {
                muxc::TextBlock tb;
                tb.Text(l.text.empty() ? hstring(L"♪") : l.text); // musical note for blank lines
                tb.TextWrapping(mux::TextWrapping::Wrap);
                tb.FontSize(18);
                tb.Opacity(0.45);
                m_lyricsPanel.Children().Append(tb);
                m_lyricLineBlocks.push_back(tb);
            }
            UpdateLyricsHighlight(DZPositionMS()); // style the current line immediately
        } else if (!m_lyrics.plain.empty()) {
            muxc::TextBlock tb;
            tb.Text(m_lyrics.plain);
            tb.TextWrapping(mux::TextWrapping::Wrap);
            tb.FontSize(16);
            m_lyricsPanel.Children().Append(tb);
        } else {
            ShowLyricsMessage(L"No lyrics available.");
        }
    }

    void ShowLyricsMessage(hstring msg) {
        m_lyricsPanel.Children().Clear();
        m_lyricLineBlocks.clear();
        m_lyricActive = -1;
        muxc::TextBlock tb; tb.Text(msg); tb.Opacity(0.7); tb.TextWrapping(mux::TextWrapping::Wrap);
        m_lyricsPanel.Children().Append(tb);
    }

    // Active line = last synced line whose timeMs <= pos. Restyle on change only.
    void UpdateLyricsHighlight(int64_t pos) {
        if (m_lyricLineBlocks.empty()) return;
        int active = -1;
        for (int i = 0; i < static_cast<int>(m_lyrics.synced.size()); ++i) {
            if (m_lyrics.synced[i].timeMs <= pos) active = i; else break;
        }
        if (active == m_lyricActive) return;
        if (m_lyricActive >= 0 && m_lyricActive < static_cast<int>(m_lyricLineBlocks.size())) {
            auto prev = m_lyricLineBlocks[m_lyricActive];
            prev.Opacity(0.45);
            prev.FontWeight(wut::FontWeights::Normal());
            prev.ClearValue(muxc::TextBlock::ForegroundProperty()); // back to theme default
        }
        m_lyricActive = active;
        if (active >= 0 && active < static_cast<int>(m_lyricLineBlocks.size())) {
            auto cur = m_lyricLineBlocks[active];
            cur.Opacity(1.0);
            cur.FontWeight(wut::FontWeights::SemiBold());
            cur.Foreground(m_accent);
            ScrollLyricToActive();
        }
    }

    void ScrollLyricToActive() {
        if (m_lyricActive < 0 || m_lyricActive >= static_cast<int>(m_lyricLineBlocks.size())) return;
        auto block = m_lyricLineBlocks[m_lyricActive];
        try {
            auto gt = block.TransformToVisual(m_lyricsPanel); // panel == scroll content
            auto pt = gt.TransformPoint(wf::Point{ 0.0f, 0.0f });
            double target = static_cast<double>(pt.Y)
                          - m_lyricsScroll.ViewportHeight() / 2.0
                          + block.ActualHeight() / 2.0;          // center the active line
            if (target < 0.0) target = 0.0;
            m_lyricsScroll.ChangeView(nullptr, Ref(target), nullptr);
        } catch (...) {}
    }

    // The now-playing track (head of the active queue), used by both views.
    hstring CurrentTrackId() {
        if (m_queueIndex >= 0 && m_queueIndex < static_cast<int>(m_queue.size())) return m_queue[m_queueIndex].id;
        return L"";
    }
    hstring CurrentArtistId() {
        if (m_queueIndex >= 0 && m_queueIndex < static_cast<int>(m_queue.size())) return m_queue[m_queueIndex].artistId;
        return L"";
    }

    fire_and_forget RunSearch() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        auto q = m_searchBox.Text();
        if (q.empty()) co_return;
        co_await winrt::resume_background();
        std::string s = to_string(q);
        hstring json = TakeJson(DZSearchJSON(s.data()));
        auto tracks = ParseTracks(json);
        auto albums = ParseAlbums(json);
        auto plists = ParsePlaylists(json);
        co_await resume_foreground(m_win.DispatcherQueue());
        m_searchTracks = std::move(tracks);
        m_searchAlbums = std::move(albums);
        m_searchPlaylists = std::move(plists);
        ++m_artGen;
        FillTrackList(m_searchTrackList, m_searchTracks);
        FillSearchGrid();
    }

    // ---- item activation ----------------------------------------------------
    static int TagIndex(wf::IInspectable const& clicked) {
        auto fe = clicked.try_as<mux::FrameworkElement>();
        return fe ? unbox_value_or<int>(fe.Tag(), -1) : -1;
    }
    void OnTrackClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_tracks, i);
    }
    void OnSearchTrackClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_searchTracks, i);
    }
    void OnPlaylistClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_playlists.size())) OpenPlaylist(m_playlists[i]);
    }
    void OnSearchGridClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_searchActions.size())) m_searchActions[i]();
    }
    void OnSearchClick(wf::IInspectable const&, mux::RoutedEventArgs const&) { RunSearch(); }
    void OnSearchKey(wf::IInspectable const&, muxin::KeyRoutedEventArgs const& e) {
        if (e.Key() == wsys::VirtualKey::Enter) RunSearch();
    }

    // ---- playback -----------------------------------------------------------
    void PlayFrom(std::vector<Track> const& list, int index) {
        m_queue = list; m_queueIndex = index; PlayCurrent();
    }
    void PlayCurrent() {
        if (m_queueIndex < 0 || m_queueIndex >= static_cast<int>(m_queue.size())) return;
        Track t = m_queue[m_queueIndex];
        SetNowPlaying(t);
        m_updatingSeek = true;
        m_seek.Maximum(static_cast<double>(t.durationMs > 0 ? t.durationMs : 1));
        m_seek.Value(0);
        m_updatingSeek = false;
        m_posText.Text(TimeText(0));
        m_durText.Text(TimeText(t.durationMs));
        DispatchPlay(t.id, t.durationMs);
    }
    fire_and_forget DispatchPlay(hstring id, int64_t dur) {
        auto strong = get_strong();
        co_await winrt::resume_background();
        std::string s = to_string(id);
        DZPlay(s.data(), dur); // prepares the stream over the network -> blocks
    }
    void SetNowPlaying(Track const& t) {
        m_nowTitle.Text(t.name);
        m_curArtist = t.artistLine;
        m_nowArtist.Text(t.artistLine);
        m_cover.Source(nullptr);
        int token = ++m_playGen;
        if (!t.artworkUrl.empty()) LoadArt(m_cover, t.artworkUrl, token, true);
        // Push the new track to the OS media overlay / lock screen.
        UpdateSmtcMetadata(t);
    }
    void Next() {
        if (m_queue.empty()) return;
        if (m_shuffle && m_queue.size() > 1) {
            int n = m_queueIndex;
            while (n == m_queueIndex) n = static_cast<int>(rand() % m_queue.size());
            m_queueIndex = n;
        } else if (m_queueIndex + 1 < static_cast<int>(m_queue.size())) {
            ++m_queueIndex;
        } else if (m_repeat == 1) {
            m_queueIndex = 0;
        } else { return; }
        PlayCurrent();
    }
    void Prev() {
        if (m_queue.empty()) return;
        if (m_queueIndex > 0) --m_queueIndex;
        PlayCurrent();
    }
    void OnPlayPause(wf::IInspectable const&, mux::RoutedEventArgs const&) { DZTogglePause(); }
    void OnPrev(wf::IInspectable const&, mux::RoutedEventArgs const&) { Prev(); }
    void OnNext(wf::IInspectable const&, mux::RoutedEventArgs const&) { Next(); }
    void OnShuffle(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        auto r = m_shuffleBtn.IsChecked(); m_shuffle = r && r.Value();
    }
    void OnRepeat(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        m_repeat = (m_repeat + 1) % 3;
        m_repeatBtn.Content(box_value(m_repeat == 0 ? L"Repeat: Off" : m_repeat == 1 ? L"Repeat: All" : L"Repeat: One"));
    }
    void OnSeekChanged(wf::IInspectable const&, muxp::RangeBaseValueChangedEventArgs const& e) {
        if (m_updatingSeek) return; // programmatic update from the poll tick
        int64_t ms = static_cast<int64_t>(llround(e.NewValue()));
        DZSeek(ms);
        m_posText.Text(TimeText(ms));
        m_lastSeek = std::chrono::steady_clock::now();
    }
    void OnVolumeChanged(wf::IInspectable const&, muxp::RangeBaseValueChangedEventArgs const& e) {
        if (m_updatingVol) return;
        DZSetVolume(e.NewValue() / 100.0);
    }

    // ---- 300 ms poll: cheap state reads + auto-advance + SMTC push ----------
    void OnTick(mud::DispatcherQueueTimer const&, wf::IInspectable const&) {
        if (!m_loggedIn) return;
        int st = DZState();
        int64_t pos = DZPositionMS(), dur = DZDurationMS();
        if (dur > 0) {
            if (m_seek.Maximum() != static_cast<double>(dur)) {
                m_updatingSeek = true; m_seek.Maximum(static_cast<double>(dur)); m_updatingSeek = false;
            }
            m_durText.Text(TimeText(dur));
        }
        auto now = std::chrono::steady_clock::now();
        if (now - m_lastSeek > std::chrono::milliseconds(400)) { // don't fight a live drag
            m_updatingSeek = true;
            double v = static_cast<double>(pos);
            if (dur > 0 && v > static_cast<double>(dur)) v = static_cast<double>(dur);
            m_seek.Value(v);
            m_updatingSeek = false;
        }
        m_posText.Text(TimeText(pos));
        m_playIcon.Glyph(st == 2 ? L"" : L""); // pause glyph while playing

        // Show the actual output format next to the artist.
        if (!m_curArtist.empty()) {
            if (char* fp = DZFormat()) {
                hstring f = to_hstring(std::string(fp));
                DZFree(fp);
                m_nowArtist.Text(f.empty() ? m_curArtist
                                           : hstring(m_curArtist + L"   ·   " + f));
            }
        }

        // Lyrics page (when open): drive the synced highlight off the same tick
        // that moves the progress bar, and refetch when the track changes.
        if (m_lyricsShown) {
            if (m_lyrics.isSynced && !m_lyricLineBlocks.empty()) UpdateLyricsHighlight(pos);
            hstring cur = CurrentTrackId();
            if (!cur.empty() && cur != m_lyricsTrackId) LoadLyrics(cur);
        }

        // Mirror state to the OS overlay: status on change, timeline ~every 5 s.
        if (m_smtc) {
            wm::MediaPlaybackStatus ps =
                st == 2 ? wm::MediaPlaybackStatus::Playing :
                st == 3 ? wm::MediaPlaybackStatus::Paused  :
                st == 1 ? wm::MediaPlaybackStatus::Changing :
                          wm::MediaPlaybackStatus::Stopped;
            if (ps != m_lastSmtcStatus) { try { m_smtc.PlaybackStatus(ps); } catch (...) {} m_lastSmtcStatus = ps; }
            if (++m_smtcTimelineTick >= 16) { m_smtcTimelineTick = 0; UpdateSmtcTimeline(pos, dur); }
        }

        int f = DZFinishedCount();
        if (f != m_lastFinished) { m_lastFinished = f; if (m_repeat == 2) PlayCurrent(); else Next(); }
    }

    // ---- SystemMediaTransportControls (OS media overlay / media keys) -------
    void SetupSMTC() {
        try {
            auto interop = winrt::get_activation_factory<
                wm::SystemMediaTransportControls, ::ISystemMediaTransportControlsInterop>();
            winrt::check_hresult(interop->GetForWindow(
                m_appHwnd,
                winrt::guid_of<wm::SystemMediaTransportControls>(),
                winrt::put_abi(m_smtc)));
        } catch (...) { m_smtc = nullptr; return; }

        m_smtc.IsEnabled(true);
        m_smtc.IsPlayEnabled(true);  m_smtc.IsPauseEnabled(true);
        m_smtc.IsNextEnabled(true);  m_smtc.IsPreviousEnabled(true);
        m_smtc.DisplayUpdater().Type(wm::MediaPlaybackType::Music);

        // Handlers run on a threadpool thread -> marshal to the UI thread, then
        // route into the EXISTING transport logic (no duplicated playback code).
        m_smtcButtonToken = m_smtc.ButtonPressed({ get_weak(), &MainWindow::OnSmtcButton });
        m_smtcPosToken    = m_smtc.PlaybackPositionChangeRequested({ get_weak(), &MainWindow::OnSmtcSeek });
    }

    void OnSmtcButton(wm::SystemMediaTransportControls const&,
                      wm::SystemMediaTransportControlsButtonPressedEventArgs const& a) {
        auto btn = a.Button();
        m_win.DispatcherQueue().TryEnqueue([weak = get_weak(), btn] {
            auto self = weak.get(); if (!self) return;
            switch (btn) {
                case wm::SystemMediaTransportControlsButton::Play:     DZResume(); break;
                case wm::SystemMediaTransportControlsButton::Pause:    DZPause();  break;
                case wm::SystemMediaTransportControlsButton::Next:     self->Next(); break;
                case wm::SystemMediaTransportControlsButton::Previous: self->Prev(); break;
                default: break;
            }
        });
    }

    void OnSmtcSeek(wm::SystemMediaTransportControls const&,
                    wm::PlaybackPositionChangeRequestedEventArgs const& a) {
        int64_t ms = std::chrono::duration_cast<std::chrono::milliseconds>(
            a.RequestedPlaybackPosition()).count();
        m_win.DispatcherQueue().TryEnqueue([weak = get_weak(), ms] {
            auto self = weak.get(); if (!self) return;
            DZSeek(ms);
            self->m_lastSeek = std::chrono::steady_clock::now();
            self->m_updatingSeek = true; self->m_seek.Value(static_cast<double>(ms)); self->m_updatingSeek = false;
            self->m_posText.Text(TimeText(ms));
            self->UpdateSmtcTimeline(ms, static_cast<int64_t>(self->m_seek.Maximum()));
        });
    }

    void UpdateSmtcMetadata(Track const& t) {
        if (!m_smtc) return;
        try {
            auto du = m_smtc.DisplayUpdater();
            du.Type(wm::MediaPlaybackType::Music);
            auto mp = du.MusicProperties();
            mp.Title(t.name);
            mp.Artist(t.artistLine);
            mp.AlbumTitle(t.albumName);
            if (!t.artworkUrl.empty()) {
                du.Thumbnail(wss::RandomAccessStreamReference::CreateFromUri(wf::Uri(t.artworkUrl)));
            }
            du.Update();
            m_smtc.PlaybackStatus(wm::MediaPlaybackStatus::Playing);
            m_lastSmtcStatus = wm::MediaPlaybackStatus::Playing;
            UpdateSmtcTimeline(0, t.durationMs);
            m_smtcTimelineTick = 0;
        } catch (...) {}
    }

    void UpdateSmtcTimeline(int64_t posMs, int64_t durMs) {
        if (!m_smtc) return;
        try {
            wm::SystemMediaTransportControlsTimelineProperties tl;
            std::chrono::milliseconds end(durMs > 0 ? durMs : 0);
            tl.StartTime(std::chrono::milliseconds(0));
            tl.EndTime(end);
            tl.Position(std::chrono::milliseconds(posMs < 0 ? 0 : posMs));
            tl.MinSeekTime(std::chrono::milliseconds(0));
            tl.MaxSeekTime(end);
            m_smtc.UpdateTimelineProperties(tl);
        } catch (...) {}
    }

    // ---- tray icon + close-to-tray (background playback) --------------------
    void SetupTray() {
        WNDCLASSEXW wc{};
        wc.cbSize        = sizeof(wc);
        wc.lpfnWndProc   = &MainWindow::TrayProc;
        wc.hInstance     = GetModuleHandleW(nullptr);
        wc.lpszClassName = L"OpenDeezerTrayWnd";
        RegisterClassExW(&wc); // harmless if already registered

        // Message-only window receives the tray callbacks + menu commands.
        m_msgHwnd = CreateWindowExW(0, wc.lpszClassName, L"OpenDeezerTray", 0,
            0, 0, 0, 0, HWND_MESSAGE, nullptr, wc.hInstance, nullptr);
        if (m_msgHwnd) SetWindowLongPtrW(m_msgHwnd, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(this));

        HICON hIcon = static_cast<HICON>(LoadImageW(GetModuleHandleW(nullptr),
            MAKEINTRESOURCEW(1), IMAGE_ICON, 0, 0, LR_DEFAULTSIZE | LR_SHARED));
        if (!hIcon) hIcon = LoadIconW(nullptr, IDI_APPLICATION);

        m_nid = {};
        m_nid.cbSize           = sizeof(m_nid);
        m_nid.hWnd             = m_msgHwnd;
        m_nid.uID              = kTrayUID;
        m_nid.uFlags           = NIF_ICON | NIF_MESSAGE | NIF_TIP;
        m_nid.uCallbackMessage = kTrayCallback;
        m_nid.hIcon            = hIcon;
        wcscpy_s(m_nid.szTip, L"OpenDeezer");
        Shell_NotifyIconW(NIM_ADD, &m_nid);
        m_trayAdded = true;
    }

    void RemoveTray() {
        if (m_trayAdded) { Shell_NotifyIconW(NIM_DELETE, &m_nid); m_trayAdded = false; }
    }

    void RestoreWindow() {
        try {
            m_win.AppWindow().Show();
            m_win.Activate();
            SetForegroundWindow(m_appHwnd);
        } catch (...) {}
    }

    void ShowTrayMenu() {
        HMENU menu = CreatePopupMenu();
        AppendMenuW(menu, MF_STRING, kMenuRestore, L"Open OpenDeezer");
        AppendMenuW(menu, MF_SEPARATOR, 0, nullptr);
        AppendMenuW(menu, MF_STRING, kMenuQuit, L"Quit");
        POINT p; GetCursorPos(&p);
        SetForegroundWindow(m_msgHwnd); // so the menu dismisses on focus loss
        TrackPopupMenu(menu, TPM_RIGHTBUTTON, p.x, p.y, 0, m_msgHwnd, nullptr);
        DestroyMenu(menu);
    }

    void QuitApp() {
        m_quitting = true;
        RemoveTray();
        if (m_smtc) { try { m_smtc.IsEnabled(false); } catch (...) {} }
        try { mux::Application::Current().Exit(); } catch (...) {}
    }

    // Close button: honor close-to-tray (keep engine playing in the background).
    void OnClosing(muw::AppWindow const&, muw::AppWindowClosingEventArgs const& args) {
        if (m_quitting) return;
        if (m_settings.closeToTray) {
            args.Cancel(true);
            try { m_win.AppWindow().Hide(); } catch (...) {}
        } else {
            RemoveTray(); // real close -> let the process exit
        }
    }

    static LRESULT CALLBACK TrayProc(HWND h, UINT msg, WPARAM w, LPARAM l) {
        auto self = reinterpret_cast<MainWindow*>(GetWindowLongPtrW(h, GWLP_USERDATA));
        if (self && msg == kTrayCallback) {
            switch (LOWORD(l)) {
                case WM_LBUTTONDBLCLK: self->RestoreWindow(); break;
                case WM_RBUTTONUP:
                case WM_CONTEXTMENU:   self->ShowTrayMenu();  break;
            }
            return 0;
        }
        if (self && msg == WM_COMMAND) {
            switch (LOWORD(w)) {
                case kMenuRestore: self->RestoreWindow(); break;
                case kMenuQuit:    self->QuitApp();       break;
            }
            return 0;
        }
        return DefWindowProcW(h, msg, w, l);
    }

    // ---- settings persistence ----------------------------------------------
    void LoadSettings() {
        auto path = SettingsPath(); if (path.empty()) return;
        std::ifstream f(path.c_str(), std::ios::binary);
        if (!f) return;
        std::string s((std::istreambuf_iterator<char>(f)), std::istreambuf_iterator<char>());
        wdj::JsonObject o{ nullptr };
        if (wdj::JsonObject::TryParse(to_hstring(s), o)) {
            m_settings.quality = (int)o.GetNamedNumber(L"quality", (double)m_settings.quality);
            m_settings.closeToTray = o.GetNamedBoolean(L"closeToTray", m_settings.closeToTray);
            m_settings.replayGain = o.GetNamedBoolean(L"replayGain", m_settings.replayGain);
        }
    }

    void SaveSettings() {
        auto dir = ConfigDir(); if (dir.empty()) return;
        CreateDirectoryW(dir.c_str(), nullptr);
        wdj::JsonObject o;
        o.SetNamedValue(L"quality", wdj::JsonValue::CreateNumberValue(m_settings.quality));
        o.SetNamedValue(L"closeToTray", wdj::JsonValue::CreateBooleanValue(m_settings.closeToTray));
        o.SetNamedValue(L"replayGain", wdj::JsonValue::CreateBooleanValue(m_settings.replayGain));
        std::string s = to_string(o.Stringify());
        std::ofstream f(SettingsPath().c_str(), std::ios::binary | std::ios::trunc);
        if (f) f.write(s.data(), static_cast<std::streamsize>(s.size()));
    }

    // ---- dialogs ------------------------------------------------------------
    fire_and_forget ShowMessage(hstring title, hstring body) {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(title));
        muxc::TextBlock t; t.Text(body); t.TextWrapping(mux::TextWrapping::Wrap);
        dlg.Content(t);
        dlg.CloseButtonText(L"OK");
        co_await dlg.ShowAsync();
    }

    fire_and_forget ShowSettings() {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"Settings"));

        muxc::StackPanel sp; sp.Spacing(18); sp.MinWidth(360);

        // Audio quality
        muxc::StackPanel qsec; qsec.Spacing(4);
        muxc::TextBlock qh; qh.Text(L"Audio quality"); qh.FontWeight(wut::FontWeights::SemiBold());
        muxc::ComboBox quality;
        quality.Items().Append(box_value(L"Normal — MP3 128 kbps"));
        quality.Items().Append(box_value(L"High — MP3 320 kbps"));
        quality.Items().Append(box_value(L"HiFi — FLAC lossless (falls back to MP3)"));
        quality.SelectedIndex(m_settings.quality);
        quality.HorizontalAlignment(mux::HorizontalAlignment::Stretch);
        qsec.Children().Append(qh); qsec.Children().Append(quality);

        // Warn when the selected quality exceeds what the signed-in plan supports.
        if (m_account.loggedIn) {
            bool exceeds = (m_settings.quality >= 2 && !m_account.canHifi) ||
                           (m_settings.quality >= 1 && !m_account.canHq);
            if (exceeds) {
                muxc::TextBlock qn;
                qn.Text(L"Your plan (" + m_account.offer + L") may not support this quality; playback falls back automatically.");
                qn.TextWrapping(mux::TextWrapping::Wrap);
                qn.Opacity(0.8);
                qsec.Children().Append(qn);
            }
        }

        // Volume normalization (ReplayGain) -- bound straight to the engine state.
        muxc::StackPanel rsec; rsec.Spacing(4);
        muxc::TextBlock rh; rh.Text(L"Volume normalization"); rh.FontWeight(wut::FontWeights::SemiBold());
        muxc::ToggleSwitch rg;
        rg.OnContent(box_value(L"Normalize loudness across tracks (ReplayGain)"));
        rg.OffContent(box_value(L"Play tracks at their original loudness"));
        rg.IsOn(DZReplayGain() != 0);
        rsec.Children().Append(rh); rsec.Children().Append(rg);

        // Background / close-to-tray
        muxc::StackPanel tsec; tsec.Spacing(4);
        muxc::TextBlock th; th.Text(L"Background playback"); th.FontWeight(wut::FontWeights::SemiBold());
        muxc::ToggleSwitch tray;
        tray.OnContent(box_value(L"Closing the window keeps playing in the tray"));
        tray.OffContent(box_value(L"Closing the window quits OpenDeezer"));
        tray.IsOn(m_settings.closeToTray);
        tsec.Children().Append(th); tsec.Children().Append(tray);

        sp.Children().Append(qsec);
        sp.Children().Append(rsec);
        sp.Children().Append(tsec);
        dlg.Content(sp);
        dlg.PrimaryButtonText(L"Save");
        dlg.CloseButtonText(L"Cancel");
        dlg.DefaultButton(muxc::ContentDialogButton::Primary);

        auto res = co_await dlg.ShowAsync();
        if (res == muxc::ContentDialogResult::Primary) {
            int lvl = quality.SelectedIndex();
            m_settings.quality = lvl < 0 ? 0 : (lvl > 2 ? 2 : lvl);
            m_settings.closeToTray = tray.IsOn();
            m_settings.replayGain = rg.IsOn();
            DZSetQuality(m_settings.quality); // applies to the NEXT track
            DZSetReplayGain(m_settings.replayGain ? 1 : 0);
            SaveSettings();
        }
    }

    fire_and_forget ShowAbout() {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"About OpenDeezer"));
        muxc::StackPanel sp; sp.Spacing(8);
        muxc::TextBlock h; h.Text(L"OpenDeezer 0.3.0"); h.FontSize(22); h.FontWeight(wut::FontWeights::SemiBold());
        h.Foreground(m_accent);
        muxc::TextBlock tag; tag.Text(L"An open source reimplementation of Deezer."); tag.TextWrapping(mux::TextWrapping::Wrap);
        muxc::TextBlock body; body.TextWrapping(mux::TextWrapping::Wrap);
        body.Text(L"Native Windows client (WinUI 3 · C++/WinRT · Fluent). The engine — login, browse, "
                  L"Blowfish decrypt, MP3 decode, WASAPI playback — is the Go core libdeezercore.dll, linked in-process.");
        muxc::TextBlock by; by.Text(L"By Cycl0o0. Licensed under AGPL-3.0."); by.Opacity(0.8);
        sp.Children().Append(h); sp.Children().Append(tag); sp.Children().Append(body);
        // Signed-in account tier: "<name> · <offer>".
        if (m_account.loggedIn && !m_account.name.empty()) {
            muxc::TextBlock acct;
            acct.Text(L"Signed in: " + m_account.name + L" · " + m_account.offer);
            acct.TextWrapping(mux::TextWrapping::Wrap);
            acct.FontWeight(wut::FontWeights::SemiBold());
            sp.Children().Append(acct);
        }
        sp.Children().Append(by);
        dlg.Content(sp);
        dlg.CloseButtonText(L"Close");
        co_await dlg.ShowAsync();
    }

    // ---- members ------------------------------------------------------------
    mux::Window m_win{ nullptr };
    muxm::SolidColorBrush m_accent{ nullptr };
    mud::DispatcherQueueTimer m_timer{ nullptr };

    muxc::NavigationView m_nav{ nullptr };
    muxc::NavigationViewItem m_likedItem{ nullptr }, m_playlistsItem{ nullptr }, m_searchItem{ nullptr },
                             m_chartsItem{ nullptr },
                             m_settingsItem{ nullptr }, m_aboutItem{ nullptr }, m_lastContentItem{ nullptr };

    mux::UIElement m_tracksPage{ nullptr }, m_playlistsPage{ nullptr }, m_searchPage{ nullptr };
    muxc::ListView m_trackList{ nullptr }, m_searchTrackList{ nullptr };
    muxc::GridView m_playlistGrid{ nullptr }, m_searchGrid{ nullptr };
    muxc::TextBox  m_searchBox{ nullptr };

    muxc::Image     m_cover{ nullptr };
    muxc::TextBlock m_nowTitle{ nullptr }, m_nowArtist{ nullptr }, m_posText{ nullptr }, m_durText{ nullptr };
    hstring m_curArtist; // base artist line; format badge is appended each tick
    muxc::Slider    m_seek{ nullptr }, m_volume{ nullptr };
    muxc::Button    m_playBtn{ nullptr }, m_repeatBtn{ nullptr };
    muxc::FontIcon  m_playIcon{ nullptr };
    muxp::ToggleButton m_shuffleBtn{ nullptr };
    muxc::HyperlinkButton m_lyricsBtn{ nullptr }, m_artistBtn{ nullptr }; // -> lyrics / artist views

    // ---- lyrics view --------------------------------------------------------
    mux::UIElement      m_lyricsPage{ nullptr };
    muxc::ScrollViewer  m_lyricsScroll{ nullptr };
    muxc::StackPanel    m_lyricsPanel{ nullptr };
    std::vector<muxc::TextBlock> m_lyricLineBlocks;       // parallel to m_lyrics.synced
    Lyrics  m_lyrics{};
    hstring m_lyricsTrackId;                              // track currently rendered/fetching
    std::map<std::wstring, Lyrics> m_lyricsCache;         // per track id
    int  m_lyricsGen = 0, m_lyricActive = -1;
    bool m_lyricsShown = false;                           // lyrics page is the live content

    // ---- artist view --------------------------------------------------------
    mux::UIElement     m_artistPage{ nullptr };
    muxc::ScrollViewer m_artistScroll{ nullptr };
    muxc::TextBlock    m_artistHeader{ nullptr }, m_artistFans{ nullptr };
    muxc::ListView     m_artistTopList{ nullptr };
    muxc::GridView     m_artistAlbumsGrid{ nullptr }, m_artistRelatedGrid{ nullptr };
    std::vector<Track>      m_artistTop;
    std::vector<Album>      m_artistAlbums;
    std::vector<ArtistInfo> m_artistRelated;

    std::vector<Track>    m_tracks, m_searchTracks, m_queue;
    std::vector<Playlist> m_playlists, m_searchPlaylists;
    std::vector<Album>    m_searchAlbums;
    std::vector<std::function<void()>> m_searchActions; // album/playlist tile -> open

    bool m_loggedIn = false, m_shuffle = false, m_updatingSeek = false, m_updatingVol = false, m_suppressNav = false;
    int  m_lastFinished = 0, m_artGen = 0, m_playGen = 0, m_queueIndex = -1, m_repeat = 0;
    std::chrono::steady_clock::time_point m_lastSeek{};

    // OS integration state
    Settings m_settings{};
    Account  m_account{};   // cached signed-in tier (filled by LoadAccount after login)
    HWND m_appHwnd{ nullptr }, m_msgHwnd{ nullptr };
    NOTIFYICONDATAW m_nid{};
    bool m_trayAdded = false, m_quitting = false;

    wm::SystemMediaTransportControls m_smtc{ nullptr };
    winrt::event_token m_smtcButtonToken{}, m_smtcPosToken{};
    wm::MediaPlaybackStatus m_lastSmtcStatus{ wm::MediaPlaybackStatus::Closed };
    int m_smtcTimelineTick = 0;

    static constexpr UINT kTrayCallback = WM_APP + 1;
    static constexpr UINT kTrayUID      = 1;
    static constexpr UINT kMenuRestore  = 1001;
    static constexpr UINT kMenuQuit     = 1002;
};

// =============================================================================
//  App -- code-only Application. WinUI 3's built-in controls register their own
//  metadata internally, so a pure-code app needs no IXamlMetadataProvider;
//  XamlControlsResources supplies the default Fluent styles.
// =============================================================================
struct App : mux::ApplicationT<App> {
    void OnLaunched(mux::LaunchActivatedEventArgs const&) {
        Resources().MergedDictionaries().Append(muxc::XamlControlsResources{}); // theme styles
        // Deezer-purple accent override before any UI is built.
        winrt::Windows::UI::Color accent{ 0xFF, 0xA2, 0x38, 0xFF };
        Resources().Insert(box_value(hstring(L"SystemAccentColor")), box_value(accent));
        m_window = winrt::make_self<MainWindow>();
        m_window->Activate();
    }

private:
    winrt::com_ptr<MainWindow> m_window{ nullptr };
};

int __stdcall wWinMain(HINSTANCE, HINSTANCE, PWSTR, int) {
    srand(GetTickCount());                                         // vary shuffle per run
    winrt::init_apartment(winrt::apartment_type::single_threaded); // STA required for XAML
    mux::Application::Start([](auto&&) { winrt::make<App>(); });
    return 0;
}
