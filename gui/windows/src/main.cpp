// OpenDeezer - native Windows front-end (WinUI 3, C++/WinRT, Fluent).
//
// The whole engine (login, browse, Blowfish decrypt, MP3 decode, WASAPI
// playback) is the Go core compiled to a C-ABI shared library
// (lib/libdeezercore.dll) and called in-process over extern "C". This file is
// UI only: an entirely code-built NavigationView + track ListView + playlist /
// search grids + a bottom now-playing transport bar + an About dialog. No XAML
// markup, no .idl, no markup compiler -- the App subclass implements
// IXamlMetadataProvider so default control themes resolve.
//
// Every blocking DZ* call (DZInit / browse / DZPlay / DZFetch) runs on a
// background thread via winrt::resume_background and is marshalled back to the
// UI thread with winrt::resume_foreground(DispatcherQueue). A single 300 ms
// DispatcherQueueTimer polls cheap player state and auto-advances when
// DZFinishedCount() increments.

#include <windows.h>
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

#include <string>
#include <vector>
#include <functional>
#include <chrono>
#include <cmath>
#include <fstream>
#include <iterator>

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
namespace wdj   = winrt::Windows::Data::Json;
namespace wss   = winrt::Windows::Storage::Streams;
namespace wut   = winrt::Windows::UI::Text;
namespace wsys  = winrt::Windows::System;
using IInspectable = winrt::Windows::Foundation::IInspectable;
using winrt::box_value;
using winrt::unbox_value_or;
using winrt::hstring;
using winrt::to_hstring;
using winrt::to_string;
using winrt::fire_and_forget;

// ---- wire models (mirror corelib jTrack/jAlbum/jPlaylist) -------------------
struct Track    { hstring id, name, artistLine, albumName, artworkUrl; int64_t durationMs = 0; };
struct Album    { hstring id, name, artistLine, artworkUrl; };
struct Playlist { hstring id, name, owner, artworkUrl; int trackCount = 0; };

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

static std::vector<Track> ParseTracks(hstring const& json) {
    std::vector<Track> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"tracks", wdj::JsonArray{})) {
        auto o = v.GetObject();
        Track t;
        t.id         = o.GetNamedString(L"id", L"");
        t.name       = o.GetNamedString(L"name", L"");
        t.durationMs = static_cast<int64_t>(o.GetNamedNumber(L"durationMs", 0));
        t.artistLine = o.GetNamedString(L"artistLine", L"");
        t.albumName  = o.GetNamedString(L"albumName", L"");
        t.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
        out.push_back(std::move(t));
    }
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

