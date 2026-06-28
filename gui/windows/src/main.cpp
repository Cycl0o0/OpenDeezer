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
// Login: on startup a saved/env ARL is tried silently; otherwise a chooser offers
// "Log in with Deezer" -- a WebView2 (Microsoft.UI.Xaml.Controls.WebView2) pointed
// at the Deezer web login whose CoreWebView2 cookie store is polled until the
// HttpOnly "arl" cookie appears, then captured and persisted to
// %APPDATA%\opendeezer\arl.txt -- with manual ARL entry kept as a fallback.
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

#include <winrt/Microsoft.Web.WebView2.Core.h>    // CoreWebView2 + CookieManager (arl capture)

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
    char*          DZAccountJSON(void);               // {userId,name,offer,canHq,canHifi,premium,loggedIn}; premium=false = Deezer Free (no on-demand)
    char*          DZChartsJSON(void);                // {tracks,albums,artists,playlists}
    char*          DZArtistTopJSON(char* id);         // {tracks:[...]}
    char*          DZArtistProfileJSON(char* id);     // {artist,top,albums,related}
    char*          DZLyricsJSON(char* id);            // {plain,synced:[{timeMs,text}],isSynced}
    void           DZSetReplayGain(int on);           // 0 off / 1 on
    int            DZReplayGain(void);                // current state 0/1
    // ---- v0.4 additions -----------------------------------------------------
    int            DZAddFavorite(char* trackID);              // 1 ok / 0 fail
    int            DZRemoveFavorite(char* trackID);           // 1 ok / 0 fail
    int            DZAddToPlaylist(char* playlistID, char* trackID);    // 1/0
    int            DZRemoveFromPlaylist(char* playlistID, char* trackID); // 1/0
    char*          DZCreatePlaylist(char* title);            // {"id":"..."} | {"error":...}
    int            DZRenamePlaylist(char* playlistID, char* title);     // 1/0
    int            DZDeletePlaylist(char* playlistID);       // 1/0
    char*          DZFlowJSON(void);                          // {"tracks":[...]}
    char*          DZSearchPodcastsJSON(char* q);            // {"podcasts":[{id,name,description,artworkUrl,episodeCount}]}
    char*          DZPodcastEpisodesJSON(char* podcastID);   // {"episodes":[{id,title,description,artworkUrl,durationMs,releaseDate}]}
    int            DZPlayEpisode(char* episodeID, long long durationMS); // plain stream; 1/0
    char*          DZAudioDevicesJSON(void);                 // {"devices":[{id,name,isDefault}]}  (id ""=default)
    int            DZSetAudioDevice(char* id);               // 1/0 ("" = system default)
    char*          DZCurrentAudioDevice(void);               // selected device id ("" = default)
    void           DZSetGapless(int on);                     // 0/1
    int            DZGapless(void);                          // current state 0/1
    void           DZSetCrossfadeMS(int ms);                 // 0 = off
    int            DZCrossfadeMS(void);                      // current ms
    void           DZPreload(char* trackID, long long durationMS); // warm next for gapless/crossfade
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
namespace wv2   = winrt::Microsoft::Web::WebView2::Core; // CoreWebView2 cookie store (arl capture)
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
// isEpisode flags a podcast episode: it shares the queue but plays through the
// plain-stream path (DZPlayEpisode) and skips like / add-to-playlist / preload.
struct Track    { hstring id, name, artistId, artistLine, albumName, artworkUrl; int64_t durationMs = 0; bool isEpisode = false; bool isExplicit = false; };
struct Album    { hstring id, name, artistLine, artworkUrl; };
struct Playlist { hstring id, name, owner, artworkUrl; int trackCount = 0; };
// premium=false is a Deezer Free account that CANNOT stream on-demand -> the app
// gates itself behind a block message (see ShowBlocked / FinishLogin).
struct Account  { hstring userId, name, offer; bool canHq = false, canHifi = false, loggedIn = false, premium = false; };
// DZSearchPodcastsJSON / DZPodcastEpisodesJSON wire rows.
struct Podcast  { hstring id, name, description, artworkUrl; int episodeCount = 0; };
struct Episode  { hstring id, title, description, artworkUrl, releaseDate; int64_t durationMs = 0; };

// jArtistInfo: {id,name,artworkUrl,nbFans}  (related artists + artist header)
struct ArtistInfo { hstring id, name, artworkUrl; int64_t nbFans = 0; };
// DZLyricsJSON: {plain, synced:[{timeMs,text}], isSynced}
struct LyricLine  { int64_t timeMs = 0; hstring text; };
struct Lyrics     { hstring plain; std::vector<LyricLine> synced; bool isSynced = false; };
// DZArtistProfileJSON: {artist, top:[T], albums:[A], related:[Ar]}
struct ArtistProfile { ArtistInfo artist; std::vector<Track> top; std::vector<Album> albums; std::vector<ArtistInfo> related; };

// ---- persisted settings -----------------------------------------------------
// quality: 0 Normal,1 High,2 HiFi. audioDevice "" = system default. crossfadeMs 0 = off.
struct Settings { int quality = 1; bool closeToTray = true; bool replayGain = false;
                  bool gapless = false; int crossfadeMs = 0; hstring audioDevice; };

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
    t.isExplicit = o.GetNamedBoolean(L"explicit", false); // explicit content -> "E" badge
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
    a.premium  = o.GetNamedBoolean(L"premium", false); // false = Deezer Free (no on-demand)
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

// Charts / browse "artists":[{id,name,artworkUrl,nbFans}] -> standalone artist tiles.
static std::vector<ArtistInfo> ParseArtists(hstring const& json) {
    std::vector<ArtistInfo> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"artists", wdj::JsonArray{}))
        out.push_back(ArtistFromObj(v.GetObject()));
    return out;
}

static std::vector<Podcast> ParsePodcasts(hstring const& json) {
    std::vector<Podcast> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"podcasts", wdj::JsonArray{})) {
        auto o = v.GetObject();
        Podcast p;
        p.id           = o.GetNamedString(L"id", L"");
        p.name         = o.GetNamedString(L"name", L"");
        p.description  = o.GetNamedString(L"description", L"");
        p.artworkUrl   = o.GetNamedString(L"artworkUrl", L"");
        p.episodeCount = static_cast<int>(o.GetNamedNumber(L"episodeCount", 0));
        out.push_back(std::move(p));
    }
    return out;
}

static std::vector<Episode> ParseEpisodes(hstring const& json) {
    std::vector<Episode> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"episodes", wdj::JsonArray{})) {
        auto o = v.GetObject();
        Episode e;
        e.id          = o.GetNamedString(L"id", L"");
        e.title       = o.GetNamedString(L"title", L"");
        e.description = o.GetNamedString(L"description", L"");
        e.artworkUrl  = o.GetNamedString(L"artworkUrl", L"");
        e.durationMs  = static_cast<int64_t>(o.GetNamedNumber(L"durationMs", 0));
        e.releaseDate = o.GetNamedString(L"releaseDate", L"");
        out.push_back(std::move(e));
    }
    return out;
}

// DZCreatePlaylist -> {"id":"..."} (or {"error":"..."}); returns "" on failure.
static hstring ParseCreatedId(hstring const& json) {
    wdj::JsonObject o{ nullptr };
    if (!wdj::JsonObject::TryParse(json, o)) return L"";
    return o.GetNamedString(L"id", L"");
}

// DZAudioDevicesJSON -> {"devices":[{id,name,isDefault}]}.
struct AudioDevice { hstring id, name; bool isDefault = false; };
static std::vector<AudioDevice> ParseDevices(hstring const& json) {
    std::vector<AudioDevice> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"devices", wdj::JsonArray{})) {
        auto o = v.GetObject();
        AudioDevice d;
        d.id        = o.GetNamedString(L"id", L"");
        d.name      = o.GetNamedString(L"name", L"");
        d.isDefault = o.GetNamedBoolean(L"isDefault", false);
        out.push_back(std::move(d));
    }
    return out;
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