static std::vector<Album> ParseAlbums(hstring const& json) {
    std::vector<Album> out;
    wdj::JsonObject obj{ nullptr };
    if (!wdj::JsonObject::TryParse(json, obj)) return out;
    for (auto const& v : obj.GetNamedArray(L"albums", wdj::JsonArray{})) {
        auto o = v.GetObject();
        Album a;
        a.id         = o.GetNamedString(L"id", L"");
        a.name       = o.GetNamedString(L"name", L"");
        a.artworkUrl = o.GetNamedString(L"artworkUrl", L"");
        auto artists = o.GetNamedArray(L"artists", wdj::JsonArray{});
        if (artists.Size() > 0) a.artistLine = artists.GetObjectAt(0).GetNamedString(L"name", L"");
        out.push_back(std::move(a));
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
struct MainWindow : winrt::implements<MainWindow, IInspectable> {
    MainWindow() { BuildUi(); }

    void Activate() {
        m_win.Activate();
        // The poll timer + login both need the live DispatcherQueue / XamlRoot,
        // so they start after the window is up.
        m_timer = m_win.DispatcherQueue().CreateTimer();
        m_timer.Interval(std::chrono::milliseconds(300));
        m_timer.Tick({ get_weak(), &MainWindow::OnTick });
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
        m_searchItem    = NavItem(L"Search",      muxc::Symbol::Find,  L"search");
        m_nav.MenuItems().Append(m_likedItem);
        m_nav.MenuItems().Append(m_playlistsItem);
        m_nav.MenuItems().Append(m_searchItem);

        m_aboutItem = NavItem(L"About", muxc::Symbol::Help, L"about");
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
        muxc::Grid::SetColumn(now, 1); bar.Children().Append(now);

        muxc::StackPanel tr; tr.Orientation(muxc::Orientation::Horizontal); tr.Spacing(4); tr.VerticalAlignment(mux::VerticalAlignment::Center);
        auto glyphBtn = [](hstring g) { muxc::Button b; muxc::FontIcon fi; fi.Glyph(g); b.Content(fi); return b; };
        auto prevBtn = glyphBtn(L""); prevBtn.Click({ get_weak(), &MainWindow::OnPrev });
        m_playBtn = muxc::Button(); m_playIcon = muxc::FontIcon(); m_playIcon.Glyph(L""); m_playBtn.Content(m_playIcon);
        m_playBtn.Foreground(m_accent); m_playBtn.Click({ get_weak(), &MainWindow::OnPlayPause });
        auto nextBtn = glyphBtn(L""); nextBtn.Click({ get_weak(), &MainWindow::OnNext });
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
        m_shuffleBtn = muxp::ToggleButton(); { muxc::FontIcon fi; fi.Glyph(L""); m_shuffleBtn.Content(fi); }
        m_shuffleBtn.Click({ get_weak(), &MainWindow::OnShuffle });
        m_repeatBtn = muxc::Button(); m_repeatBtn.Content(box_value(L"Repeat: Off"));
        m_repeatBtn.Click({ get_weak(), &MainWindow::OnRepeat });
        modes.Children().Append(m_shuffleBtn); modes.Children().Append(m_repeatBtn);
        muxc::Grid::SetColumn(modes, 6); bar.Children().Append(modes);

        muxc::StackPanel vol; vol.Orientation(muxc::Orientation::Horizontal); vol.Spacing(6); vol.VerticalAlignment(mux::VerticalAlignment::Center);
        { muxc::FontIcon sp; sp.Glyph(L""); sp.VerticalAlignment(mux::VerticalAlignment::Center); vol.Children().Append(sp); }
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
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
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
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        if (ok) {
            m_loggedIn = true;
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
        if (tag == L"about") {
            ShowAbout();
            m_suppressNav = true;
            nav.SelectedItem(m_lastContentItem ? m_lastContentItem : m_likedItem);
            m_suppressNav = false;
            return;
        }
        m_lastContentItem = item;
        if (tag == L"liked") {
            nav.Header(box_value(L"Liked Songs")); nav.Content(m_tracksPage); LoadFavorites();
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
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    fire_and_forget LoadPlaylists() {
        auto strong = get_strong();
        if (!m_loggedIn) co_return;
        co_await winrt::resume_background();
        auto ps = ParsePlaylists(TakeJson(DZPlaylistsJSON()));
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        m_playlists = std::move(ps);
        ++m_artGen;
        FillPlaylistGrid();
    }

    fire_and_forget OpenPlaylist(Playlist p) {
        auto strong = get_strong();
        m_nav.Header(box_value(p.name));
        m_nav.Content(m_tracksPage);
        co_await winrt::resume_background();
        std::string s = to_string(p.id);
        auto tracks = ParseTracks(TakeJson(DZPlaylistTracksJSON(s.data())));
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
    }

    fire_and_forget OpenAlbum(Album a) {
        auto strong = get_strong();
        m_nav.Header(box_value(a.name));
        m_nav.Content(m_tracksPage);
        co_await winrt::resume_background();
        std::string s = to_string(a.id);
        auto tracks = ParseTracks(TakeJson(DZAlbumTracksJSON(s.data())));
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        m_tracks = std::move(tracks);
        ++m_artGen;
        FillTrackList(m_trackList, m_tracks);
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
        co_await winrt::resume_foreground(m_win.DispatcherQueue());
        m_searchTracks = std::move(tracks);
        m_searchAlbums = std::move(albums);
        m_searchPlaylists = std::move(plists);
        ++m_artGen;
        FillTrackList(m_searchTrackList, m_searchTracks);
        FillSearchGrid();
    }

    // ---- item activation ----------------------------------------------------
    static int TagIndex(IInspectable const& clicked) {
        auto fe = clicked.try_as<mux::FrameworkElement>();
        return fe ? unbox_value_or<int>(fe.Tag(), -1) : -1;
    }
    void OnTrackClick(IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_tracks, i);
    }
    void OnSearchTrackClick(IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem()); if (i >= 0) PlayFrom(m_searchTracks, i);
    }
    void OnPlaylistClick(IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_playlists.size())) OpenPlaylist(m_playlists[i]);
    }
    void OnSearchGridClick(IInspectable const&, muxc::ItemClickEventArgs const& e) {
        int i = TagIndex(e.ClickedItem());
        if (i >= 0 && i < static_cast<int>(m_searchActions.size())) m_searchActions[i]();
    }
    void OnSearchClick(IInspectable const&, mux::RoutedEventArgs const&) { RunSearch(); }
    void OnSearchKey(IInspectable const&, muxin::KeyRoutedEventArgs const& e) {
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
        m_nowArtist.Text(t.artistLine);
        m_cover.Source(nullptr);
        int token = ++m_playGen;
        if (!t.artworkUrl.empty()) LoadArt(m_cover, t.artworkUrl, token, true);
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
    void OnPlayPause(IInspectable const&, mux::RoutedEventArgs const&) { DZTogglePause(); }
    void OnPrev(IInspectable const&, mux::RoutedEventArgs const&) { Prev(); }
    void OnNext(IInspectable const&, mux::RoutedEventArgs const&) { Next(); }
    void OnShuffle(IInspectable const&, mux::RoutedEventArgs const&) {
        auto r = m_shuffleBtn.IsChecked(); m_shuffle = r && r.Value();
    }
    void OnRepeat(IInspectable const&, mux::RoutedEventArgs const&) {
        m_repeat = (m_repeat + 1) % 3;
        m_repeatBtn.Content(box_value(m_repeat == 0 ? L"Repeat: Off" : m_repeat == 1 ? L"Repeat: All" : L"Repeat: One"));
    }
    void OnSeekChanged(IInspectable const&, muxp::RangeBaseValueChangedEventArgs const& e) {
        if (m_updatingSeek) return; // programmatic update from the poll tick
        int64_t ms = static_cast<int64_t>(llround(e.NewValue()));
        DZSeek(ms);
        m_posText.Text(TimeText(ms));
        m_lastSeek = std::chrono::steady_clock::now();
    }
    void OnVolumeChanged(IInspectable const&, muxp::RangeBaseValueChangedEventArgs const& e) {
        if (m_updatingVol) return;
        DZSetVolume(e.NewValue() / 100.0);
    }

    // ---- 300 ms poll: cheap state reads + auto-advance ----------------------
    void OnTick(mud::DispatcherQueueTimer const&, IInspectable const&) {
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
        m_playIcon.Glyph(st == 2 ? L"" : L""); // pause glyph while playing
        int f = DZFinishedCount();
        if (f != m_lastFinished) { m_lastFinished = f; if (m_repeat == 2) PlayCurrent(); else Next(); }
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

    fire_and_forget ShowAbout() {
        auto strong = get_strong();
        muxc::ContentDialog dlg;
        dlg.XamlRoot(m_win.Content().XamlRoot());
        dlg.Title(box_value(L"About OpenDeezer"));
        muxc::StackPanel sp; sp.Spacing(8);
        muxc::TextBlock h; h.Text(L"OpenDeezer"); h.FontSize(22); h.FontWeight(wut::FontWeights::SemiBold());
        h.Foreground(m_accent);
        muxc::TextBlock tag; tag.Text(L"An open source reimplementation of Deezer."); tag.TextWrapping(mux::TextWrapping::Wrap);
        muxc::TextBlock body; body.TextWrapping(mux::TextWrapping::Wrap);
        body.Text(L"Native Windows client (WinUI 3 · C++/WinRT · Fluent). The engine — login, browse, "
                  L"Blowfish decrypt, MP3 decode, WASAPI playback — is the Go core libdeezercore.dll, linked in-process.");
        muxc::TextBlock by; by.Text(L"By Cycl0o0. Licensed under AGPL-3.0."); by.Opacity(0.8);
        sp.Children().Append(h); sp.Children().Append(tag); sp.Children().Append(body); sp.Children().Append(by);
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
                             m_aboutItem{ nullptr }, m_lastContentItem{ nullptr };

    mux::UIElement m_tracksPage{ nullptr }, m_playlistsPage{ nullptr }, m_searchPage{ nullptr };
    muxc::ListView m_trackList{ nullptr }, m_searchTrackList{ nullptr };
    muxc::GridView m_playlistGrid{ nullptr }, m_searchGrid{ nullptr };
    muxc::TextBox  m_searchBox{ nullptr };

    muxc::Image     m_cover{ nullptr };
    muxc::TextBlock m_nowTitle{ nullptr }, m_nowArtist{ nullptr }, m_posText{ nullptr }, m_durText{ nullptr };
    muxc::Slider    m_seek{ nullptr }, m_volume{ nullptr };
    muxc::Button    m_playBtn{ nullptr }, m_repeatBtn{ nullptr };
    muxc::FontIcon  m_playIcon{ nullptr };
    muxp::ToggleButton m_shuffleBtn{ nullptr };

    std::vector<Track>    m_tracks, m_searchTracks, m_queue;
    std::vector<Playlist> m_playlists, m_searchPlaylists;
    std::vector<Album>    m_searchAlbums;
    std::vector<std::function<void()>> m_searchActions; // album/playlist tile -> open

    bool m_loggedIn = false, m_shuffle = false, m_updatingSeek = false, m_updatingVol = false, m_suppressNav = false;
    int  m_lastFinished = 0, m_artGen = 0, m_playGen = 0, m_queueIndex = -1, m_repeat = 0;
    std::chrono::steady_clock::time_point m_lastSeek{};
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