// Persist a captured/entered ARL to %APPDATA%\opendeezer\arl.txt -- the SAME file
// LoadArl() reads at startup, so the next launch auto-logs-in. Mirrors SaveSettings.
static void SaveArl(hstring const& arl) {
    auto d = ConfigDir(); if (d.empty()) return;
    CreateDirectoryW(d.c_str(), nullptr);
    std::string s = to_string(arl);
    std::wstring path = d + L"\\arl.txt";
    std::ofstream f(path.c_str(), std::ios::binary | std::ios::trunc);
    if (f) f.write(s.data(), static_cast<std::streamsize>(s.size()));
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

        m_likedItem     = NavItem(L"Liked Songs", muxc::Symbol::Audio,      L"liked");
        m_flowItem      = NavItem(L"Flow",        muxc::Symbol::Play,       L"flow");
        m_playlistsItem = NavItem(L"Playlists",   muxc::Symbol::List,       L"playlists");
        m_chartsItem    = NavItem(L"Charts",      muxc::Symbol::World,      L"charts");
        m_podcastsItem  = NavItem(L"Podcasts",    muxc::Symbol::Microphone, L"podcasts");
        m_searchItem    = NavItem(L"Search",      muxc::Symbol::Find,       L"search");
        m_nav.MenuItems().Append(m_likedItem);
        m_nav.MenuItems().Append(m_flowItem);
        m_nav.MenuItems().Append(m_playlistsItem);
        m_nav.MenuItems().Append(m_chartsItem);
        m_nav.MenuItems().Append(m_podcastsItem);
        m_nav.MenuItems().Append(m_searchItem);

        // Account: re-open the EXISTING login chooser on demand so an already
        // (auto-)logged-in user can re-authenticate or switch accounts. Handled
        // like Settings/About in OnNav (a modal action, not a page).
        m_accountItem  = NavItem(L"Log in / Switch account…", muxc::Symbol::Contact, L"account");
        m_settingsItem = NavItem(L"Settings", muxc::Symbol::Setting, L"settings");
        m_aboutItem    = NavItem(L"About",    muxc::Symbol::Help,    L"about");
        m_nav.FooterMenuItems().Append(m_accountItem);
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

        // Playlists page: a "New Playlist" toolbar over the grid. Rename / delete
        // live on each tile's right-click context menu (built in FillPlaylistGrid).
        m_playlistGrid = muxc::GridView();
        m_playlistGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_playlistGrid.IsItemClickEnabled(true);
        m_playlistGrid.ItemClick({ get_weak(), &MainWindow::OnPlaylistClick });
        {
            muxc::Grid pg;
            { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); pg.RowDefinitions().Append(r); }
            { muxc::RowDefinition r; r.Height(mux::GridLength{ 1, mux::GridUnitType::Star }); pg.RowDefinitions().Append(r); }
            pg.RowSpacing(8); pg.Padding({ 4, 4, 4, 4 });
            muxc::StackPanel bar; bar.Orientation(muxc::Orientation::Horizontal); bar.Spacing(8);
            muxc::Button newBtn; { muxc::StackPanel c; c.Orientation(muxc::Orientation::Horizontal); c.Spacing(6);
                muxc::FontIcon fi; fi.Glyph(L""); c.Children().Append(fi);
                muxc::TextBlock tb; tb.Text(L"New Playlist"); c.Children().Append(tb); newBtn.Content(c); }
            newBtn.Click({ get_weak(), &MainWindow::OnNewPlaylist });
            bar.Children().Append(newBtn);
            muxc::Grid::SetRow(bar, 0); pg.Children().Append(bar);
            muxc::Grid::SetRow(m_playlistGrid, 1); pg.Children().Append(m_playlistGrid);
            m_playlistsPage = pg;
        }

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
        BuildChartsPage();
        BuildPodcastPage();
    }

    // Charts: a scrolling column of Top Tracks + Albums + Artists + Playlists,
    // mirroring the artist page (inner lists don't scroll; outer ScrollViewer does).
    void BuildChartsPage() {
        m_chartsScroll = muxc::ScrollViewer();
        m_chartsScroll.Padding({ 16, 12, 16, 16 });
        m_chartsScroll.HorizontalScrollMode(muxc::ScrollMode::Disabled);
        m_chartsScroll.HorizontalScrollBarVisibility(muxc::ScrollBarVisibility::Disabled);
        muxc::StackPanel col; col.Spacing(8);
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
        m_chartsTrackList = muxc::ListView();
        m_chartsTrackList.SelectionMode(muxc::ListViewSelectionMode::None);
        m_chartsTrackList.IsItemClickEnabled(true);
        m_chartsTrackList.ItemClick({ get_weak(), &MainWindow::OnChartsTrackClick });
        noInnerScroll(m_chartsTrackList);
        col.Children().Append(m_chartsTrackList);

        col.Children().Append(section(L"Albums"));
        m_chartsAlbumsGrid = muxc::GridView();
        m_chartsAlbumsGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_chartsAlbumsGrid.IsItemClickEnabled(true);
        m_chartsAlbumsGrid.ItemClick({ get_weak(), &MainWindow::OnChartsAlbumClick });
        noInnerScroll(m_chartsAlbumsGrid);
        col.Children().Append(m_chartsAlbumsGrid);

        col.Children().Append(section(L"Artists"));
        m_chartsArtistsGrid = muxc::GridView();
        m_chartsArtistsGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_chartsArtistsGrid.IsItemClickEnabled(true);
        m_chartsArtistsGrid.ItemClick({ get_weak(), &MainWindow::OnChartsArtistClick });
        noInnerScroll(m_chartsArtistsGrid);
        col.Children().Append(m_chartsArtistsGrid);

        col.Children().Append(section(L"Playlists"));
        m_chartsPlaylistsGrid = muxc::GridView();
        m_chartsPlaylistsGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_chartsPlaylistsGrid.IsItemClickEnabled(true);
        m_chartsPlaylistsGrid.ItemClick({ get_weak(), &MainWindow::OnChartsPlaylistClick });
        noInnerScroll(m_chartsPlaylistsGrid);
        col.Children().Append(m_chartsPlaylistsGrid);

        m_chartsScroll.Content(col);
        m_chartsPage = m_chartsScroll;
    }

    // Podcasts: a search row + a grid of shows. Clicking a show loads its episodes
    // into the shared track list (as isEpisode tracks) so playback reuses the queue.
    void BuildPodcastPage() {
        muxc::Grid pp;
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); pp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 0, mux::GridUnitType::Auto }); pp.RowDefinitions().Append(r); }
        { muxc::RowDefinition r; r.Height(mux::GridLength{ 1, mux::GridUnitType::Star }); pp.RowDefinitions().Append(r); }
        pp.Padding({ 4, 4, 4, 4 }); pp.RowSpacing(8);

        muxc::Grid queryRow; queryRow.ColumnSpacing(8);
        queryRow.ColumnDefinitions().Append(ColStar());
        queryRow.ColumnDefinitions().Append(ColAuto());
        m_podcastBox = muxc::TextBox();
        m_podcastBox.PlaceholderText(L"Search podcasts…");
        m_podcastBox.KeyDown({ get_weak(), &MainWindow::OnPodcastKey });
        muxc::Grid::SetColumn(m_podcastBox, 0);
        muxc::Button pbtn; pbtn.Content(box_value(L"Search"));
        pbtn.Click({ get_weak(), &MainWindow::OnPodcastSearchClick });
        muxc::Grid::SetColumn(pbtn, 1);
        queryRow.Children().Append(m_podcastBox);
        queryRow.Children().Append(pbtn);
        muxc::Grid::SetRow(queryRow, 0); pp.Children().Append(queryRow);

        muxc::TextBlock h; h.Text(L"Shows"); h.FontWeight(wut::FontWeights::SemiBold());
        muxc::Grid::SetRow(h, 1); pp.Children().Append(h);

        m_podcastGrid = muxc::GridView();
        m_podcastGrid.SelectionMode(muxc::ListViewSelectionMode::None);
        m_podcastGrid.IsItemClickEnabled(true);
        m_podcastGrid.ItemClick({ get_weak(), &MainWindow::OnPodcastClick });
        muxc::Grid::SetRow(m_podcastGrid, 2); pp.Children().Append(m_podcastGrid);

        m_podcastPage = pp;
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

        muxc::StackPanel now; now.VerticalAlignment(mux::VerticalAlignment::Center); now.MinWidth(170); now.MaxWidth(300);
        m_nowTitle  = muxc::TextBlock(); m_nowTitle.Text(L"Logging in…"); m_nowTitle.FontWeight(wut::FontWeights::SemiBold());
        m_nowTitle.TextWrapping(mux::TextWrapping::NoWrap); m_nowTitle.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        m_nowArtist = muxc::TextBlock(); m_nowArtist.Opacity(0.6); m_nowArtist.FontSize(12);
        m_nowArtist.TextWrapping(mux::TextWrapping::NoWrap); m_nowArtist.TextTrimming(mux::TextTrimming::CharacterEllipsis);
        now.Children().Append(m_nowTitle); now.Children().Append(m_nowArtist);
        // Quick actions for the current track: Like (heart toggle), Add-to-playlist,
        // and links to the Lyrics + Artist views.
        muxc::StackPanel meta; meta.Orientation(muxc::Orientation::Horizontal); meta.Spacing(6); meta.VerticalAlignment(mux::VerticalAlignment::Center);
        m_likeBtn = muxp::ToggleButton();
        { muxc::FontIcon fi; fi.Glyph(L""); fi.FontSize(14); m_likeBtn.Content(fi); } // Segoe MDL2 Heart
        m_likeBtn.Padding({ 6, 2, 6, 2 });
        winrt::Microsoft::UI::Xaml::Controls::ToolTipService::SetToolTip(m_likeBtn, box_value(L"Like / unlike"));
        m_likeBtn.Click({ get_weak(), &MainWindow::OnLike });
        m_addBtn = muxc::Button();
        { muxc::FontIcon fi; fi.Glyph(L"\uECC8"); fi.FontSize(14); m_addBtn.Content(fi); } // add to playlist
        m_addBtn.Padding({ 6, 2, 6, 2 });
        winrt::Microsoft::UI::Xaml::Controls::ToolTipService::SetToolTip(m_addBtn, box_value(L"Add to playlist"));
        m_addBtn.Click({ get_weak(), &MainWindow::OnAddCurrentToPlaylist });
        m_lyricsBtn = muxc::HyperlinkButton(); m_lyricsBtn.Content(box_value(L"Lyrics"));
        m_lyricsBtn.Padding({ 4, 0, 4, 0 }); m_lyricsBtn.Click({ get_weak(), &MainWindow::OnLyrics });
        m_artistBtn = muxc::HyperlinkButton(); m_artistBtn.Content(box_value(L"Artist"));
        m_artistBtn.Padding({ 4, 0, 4, 0 }); m_artistBtn.Click({ get_weak(), &MainWindow::OnArtist });
        meta.Children().Append(m_likeBtn); meta.Children().Append(m_addBtn);
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
    // A small boxed "E" tag shown at the start of an explicit track's title.
    mux::FrameworkElement MakeExplicitBadge() {
        muxc::Border b;
        winrt::Windows::UI::Color bg{ 0xFF, 0x9A, 0x9A, 0x9A }; // neutral gray chip
        b.Background(muxm::SolidColorBrush(bg));
        b.CornerRadius(mux::CornerRadius{ 3, 3, 3, 3 });
        b.Padding({ 4, 0, 4, 1 });
        b.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::TextBlock e; e.Text(L"E"); e.FontSize(10); e.FontWeight(wut::FontWeights::Bold());
        e.LineHeight(12); e.VerticalAlignment(mux::VerticalAlignment::Center);
        winrt::Windows::UI::Color fg{ 0xFF, 0xFF, 0xFF, 0xFF };
        e.Foreground(muxm::SolidColorBrush(fg));
        b.Child(e);
        winrt::Microsoft::UI::Xaml::Controls::ToolTipService::SetToolTip(b, box_value(L"Explicit content"));
        return b;
    }

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
        // Explicit tracks get a leading "E" badge; a 2-col grid keeps the title's
        // ellipsis trimming (a horizontal StackPanel would give the title infinite width).
        if (t.isExplicit) {
            muxc::Grid titleRow; titleRow.ColumnSpacing(6);
            titleRow.ColumnDefinitions().Append(ColAuto());
            titleRow.ColumnDefinitions().Append(ColStar());
            auto badge = MakeExplicitBadge();
            muxc::Grid::SetColumn(badge, 0); titleRow.Children().Append(badge);
            muxc::Grid::SetColumn(title, 1); titleRow.Children().Append(title);
            sp.Children().Append(titleRow);
        } else {
            sp.Children().Append(title);
        }
        sp.Children().Append(artist);
        muxc::Grid::SetColumn(sp, 1); g.Children().Append(sp);
        muxc::TextBlock dur; dur.Text(TimeText(t.durationMs)); dur.Opacity(0.6); dur.VerticalAlignment(mux::VerticalAlignment::Center);
        muxc::Grid::SetColumn(dur, 2); g.Children().Append(dur);
        if (!t.artworkUrl.empty()) LoadArt(img, t.artworkUrl, m_artGen, false);
        // Right-click row actions (skipped for podcast episodes, which aren't
        // library tracks and can't be liked / added to a music playlist).
        if (!t.isEpisode && !t.id.empty()) {
            muxc::MenuFlyout mf;
            muxc::MenuFlyoutItem like; like.Text(L"Like"); like.Tag(box_value(t.id));
            like.Click({ get_weak(), &MainWindow::OnRowLike });
            muxc::MenuFlyoutItem add; add.Text(L"Add to playlist…"); add.Tag(box_value(t.id));
            add.Click({ get_weak(), &MainWindow::OnRowAddToPlaylist });
            mf.Items().Append(like); mf.Items().Append(add);
            g.ContextFlyout(mf);
        }
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
            auto tile = MakeTile(p.name, sub, p.artworkUrl, i);
            // Per-tile right-click: rename / delete. Tag carries the m_playlists index.
            muxc::MenuFlyout mf;
            muxc::MenuFlyoutItem rn; rn.Text(L"Rename…"); rn.Tag(box_value(i));
            rn.Click({ get_weak(), &MainWindow::OnPlaylistRename });
            muxc::MenuFlyoutItem del; del.Text(L"Delete…"); del.Tag(box_value(i));
            del.Click({ get_weak(), &MainWindow::OnPlaylistDelete });
            mf.Items().Append(rn); mf.Items().Append(del);
            if (auto fe = tile.try_as<mux::FrameworkElement>()) fe.ContextFlyout(mf);
            m_playlistGrid.Items().Append(tile);
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
    // Startup: if a saved/env ARL exists try it silently; otherwise (or on a
    // failed silent login) present the login chooser -- "Log in with Deezer"
    // (embedded WebView2 + automatic arl-cookie capture) or manual ARL entry.
    fire_and_forget StartLogin() {
        auto strong = get_strong();
        std::wstring arlW = LoadArl();
        if (arlW.empty()) { ShowLoginChoice(); co_return; }
        co_await TryLogin(hstring{ arlW }, /*persist=*/false);
    }

    // Run DZInit(arl) off-thread; on success finish login (optionally persisting
    // the ARL so the next launch auto-logs-in), on failure re-open the chooser.
    wf::IAsyncAction TryLogin(hstring arl, bool persist) {
        auto strong = get_strong();
        m_nowTitle.Text(L"Logging in…");
        co_await winrt::resume_background();
        std::string s = to_string(arl);
        int ok = DZInit(s.data());
        co_await resume_foreground(m_win.DispatcherQueue());
        if (ok) {
            if (persist) SaveArl(arl); // remember for next launch (same file LoadArl reads)
            FinishLogin();
        } else {
            m_nowTitle.Text(L"Login failed");
            ShowMessage(L"Login failed", L"That ARL is invalid or expired. Try logging in again.");
            ShowLoginChoice();
        }
    }

    // Shared success path: apply persisted prefs, then fetch the account tier
    // up-front. OpenDeezer streams on-demand, which a Deezer Free plan cannot do,
    // so a non-premium account is gated behind a block (ShowBlocked) BEFORE any
    // browsing/playback starts. Every login path (auto-ARL, webview, manual ARL)
    // funnels through here, so the block covers all of them.
    fire_and_forget FinishLogin() {
        auto strong = get_strong();
        m_loggedIn = true;
        DZSetQuality(m_settings.quality); // apply persisted quality on startup
        DZSetReplayGain(m_settings.replayGain ? 1 : 0); // apply persisted normalization
        DZSetGapless(m_settings.gapless ? 1 : 0);       // apply persisted gapless
        DZSetCrossfadeMS(m_settings.crossfadeMs);       // apply persisted crossfade
        if (!m_settings.audioDevice.empty()) {          // apply persisted output device
            std::string dev = to_string(m_settings.audioDevice);
            DZSetAudioDevice(dev.data());
        }
        // Fetch tier (name / offer / hq-hifi caps / premium) off-thread.
        m_nowTitle.Text(L"Checking account…");
        co_await winrt::resume_background();
        auto acct = ParseAccount(TakeJson(DZAccountJSON()));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_account = std::move(acct);
        if (!m_account.premium) { ShowBlocked(); co_return; } // Free account -> gate the app

        m_lastFinished = DZFinishedCount();
        m_updatingVol = true; m_volume.Value(DZVolume() * 100.0); m_updatingVol = false;
        m_timer.Start();
        m_nowTitle.Text(L"Not playing");
        m_nowArtist.Text(L"");
        m_suppressNav = false;
        m_nav.SelectedItem(m_likedItem); // -> OnNav -> LoadFavorites
    }

    // Free-account block: replace the ENTIRE window content (no nav, no transport
    // bar) with a non-dismissible message so a Deezer Free user can neither browse
    // nor play. The only action is Quit. A Premium subscription is required.
    void ShowBlocked() {
        m_blocked = true;
        if (m_timer)  { try { m_timer.Stop(); } catch (...) {} }
        try { DZStop(); } catch (...) {}

        muxc::Grid page;
        winrt::Windows::UI::Color bg{ 0xFF, 0x14, 0x04, 0x1E }; // dark Deezer backdrop
        page.Background(muxm::SolidColorBrush(bg));

        muxc::StackPanel sp; sp.Spacing(14); sp.MaxWidth(560); sp.Padding({ 24, 24, 24, 24 });
        sp.HorizontalAlignment(mux::HorizontalAlignment::Center);
        sp.VerticalAlignment(mux::VerticalAlignment::Center);

        muxc::TextBlock brand; brand.Text(L"OpenDeezer"); brand.FontSize(22);
        brand.FontWeight(wut::FontWeights::SemiBold()); brand.Foreground(m_accent);
        brand.HorizontalAlignment(mux::HorizontalAlignment::Center);

        muxc::TextBlock title; title.Text(L"Sorry — your account isn't supported");
        title.FontSize(26); title.FontWeight(wut::FontWeights::SemiBold());
        title.TextWrapping(mux::TextWrapping::Wrap); title.TextAlignment(mux::TextAlignment::Center);
        title.HorizontalAlignment(mux::HorizontalAlignment::Center);

        hstring offer = m_account.offer.empty() ? hstring(L"Deezer Free") : m_account.offer;
        muxc::TextBlock body; body.TextWrapping(mux::TextWrapping::Wrap);
        body.TextAlignment(mux::TextAlignment::Center); body.Opacity(0.85);
        body.HorizontalAlignment(mux::HorizontalAlignment::Center);
        body.Text(L"OpenDeezer needs a Deezer Premium subscription to stream. "
                  L"Your account: " + offer + L". "
                  L"Subscribe at deezer.com, then restart OpenDeezer.");

        muxc::Button quit; quit.Content(box_value(L"Quit"));
        quit.HorizontalAlignment(mux::HorizontalAlignment::Center);
        quit.Click({ get_weak(), &MainWindow::OnBlockedQuit });

        sp.Children().Append(brand);
        sp.Children().Append(title);
        sp.Children().Append(body);
        sp.Children().Append(quit);
        page.Children().Append(sp);

        m_win.Content(page); // wholesale replace -> the app can no longer be used
    }

    void OnBlockedQuit(wf::IInspectable const&, mux::RoutedEventArgs const&) { QuitApp(); }

    // Login chooser: "Log in with Deezer" opens the embedded webview, "Enter ARL"
    // is the manual fallback. Cancel leaves the app idle (relaunch to retry).
    fire_and_forget ShowLoginChoice() {
        auto strong = get_strong();
        m_nowTitle.Text(L"Not signed in");
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"Sign in to Deezer"));
        muxc::TextBlock t; t.TextWrapping(mux::TextWrapping::Wrap);
        t.Text(L"Log in with your Deezer account in the in-app browser — OpenDeezer "
               L"captures the login automatically, so you never have to copy an ARL by "
               L"hand. (Advanced: you can still paste an ARL manually.)");
        dlg.Content(t);
        dlg.PrimaryButtonText(L"Log in with Deezer");
        dlg.SecondaryButtonText(L"Enter ARL manually");
        dlg.CloseButtonText(L"Cancel");
        dlg.DefaultButton(muxc::ContentDialogButton::Primary);
        auto res = co_await dlg.ShowAsync();
        if (res == muxc::ContentDialogResult::Primary) {
            hstring arl = co_await ShowWebLogin();
            if (!arl.empty()) { co_await TryLogin(arl, /*persist=*/true); }
            else              { ShowLoginChoice(); } // cancelled / webview unavailable
        } else if (res == muxc::ContentDialogResult::Secondary) {
            hstring entered = co_await PromptText(L"Log in with ARL",
                                                  L"Paste your Deezer ARL", L"");
            std::wstring w{ entered.c_str() }; Trim(w);
            if (!w.empty()) { co_await TryLogin(hstring{ w }, /*persist=*/true); }
            else            { ShowLoginChoice(); }
        }
        // Cancel: stay idle; nav stays empty until the user re-triggers login.
    }

    // Embedded Deezer login: host a WebView2 in a modal dialog pointed at the web
    // login, then poll the CoreWebView2 cookie store until a non-empty "arl"
    // cookie (domain .deezer.com) appears -- HttpOnly, so it is only readable via
    // CookieManager.GetCookiesAsync (not document.cookie). Returns the captured
    // ARL, or "" if the user cancels / the WebView2 runtime is unavailable.
    wf::IAsyncOperation<hstring> ShowWebLogin() {
        auto strong = get_strong();
        m_capturedArl = L"";

        m_loginDialog = muxc::ContentDialog();
        m_loginDialog.XamlRoot(m_win.Content().XamlRoot());
        m_loginDialog.Title(box_value(L"Log in with Deezer"));
        // Let the dialog grow to fit the web page (defaults cap around 548 px).
        m_loginDialog.Resources().Insert(box_value(hstring(L"ContentDialogMaxWidth")),  box_value(620.0));
        m_loginDialog.Resources().Insert(box_value(hstring(L"ContentDialogMaxHeight")), box_value(740.0));

        m_loginWebView = muxc::WebView2();
        m_loginWebView.Width(560);
        m_loginWebView.Height(640);
        m_loginDialog.Content(m_loginWebView);
        m_loginDialog.CloseButtonText(L"Cancel");

        // Setting Source kicks off implicit CoreWebView2 init once the control is
        // loaded (i.e. after the dialog shows). We must NOT co_await
        // EnsureCoreWebView2Async() here: it would not complete until the control
        // loads, which only happens after ShowAsync -> deadlock. The poll below
        // simply waits for CoreWebView2() to become non-null.
        try { m_loginWebView.Source(wf::Uri(L"https://www.deezer.com/login")); }
        catch (...) { m_loginWebView = nullptr; m_loginDialog = nullptr; co_return hstring(L""); }

        m_arlPollTimer = m_win.DispatcherQueue().CreateTimer();
        m_arlPollTimer.Interval(std::chrono::milliseconds(700));
        m_arlPollTimer.Tick({ get_weak(), &MainWindow::OnArlPoll });
        m_arlPollTimer.Start();

        co_await m_loginDialog.ShowAsync(); // returns when arl captured (Hide) or Cancel

        if (m_arlPollTimer) { m_arlPollTimer.Stop(); m_arlPollTimer = nullptr; }
        m_loginWebView = nullptr;
        m_loginDialog  = nullptr;
        co_return m_capturedArl;
    }

    // Cookie poll (UI thread): once CoreWebView2 is up, read the deezer.com cookie
    // jar and, when a non-empty "arl" appears, stash it and close the dialog.
    fire_and_forget OnArlPoll(mud::DispatcherQueueTimer const&, wf::IInspectable const&) {
        auto strong = get_strong();
        if (m_arlPollBusy || !m_loginWebView) co_return;
        auto core = m_loginWebView.CoreWebView2();
        if (!core) co_return;             // CoreWebView2 not initialized yet
        m_arlPollBusy = true;
        try {
            auto cookies = co_await core.CookieManager().GetCookiesAsync(L"https://www.deezer.com");
            if (m_loginWebView) {         // dialog still open after the await
                for (auto const& c : cookies) {
                    if (c.Name() == L"arl") {
                        hstring v = c.Value();
                        if (!v.empty()) {
                            m_capturedArl = v;
                            if (m_arlPollTimer) m_arlPollTimer.Stop();
                            if (m_loginDialog)  m_loginDialog.Hide();
                        }
                        break;
                    }
                }
            }
        } catch (...) {}
        m_arlPollBusy = false;
    }

    // ---- navigation ---------------------------------------------------------
    void OnNav(muxc::NavigationView const& nav, muxc::NavigationViewSelectionChangedEventArgs const& args) {
        if (m_suppressNav) return;
        auto item = args.SelectedItem().try_as<muxc::NavigationViewItem>();
        if (!item) return;
        auto tag = unbox_value_or<hstring>(item.Tag(), L"");
        // About / Settings / Account are modal actions, not pages: open then
        // revert selection. "account" re-opens the EXISTING login chooser
        // (web-login + manual-ARL) so the user can re-auth / switch accounts;
        // on success TryLogin -> FinishLogin reflects the new account.
        if (tag == L"about" || tag == L"settings" || tag == L"account") {
            if (tag == L"about")          ShowAbout();
            else if (tag == L"settings")  ShowSettings();
            else                          ShowLoginChoice();
            m_suppressNav = true;
            nav.SelectedItem(m_lastContentItem ? m_lastContentItem : m_likedItem);
            m_suppressNav = false;
            return;
        }
        m_lastContentItem = item;
        m_lyricsShown = false; // leaving the lyrics/artist page for a menu page
        if (tag == L"liked") {
            nav.Header(box_value(L"Liked Songs")); nav.Content(m_tracksPage); LoadFavorites();
        } else if (tag == L"flow") {
            nav.Header(box_value(L"Flow")); nav.Content(m_tracksPage); LoadFlow();
        } else if (tag == L"charts") {
            nav.Header(box_value(L"Charts")); nav.Content(m_chartsPage); LoadCharts();
        } else if (tag == L"playlists") {
            nav.Header(box_value(L"Playlists")); nav.Content(m_playlistsPage); LoadPlaylists();
        } else if (tag == L"podcasts") {
            nav.Header(box_value(L"Podcasts")); nav.Content(m_podcastPage);
            m_podcastBox.Focus(mux::FocusState::Programmatic);
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

    // Charts: {tracks,albums,artists,playlists} -> the dedicated sectioned page.
    fire_and_forget LoadCharts() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        hstring json = TakeJson(DZChartsJSON());
        auto tracks    = ParseTracks(json);
        auto albums    = ParseAlbums(json);
        auto artists   = ParseArtists(json);
        auto playlists = ParsePlaylists(json);
        co_await resume_foreground(m_win.DispatcherQueue());
        m_chartsTracks    = std::move(tracks);
        m_chartsAlbums    = std::move(albums);
        m_chartsArtists   = std::move(artists);
        m_chartsPlaylists = std::move(playlists);
        ++m_artGen;
        FillTrackList(m_chartsTrackList, m_chartsTracks);
        FillTileGrid(m_chartsAlbumsGrid,    m_chartsAlbums);
        FillArtistTiles(m_chartsArtistsGrid, m_chartsArtists);
        FillPlaylistTiles(m_chartsPlaylistsGrid, m_chartsPlaylists);
        try { m_chartsScroll.ChangeView(nullptr, Ref(0.0), nullptr); } catch (...) {}
    }

    // Flow: the personalized stream -> the shared track list, then auto-play head.
    fire_and_forget LoadFlow() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto tracks = ParseTracks(TakeJson(DZFlowJSON())); // {"tracks":[...]}
        co_await resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
        if (!m_tracks.empty()) PlayFrom(m_tracks, 0);
    }

    // Generic tile fillers (Tag = index into the matching vector).
    void FillTileGrid(muxc::GridView const& grid, std::vector<Album> const& albums) {
        grid.Items().Clear();
        int i = 0;
        for (auto const& a : albums) { grid.Items().Append(MakeTile(a.name, a.artistLine, a.artworkUrl, i)); ++i; }
    }
    void FillArtistTiles(muxc::GridView const& grid, std::vector<ArtistInfo> const& artists) {
        grid.Items().Clear();
        int i = 0;
        for (auto const& a : artists) { grid.Items().Append(MakeTile(a.name, FansText(a.nbFans), a.artworkUrl, i)); ++i; }
    }
    void FillPlaylistTiles(muxc::GridView const& grid, std::vector<Playlist> const& plists) {
        grid.Items().Clear();
        int i = 0;
        for (auto const& p : plists) { grid.Items().Append(MakeTile(p.name, p.owner, p.artworkUrl, i)); ++i; }
    }

    // ---- charts activation --------------------------------------------------
    void OnChartsTrackClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_chartsTracks, i);
    }
    void OnChartsAlbumClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_chartsAlbums.size())) OpenAlbum(m_chartsAlbums[i]);
    }
    void OnChartsArtistClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_chartsArtists.size())) OpenArtist(m_chartsArtists[i].id);
    }
    void OnChartsPlaylistClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_chartsPlaylists.size())) OpenPlaylist(m_chartsPlaylists[i]);
    }

    // ---- podcasts -----------------------------------------------------------
    void OnPodcastSearchClick(wf::IInspectable const&, mux::RoutedEventArgs const&) { RunPodcastSearch(); }
    void OnPodcastKey(wf::IInspectable const&, muxin::KeyRoutedEventArgs const& e) {
        if (e.Key() == wsys::VirtualKey::Enter) RunPodcastSearch();
    }
    fire_and_forget RunPodcastSearch() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        auto q = m_podcastBox.Text();
        if (q.empty()) co_return;
        co_await winrt::resume_background();
        std::string s = to_string(q);
        auto pods = ParsePodcasts(TakeJson(DZSearchPodcastsJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        m_podcasts = std::move(pods);
        ++m_artGen;
        m_podcastGrid.Items().Clear();
        int i = 0;
        for (auto const& p : m_podcasts) {
            hstring sub = p.episodeCount > 0 ? to_hstring(p.episodeCount) + L" episodes" : p.description;
            m_podcastGrid.Items().Append(MakeTile(p.name, sub, p.artworkUrl, i));
            ++i;
        }
    }
    void OnPodcastClick(wf::IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_podcasts.size())) OpenPodcast(m_podcasts[i]);
    }
    // Episodes load into the shared track list as isEpisode tracks; clicking a row
    // plays through the episode (plain-stream) path via the unified queue.
    fire_and_forget OpenPodcast(Podcast pod) {
        auto strong = get_strong();
        m_lyricsShown = false;
        m_nav.Header(box_value(pod.name));
        m_nav.Content(m_tracksPage);
        co_await winrt::resume_background();
        std::string s = to_string(pod.id);
        auto eps = ParseEpisodes(TakeJson(DZPodcastEpisodesJSON(s.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        std::vector<Track> tracks;
        tracks.reserve(eps.size());
        for (auto const& e : eps) {
            Track t;
            t.id         = e.id;
            t.name       = e.title;
            t.artistLine = pod.name;
            t.albumName  = pod.name;
            t.artworkUrl = e.artworkUrl.empty() ? pod.artworkUrl : e.artworkUrl;
            t.durationMs = e.durationMs;
            t.isEpisode  = true;
            tracks.push_back(std::move(t));
        }
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
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

    // ---- library mutations: like / add-to-playlist / playlist CRUD ----------
    // No "is-liked" query exists, so the heart is a local toggle: checked -> add,
    // unchecked -> remove. It resets to off on every track change (SetNowPlaying).
    void OnLike(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        if (m_suppressLike) return; // programmatic reset, not a user click
        hstring id = CurrentTrackId();
        bool want = m_likeBtn.IsChecked() && m_likeBtn.IsChecked().Value();
        if (id.empty()) { m_suppressLike = true; m_likeBtn.IsChecked(false); m_suppressLike = false; return; }
        DispatchLike(id, want);
    }
    fire_and_forget DispatchLike(hstring id, bool like) {
        auto strong = get_strong();
        co_await winrt::resume_background();
        std::string s = to_string(id);
        if (like) DZAddFavorite(s.data()); else DZRemoveFavorite(s.data());
    }
    void OnRowLike(wf::IInspectable const& sender, mux::RoutedEventArgs const&) {
        auto fe = sender.try_as<mux::FrameworkElement>(); if (!fe) return;
        hstring id = unbox_value_or<hstring>(fe.Tag(), L"");
        if (!id.empty()) DispatchLike(id, true); // row context menu is a one-shot like
    }
    void OnRowAddToPlaylist(wf::IInspectable const& sender, mux::RoutedEventArgs const&) {
        auto fe = sender.try_as<mux::FrameworkElement>(); if (!fe) return;
        hstring id = unbox_value_or<hstring>(fe.Tag(), L"");
        if (!id.empty()) ShowAddToPlaylist(id);
    }
    void OnAddCurrentToPlaylist(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        hstring id = CurrentTrackId();
        if (id.empty()) { ShowMessage(L"No track", L"Start playing a track to add it to a playlist."); return; }
        ShowAddToPlaylist(id);
    }

    // Picker: "New playlist…" + the user's playlists. A selection adds the track;
    // "New playlist…" prompts for a name, creates it, then adds the track.
    fire_and_forget ShowAddToPlaylist(hstring trackId) {
        auto strong = get_strong();
        if (!m_loggedIn || trackId.empty()) co_return;
        co_await winrt::resume_background();
        auto plists = ParsePlaylists(TakeJson(DZPlaylistsJSON()));
        co_await resume_foreground(m_win.DispatcherQueue());

        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"Add to playlist"));
        muxc::ListView list; list.SelectionMode(muxc::ListViewSelectionMode::Single);
        list.MaxHeight(360); list.MinWidth(320);
        { muxc::TextBlock tb; tb.Text(L"＋  New playlist…"); list.Items().Append(tb); } // index 0
        for (auto const& p : plists) { muxc::TextBlock tb; tb.Text(p.name); list.Items().Append(tb); }
        list.SelectedIndex(plists.empty() ? 0 : 1);
        dlg.Content(list);
        dlg.PrimaryButtonText(L"Add");
        dlg.CloseButtonText(L"Cancel");
        dlg.DefaultButton(muxc::ContentDialogButton::Primary);
        auto res = co_await dlg.ShowAsync();
        if (res != muxc::ContentDialogResult::Primary) co_return;

        int idx = list.SelectedIndex();
        if (idx < 0) co_return;
        hstring playlistId;
        if (idx == 0) {                                  // New playlist…
            hstring name = co_await PromptText(L"New playlist", L"Playlist name", L"");
            std::wstring w{ name.c_str() }; Trim(w);
            if (w.empty()) co_return;
            hstring title{ w };
            co_await winrt::resume_background();
            std::string ts = to_string(title);
            playlistId = ParseCreatedId(TakeJson(DZCreatePlaylist(ts.data())));
            co_await resume_foreground(m_win.DispatcherQueue());
            if (playlistId.empty()) { ShowMessage(L"Couldn't create playlist", L"The playlist could not be created."); co_return; }
        } else {
            int pi = idx - 1;
            if (pi < 0 || pi >= static_cast<int>(plists.size())) co_return;
            playlistId = plists[pi].id;
        }
        if (playlistId.empty()) co_return;
        co_await winrt::resume_background();
        std::string ps = to_string(playlistId), tk = to_string(trackId);
        int ok = DZAddToPlaylist(ps.data(), tk.data());
        co_await resume_foreground(m_win.DispatcherQueue());
        if (!ok) ShowMessage(L"Couldn't add to playlist", L"The track could not be added.");
    }

    fire_and_forget OnNewPlaylist(wf::IInspectable const&, mux::RoutedEventArgs const&) {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        hstring name = co_await PromptText(L"New playlist", L"Playlist name", L"");
        std::wstring w{ name.c_str() }; Trim(w);
        if (w.empty()) co_return;
        hstring title{ w };
        co_await winrt::resume_background();
        std::string ts = to_string(title);
        hstring newId = ParseCreatedId(TakeJson(DZCreatePlaylist(ts.data())));
        co_await resume_foreground(m_win.DispatcherQueue());
        if (newId.empty()) { ShowMessage(L"Couldn't create playlist", L"The playlist could not be created."); co_return; }
        LoadPlaylists();                                 // refresh the grid
    }

    fire_and_forget OnPlaylistRename(wf::IInspectable const& sender, mux::RoutedEventArgs const&) {
        auto strong = get_strong();
        auto fe = sender.try_as<mux::FrameworkElement>(); if (!fe) co_return;
        int i = unbox_value_or<int>(fe.Tag(), -1);
        if (i < 0 || i >= static_cast<int>(m_playlists.size())) co_return;
        Playlist p = m_playlists[i];
        hstring name = co_await PromptText(L"Rename playlist", L"Playlist name", p.name);
        std::wstring w{ name.c_str() }; Trim(w);
        if (w.empty()) co_return;
        hstring title{ w };
        co_await winrt::resume_background();
        std::string ps = to_string(p.id), ts = to_string(title);
        int ok = DZRenamePlaylist(ps.data(), ts.data());
        co_await resume_foreground(m_win.DispatcherQueue());
        if (!ok) { ShowMessage(L"Couldn't rename", L"The playlist could not be renamed."); co_return; }
        LoadPlaylists();
    }

    fire_and_forget OnPlaylistDelete(wf::IInspectable const& sender, mux::RoutedEventArgs const&) {
        auto strong = get_strong();
        auto fe = sender.try_as<mux::FrameworkElement>(); if (!fe) co_return;
        int i = unbox_value_or<int>(fe.Tag(), -1);
        if (i < 0 || i >= static_cast<int>(m_playlists.size())) co_return;
        Playlist p = m_playlists[i];
        bool yes = co_await Confirm(L"Delete playlist",
            L"Delete “" + p.name + L"”? This can't be undone.", L"Delete");
        if (!yes) co_return;
        co_await winrt::resume_background();
        std::string ps = to_string(p.id);
        int ok = DZDeletePlaylist(ps.data());
        co_await resume_foreground(m_win.DispatcherQueue());
        if (!ok) { ShowMessage(L"Couldn't delete", L"The playlist could not be deleted."); co_return; }
        LoadPlaylists();
    }

    // Small reusable modal helpers (single-line text entry + yes/no confirm).
    wf::IAsyncOperation<hstring> PromptText(hstring title, hstring placeholder, hstring initial) {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(title));
        muxc::TextBox tb; tb.PlaceholderText(placeholder); tb.Text(initial); tb.AcceptsReturn(false);
        dlg.Content(tb);
        dlg.PrimaryButtonText(L"OK");
        dlg.CloseButtonText(L"Cancel");
        dlg.DefaultButton(muxc::ContentDialogButton::Primary);
        auto res = co_await dlg.ShowAsync();
        co_return res == muxc::ContentDialogResult::Primary ? tb.Text() : hstring(L"");
    }
    wf::IAsyncOperation<bool> Confirm(hstring title, hstring body, hstring okText) {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(title));
        muxc::TextBlock t; t.Text(body); t.TextWrapping(mux::TextWrapping::Wrap);
        dlg.Content(t);
        dlg.PrimaryButtonText(okText);
        dlg.CloseButtonText(L"Cancel");
        dlg.DefaultButton(muxc::ContentDialogButton::Close);
        auto res = co_await dlg.ShowAsync();
        co_return res == muxc::ContentDialogResult::Primary;
    }

    // ---- playback -----------------------------------------------------------
    void PlayFrom(std::vector<Track> const& list, int index) {
        if (m_blocked) return; // Free account: playback gated
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
        // Gapless: warm the deterministic next track so the engine can swap to it
        // when this one ends (only for real tracks, never shuffle / repeat-one).
        hstring nextId; int64_t nextDur = 0;
        int n;
        if (m_settings.gapless && !t.isEpisode && HasDeterministicNext(&n)) {
            nextId = m_queue[n].id; nextDur = m_queue[n].durationMs;
        }
        DispatchPlay(t.id, t.durationMs, t.isEpisode, nextId, nextDur);
    }
    // Blocking play (then optional preload) serialized on one background task so
    // the preload warms only after the current stream is prepared.
    fire_and_forget DispatchPlay(hstring id, int64_t dur, bool episode, hstring nextId, int64_t nextDur) {
        auto strong = get_strong();
        co_await winrt::resume_background();
        std::string s = to_string(id);
        if (episode) DZPlayEpisode(s.data(), dur); // plain, unencrypted stream
        else         DZPlay(s.data(), dur);        // prepares the stream over the network -> blocks
        if (!nextId.empty()) {
            std::string ns = to_string(nextId);
            DZPreload(ns.data(), nextDur);         // warm next for the gapless swap
        }
    }
    fire_and_forget DispatchPreload(hstring id, int64_t dur) {
        auto strong = get_strong();
        co_await winrt::resume_background();
        std::string s = to_string(id);
        DZPreload(s.data(), dur);
    }
    // The next queue index when advance is deterministic (not shuffle, not
    // repeat-one, next exists and is a real track). Mirrors Next()'s ordering.
    bool HasDeterministicNext(int* out) {
        if (m_shuffle || m_repeat == 2 || m_queue.empty()) return false;
        int n;
        if (m_queueIndex + 1 < static_cast<int>(m_queue.size())) n = m_queueIndex + 1;
        else if (m_repeat == 1)                                  n = 0;
        else                                                     return false;
        if (n < 0 || n >= static_cast<int>(m_queue.size())) return false;
        if (m_queue[n].isEpisode) return false; // episodes don't use the preload swap
        *out = n;
        return true;
    }
    // Engine already gaplessly swapped to the preloaded next: advance the UI's
    // queue pointer + now-playing WITHOUT re-issuing play, then warm the new next.
    void AdvanceUiToPreloaded(int n) {
        m_queueIndex = n;
        Track t = m_queue[m_queueIndex];
        SetNowPlaying(t);
        m_updatingSeek = true;
        m_seek.Maximum(static_cast<double>(t.durationMs > 0 ? t.durationMs : 1));
        m_seek.Value(0);
        m_updatingSeek = false;
        m_posText.Text(TimeText(0));
        m_durText.Text(TimeText(t.durationMs));
        int n2;
        if (m_settings.gapless && !t.isEpisode && HasDeterministicNext(&n2))
            DispatchPreload(m_queue[n2].id, m_queue[n2].durationMs);
    }
    void SetNowPlaying(Track const& t) {
        // Prefix the now-playing title with the enclosed-E glyph for explicit tracks.
        m_nowTitle.Text(t.isExplicit ? hstring(L"\U0001F174 " + t.name) : t.name);
        m_curArtist = t.artistLine;
        m_nowArtist.Text(t.artistLine);
        m_cover.Source(nullptr);
        int token = ++m_playGen;
        if (!t.artworkUrl.empty()) LoadArt(m_cover, t.artworkUrl, token, true);
        // No "is-liked" query exists; reset the heart to off on every track change.
        // Like / add-to-playlist are library-track only, so disable them for episodes.
        if (m_likeBtn) {
            m_suppressLike = true; m_likeBtn.IsChecked(false); m_suppressLike = false;
            m_likeBtn.IsEnabled(!t.isEpisode);
        }
        if (m_addBtn) m_addBtn.IsEnabled(!t.isEpisode);
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
        if (f != m_lastFinished) {
            m_lastFinished = f;
            int n;
            if (m_repeat == 2) {
                PlayCurrent();                                    // repeat-one
            } else if (m_settings.gapless && st == 2 && HasDeterministicNext(&n)) {
                // The engine kept playing -> it already swapped to the preloaded
                // next track. Advance the UI pointer only (no second DZPlay).
                AdvanceUiToPreloaded(n);
            } else {
                Next();                                           // normal advance / restart
            }
        }
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
            m_settings.gapless = o.GetNamedBoolean(L"gapless", m_settings.gapless);
            m_settings.crossfadeMs = (int)o.GetNamedNumber(L"crossfadeMs", (double)m_settings.crossfadeMs);
            m_settings.audioDevice = o.GetNamedString(L"audioDevice", m_settings.audioDevice);
        }
    }

    void SaveSettings() {
        auto dir = ConfigDir(); if (dir.empty()) return;
        CreateDirectoryW(dir.c_str(), nullptr);
        wdj::JsonObject o;
        o.SetNamedValue(L"quality", wdj::JsonValue::CreateNumberValue(m_settings.quality));
        o.SetNamedValue(L"closeToTray", wdj::JsonValue::CreateBooleanValue(m_settings.closeToTray));
        o.SetNamedValue(L"replayGain", wdj::JsonValue::CreateBooleanValue(m_settings.replayGain));
        o.SetNamedValue(L"gapless", wdj::JsonValue::CreateBooleanValue(m_settings.gapless));
        o.SetNamedValue(L"crossfadeMs", wdj::JsonValue::CreateNumberValue(m_settings.crossfadeMs));
        o.SetNamedValue(L"audioDevice", wdj::JsonValue::CreateStringValue(m_settings.audioDevice));
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
        // Output devices + current engine audio state are read off the UI thread.
        co_await winrt::resume_background();
        hstring devJson = TakeJson(DZAudioDevicesJSON());
        hstring curDev;
        if (char* cd = DZCurrentAudioDevice()) { curDev = to_hstring(std::string(cd)); DZFree(cd); }
        bool curGapless  = DZGapless() != 0;
        int  curCrossfade = DZCrossfadeMS();
        co_await resume_foreground(m_win.DispatcherQueue());
        auto devices = ParseDevices(devJson);

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

        // Output device (id "" = system default).
        muxc::StackPanel asec; asec.Spacing(4);
        muxc::TextBlock ah; ah.Text(L"Output device"); ah.FontWeight(wut::FontWeights::SemiBold());
        muxc::ComboBox devCombo; devCombo.HorizontalAlignment(mux::HorizontalAlignment::Stretch);
        int selDev = 0;
        for (size_t i = 0; i < devices.size(); ++i) {
            hstring label = devices[i].name.empty() ? hstring(L"System default") : devices[i].name;
            if (devices[i].isDefault) label = label + L"  (default)";
            devCombo.Items().Append(box_value(label));
            if (devices[i].id == curDev) selDev = static_cast<int>(i);
        }
        if (!devices.empty()) devCombo.SelectedIndex(selDev);
        else { devCombo.IsEnabled(false); devCombo.PlaceholderText(L"No output devices"); }
        asec.Children().Append(ah); asec.Children().Append(devCombo);

        // Gapless playback (engine state; persisted so it survives relaunch).
        muxc::StackPanel gsec; gsec.Spacing(4);
        muxc::TextBlock gh; gh.Text(L"Gapless playback"); gh.FontWeight(wut::FontWeights::SemiBold());
        muxc::ToggleSwitch gapSwitch;
        gapSwitch.OnContent(box_value(L"Play consecutive tracks with no silence"));
        gapSwitch.OffContent(box_value(L"Brief gap between tracks"));
        gapSwitch.IsOn(curGapless);
        gsec.Children().Append(gh); gsec.Children().Append(gapSwitch);

        // Crossfade duration (0 / 3 / 6 / 12 s).
        static const int kCfVals[4] = { 0, 3000, 6000, 12000 };
        muxc::StackPanel csec; csec.Spacing(4);
        muxc::TextBlock ch; ch.Text(L"Crossfade"); ch.FontWeight(wut::FontWeights::SemiBold());
        muxc::ComboBox cfCombo; cfCombo.HorizontalAlignment(mux::HorizontalAlignment::Stretch);
        cfCombo.Items().Append(box_value(L"Off"));
        cfCombo.Items().Append(box_value(L"3 seconds"));
        cfCombo.Items().Append(box_value(L"6 seconds"));
        cfCombo.Items().Append(box_value(L"12 seconds"));
        int cfIdx = 0;
        for (int i = 3; i >= 0; --i) { if (curCrossfade >= kCfVals[i]) { cfIdx = i; break; } }
        cfCombo.SelectedIndex(cfIdx);
        csec.Children().Append(ch); csec.Children().Append(cfCombo);

        // Background / close-to-tray
        muxc::StackPanel tsec; tsec.Spacing(4);
        muxc::TextBlock th; th.Text(L"Background playback"); th.FontWeight(wut::FontWeights::SemiBold());
        muxc::ToggleSwitch tray;
        tray.OnContent(box_value(L"Closing the window keeps playing in the tray"));
        tray.OffContent(box_value(L"Closing the window quits OpenDeezer"));
        tray.IsOn(m_settings.closeToTray);
        tsec.Children().Append(th); tsec.Children().Append(tray);

        sp.Children().Append(qsec);
        sp.Children().Append(asec);
        sp.Children().Append(gsec);
        sp.Children().Append(csec);
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

            // Output device
            int di = devCombo.SelectedIndex();
            if (di >= 0 && di < static_cast<int>(devices.size())) {
                m_settings.audioDevice = devices[di].id;
                std::string dev = to_string(devices[di].id);
                DZSetAudioDevice(dev.data());
            }
            // Gapless
            m_settings.gapless = gapSwitch.IsOn();
            DZSetGapless(m_settings.gapless ? 1 : 0);
            // Crossfade
            int ci = cfCombo.SelectedIndex(); if (ci < 0 || ci > 3) ci = 0;
            m_settings.crossfadeMs = kCfVals[ci];
            DZSetCrossfadeMS(m_settings.crossfadeMs);

            SaveSettings();
        }
    }

    fire_and_forget ShowAbout() {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"About OpenDeezer"));
        muxc::StackPanel sp; sp.Spacing(8);
        muxc::TextBlock h; h.Text(L"OpenDeezer 0.6.0"); h.FontSize(22); h.FontWeight(wut::FontWeights::SemiBold());
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
                             m_chartsItem{ nullptr }, m_flowItem{ nullptr }, m_podcastsItem{ nullptr },
                             m_accountItem{ nullptr }, m_settingsItem{ nullptr }, m_aboutItem{ nullptr },
                             m_lastContentItem{ nullptr };

    mux::UIElement m_tracksPage{ nullptr }, m_playlistsPage{ nullptr }, m_searchPage{ nullptr };
    muxc::ListView m_trackList{ nullptr }, m_searchTrackList{ nullptr };
    muxc::GridView m_playlistGrid{ nullptr }, m_searchGrid{ nullptr };
    muxc::TextBox  m_searchBox{ nullptr };

    // ---- charts page (sectioned: tracks / albums / artists / playlists) ------
    mux::UIElement     m_chartsPage{ nullptr };
    muxc::ScrollViewer m_chartsScroll{ nullptr };
    muxc::ListView     m_chartsTrackList{ nullptr };
    muxc::GridView     m_chartsAlbumsGrid{ nullptr }, m_chartsArtistsGrid{ nullptr }, m_chartsPlaylistsGrid{ nullptr };
    std::vector<Track>      m_chartsTracks;
    std::vector<Album>      m_chartsAlbums;
    std::vector<ArtistInfo> m_chartsArtists;
    std::vector<Playlist>   m_chartsPlaylists;

    // ---- podcasts page -------------------------------------------------------
    mux::UIElement m_podcastPage{ nullptr };
    muxc::TextBox  m_podcastBox{ nullptr };
    muxc::GridView m_podcastGrid{ nullptr };
    std::vector<Podcast> m_podcasts;

    muxc::Image     m_cover{ nullptr };
    muxc::TextBlock m_nowTitle{ nullptr }, m_nowArtist{ nullptr }, m_posText{ nullptr }, m_durText{ nullptr };
    hstring m_curArtist; // base artist line; format badge is appended each tick
    muxc::Slider    m_seek{ nullptr }, m_volume{ nullptr };
    muxc::Button    m_playBtn{ nullptr }, m_repeatBtn{ nullptr }, m_addBtn{ nullptr };
    muxc::FontIcon  m_playIcon{ nullptr };
    muxp::ToggleButton m_shuffleBtn{ nullptr }, m_likeBtn{ nullptr };
    muxc::HyperlinkButton m_lyricsBtn{ nullptr }, m_artistBtn{ nullptr }; // -> lyrics / artist views
    bool m_suppressLike = false; // guard programmatic heart resets from OnLike

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

    bool m_loggedIn = false, m_blocked = false, m_shuffle = false, m_updatingSeek = false, m_updatingVol = false, m_suppressNav = false;
    int  m_lastFinished = 0, m_artGen = 0, m_playGen = 0, m_queueIndex = -1, m_repeat = 0;
    std::chrono::steady_clock::time_point m_lastSeek{};

    // ---- login (embedded Deezer webview + automatic arl-cookie capture) ------
    muxc::WebView2            m_loginWebView{ nullptr };
    muxc::ContentDialog       m_loginDialog{ nullptr };
    mud::DispatcherQueueTimer m_arlPollTimer{ nullptr };
    hstring m_capturedArl;             // set by OnArlPoll when the arl cookie appears
    bool    m_arlPollBusy = false;     // guards overlapping GetCookiesAsync polls

    // OS integration state
    Settings m_settings{};
    Account  m_account{};   // cached signed-in tier (filled by FinishLogin after login)
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
