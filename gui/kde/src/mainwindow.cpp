#include "mainwindow.h"
#include "logindialog.h"
#include "mpris.h"
#include "settingsdialog.h"

#include <QAction>
#include <QAbstractItemView>
#include <QApplication>
#include <QBrush>
#include <QCheckBox>
#include <QCloseEvent>
#include <QColor>
#include <QDesktopServices>
#include <QDialog>
#include <QDialogButtonBox>
#include <QDir>
#include <QDockWidget>
#include <QFile>
#include <QFileInfo>
#include <QFont>
#include <QFrame>
#include <QHBoxLayout>
#include <QHeaderView>
#include <QIcon>
#include <QImage>
#include <QInputDialog>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QKeySequence>
#include <QLabel>
#include <QLineEdit>
#include <QListView>
#include <QListWidget>
#include <QLocale>
#include <QMenu>
#include <QMenuBar>
#include <QMessageBox>
#include <QPainter>
#include <QPixmap>
#include <QProxyStyle>
#include <QPushButton>
#include <QRandomGenerator>
#include <QScrollArea>
#include <QSignalBlocker>
#include <QSlider>
#include <QSplitter>
#include <QStackedWidget>
#include <QStatusBar>
#include <QStringList>
#include <QStyle>
#include <QSystemTrayIcon>
#include <QTableWidget>
#include <QTableWidgetItem>
#include <QTime>
#include <QTimer>
#include <QToolButton>
#include <QUrl>
#include <QVBoxLayout>
#include <QtConcurrent>

// The Go engine's C API. Built by build.sh into lib/libdeezercore.{a,h}.
extern "C" {
#include "libdeezercore.h"
}

// Audio-quality controls. Declared here as well so the GUI still compiles
// against an older generated header; identical redeclarations are harmless.
extern "C" void DZSetQuality(int level); // 0=MP3_128, 1=MP3_320, 2=FLAC
extern "C" char *DZFormat(void);         // human label of the current stream
extern "C" int  DZHighQuality(void);

// v0.3 additions. Redeclared here (like the quality controls above) so the GUI
// still builds against an older generated header; identical redeclarations are
// harmless. All *JSON results are malloc'd C strings — free them with DZFree.
extern "C" char *DZAccountJSON(void);           // {userId,name,offer,canHq,canHifi,premium,loggedIn}
extern "C" char *DZChartsJSON(void);            // {tracks,albums,artists,playlists}
extern "C" char *DZArtistTopJSON(char *id);     // {tracks}
extern "C" char *DZArtistProfileJSON(char *id); // {artist,top,albums,related}
extern "C" char *DZLyricsJSON(char *trackID);   // {plain,synced:[{timeMs,text}],isSynced}
extern "C" void  DZSetReplayGain(int on);       // 1=on, 0=off
extern "C" int   DZReplayGain(void);            // 1=on, 0=off

// v0.4 additions. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclarations are
// harmless. *JSON results are malloc'd C strings — free with DZFree. Mutations
// return int (1 = ok, 0 = fail).
extern "C" int   DZAddFavorite(char *trackID);
extern "C" int   DZRemoveFavorite(char *trackID);
extern "C" int   DZAddToPlaylist(char *playlistID, char *trackID);
extern "C" int   DZRemoveFromPlaylist(char *playlistID, char *trackID);
extern "C" char *DZCreatePlaylist(char *title);            // {"id":"..."}
extern "C" int   DZRenamePlaylist(char *playlistID, char *title);
extern "C" int   DZDeletePlaylist(char *playlistID);
extern "C" char *DZFlowJSON(void);                         // {tracks:[...]}
extern "C" char *DZSearchPodcastsJSON(char *q);            // {podcasts:[...]}
extern "C" char *DZPodcastEpisodesJSON(char *podcastID);   // {episodes:[...]}
extern "C" int   DZPlayEpisode(char *episodeID, long long durationMs);
extern "C" char *DZAudioDevicesJSON(void);                 // {devices:[{id,name,isDefault}]}
extern "C" int   DZSetAudioDevice(char *id);               // "" = system default
extern "C" char *DZCurrentAudioDevice(void);               // malloc'd; free with DZFree
extern "C" void  DZSetGapless(int on);
extern "C" int   DZGapless(void);
extern "C" void  DZSetCrossfadeMS(int ms);
extern "C" int   DZCrossfadeMS(void);
extern "C" void  DZPreload(char *trackID, long long durationMs);

// Track downloads (premium-only). Redeclared here (like the blocks above) so
// the GUI still builds against an older generated header; identical
// redeclarations are harmless. DZDownloadTrack returns a malloc'd JSON string —
// {"path":"..."} on success, {"error":"..."} on failure — free with DZFree;
// pass "" for destDir to save into the shared default download folder.
// DZDownloadDir returns that folder (malloc'd — free with DZFree);
// DZSetDownloadDir sets it (1 = ok, 0 = fail). DZIsPreview reports whether the
// engine is currently streaming a 30-second preview (1 = yes, 0 = no).
extern "C" char *DZDownloadTrack(char *trackID, char *destDir);
extern "C" char *DZDownloadDir(void);
extern "C" int   DZSetDownloadDir(char *path);
extern "C" int   DZIsPreview(void);

// OpenDeezer Connect (LAN device transfer). Redeclared here (like the blocks
// above) so the GUI still builds against an older generated header; identical
// redeclarations are harmless. char* results are malloc'd — free with DZFree.
extern "C" char *DZDiscoverDevices(int timeoutMS); // JSON [{name,addr,client,version}]
extern "C" int   DZConnectDevice(char *addr);      // 1 ok, 0 fail; addr = host:port
extern "C" void  DZDisconnectDevice(void);         // return playback to this computer
extern "C" char *DZConnectedDevice(void);          // host:port ("" if local)
extern "C" void  DZSetClientInfo(char *client, char *device); // call BEFORE DZInit
// The track ACTUALLY playing, as the usual track JSON shape
// ({id,name,artistLine,albumName,artworkUrl,artistId,durationMs,explicit}); "{}"
// when nothing plays. Reflects tracks started on this device AND, when routed via
// OpenDeezer Connect, the remote device's current track. Free with DZFree.
extern "C" char *DZNowPlayingJSON(void);

// v1.0 additions. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclarations are harmless.
// Both forward to the connected remote via the control API when routed.
extern "C" void DZSetRepeat(int mode);   // 0=off, 1=all, 2=one
extern "C" void DZSetShuffle(int on);    // 1=on, 0=off

// Home aggregator. Returns {"topTracks":[jTrack],"topAlbums":[jAlbum],
// "playlists":[jPlaylist]}; best-effort (empty sections when no data).
// Redeclared here (like the blocks above) so the GUI still builds against an
// older generated header; identical redeclarations are harmless.
extern "C" char *DZHomeJSON(void);

// Phone Web Remote. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclarations are harmless.
// DZWebRemoteQRPNG returns raw PNG bytes — free with DZFreeBytes; NULL/0 when off.
extern "C" void           DZWebRemoteSetEnabled(int on);    // 1=enable (start LAN server), 0=disable
extern "C" char          *DZWebRemoteInfoJSON(void);        // {"enabled":bool,"code":"...","url":"...","port":N}
extern "C" unsigned char *DZWebRemoteQRPNG(int *outLen);    // PNG blob encoding the URL; free with DZFreeBytes

// v1.5.1 addition. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclaration is
// harmless. Checks GitHub for a newer release; never downloads/installs
// anything. Result is a malloc'd JSON string — free with DZFree.
extern "C" char *DZCheckUpdateJSON(void); // {"current","latest","hasUpdate","url","notes"}

// No-Internet detection. Redeclared here (like the blocks above) so the GUI
// still builds against an older generated header; identical redeclaration is
// harmless. Returns why the most recent DZInit failed, so the UI can show a
// No-Internet retry screen instead of pushing the user to re-authenticate:
// 0 = ok / logged in, 1 = ARL expired or invalid, 2 = no internet, 3 = other.
extern "C" int DZLoginErrorKind(void);

// v2.2.0 additions. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclarations are
// harmless. *JSON results are malloc'd C strings — free with DZFree.
//   DZTrackMixJSON / DZArtistMixJSON: "Start radio" mixes — {"tracks":[...]},
//     the exact wire shape DZFlowJSON returns, so the Flow parse+play path is
//     reused. Seeded from a track / an artist respectively.
//   DZHistoryRecentJSON(n): the newest n local listening-history entries as a
//     JSON array [{trackId,title,artist,album,startedAt,durationPlayedSec}]
//     (newest first; n<=0 = all; "[]" when unavailable).
//   DZHistoryStatsJSON(sinceDays): {"topTracks":[{trackId,title,artist,plays,
//     totalSec}],"topArtists":[{artist,plays,totalSec}],"totalSeconds":N}.
//   DZDownloadAlbum / DZDownloadPlaylist: batch download of a whole album /
//     playlist to the shared download folder — {"saved":N,"failed":N,"dir":
//     "...","error":""} — blocking + premium-only, like DZDownloadTrack.
//   DZQueueSet(js): replace the engine-side queue with the GUI's (js = JSON
//     array in the shared wire shape); 1 = ok, 0 = parse error. Once its cursor
//     is aligned (DZQueueSetIndex) the engine owns natural-finish auto-advance.
//   DZQueueSetIndex(i): align the engine queue cursor with the playing row.
//   DZQueueIndex(): the engine queue cursor (-1 when empty/unsynced) — polled in
//     tick() to follow an engine-driven advance once the queue is synced.
extern "C" char *DZTrackMixJSON(char *id);
extern "C" char *DZArtistMixJSON(char *id);
extern "C" char *DZHistoryRecentJSON(int n);
extern "C" char *DZHistoryStatsJSON(int sinceDays);
extern "C" char *DZDownloadAlbum(char *id);
extern "C" char *DZDownloadPlaylist(char *id);
extern "C" int   DZQueueSet(char *js);
extern "C" void  DZQueueSetIndex(int i);
extern "C" int   DZQueueIndex(void);

// v3.0.0 additions. Redeclared here (like the blocks above) so the GUI still
// builds against an older generated header; identical redeclarations are
// harmless. All three read the routed remote's state when casting, so a casting
// host GUI reflects the real state.
//   DZGetRepeat: current repeat mode as "off" / "all" / "one" — malloc'd C
//     string, free with DZFree.
//   DZGetShuffle: 1 when shuffle is on, else 0.
//   DZFavoriteIDsJSON: the account's liked-track ids as a JSON array of strings
//     (e.g. ["123","456"]; "[]" when none) — malloc'd, free with DZFree. Backs
//     truthful heart state without shipping every track's full metadata.
extern "C" char *DZGetRepeat(void);       // "off" | "all" | "one"
extern "C" int   DZGetShuffle(void);      // 1 = on, 0 = off
extern "C" char *DZFavoriteIDsJSON(void); // ["<trackId>", ...]

// v3.0.0 additions — Up-Next queue editor + offline downloads. Redeclared here
// (like the blocks above) so the GUI still builds against an older generated
// header; identical redeclarations are harmless. char* results are malloc'd —
// free with DZFree. DZQueueSet / DZQueueSetIndex / DZQueueIndex are declared in
// the v2.2.0 block above and reused here.
//   DZQueueJSON: the engine queue as a JSON array of the shared track shape (the
//     same shape DZQueueSet consumes; "[]" when empty) — backs the Up-Next panel.
//   DZQueueRemove(i): drop the queue entry at index i.
//   DZQueueMove(from,to): relocate the entry at `from` to index `to`.
//   DZQueueInsertNext(js): insert one track (js = a single track JSON object in
//     the shared shape) immediately after the current cursor ("Play next").
//   DZDownloadForOffline(trackID): save a track for offline playback — blocking +
//     premium-only, like DZDownloadTrack. Returns {"key":"<id>","path":"...",
//     "error":""}; the "key" identifies the stored track (feeds the offline mirror).
extern "C" char *DZQueueJSON(void);
extern "C" void  DZQueueRemove(int i);
extern "C" void  DZQueueMove(int from, int to);
extern "C" void  DZQueueInsertNext(char *js);
extern "C" char *DZDownloadForOffline(char *trackID);

namespace {

// A slider style that makes a left-click on the groove jump straight to that
// position (absolute set), instead of paging one step toward it. Applied to the
// seek slider so a click seeks directly to the clicked point (drag still works,
// driven by the existing sliderPressed/Released handlers).
class DirectJumpSliderStyle : public QProxyStyle {
public:
    using QProxyStyle::QProxyStyle;
    int styleHint(StyleHint hint, const QStyleOption *opt, const QWidget *w,
                  QStyleHintReturn *ret) const override {
        if (hint == SH_Slider_AbsoluteSetButtons)
            return Qt::LeftButton;
        return QProxyStyle::styleHint(hint, opt, w, ret);
    }
};

const char *kAccent = "#A238FF"; // Deezer "Electric Violet"

// --- small helpers ---------------------------------------------------------

// Pass a QByteArray's bytes to a non-const char* C parameter. The DZ* calls
// copy the string into Go memory during the call, so the QByteArray must simply
// outlive the call (it does — it is a named local in every caller).
char *cstr(const QByteArray &b) { return const_cast<char *>(b.constData()); }

// Take ownership of a malloc'd C string from a DZ*JSON call, copy it into a
// QByteArray and release the C buffer with DZFree.
QByteArray takeJson(char *s) {
    QByteArray b;
    if (s) {
        b = QByteArray(s);
        DZFree(s);
    }
    return b;
}

QString timeText(qint64 ms) {
    const qint64 s = qMax<qint64>(0, ms) / 1000;
    return QString::asprintf("%lld:%02lld", s / 60, s % 60);
}

QPixmap placeholderPix(int size) {
    QPixmap pm(size, size);
    pm.fill(QColor("#2A1840")); // deep purple placeholder until art arrives
    return pm;
}
QIcon placeholderIcon() { return QIcon(placeholderPix(40)); }

Track parseTrack(const QJsonObject &o) {
    Track t;
    t.id         = o.value("id").toString();
    t.name       = o.value("name").toString();
    t.durationMs = static_cast<qint64>(o.value("durationMs").toDouble());
    t.artistLine = o.value("artistLine").toString();
    t.albumName  = o.value("albumName").toString();
    t.artworkUrl = o.value("artworkUrl").toString();
    t.isExplicit = o.value("explicit").toBool();
    // First artist's id — used to open the artist view from a track.
    // DZNowPlayingJSON now exposes "artistId" directly (covers both local and
    // remote/Connect tracks); fall back to artists[0].id for full track objects
    // that carry the nested artists array (browse, search, playlist results).
    t.artistId = o.value("artistId").toString();
    if (t.artistId.isEmpty()) {
        const QJsonArray as = o.value("artists").toArray();
        if (!as.isEmpty())
            t.artistId = as.first().toObject().value("id").toString();
    }
    return t;
}

// Track title with a leading explicit-content "E" badge (the 🅴 glyph, matching
// the other OpenDeezer front-ends) when the track is flagged explicit.
QString badgedTitle(const Track &t) {
    return t.isExplicit ? QString::fromUtf8("\xF0\x9F\x85\xB4 ") + t.name : t.name;
}
ArtistInfo parseArtistInfo(const QJsonObject &o) {
    ArtistInfo a;
    a.id         = o.value("id").toString();
    a.name       = o.value("name").toString();
    a.artworkUrl = o.value("artworkUrl").toString();
    a.nbFans     = o.value("nbFans").toInt();
    return a;
}
LyricsData parseLyrics(const QByteArray &json) {
    LyricsData d;
    const QJsonObject o = QJsonDocument::fromJson(json).object();
    d.isSynced = o.value("isSynced").toBool();
    d.plain    = o.value("plain").toString();
    for (const QJsonValue &v : o.value("synced").toArray()) {
        const QJsonObject lo = v.toObject();
        LyricsLine ln;
        ln.timeMs = static_cast<qint64>(lo.value("timeMs").toDouble());
        ln.text   = lo.value("text").toString();
        d.lines.push_back(ln);
    }
    return d;
}
Album parseAlbum(const QJsonObject &o) {
    Album a;
    a.id         = o.value("id").toString();
    a.name       = o.value("name").toString();
    a.artworkUrl = o.value("artworkUrl").toString();
    const QJsonArray as = o.value("artists").toArray();
    if (!as.isEmpty())
        a.artistLine = as.first().toObject().value("name").toString();
    return a;
}
Playlist parsePlaylist(const QJsonObject &o) {
    Playlist p;
    p.id         = o.value("id").toString();
    p.name       = o.value("name").toString();
    p.owner      = o.value("owner").toString();
    p.trackCount = o.value("trackCount").toInt();
    p.artworkUrl = o.value("artworkUrl").toString();
    return p;
}
Podcast parsePodcast(const QJsonObject &o) {
    Podcast p;
    p.id           = o.value("id").toString();
    p.name         = o.value("name").toString();
    p.description  = o.value("description").toString();
    p.artworkUrl   = o.value("artworkUrl").toString();
    p.episodeCount = o.value("episodeCount").toInt();
    return p;
}
Episode parseEpisode(const QJsonObject &o) {
    Episode e;
    e.id          = o.value("id").toString();
    e.title       = o.value("title").toString();
    e.description = o.value("description").toString();
    e.artworkUrl  = o.value("artworkUrl").toString();
    e.durationMs  = static_cast<qint64>(o.value("durationMs").toDouble());
    e.releaseDate = o.value("releaseDate").toString();
    return e;
}

QVector<Track> parseTracks(const QByteArray &json) {
    QVector<Track> out;
    const QJsonObject obj = QJsonDocument::fromJson(json).object();
    for (const QJsonValue &v : obj.value("tracks").toArray())
        out.push_back(parseTrack(v.toObject()));
    return out;
}

// Serialize one Track to the shared wire shape consumed by DZQueueSet /
// DZQueueInsertNext (and re-parsed by parseTrack). Kept in one place so the
// whole-queue push and the single-track "Play next" insert always agree.
QJsonObject trackToJsonObj(const Track &t) {
    QJsonObject o;
    o["id"]         = t.id;
    o["name"]       = t.name;
    o["durationMs"] = static_cast<double>(t.durationMs);
    o["artistLine"] = t.artistLine;
    o["artistId"]   = t.artistId;
    o["albumName"]  = t.albumName;
    o["artworkUrl"] = t.artworkUrl;
    o["explicit"]   = t.isExplicit;
    if (!t.artistId.isEmpty() || !t.artistLine.isEmpty()) {
        QJsonObject a;
        a["id"]   = t.artistId;
        a["name"] = t.artistLine;
        o["artists"] = QJsonArray{a};
    }
    return o;
}

// The engine queue as a QVector<Track>: DZQueueJSON returns a bare JSON array of
// the shared track shape.
QVector<Track> parseQueue(const QByteArray &json) {
    QVector<Track> out;
    for (const QJsonValue &v : QJsonDocument::fromJson(json).array())
        out.push_back(parseTrack(v.toObject()));
    return out;
}

// DZDiscoverDevices returns a JSON array (not an object) of device records.
QVector<ConnectDevice> parseDevices(const QByteArray &json) {
    QVector<ConnectDevice> out;
    for (const QJsonValue &v : QJsonDocument::fromJson(json).array()) {
        const QJsonObject o = v.toObject();
        ConnectDevice d;
        d.name    = o.value("name").toString();
        d.addr    = o.value("addr").toString();
        d.client  = o.value("client").toString();
        d.version = o.value("version").toString();
        out.push_back(d);
    }
    return out;
}

// Friendly device type from a client id (mirrors the engine + the other GUIs).
QString deviceTypeLabel(const QString &client) {
    if (client == QLatin1String("tui"))     return QCoreApplication::translate("MainWindow", "Terminal");
    if (client == QLatin1String("darwin") || client == QLatin1String("macos"))
        return QStringLiteral("macOS");
    if (client == QLatin1String("windows")) return QStringLiteral("Windows");
    if (client == QLatin1String("linux") || client == QLatin1String("gnome") ||
        client == QLatin1String("kde"))
        return QStringLiteral("Linux");
    if (client.isEmpty())                   return QStringLiteral("OpenDeezer");
    return client;
}

// ARL: $DEEZER_ARL first, then ~/.config/opendeezer/arl.txt (legacy deezertui).
QString loadARL() {
    const QByteArray env = qgetenv("DEEZER_ARL");
    if (!env.isEmpty())
        return QString::fromUtf8(env).trimmed();
    const QString home = QDir::homePath();
    for (const QString &sub : {QStringLiteral("opendeezer"), QStringLiteral("deezertui")}) {
        QFile f(home + "/.config/" + sub + "/arl.txt");
        if (f.open(QIODevice::ReadOnly)) {
            const QString s = QString::fromUtf8(f.readAll()).trimmed();
            if (!s.isEmpty())
                return s;
        }
    }
    return QString();
}

// Where a captured/entered ARL is written so the next launch auto-logs-in. Must
// match the path loadARL() reads (~/.config/opendeezer/arl.txt).
QString arlConfigPath() {
    return QDir::homePath() + "/.config/opendeezer/arl.txt";
}

// The app icon: prefer the embedded official logo (Qt resource), then the
// installed theme icon, then a drawn Deezer-purple disc as a last resort.
QIcon appIcon() {
    QIcon embedded(QStringLiteral(":/opendeezer.png"));
    if (!embedded.isNull())
        return embedded;
    QIcon themed = QIcon::fromTheme(QStringLiteral("org.opendeezer.OpenDeezer"));
    if (!themed.isNull())
        return themed;
    QPixmap pm(64, 64);
    pm.fill(Qt::transparent);
    QPainter p(&pm);
    p.setRenderHint(QPainter::Antialiasing);
    p.setBrush(QColor(kAccent));
    p.setPen(Qt::NoPen);
    p.drawEllipse(2, 2, 60, 60);
    QFont f = p.font();
    f.setPointSize(30);
    f.setBold(true);
    p.setFont(f);
    p.setPen(Qt::white);
    p.drawText(pm.rect(), Qt::AlignCenter, QStringLiteral("♪")); // ♪
    p.end();
    return QIcon(pm);
}

QToolButton *mediaButton(QStyle *style, QStyle::StandardPixmap icon) {
    auto *b = new QToolButton;
    b->setIcon(style->standardIcon(icon));
    b->setAutoRaise(true);
    b->setIconSize(QSize(22, 22));
    return b;
}

} // namespace

// ---------------------------------------------------------------------------

MainWindow::MainWindow(QWidget *parent) : QMainWindow(parent) {
    setWindowTitle("OpenDeezer");
    setWindowIcon(appIcon());
    setMinimumSize(900, 600);

    // Identify this front-end on the LAN for OpenDeezer Connect (discovery +
    // /whoami). Must run before DZInit; "kde" maps to a "Linux" device type.
    {
        QByteArray client = "kde";
        QByteArray device = "OpenDeezer (KDE)";
        DZSetClientInfo(cstr(client), cstr(device));
    }

    // Load persisted settings (config dir lives alongside arl.txt).
    const QString cfg = settingsPath();
    QDir().mkpath(QFileInfo(cfg).absolutePath());
    m_quality = SettingsDialog::loadQuality(cfg);
    m_closeToTray = SettingsDialog::loadCloseToTray(cfg);

    // The "Up Next" dock is added to the QMainWindow before the menu so the View
    // menu can expose its toggle action.
    buildQueueDock();
    buildMenu();
    buildSidebar();

    m_stack = new QStackedWidget;
    m_stack->addWidget(buildHomePage());            // index 0
    m_stack->addWidget(buildTracksPage());          // index 1
    m_stack->addWidget(buildPlaylistsPage());       // index 2
    m_stack->addWidget(buildSearchPage());          // index 3
    m_stack->addWidget(buildLyricsPage());          // index 4
    m_stack->addWidget(buildArtistPage());          // index 5
    m_stack->addWidget(buildChartsPage());          // index 6
    m_stack->addWidget(buildPodcastsPage());        // index 7
    m_stack->addWidget(buildPodcastEpisodesPage()); // index 8
    m_stack->addWidget(buildHistoryPage());         // index 9

    auto *split = new QSplitter(Qt::Horizontal);
    split->addWidget(m_sidebar);
    split->addWidget(m_stack);
    split->setStretchFactor(1, 1);
    split->setSizes({200, 800});

    auto *central = new QWidget;
    auto *v = new QVBoxLayout(central);
    v->setContentsMargins(0, 0, 0, 0);
    v->setSpacing(0);
    v->addWidget(split, 1);
    v->addWidget(buildTransport());
    m_centralLayout = v; // update banner inserts itself at row 0, above the splitter

    // The whole app lives in a top-level stack so the No-Internet retry page can
    // take over the window (without tearing down the live widgets) on an offline
    // login, then hand back to the app on a successful retry (see showNoInternet).
    m_rootStack = new QStackedWidget;
    m_rootStack->addWidget(central);   // index 0: the app
    setCentralWidget(m_rootStack);

    // One GUI-thread poll timer drives the seek bar, the play/pause icon and
    // auto-advance. Only cheap, non-blocking DZ* state reads happen here.
    m_poll = new QTimer(this);
    m_poll->setInterval(300);
    connect(m_poll, &QTimer::timeout, this, &MainWindow::tick);

    setupMpris();   // session-bus media controls / now-playing
    setupTray();    // background playback + close-to-tray
    checkForUpdates(); // once per launch, background, non-intrusive (no login needed)

    statusBar()->showMessage(tr("Logging in…"));
    // Defer to the event loop: startLogin() may exec() the modal login dialog,
    // and running that nested loop from inside the constructor (before the main
    // window is shown / app.exec() runs) blocks construction so no window ever
    // appears. singleShot(0) fires it after the window is up.
    QTimer::singleShot(0, this, &MainWindow::startLogin);
}

// Worker lambdas capture `this` and post results back with
// QMetaObject::invokeMethod(this, ...), but this window is a stack object in
// opendeezer_run destroyed the moment app.exec() returns — drain both pools
// before the memory (and, in the dlopen'd launcher case, the code) goes away.
// Results already queued to the dead event loop are purged by ~QObject.
MainWindow::~MainWindow() {
    m_artPool.waitForDone();
    QThreadPool::globalInstance()->waitForDone();
}

// ---- OS integration: MPRIS, tray, settings --------------------------------

QString MainWindow::settingsPath() const {
    // Live next to arl.txt under the app config dir (~/.config/opendeezer).
    return QDir::homePath() + "/.config/opendeezer/settings.ini";
}

// Register on the session bus and wire every MPRIS command to MainWindow's own
// existing transport handlers — no playback logic is duplicated here.
void MainWindow::setupMpris() {
    m_mpris = new MprisController(this);
    if (!m_mpris->registerOnBus()) {
        // No usable session bus (e.g. headless) — degrade silently.
        return;
    }
    connect(m_mpris, &MprisController::playPauseRequested, this, &MainWindow::togglePause);
    connect(m_mpris, &MprisController::nextRequested,      this, &MainWindow::next);
    connect(m_mpris, &MprisController::prevRequested,      this, &MainWindow::prev);
    // Transport commands become blocking HTTP requests (15 s timeout) when
    // routed to a Connect device — keep them off the GUI thread like every
    // other blocking DZ call.
    connect(m_mpris, &MprisController::playRequested,  this,
            [] { QtConcurrent::run([] { DZResume(); }); });
    connect(m_mpris, &MprisController::pauseRequested, this,
            [] { QtConcurrent::run([] { DZPause(); }); });
    connect(m_mpris, &MprisController::stopRequested,  this,
            [] { QtConcurrent::run([] { DZStop(); }); });
    connect(m_mpris, &MprisController::seekRequested, this, [this](qlonglong offUs) {
        // MPRIS Seek is relative (µs); the engine seeks to an absolute ms.
        const qint64 target = qMax<qint64>(0, DZPositionMS() + offUs / 1000);
        QtConcurrent::run([target] { DZSeek(target); });
        m_mpris->notifySeeked(target);
    });
    connect(m_mpris, &MprisController::setPositionRequested, this, [this](qlonglong posUs) {
        const qint64 ms = qMax<qint64>(0, posUs / 1000);
        QtConcurrent::run([ms] { DZSeek(ms); });
        m_mpris->notifySeeked(ms);
    });
    connect(m_mpris, &MprisController::volumeChangeRequested, this, [this](double v) {
        m_vol->setValue(static_cast<int>(qRound(qBound(0.0, v, 1.0) * 100)));
    });
    connect(m_mpris, &MprisController::raiseRequested, this, [this] {
        showNormal();
        raise();
        activateWindow();
    });
    connect(m_mpris, &MprisController::quitRequested, this, &MainWindow::quitApp);
}

// A tray icon keeps the app reachable while the window is hidden and playback
// continues in the background. Only created when a system tray is available.
void MainWindow::setupTray() {
    if (!QSystemTrayIcon::isSystemTrayAvailable())
        return;
    m_tray = new QSystemTrayIcon(appIcon(), this);
    m_tray->setToolTip(QStringLiteral("OpenDeezer"));

    auto *menu = new QMenu(this);
    auto *restore = menu->addAction(tr("Show OpenDeezer"));
    connect(restore, &QAction::triggered, this, [this] {
        showNormal();
        raise();
        activateWindow();
    });
    menu->addSeparator();
    auto *quit = menu->addAction(tr("Quit"));
    connect(quit, &QAction::triggered, this, &MainWindow::quitApp);
    m_tray->setContextMenu(menu);

    connect(m_tray, &QSystemTrayIcon::activated, this,
            [this](QSystemTrayIcon::ActivationReason reason) {
                if (reason == QSystemTrayIcon::Trigger ||
                    reason == QSystemTrayIcon::DoubleClick) {
                    if (isVisible()) {
                        hide();
                    } else {
                        showNormal();
                        raise();
                        activateWindow();
                    }
                }
            });
    m_tray->show();
}

void MainWindow::openSettings() {
    // Enumerate output devices (local hardware enumeration — not network) and the
    // engine's currently selected device, then hand both to the dialog.
    QVector<AudioDevice> devices;
    QString curDevice;
    if (m_loggedIn) {
        const QJsonObject obj =
            QJsonDocument::fromJson(takeJson(DZAudioDevicesJSON())).object();
        for (const QJsonValue &v : obj.value("devices").toArray()) {
            const QJsonObject d = v.toObject();
            AudioDevice dev;
            dev.id        = d.value("id").toString();
            dev.name      = d.value("name").toString();
            dev.isDefault = d.value("isDefault").toBool();
            devices.push_back(dev);
        }
        if (char *c = DZCurrentAudioDevice()) {
            curDevice = QString::fromUtf8(c);
            DZFree(c);
        }
    }

    SettingsDialog dlg(settingsPath(), devices, curDevice, m_premium, this);
    connect(&dlg, &SettingsDialog::qualityChanged, this, &MainWindow::applyQuality);
    connect(&dlg, &SettingsDialog::replayGainChanged, this, &MainWindow::applyReplayGain);
    connect(&dlg, &SettingsDialog::closeToTrayChanged, this,
            [this](bool on) { m_closeToTray = on; });
    connect(&dlg, &SettingsDialog::outputDeviceChanged, this, &MainWindow::applyAudioDevice);
    connect(&dlg, &SettingsDialog::gaplessChanged, this, &MainWindow::applyGapless);
    connect(&dlg, &SettingsDialog::crossfadeChanged, this, &MainWindow::applyCrossfade);
    dlg.exec();
}

// Phone Remote: a modal dialog with an enable toggle. When on, shows a 512×512
// QR PNG (from DZWebRemoteQRPNG — rendered via QPixmap::loadFromData, the same
// path used for cover-art bytes), the 6-digit pairing code (large, monospace,
// accent colour) and the URL (selectable text). All values come from
// DZWebRemoteInfoJSON, which is re-read on every toggle so the display is always
// current. Disabled by default; LAN-only; no data leaves the local network.
void MainWindow::openPhoneRemote() {
    QDialog dlg(this);
    dlg.setWindowTitle(tr("Phone Remote"));
    dlg.setMinimumWidth(360);
    auto *v = new QVBoxLayout(&dlg);
    v->setSpacing(10);

    auto *toggle = new QCheckBox(tr("Enable Phone Remote"));
    v->addWidget(toggle);

    // Container for QR / code / URL — shown only when the remote is enabled.
    auto *remoteBox = new QWidget;
    auto *rv = new QVBoxLayout(remoteBox);
    rv->setContentsMargins(0, 8, 0, 0);
    rv->setSpacing(8);

    // QR image: 200×200 display of the 512×512 PNG from DZWebRemoteQRPNG.
    auto *qrLabel = new QLabel;
    qrLabel->setAlignment(Qt::AlignHCenter | Qt::AlignVCenter);
    qrLabel->setFixedSize(200, 200);
    rv->addWidget(qrLabel, 0, Qt::AlignHCenter);

    // Six-digit pairing code: large, monospace, Deezer accent.
    auto *codeLabel = new QLabel;
    codeLabel->setAlignment(Qt::AlignHCenter);
    {
        QFont f = codeLabel->font();
        f.setFamily(QStringLiteral("Monospace"));
        f.setStyleHint(QFont::Monospace);
        f.setFixedPitch(true);
        f.setPointSize(f.pointSize() + 10);
        f.setBold(true);
        codeLabel->setFont(f);
    }
    codeLabel->setStyleSheet(QString("color:%1;").arg(kAccent));
    rv->addWidget(codeLabel, 0, Qt::AlignHCenter);

    // URL text: selectable so the user can copy it manually.
    auto *urlLabel = new QLabel;
    urlLabel->setAlignment(Qt::AlignHCenter);
    urlLabel->setTextInteractionFlags(Qt::TextSelectableByMouse);
    urlLabel->setWordWrap(true);
    rv->addWidget(urlLabel, 0, Qt::AlignHCenter);

    auto *hint = new QLabel(
        tr("Scan with your phone (same Wi-Fi), then enter the code."));
    hint->setAlignment(Qt::AlignHCenter);
    hint->setWordWrap(true);
    rv->addWidget(hint, 0, Qt::AlignHCenter);

    v->addWidget(remoteBox);

    auto *bb = new QDialogButtonBox(QDialogButtonBox::Close);
    v->addWidget(bb);
    connect(bb, &QDialogButtonBox::rejected, &dlg, &QDialog::reject);

    // Read DZWebRemoteInfoJSON + DZWebRemoteQRPNG and update all widgets.
    // Called on open (to reflect the engine's current state) and on every toggle.
    auto refreshInfo = [&]() {
        const QByteArray infoJson = takeJson(DZWebRemoteInfoJSON());
        const QJsonObject info = QJsonDocument::fromJson(infoJson).object();
        const bool on = info.value("enabled").toBool();
        remoteBox->setVisible(on);
        if (on) {
            codeLabel->setText(info.value("code").toString());
            urlLabel->setText(info.value("url").toString());
            // Load the QR PNG directly from raw bytes — same path as cover-art
            // loading (DZFetch → QImage::fromData), just without the network fetch.
            int qrLen = 0;
            unsigned char *qrData = DZWebRemoteQRPNG(&qrLen);
            if (qrData && qrLen > 0) {
                const QByteArray pngBytes(reinterpret_cast<const char *>(qrData), qrLen);
                QPixmap qrPix;
                if (qrPix.loadFromData(pngBytes, "PNG") && !qrPix.isNull())
                    qrLabel->setPixmap(qrPix.scaled(
                        200, 200, Qt::KeepAspectRatio, Qt::SmoothTransformation));
                DZFreeBytes(qrData);
            }
        }
        dlg.adjustSize();
    };

    // Seed the toggle from the engine's current state. Connect the signal only
    // after seeding so the handler doesn't fire on the programmatic setChecked.
    {
        const QByteArray j = takeJson(DZWebRemoteInfoJSON());
        toggle->setChecked(
            QJsonDocument::fromJson(j).object().value("enabled").toBool());
    }
    connect(toggle, &QCheckBox::toggled, &dlg, [&](bool on) {
        DZWebRemoteSetEnabled(on ? 1 : 0);
        refreshInfo();
    });
    refreshInfo(); // initial render (seeds QR/code/URL or hides the box)

    dlg.exec();
}

// Once-per-launch GitHub release check. Called directly from the constructor —
// safe because the actual work runs on a QtConcurrent worker thread, so this
// returns immediately and never blocks startup. Never downloads or installs
// anything. Silent on a network failure or when already up to date; only
// surfaces a banner when a newer release actually exists. Doesn't need a
// logged-in client.
void MainWindow::checkForUpdates() {
    QtConcurrent::run([this] {
        const QByteArray j = takeJson(DZCheckUpdateJSON());
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject o = QJsonDocument::fromJson(j).object();
            if (!o.value("hasUpdate").toBool())
                return; // up to date, draft/prerelease, or the check failed
            showUpdateBanner(o.value("latest").toString(),
                            o.value("url").toString(),
                            o.value("notes").toString());
        });
    });
}

// A small dismissible bar across the top of the window: "OpenDeezer <latest>
// available" plus a Download button that opens the GitHub release page in the
// user's browser (QDesktopServices — no in-app download/install). Inserted at
// the top of the central layout, above the sidebar/content splitter.
void MainWindow::showUpdateBanner(const QString &latest, const QString &url,
                                  const QString &notes) {
    if (m_updateBanner || !m_centralLayout)
        return; // already showing, or the window isn't built yet

    auto *bar = new QFrame;
    bar->setStyleSheet(QString(
        "QFrame{background:%1;} QLabel{color:white;} "
        "QPushButton{background:white;color:%1;border-radius:3px;padding:3px 12px;} "
        "QToolButton{color:white;border:none;font-weight:bold;padding:0 4px;}")
        .arg(kAccent));
    auto *h = new QHBoxLayout(bar);
    h->setContentsMargins(14, 6, 8, 6);

    auto *label = new QLabel(tr("OpenDeezer %1 available").arg(latest));
    h->addWidget(label);
    h->addStretch(1);

    if (!notes.isEmpty()) {
        auto *notesBtn = new QPushButton(tr("Release notes"));
        connect(notesBtn, &QPushButton::clicked, this, [this, latest, notes] {
            QMessageBox::information(this, QStringLiteral("OpenDeezer %1").arg(latest), notes);
        });
        h->addWidget(notesBtn);
    }

    auto *download = new QPushButton(tr("Download"));
    connect(download, &QPushButton::clicked, this,
            [url] { QDesktopServices::openUrl(QUrl(url)); });
    h->addWidget(download);

    auto *dismiss = new QToolButton;
    dismiss->setText(QString::fromUtf8("\xE2\x9C\x95")); // ✕
    dismiss->setAutoRaise(true);
    connect(dismiss, &QToolButton::clicked, this, [this] {
        if (m_updateBanner) {
            m_updateBanner->deleteLater();
            m_updateBanner = nullptr;
        }
    });
    h->addWidget(dismiss);

    m_centralLayout->insertWidget(0, bar);
    m_updateBanner = bar;
}

// Parse DZAccountJSON {name,offer,canHq,canHifi,premium,loggedIn} into the cached
// tier fields used by the About box, status bar, the quality entitlement note and
// the Free-account block (premium=false ⇒ can't stream on-demand).
void MainWindow::applyAccount(const QByteArray &json) {
    const QJsonObject o = QJsonDocument::fromJson(json).object();
    m_accountName  = o.value("name").toString();
    m_accountOffer = o.value("offer").toString();
    m_canHq        = o.value("canHq").toBool();
    m_canHifi      = o.value("canHifi").toBool();
    m_premium      = o.value("premium").toBool();
    m_haveAccount  = o.value("loggedIn").toBool() || !m_accountName.isEmpty();
}

void MainWindow::applyQuality(int level) {
    m_quality = level;
    DZSetQuality(level);
    const QString names[] = {tr("Normal (MP3 128)"), tr("High (MP3 320)"),
                             tr("HiFi (FLAC)")};
    QString msg = tr("Quality: %1").arg(names[level < 0 ? 0 : (level > 2 ? 2 : level)]);
    // Note when the chosen tier exceeds the account's entitlement; the engine
    // transparently falls back, so this is informational only.
    if (m_haveAccount) {
        if (level >= 2 && !m_canHifi)
            msg += tr(" — your plan has no HiFi; the engine will fall back");
        else if (level >= 1 && !m_canHq)
            msg += tr(" — your plan has no High quality; the engine will fall back");
    }
    statusBar()->showMessage(msg, 4000);
}

void MainWindow::applyReplayGain(bool on) {
    m_replayGain = on;
    DZSetReplayGain(on ? 1 : 0);
    statusBar()->showMessage(on ? tr("ReplayGain: on")
                                : tr("ReplayGain: off"), 3000);
}

// Switching the output device reinitialises the audio backend, which can briefly
// block — do it off the GUI thread.
void MainWindow::applyAudioDevice(const QString &deviceId) {
    const QByteArray idb = deviceId.toUtf8();
    QtConcurrent::run([this, idb] {
        const int ok = DZSetAudioDevice(cstr(idb));
        QMetaObject::invokeMethod(this, [this, ok] {
            statusBar()->showMessage(ok ? tr("Output device changed")
                                        : tr("Couldn't change output device"),
                                     3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::applyGapless(bool on) {
    m_gapless = on;
    DZSetGapless(on ? 1 : 0);
    statusBar()->showMessage(on ? tr("Gapless: on")
                                : tr("Gapless: off"), 3000);
    // Keep the next-track preload in sync with the new setting.
    preloadNext();
}

void MainWindow::applyCrossfade(int ms) {
    m_crossfadeMs = ms;
    DZSetCrossfadeMS(ms);
    statusBar()->showMessage(ms > 0
        ? tr("Crossfade: %1s").arg(ms / 1000)
        : tr("Crossfade: off"), 3000);
    preloadNext();
}

void MainWindow::quitApp() {
    m_forceQuit = true;
    if (m_tray)
        m_tray->hide();
    close();
}

// Honour the close-to-tray setting: hide to the tray and keep the engine
// playing, unless the user explicitly chose Quit.
void MainWindow::closeEvent(QCloseEvent *event) {
    if (!m_forceQuit && m_closeToTray && m_tray) {
        hide();
        event->ignore();
        if (!m_trayHintShown) {
            m_tray->showMessage(QStringLiteral("OpenDeezer"),
                                tr("Still playing in the background."),
                                appIcon(), 4000);
            m_trayHintShown = true;
        }
        return;
    }
    // DZStop is a blocking HTTP request when routed to a Connect device — run
    // it on a worker; the destructor drains the pool before the engine goes.
    QtConcurrent::run([] { DZStop(); });
    QMainWindow::closeEvent(event);
    qApp->quit();
}

// ---- menu -----------------------------------------------------------------

void MainWindow::buildMenu() {
    auto *file = menuBar()->addMenu(tr("&File"));
    // Reachable even when already auto-logged-in from a stored ARL — opens the
    // Deezer web-login dialog on demand (sign in / switch account).
    auto *login = file->addAction(tr("&Log in / Switch account…"));
    connect(login, &QAction::triggered, this, [this] { promptLogin(); });
    file->addSeparator();
    auto *settings = file->addAction(tr("&Settings…"));
    settings->setShortcut(QKeySequence::Preferences);
    connect(settings, &QAction::triggered, this, &MainWindow::openSettings);
    auto *phoneRemote = file->addAction(tr("Phone &Remote…"));
    connect(phoneRemote, &QAction::triggered, this, &MainWindow::openPhoneRemote);
    file->addSeparator();
    auto *quit = file->addAction(tr("&Quit"));
    quit->setShortcut(QKeySequence::Quit);
    connect(quit, &QAction::triggered, this, &MainWindow::quitApp);

    auto *edit = menuBar()->addMenu(tr("Edit"));
    auto *search = edit->addAction(tr("Search"));
    search->setShortcut(QKeySequence::Find);
    connect(search, &QAction::triggered, this, [this] {
        // Selecting the sidebar row performs the normal page transition and
        // focuses the search field in onSidebarChanged().
        if (m_sidebar)
            m_sidebar->setCurrentRow(4);
    });

    auto *view = menuBar()->addMenu(tr("&View"));
    if (m_queueDock) {
        // QDockWidget's own toggle action: checked mirrors visibility, and it
        // shows/hides the dock. Give it a friendlier label + shortcut.
        QAction *q = m_queueDock->toggleViewAction();
        q->setText(tr("&Up Next queue"));
        q->setShortcut(QKeySequence(QStringLiteral("Ctrl+U")));
        view->addAction(q);
    }

    auto *help = menuBar()->addMenu(tr("&Help"));
    auto *about = help->addAction(tr("&About OpenDeezer"));
    connect(about, &QAction::triggered, this, [this] {
        QString text =
            QStringLiteral("<h3>OpenDeezer 2.2.3</h3><p>") +
            tr("A Deezer client for the desktop.") + QStringLiteral("</p>");
        // Show the signed-in account tier (from DZAccountJSON) when available.
        if (m_haveAccount && !m_accountName.isEmpty())
            text += tr("<p>Signed in as <b>%1</b> · %2</p>")
                        .arg(m_accountName.toHtmlEscaped(),
                             m_accountOffer.toHtmlEscaped());
        text += tr("<p>By <b>Cycl0o0</b>.<br>Licensed under <b>AGPL-3.0</b>.</p>");
        QMessageBox::about(this, tr("About OpenDeezer"), text);
    });
}

// ---- sidebar --------------------------------------------------------------

void MainWindow::buildSidebar() {
    m_sidebar = new QListWidget;
    m_sidebar->setMaximumWidth(240);
    m_sidebar->addItem(QStringLiteral("⌂  ") + tr("Home"));         // 0
    m_sidebar->addItem(QStringLiteral("♥  ") + tr("Liked Songs")); // 1
    m_sidebar->addItem(QStringLiteral("⚡  Flow"));         // 2
    m_sidebar->addItem(QStringLiteral("☰  ") + tr("Playlists"));    // 3
    m_sidebar->addItem(QStringLiteral("⌕  ") + tr("Search"));       // 4
    m_sidebar->addItem(QStringLiteral("★  ") + tr("Charts"));       // 5
    m_sidebar->addItem(QStringLiteral("◉  ") + tr("Podcasts"));     // 6
    m_sidebar->addItem(QStringLiteral("↺  ") + tr("Recently played")); // 7
    connect(m_sidebar, &QListWidget::currentRowChanged, this, &MainWindow::onSidebarChanged);
}

void MainWindow::onSidebarChanged(int row) {
    switch (row) {
    case 0:                                 // Home
        m_stack->setCurrentIndex(0);
        loadHome();
        break;
    case 1:                                 // Liked Songs
        m_stack->setCurrentIndex(1);
        loadFavorites();
        break;
    case 2:                                 // Flow — shares the track table
        m_stack->setCurrentIndex(1);
        loadFlow();
        break;
    case 3:                                 // Playlists
        m_stack->setCurrentIndex(2);
        loadPlaylists();
        break;
    case 4:                                 // Search
        m_stack->setCurrentIndex(3);
        if (m_searchEdit)
            m_searchEdit->setFocus();
        break;
    case 5:                                 // Charts
        m_stack->setCurrentIndex(6);
        loadCharts();
        break;
    case 6:                                 // Podcasts
        m_stack->setCurrentIndex(7);
        if (m_podcastSearchEdit)
            m_podcastSearchEdit->setFocus();
        break;
    case 7:                                 // Recently played + listening stats
        m_stack->setCurrentIndex(9);
        loadHistory();
        break;
    default:
        break;
    }
}

// ---- pages ----------------------------------------------------------------

// Home: the Discovery Home landing page. Wrapped in a QScrollArea so it scrolls
// when the window is short. Built once; data is populated by loadHome() each time
// the page becomes visible (the greeting is also refreshed for the time of day).
QWidget *MainWindow::buildHomePage() {
    auto *scroll = new QScrollArea;
    scroll->setWidgetResizable(true);
    scroll->setFrameShape(QFrame::NoFrame);

    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    v->setContentsMargins(20, 20, 20, 20);
    v->setSpacing(18);

    // --- Time-based greeting (large, bold) ---
    {
        const int h = QTime::currentTime().hour();
        const QString greet = h < 12 ? tr("Good morning")
                            : h < 18 ? tr("Good afternoon")
                                     : tr("Good evening");
        m_homeGreeting = new QLabel(greet);
        QFont f = m_homeGreeting->font();
        f.setPointSize(f.pointSize() + 12);
        f.setBold(true);
        m_homeGreeting->setFont(f);
        m_homeGreeting->setStyleSheet(QString("color:%1;").arg(kAccent));
    }
    v->addWidget(m_homeGreeting);

    // --- Quick-pick row: Liked Songs · Flow · Charts · Podcasts ---
    // Each button triggers the same sidebar action the nav items already use, so
    // the sidebar selection stays in sync.
    {
        auto *sectionLabel = new QLabel(tr("Quick pick"));
        QFont sf = sectionLabel->font();
        sf.setPointSize(sf.pointSize() + 2);
        sf.setBold(true);
        sectionLabel->setFont(sf);
        v->addWidget(sectionLabel);

        struct QuickCard { const char *emoji; QString label; int sidebarRow; };
        const QuickCard kQuickCards[] = {
            { "\xe2\x99\xa5", tr("Liked Songs"), 1 },   // ♥
            { "\xe2\x9a\xa1", QStringLiteral("Flow"), 2 },   // ⚡ (Flow: brand, never translated)
            { "\xe2\x98\x85", tr("Charts"),      5 },   // ★
            { "\xe2\x97\x89", tr("Podcasts"),    6 },   // ◉
        };
        auto *row = new QHBoxLayout;
        row->setSpacing(12);
        for (const QuickCard &c : kQuickCards) {
            auto *btn = new QToolButton;
            btn->setText(QString::fromUtf8(c.emoji) + QStringLiteral("  ") + c.label);
            btn->setToolButtonStyle(Qt::ToolButtonTextOnly);
            btn->setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Fixed);
            btn->setMinimumHeight(48);
            btn->setStyleSheet(QStringLiteral(
                "QToolButton{"
                "  background:#2A1840;"
                "  color:#FFFFFF;"
                "  border-radius:8px;"
                "  font-weight:bold;"
                "  font-size:13px;"
                "  padding:8px 14px;"
                "}"
                "QToolButton:hover{"
                "  background:#A238FF;"
                "}"));
            const int row_ = c.sidebarRow;
            connect(btn, &QToolButton::clicked, this, [this, row_] {
                m_sidebar->setCurrentRow(row_);
            });
            row->addWidget(btn);
        }
        v->addLayout(row);
    }

    // --- Top Tracks horizontal rail ---
    // Single-row, left-to-right, horizontal scroll; activating an item plays it.
    {
        auto *sectionLabel = new QLabel(tr("Top Tracks"));
        QFont sf = sectionLabel->font();
        sf.setPointSize(sf.pointSize() + 2);
        sf.setBold(true);
        sectionLabel->setFont(sf);
        v->addWidget(sectionLabel);

        m_homeTracksRail = new QListWidget;
        m_homeTracksRail->setViewMode(QListView::IconMode);
        m_homeTracksRail->setFlow(QListView::LeftToRight);
        m_homeTracksRail->setWrapping(false);
        m_homeTracksRail->setIconSize(QSize(80, 80));
        m_homeTracksRail->setGridSize(QSize(112, 138));
        m_homeTracksRail->setResizeMode(QListView::Fixed);
        m_homeTracksRail->setMovement(QListView::Static);
        m_homeTracksRail->setWordWrap(true);
        m_homeTracksRail->setFixedHeight(162);
        m_homeTracksRail->setVerticalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
        m_homeTracksRail->setHorizontalScrollBarPolicy(Qt::ScrollBarAsNeeded);
        connect(m_homeTracksRail, &QListWidget::itemActivated, this,
                [this](QListWidgetItem *it) {
                    const int idx = it->data(Qt::UserRole).toInt();
                    if (idx >= 0 && idx < m_homeTracks.size())
                        playFrom(m_homeTracks, idx);
                });
        v->addWidget(m_homeTracksRail);
    }

    // --- Your Playlists horizontal rail ---
    // Same icon-card style as the Playlists page; activating one opens it.
    {
        auto *sectionLabel = new QLabel(tr("Your Playlists"));
        QFont sf = sectionLabel->font();
        sf.setPointSize(sf.pointSize() + 2);
        sf.setBold(true);
        sectionLabel->setFont(sf);
        v->addWidget(sectionLabel);

        m_homePlaylistsRail = new QListWidget;
        m_homePlaylistsRail->setViewMode(QListView::IconMode);
        m_homePlaylistsRail->setFlow(QListView::LeftToRight);
        m_homePlaylistsRail->setWrapping(false);
        m_homePlaylistsRail->setIconSize(QSize(110, 110));
        m_homePlaylistsRail->setGridSize(QSize(142, 168));
        m_homePlaylistsRail->setResizeMode(QListView::Fixed);
        m_homePlaylistsRail->setMovement(QListView::Static);
        m_homePlaylistsRail->setWordWrap(true);
        m_homePlaylistsRail->setFixedHeight(192);
        m_homePlaylistsRail->setVerticalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
        m_homePlaylistsRail->setHorizontalScrollBarPolicy(Qt::ScrollBarAsNeeded);
        connect(m_homePlaylistsRail, &QListWidget::itemActivated, this,
                [this](QListWidgetItem *it) {
                    const int idx = it->data(Qt::UserRole).toInt();
                    if (idx >= 0 && idx < m_homePlaylists.size())
                        openPlaylist(m_homePlaylists[idx]);
                });
        v->addWidget(m_homePlaylistsRail);
    }

    v->addStretch(1);
    scroll->setWidget(w);
    return scroll;
}

// Load Home data from DZHomeJSON off the GUI thread, then populate the two rails.
// The greeting label is also refreshed for the current time of day on each visit.
void MainWindow::loadHome() {
    if (!m_loggedIn)
        return;
    // Refresh the greeting in case the time of day has changed since construction.
    if (m_homeGreeting) {
        const int h = QTime::currentTime().hour();
        m_homeGreeting->setText(h < 12 ? tr("Good morning")
                              : h < 18 ? tr("Good afternoon")
                                       : tr("Good evening"));
    }
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        const QByteArray j = takeJson(DZHomeJSON());
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject obj = QJsonDocument::fromJson(j).object();
            const int gen = ++m_artGen;

            // Top Tracks rail — play on activate (reuses playFrom + cover-art path).
            m_homeTracks.clear();
            for (const QJsonValue &v : obj.value("topTracks").toArray())
                m_homeTracks.push_back(parseTrack(v.toObject()));
            m_homeTracksRail->clear();
            if (m_homeTracks.isEmpty()) {
                m_homeTracksRail->addItem(
                    new QListWidgetItem(tr("No tracks available.")));
            } else {
                for (int i = 0; i < m_homeTracks.size(); ++i) {
                    const Track &t = m_homeTracks[i];
                    auto *it = new QListWidgetItem(
                        QIcon(placeholderPix(80)),
                        t.name + "\n" + t.artistLine);
                    it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                    it->setData(Qt::UserRole, i);
                    m_homeTracksRail->addItem(it);
                    if (!t.artworkUrl.isEmpty())
                        fetchImage(t.artworkUrl, gen, [it](const QImage &img) {
                            it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                                80, 80, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                        });
                }
            }

            // Your Playlists rail — open on activate (reuses openPlaylist path).
            m_homePlaylists.clear();
            for (const QJsonValue &v : obj.value("playlists").toArray())
                m_homePlaylists.push_back(parsePlaylist(v.toObject()));
            m_homePlaylistsRail->clear();
            if (m_homePlaylists.isEmpty()) {
                m_homePlaylistsRail->addItem(
                    new QListWidgetItem(tr("No playlists available.")));
            } else {
                for (int i = 0; i < m_homePlaylists.size(); ++i) {
                    const Playlist &p = m_homePlaylists[i];
                    auto *it = new QListWidgetItem(
                        QIcon(placeholderPix(110)),
                        p.name + "\n" + p.owner);
                    it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                    it->setData(Qt::UserRole, i);
                    m_homePlaylistsRail->addItem(it);
                    if (!p.artworkUrl.isEmpty())
                        fetchImage(p.artworkUrl, gen, [it](const QImage &img) {
                            it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                                110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                        });
                }
            }

            statusBar()->showMessage(tr("Home"), 3000);
        }, Qt::QueuedConnection);
    });
}

QTableWidget *MainWindow::makeTrackTable() {
    auto *t = new QTableWidget(0, 4);
    t->setHorizontalHeaderLabels({tr("Title"), tr("Artist"), tr("Album"), tr("Duration")});
    t->verticalHeader()->setVisible(false);
    t->verticalHeader()->setDefaultSectionSize(48);
    t->setIconSize(QSize(40, 40));
    t->setEditTriggers(QAbstractItemView::NoEditTriggers);
    t->setSelectionBehavior(QAbstractItemView::SelectRows);
    t->setSelectionMode(QAbstractItemView::SingleSelection);
    t->setShowGrid(false);
    t->setWordWrap(false);
    auto *h = t->horizontalHeader();
    h->setSectionResizeMode(0, QHeaderView::Stretch);
    h->setSectionResizeMode(1, QHeaderView::ResizeToContents);
    h->setSectionResizeMode(2, QHeaderView::ResizeToContents);
    h->setSectionResizeMode(3, QHeaderView::ResizeToContents);
    return t;
}

QWidget *MainWindow::buildTracksPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *head = new QHBoxLayout;
    m_tracksHeader = new QLabel(tr("Liked Songs"));
    QFont f = m_tracksHeader->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    m_tracksHeader->setFont(f);
    head->addWidget(m_tracksHeader);
    head->addStretch(1);
    // "Download album"/"Download playlist" — visible only while an album or a
    // playlist is displayed (see updateListDownloadButton), premium-gated like
    // the single-track download.
    m_downloadListBtn = new QPushButton(tr("Download album"));
    m_downloadListBtn->setVisible(false);
    connect(m_downloadListBtn, &QPushButton::clicked, this,
            &MainWindow::downloadCurrentList);
    head->addWidget(m_downloadListBtn);
    v->addLayout(head);

    m_trackTable = makeTrackTable();
    // cellActivated fires on Enter + (single/double)-click per the KDE setting.
    connect(m_trackTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_tableTracks, row); });
    installTrackMenu(m_trackTable, &m_tableTracks);
    v->addWidget(m_trackTable, 1);
    return w;
}

QWidget *MainWindow::buildPlaylistsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *head = new QHBoxLayout;
    auto *title = new QLabel(tr("Your Playlists"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    head->addWidget(title);
    head->addStretch(1);
    auto *newBtn = new QPushButton(QStringLiteral("＋ ") + tr("New Playlist"));
    connect(newBtn, &QPushButton::clicked, this, &MainWindow::createPlaylist);
    head->addWidget(newBtn);
    v->addLayout(head);

    m_playlistGrid = new QListWidget;
    m_playlistGrid->setViewMode(QListView::IconMode);
    m_playlistGrid->setIconSize(QSize(120, 120));
    m_playlistGrid->setGridSize(QSize(150, 180));
    m_playlistGrid->setResizeMode(QListView::Adjust);
    m_playlistGrid->setMovement(QListView::Static);
    m_playlistGrid->setWordWrap(true);
    connect(m_playlistGrid, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int idx = it->data(Qt::UserRole).toInt();
        if (idx >= 0 && idx < m_playlists.size())
            openPlaylist(m_playlists[idx]);
    });
    // Right-click: open / rename / delete a playlist.
    m_playlistGrid->setContextMenuPolicy(Qt::CustomContextMenu);
    connect(m_playlistGrid, &QWidget::customContextMenuRequested, this,
            [this](const QPoint &pos) {
                QListWidgetItem *it = m_playlistGrid->itemAt(pos);
                if (!it)
                    return;
                const int idx = it->data(Qt::UserRole).toInt();
                if (idx < 0 || idx >= m_playlists.size())
                    return;
                const Playlist p = m_playlists[idx];
                QMenu menu(this);
                QAction *open = menu.addAction(tr("Open"));
                QAction *ren  = menu.addAction(tr("Rename…"));
                QAction *del  = menu.addAction(tr("Delete…"));
                QAction *chosen = menu.exec(m_playlistGrid->viewport()->mapToGlobal(pos));
                if (chosen == open)      openPlaylist(p);
                else if (chosen == ren)  renamePlaylist(p);
                else if (chosen == del)  deletePlaylist(p);
            });
    v->addWidget(m_playlistGrid, 1);
    return w;
}

QWidget *MainWindow::buildSearchPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *top = new QHBoxLayout;
    m_searchEdit = new QLineEdit;
    m_searchEdit->setPlaceholderText(tr("Search Deezer…"));
    auto *btn = new QPushButton(tr("Search"));
    top->addWidget(m_searchEdit, 1);
    top->addWidget(btn);
    v->addLayout(top);

    v->addWidget(new QLabel(tr("Tracks")));
    m_searchTrackTable = makeTrackTable();
    connect(m_searchTrackTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_searchTracks, row); });
    installTrackMenu(m_searchTrackTable, &m_searchTracks);
    v->addWidget(m_searchTrackTable, 2);

    v->addWidget(new QLabel(tr("Albums & Playlists")));
    m_searchResults = new QListWidget;
    m_searchResults->setViewMode(QListView::IconMode);
    m_searchResults->setIconSize(QSize(110, 110));
    m_searchResults->setGridSize(QSize(140, 165));
    m_searchResults->setResizeMode(QListView::Adjust);
    m_searchResults->setMovement(QListView::Static);
    m_searchResults->setWordWrap(true);
    connect(m_searchResults, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int kind = it->data(Qt::UserRole).toInt();       // 0 album, 1 playlist, 2 artist
        const int idx  = it->data(Qt::UserRole + 1).toInt();
        if (kind == 0 && idx < m_searchAlbums.size())
            openAlbum(m_searchAlbums[idx]);
        else if (kind == 1 && idx < m_searchPlaylists.size())
            openPlaylist(m_searchPlaylists[idx]);
        else if (kind == 2 && idx < m_searchArtists.size())
            openArtist(m_searchArtists[idx].id);
    });
    v->addWidget(m_searchResults, 1);

    connect(btn, &QPushButton::clicked, this, &MainWindow::runSearch);
    connect(m_searchEdit, &QLineEdit::returnPressed, this, &MainWindow::runSearch);
    return w;
}

// ---- transport bar --------------------------------------------------------

QWidget *MainWindow::buildTransport() {
    auto *bar = new QWidget;
    bar->setFixedHeight(76);
    auto *h = new QHBoxLayout(bar);
    h->setContentsMargins(10, 8, 10, 8);

    m_cover = new QLabel;
    m_cover->setFixedSize(56, 56);
    m_cover->setScaledContents(true);
    m_cover->setPixmap(placeholderPix(56));
    h->addWidget(m_cover);

    // Small "E" badge — visible only when the current track is flagged explicit.
    m_explicitBadge = new QLabel(QStringLiteral("E"));
    m_explicitBadge->setStyleSheet(
        "QLabel{background:#666666;color:#FFFFFF;border-radius:3px;"
        "padding:0px 3px;font-size:10px;font-weight:bold;}");
    m_explicitBadge->setAlignment(Qt::AlignCenter);
    m_explicitBadge->setFixedHeight(16);
    m_explicitBadge->setVisible(false);
    h->addWidget(m_explicitBadge);

    // "Preview" badge — visible only while the engine is streaming a 30-second
    // preview instead of the full track (polled from DZIsPreview in tick()).
    m_previewBadge = new QLabel(tr("Preview"));
    m_previewBadge->setStyleSheet(
        "QLabel{background:#A238FF;color:#FFFFFF;border-radius:3px;"
        "padding:0px 4px;font-size:10px;font-weight:bold;}");
    m_previewBadge->setAlignment(Qt::AlignCenter);
    m_previewBadge->setFixedHeight(16);
    m_previewBadge->setVisible(false);
    h->addWidget(m_previewBadge);

    // "Offline" badge — visible only when the current track is saved for offline
    // playback (its id is in m_offlineIds; see refreshOfflineIndicators).
    m_offlineBadge = new QLabel(tr("Offline"));
    m_offlineBadge->setStyleSheet(
        "QLabel{background:#1DB954;color:#FFFFFF;border-radius:3px;"
        "padding:0px 4px;font-size:10px;font-weight:bold;}");
    m_offlineBadge->setAlignment(Qt::AlignCenter);
    m_offlineBadge->setFixedHeight(16);
    m_offlineBadge->setVisible(false);
    h->addWidget(m_offlineBadge);

    m_nowPlaying = new QLabel(tr("Not playing"));
    m_nowPlaying->setMinimumWidth(180);
    // Right-click the now-playing title to save the current track for offline.
    m_nowPlaying->setContextMenuPolicy(Qt::CustomContextMenu);
    connect(m_nowPlaying, &QWidget::customContextMenuRequested, this,
            [this](const QPoint &pos) {
                if (!m_hasCurrent || m_currentIsEpisode)
                    return;
                QMenu menu(this);
                QAction *off = menu.addAction(tr("Download for offline"));
                if (m_offlineIds.contains(m_current.id)) {
                    off->setEnabled(false);
                    off->setText(tr("Available offline"));
                } else if (!m_premium) {
                    off->setEnabled(false);
                    off->setToolTip(tr("Requires a paid Deezer plan"));
                    menu.setToolTipsVisible(true);
                }
                if (menu.exec(m_nowPlaying->mapToGlobal(pos)) == off &&
                    off->isEnabled())
                    downloadForOffline(m_current);
            });
    h->addWidget(m_nowPlaying, 0);

    // Lyrics / Artist detail for the current track, sitting next to its title.
    auto *lyricsBtn = new QToolButton;
    {
        QIcon ico = QIcon::fromTheme(QStringLiteral("view-media-lyrics"));
        if (ico.isNull())
            ico = QIcon::fromTheme(QStringLiteral("view-media-lyrics-symbolic"));
        if (ico.isNull())
            ico = QIcon::fromTheme(QStringLiteral("text-x-generic"));
        lyricsBtn->setIcon(ico);
    }
    lyricsBtn->setText(QString());
    lyricsBtn->setIconSize(QSize(22, 22));
    lyricsBtn->setAutoRaise(true);
    lyricsBtn->setToolTip(tr("Lyrics"));
    connect(lyricsBtn, &QToolButton::clicked, this, &MainWindow::openLyrics);
    h->addWidget(lyricsBtn);

    auto *artistBtn = new QToolButton;
    {
        QIcon ico = QIcon::fromTheme(QStringLiteral("view-media-artist"));
        if (ico.isNull())
            ico = QIcon::fromTheme(QStringLiteral("im-user"));
        if (ico.isNull())
            ico = QIcon::fromTheme(QStringLiteral("user"));
        artistBtn->setIcon(ico);
    }
    artistBtn->setText(QString());
    artistBtn->setIconSize(QSize(22, 22));
    artistBtn->setAutoRaise(true);
    artistBtn->setToolTip(tr("Artist"));
    connect(artistBtn, &QToolButton::clicked, this, &MainWindow::openArtistForCurrent);
    h->addWidget(artistBtn);

    // Heart toggle: like/unlike the current track (DZAddFavorite/DZRemoveFavorite).
    m_likeBtn = new QToolButton;
    m_likeBtn->setAutoRaise(true);
    m_likeBtn->setCheckable(true);
    m_likeBtn->setIconSize(QSize(22, 22));
    connect(m_likeBtn, &QToolButton::clicked, this, &MainWindow::toggleLikeCurrent);
    setLikeButton(false);
    h->addWidget(m_likeBtn);

    m_prevBtn = mediaButton(style(), QStyle::SP_MediaSkipBackward);
    m_playBtn = mediaButton(style(), QStyle::SP_MediaPlay);
    m_nextBtn = mediaButton(style(), QStyle::SP_MediaSkipForward);
    connect(m_prevBtn, &QToolButton::clicked, this, &MainWindow::prev);
    connect(m_playBtn, &QToolButton::clicked, this, &MainWindow::togglePause);
    connect(m_nextBtn, &QToolButton::clicked, this, &MainWindow::next);
    h->addWidget(m_prevBtn);
    h->addWidget(m_playBtn);
    h->addWidget(m_nextBtn);

    m_posLabel = new QLabel("0:00");
    h->addWidget(m_posLabel);
    m_seek = new QSlider(Qt::Horizontal);
    m_seek->setRange(0, 1);
    // Click anywhere on the groove to jump straight there (drag still seeks via
    // the sliderPressed/Released handlers below). Parented to the slider so the
    // proxy style is destroyed with it.
    auto *seekStyle = new DirectJumpSliderStyle;
    seekStyle->setParent(m_seek);
    m_seek->setStyle(seekStyle);
    h->addWidget(m_seek, 1);
    m_durLabel = new QLabel("0:00");
    h->addWidget(m_durLabel);

    connect(m_seek, &QSlider::sliderPressed, this, [this] { m_seeking = true; });
    connect(m_seek, &QSlider::sliderReleased, this, [this] {
        m_seeking = false;
        // DZSeek is a blocking HTTP request when routed to a Connect device.
        const qint64 pos = m_seek->value();
        QtConcurrent::run([pos] { DZSeek(pos); });
        if (m_mpris)
            m_mpris->notifySeeked(pos); // discontinuous jump
    });
    connect(m_seek, &QSlider::valueChanged, this, [this](int v) {
        if (m_seeking)
            m_posLabel->setText(timeText(v));
    });

    m_shuffleBtn = new QToolButton;
    m_shuffleBtn->setIcon(QIcon::fromTheme(QStringLiteral("media-playlist-shuffle")));
    m_shuffleBtn->setText(QString());
    m_shuffleBtn->setIconSize(QSize(22, 22));
    m_shuffleBtn->setCheckable(true);
    m_shuffleBtn->setAutoRaise(true);
    m_shuffleBtn->setToolTip(tr("Shuffle"));
    connect(m_shuffleBtn, &QToolButton::toggled, this, [this](bool on) {
        m_shuffle = on;
        QtConcurrent::run([on] { DZSetShuffle(on ? 1 : 0); });
    });
    h->addWidget(m_shuffleBtn);

    m_repeatBtn = new QToolButton;
    m_repeatBtn->setIcon(QIcon::fromTheme(QStringLiteral("media-playlist-repeat")));
    m_repeatBtn->setText(QString());
    m_repeatBtn->setIconSize(QSize(22, 22));
    m_repeatBtn->setCheckable(true);
    m_repeatBtn->setChecked(false);
    m_repeatBtn->setAutoRaise(true);
    m_repeatBtn->setToolTip(tr("Repeat: off"));
    connect(m_repeatBtn, &QToolButton::clicked, this, [this] {
        // Click cycles off -> all -> one; paint the new state and command the
        // engine. tick() reconciles the button back to engine truth afterwards.
        applyRepeatButton((m_repeat + 1) % 3);
        const int mode = m_repeat;
        QtConcurrent::run([mode] { DZSetRepeat(mode); });
    });
    h->addWidget(m_repeatBtn);

    // OpenDeezer Connect: cast playback to another OpenDeezer device on the LAN.
    m_connectBtn = new QToolButton;
    {
        QIcon ico = QIcon::fromTheme(QStringLiteral("network-wireless"));
        if (ico.isNull())
            ico = QIcon::fromTheme(QStringLiteral("video-display"));
        m_connectBtn->setIcon(ico);
    }
    m_connectBtn->setText(QString());
    m_connectBtn->setIconSize(QSize(22, 22));
    m_connectBtn->setAutoRaise(true);
    m_connectBtn->setToolTip(tr("Connect to a device"));
    connect(m_connectBtn, &QToolButton::clicked, this, &MainWindow::openConnectPicker);
    h->addWidget(m_connectBtn);

    // Up-Next: show/hide the queue editor dock. Mirrors (and drives) the dock's
    // own toggle action so the button's checked state tracks visibility.
    if (m_queueDock) {
        auto *queueBtn = new QToolButton;
        {
            QIcon ico = QIcon::fromTheme(QStringLiteral("view-media-playlist"));
            if (ico.isNull())
                ico = QIcon::fromTheme(QStringLiteral("media-playlist-normal"));
            if (ico.isNull())
                ico = QIcon::fromTheme(QStringLiteral("format-list-unordered"));
            queueBtn->setIcon(ico);
        }
        queueBtn->setText(QString());
        queueBtn->setIconSize(QSize(22, 22));
        queueBtn->setAutoRaise(true);
        queueBtn->setCheckable(true);
        queueBtn->setChecked(m_queueDock->isVisible());
        queueBtn->setToolTip(tr("Up Next"));
        connect(queueBtn, &QToolButton::clicked, this,
                [this] { m_queueDock->setVisible(!m_queueDock->isVisible()); });
        // Keep the button in sync when the dock is closed via its own title-bar X
        // or the View menu.
        connect(m_queueDock->toggleViewAction(), &QAction::toggled, queueBtn,
                &QToolButton::setChecked);
        h->addWidget(queueBtn);
    }

    {
        auto *volLabel = new QLabel;
        volLabel->setPixmap(
            QIcon::fromTheme(QStringLiteral("audio-volume-high")).pixmap(16, 16));
        volLabel->setToolTip(tr("Volume"));
        h->addWidget(volLabel);
    }
    m_vol = new QSlider(Qt::Horizontal);
    m_vol->setRange(0, 100);
    m_vol->setValue(100);
    m_vol->setFixedWidth(110);
    connect(m_vol, &QSlider::valueChanged, this, &MainWindow::setVolume);
    h->addWidget(m_vol);

    // Deezer-purple accent, scoped to the accent widgets only so the rest of the
    // app keeps the native Breeze style.
    const QString sliderQss = QString("QSlider::sub-page:horizontal{background:%1;border-radius:2px;}"
                                      "QSlider::handle:horizontal{background:%1;width:12px;"
                                      "margin:-4px 0;border-radius:6px;}")
                                  .arg(kAccent);
    m_seek->setStyleSheet(sliderQss);
    m_vol->setStyleSheet(sliderQss);
    m_playBtn->setStyleSheet(QString("QToolButton{color:%1;}").arg(kAccent));
    const QString toggleQss = QString("QToolButton:checked{color:%1;font-weight:bold;}").arg(kAccent);
    m_shuffleBtn->setStyleSheet(toggleQss);
    m_repeatBtn->setStyleSheet(toggleQss);
    return bar;
}

// ---- login ----------------------------------------------------------------

void MainWindow::startLogin() {
    const QString arl = loadARL();
    if (arl.isEmpty()) {
        // No stored ARL — offer the webview / manual-entry login dialog.
        promptLogin();
        return;
    }
    const QByteArray ab = arl.toUtf8();
    // DZInit blocks on the network — never on the GUI thread.
    QtConcurrent::run([this, ab] {
        const int ok = DZInit(cstr(ab));
        // Plan + entitlements (cheap cached read once logged in).
        QByteArray acct;
        if (ok)
            acct = takeJson(DZAccountJSON());
        QMetaObject::invokeMethod(this, [this, ok, acct] {
            if (ok) {
                finishLogin(acct);
            } else if (DZLoginErrorKind() == 2) {
                // The machine is offline — the stored ARL may well be fine, so
                // don't push the user to re-authenticate. Show a blocking
                // No-Internet page whose Retry re-runs this login.
                showNoInternet();
            } else {
                // The stored ARL is stale — fall back to the login dialog so the
                // user can re-authenticate without editing files by hand.
                statusBar()->showMessage(tr("Session expired — sign in again"), 4000);
                promptLogin();
            }
        }, Qt::QueuedConnection);
    });
}

// Show the Deezer login dialog (embedded webview with automatic arl capture, or
// manual ARL entry). The dialog verifies + persists the ARL with DZInit itself,
// so on Accepted the engine is already logged in; we just bring the app up.
void MainWindow::promptLogin() {
    statusBar()->showMessage(tr("Log in to continue"));
    LoginDialog dlg(arlConfigPath(), this);
    if (dlg.exec() == QDialog::Accepted) {
        finishLogin(takeJson(DZAccountJSON()));
    } else {
        statusBar()->showMessage(tr("Not logged in"));
    }
}

// Post-login bring-up shared by the auto-login (stored ARL) and dialog paths.
// The engine is already logged in by the time this runs; acct is DZAccountJSON.
void MainWindow::finishLogin(const QByteArray &acct) {
    m_loggedIn = true;
    applyAccount(acct);          // tier + HiFi/HQ entitlements
    // The engine now streams full standard-quality (128 kbps) tracks for Free
    // accounts too (some tracks may still be preview-only, reflected by the
    // transport badge), so both tiers bring up the same browsing/playback UI —
    // there's no longer a blocking "Premium required" gate. m_premium still gates
    // the Download action and drives the Free hint shown below.
    // Any account (re-)logged in: make sure the app UI is the visible page.
    if (m_rootStack)
        m_rootStack->setCurrentIndex(0);
    m_lastFinished = DZFinishedCount();
    m_vol->setValue(static_cast<int>(qRound(DZVolume() * 100)));
    // ReplayGain: apply the persisted preference, then mirror back the engine's
    // actual state from DZReplayGain.
    DZSetReplayGain(SettingsDialog::loadReplayGain(settingsPath()) ? 1 : 0);
    m_replayGain = (DZReplayGain() != 0);
    // Gapless / crossfade / output device: apply persisted prefs and mirror the
    // engine's actual state back into the cached fields.
    DZSetGapless(SettingsDialog::loadGapless(settingsPath()) ? 1 : 0);
    m_gapless = (DZGapless() != 0);
    DZSetCrossfadeMS(SettingsDialog::loadCrossfadeMs(settingsPath()));
    m_crossfadeMs = DZCrossfadeMS();
    const QString dev = SettingsDialog::loadOutputDevice(settingsPath());
    if (!dev.isEmpty()) {
        const QByteArray db = dev.toUtf8();
        DZSetAudioDevice(cstr(db));
    }
    applyQuality(m_quality);     // apply persisted quality (+ entitlement note)
    refreshConnectButton();      // reflect any active OpenDeezer Connect device
    seedFavorites();             // authoritative liked-ids mirror for truthful hearts
    if (m_queueDock) {           // reveal the Up-Next dock now that we're logged in
        m_queueDock->show();
        refreshQueuePanel();
    }
    m_poll->start();
    // Qt makes the first sidebar row current when items are added (before login),
    // so setCurrentRow(0) is a no-op here and would NOT re-fire onSidebarChanged —
    // load Home explicitly now that the engine is authenticated (was: empty Home).
    m_sidebar->setCurrentRow(0);
    m_stack->setCurrentIndex(0);
    loadHome();
    const QString conn = (m_haveAccount && !m_accountName.isEmpty())
        ? m_accountName + " · " + m_accountOffer
        : tr("Connected");
    statusBar()->showMessage(conn, 4000);

    // Free tier streams at standard quality (128 kbps) — surface it unobtrusively
    // as a permanent label in the status-bar corner (the per-track preview badge
    // in the transport bar still reflects any preview-only track live). Built
    // once, then toggled per login so switching to a Premium account hides it.
    if (!m_premium) {
        if (!m_freeHint) {
            m_freeHint = new QLabel(tr("Free · standard quality (128 kbps)"));
            m_freeHint->setToolTip(tr("Deezer Free streams at standard quality "
                                      "(128 kbps). Upgrade for High and HiFi "
                                      "quality, and for downloads."));
            QFont ff = m_freeHint->font();
            ff.setPointSize(qMax(1, ff.pointSize() - 1));
            m_freeHint->setFont(ff);
            statusBar()->addPermanentWidget(m_freeHint);
        }
        m_freeHint->show();
    } else if (m_freeHint) {
        m_freeHint->hide();
    }
}

// Login failed because the machine is offline (DZLoginErrorKind() == 2). Swap
// the whole window to a blocking "No Internet" page — the live app widgets stay
// alive on stack page 0 but are unreachable until a successful retry. The Retry
// button re-runs startLogin(), which reads the stored ARL exactly as at launch
// and, on success, finishLogin() switches m_rootStack back to index 0.
void MainWindow::showNoInternet() {
    if (m_poll)
        m_poll->stop();

    // Build the page once; on later offline retries just re-show it.
    if (!m_noInternetPage) {
        m_noInternetPage = new QWidget;
        auto *outer = new QVBoxLayout(m_noInternetPage);
        outer->addStretch(1);

        auto *title = new QLabel(tr("No Internet Connection"));
        title->setAlignment(Qt::AlignCenter);
        title->setWordWrap(true);
        QFont tf = title->font();
        tf.setPointSize(tf.pointSize() + 8);
        tf.setBold(true);
        title->setFont(tf);
        title->setStyleSheet(QString("color:%1;").arg(kAccent));
        outer->addWidget(title);

        outer->addSpacing(12);

        auto *body = new QLabel(tr("Check your connection and try again."));
        body->setAlignment(Qt::AlignCenter);
        body->setWordWrap(true);
        body->setMaximumWidth(560);
        // Centre the constrained body within the page.
        auto *bodyRow = new QHBoxLayout;
        bodyRow->addStretch(1);
        bodyRow->addWidget(body);
        bodyRow->addStretch(1);
        outer->addLayout(bodyRow);

        outer->addSpacing(24);

        auto *retryBtn = new QPushButton(tr("Retry"));
        connect(retryBtn, &QPushButton::clicked, this, &MainWindow::startLogin);
        auto *btnRow = new QHBoxLayout;
        btnRow->addStretch(1);
        btnRow->addWidget(retryBtn);
        btnRow->addStretch(1);
        outer->addLayout(btnRow);

        outer->addStretch(1);
        // Appended after the app (0); becomes index 1 at a cold offline launch,
        // or index 2 if the Free-account block page was created first. We select
        // it by pointer below, so the exact index never matters.
        m_rootStack->addWidget(m_noInternetPage);
    }

    m_rootStack->setCurrentWidget(m_noInternetPage);
    statusBar()->showMessage(tr("No Internet Connection"));
}

// ---- browse ---------------------------------------------------------------

// Seed the liked-ids mirror from the engine at login so the transport heart is
// truthful for EVERY track from the first play — not only after the Liked Songs
// view has been visited. DZFavoriteIDsJSON fetches favorites over the network,
// so it runs on a worker; the rebuild + heart repaint marshal back to the GUI
// thread. loadFavorites() later rebuilds this from the full favorites load.
void MainWindow::seedFavorites() {
    if (!m_loggedIn)
        return;
    QtConcurrent::run([this] {
        const QByteArray j = takeJson(DZFavoriteIDsJSON());
        QMetaObject::invokeMethod(this, [this, j] {
            m_likedIds.clear();
            for (const QJsonValue &v : QJsonDocument::fromJson(j).array())
                m_likedIds.insert(v.toString());
            refreshLikeButton();
        }, Qt::QueuedConnection);
    });
}

void MainWindow::loadFavorites() {
    if (!m_loggedIn)
        return;
    m_tracksHeader->setText(tr("Liked Songs"));
    m_currentPlaylistId.clear();
    m_currentAlbumId.clear();
    updateListDownloadButton();          // Liked Songs isn't a downloadable unit
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        const QVector<Track> tracks = parseTracks(takeJson(DZFavoritesJSON()));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            // This IS the full liked set — clear + rebuild the mirror so unlikes
            // made elsewhere (phone, web, another device) are pruned, not just
            // additions accumulated.
            m_likedIds.clear();
            for (const Track &t : tracks)
                m_likedIds.insert(t.id);
            refreshLikeButton();
            fillTrackTable(m_trackTable, tracks, gen);
            statusBar()->showMessage(tr("Liked Songs — %n track(s)", "", int(tracks.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

// Flow: the user's personalised stream. Loads into the shared track table (like
// Liked Songs) and starts playing from the top.
void MainWindow::loadFlow() {
    if (!m_loggedIn)
        return;
    m_tracksHeader->setText(QStringLiteral("Flow"));
    m_currentPlaylistId.clear();
    m_currentAlbumId.clear();
    updateListDownloadButton();          // Flow isn't a downloadable unit
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        const QVector<Track> tracks = parseTracks(takeJson(DZFlowJSON()));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            statusBar()->showMessage(QStringLiteral("Flow — ") + tr("%n track(s)", "", int(tracks.size())), 3000);
            if (!tracks.isEmpty())
                playFrom(tracks, 0); // Flow auto-plays
        }, Qt::QueuedConnection);
    });
}

// ---- start radio (song/artist mix) ----------------------------------------

// "Start radio" from a track: DZTrackMixJSON returns the same {tracks:[...]}
// shape as Flow, so this mirrors loadFlow — fetch on a worker, then playMix.
void MainWindow::startTrackRadio(const QString &trackId) {
    if (!m_loggedIn || trackId.isEmpty())
        return;
    statusBar()->showMessage(tr("Starting radio…"));
    const QByteArray idb = trackId.toUtf8();
    QtConcurrent::run([this, idb] {
        const QVector<Track> tracks = parseTracks(takeJson(DZTrackMixJSON(cstr(idb))));
        QMetaObject::invokeMethod(this, [this, tracks] { playMix(tracks); },
                                  Qt::QueuedConnection);
    });
}

// "Start radio" from an artist: DZArtistMixJSON, same {tracks:[...]} shape.
void MainWindow::startArtistRadio(const QString &artistId) {
    if (!m_loggedIn || artistId.isEmpty())
        return;
    statusBar()->showMessage(tr("Starting radio…"));
    const QByteArray idb = artistId.toUtf8();
    QtConcurrent::run([this, idb] {
        const QVector<Track> tracks = parseTracks(takeJson(DZArtistMixJSON(cstr(idb))));
        QMetaObject::invokeMethod(this, [this, tracks] { playMix(tracks); },
                                  Qt::QueuedConnection);
    });
}

// Shared radio landing: show the mix in the shared track table (stack 1) and
// auto-play from the top, exactly like Flow.
void MainWindow::playMix(const QVector<Track> &tracks) {
    if (tracks.isEmpty()) {
        statusBar()->showMessage(tr("No radio available for this"), 3000);
        return;
    }
    m_tracksHeader->setText(tr("Radio"));
    m_currentPlaylistId.clear();
    m_currentAlbumId.clear();
    updateListDownloadButton();
    const int gen = ++m_artGen;
    m_tableTracks = tracks;
    fillTrackTable(m_trackTable, tracks, gen);
    m_stack->setCurrentIndex(1);
    playFrom(tracks, 0);
    statusBar()->showMessage(tr("Radio — %n track(s)", "", int(tracks.size())), 3000);
}

// Global charts: tracks fill the charts track table; albums, artists and
// playlists fill the grid below (each tile opens its existing detail view).
void MainWindow::loadCharts() {
    if (!m_loggedIn)
        return;
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        const QByteArray j = takeJson(DZChartsJSON());
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject obj = QJsonDocument::fromJson(j).object();
            const int gen = ++m_artGen;

            m_chartsTracks.clear();
            for (const QJsonValue &v : obj.value("tracks").toArray())
                m_chartsTracks.push_back(parseTrack(v.toObject()));
            fillTrackTable(m_chartsTrackTable, m_chartsTracks, gen);

            m_chartsAlbums.clear();
            m_chartsArtists.clear();
            m_chartsPlaylists.clear();
            for (const QJsonValue &v : obj.value("albums").toArray())
                m_chartsAlbums.push_back(parseAlbum(v.toObject()));
            for (const QJsonValue &v : obj.value("artists").toArray())
                m_chartsArtists.push_back(parseArtistInfo(v.toObject()));
            for (const QJsonValue &v : obj.value("playlists").toArray())
                m_chartsPlaylists.push_back(parsePlaylist(v.toObject()));

            // kind tags in UserRole: 0 album, 1 playlist, 2 artist.
            m_chartsResults->clear();
            auto addTile = [this, gen](const QString &text, const QString &art,
                                       int kind, int idx) {
                auto *it = new QListWidgetItem(QIcon(placeholderPix(110)), text);
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, kind);
                it->setData(Qt::UserRole + 1, idx);
                m_chartsResults->addItem(it);
                if (!art.isEmpty())
                    fetchImage(art, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            };
            for (int i = 0; i < m_chartsAlbums.size(); ++i)
                addTile(m_chartsAlbums[i].name + "\n" + m_chartsAlbums[i].artistLine,
                        m_chartsAlbums[i].artworkUrl, 0, i);
            for (int i = 0; i < m_chartsArtists.size(); ++i)
                addTile(m_chartsArtists[i].name + "\n" + tr("Artist"),
                        m_chartsArtists[i].artworkUrl, 2, i);
            for (int i = 0; i < m_chartsPlaylists.size(); ++i)
                addTile(m_chartsPlaylists[i].name + "\n" + m_chartsPlaylists[i].owner,
                        m_chartsPlaylists[i].artworkUrl, 1, i);

            statusBar()->showMessage(
                tr("Charts — %n track(s)", "", int(m_chartsTracks.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::loadPlaylists() {
    if (!m_loggedIn)
        return;
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        QVector<Playlist> ps;
        const QJsonObject obj = QJsonDocument::fromJson(takeJson(DZPlaylistsJSON())).object();
        for (const QJsonValue &v : obj.value("playlists").toArray())
            ps.push_back(parsePlaylist(v.toObject()));
        QMetaObject::invokeMethod(this, [this, ps] {
            const int gen = ++m_artGen;
            m_playlists = ps;
            m_playlistGrid->clear();
            for (int i = 0; i < ps.size(); ++i) {
                const Playlist &p = ps[i];
                auto *it = new QListWidgetItem(
                    QIcon(placeholderPix(120)),
                    p.name + "\n" + tr("%n track(s)", "", p.trackCount));
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, i);
                m_playlistGrid->addItem(it);
                if (!p.artworkUrl.isEmpty())
                    fetchImage(p.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            120, 120, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            statusBar()->showMessage(tr("%n playlist(s)", "", int(ps.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openPlaylist(const Playlist &p) {
    statusBar()->showMessage(tr("Loading…"));
    m_tracksHeader->setText(p.owner.isEmpty() ? p.name : p.name + "   ·   " + p.owner);
    m_currentPlaylistId = p.id; // enables "Remove from this playlist" in the track menu
    m_currentPlaylistName = p.name;
    m_currentAlbumId.clear();
    updateListDownloadButton(); // shows "Download playlist"
    const QByteArray id = p.id.toUtf8();
    QtConcurrent::run([this, id] {
        const QVector<Track> tracks = parseTracks(takeJson(DZPlaylistTracksJSON(cstr(id))));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            m_stack->setCurrentIndex(1); // track table page
            statusBar()->showMessage(tr("%n track(s)", "", int(tracks.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openAlbum(const Album &a) {
    statusBar()->showMessage(tr("Loading…"));
    m_tracksHeader->setText(a.artistLine.isEmpty() ? a.name : a.name + "   ·   " + a.artistLine);
    m_currentPlaylistId.clear(); // album is not a removable-from playlist
    m_currentAlbumId = a.id;
    m_currentAlbumName = a.name;
    updateListDownloadButton(); // shows "Download album"
    const QByteArray id = a.id.toUtf8();
    QtConcurrent::run([this, id] {
        const QVector<Track> tracks = parseTracks(takeJson(DZAlbumTracksJSON(cstr(id))));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            m_stack->setCurrentIndex(1); // track table page
            statusBar()->showMessage(tr("%n track(s)", "", int(tracks.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::runSearch() {
    if (!m_loggedIn)
        return;
    const QString q = m_searchEdit->text().trimmed();
    if (q.isEmpty())
        return;
    statusBar()->showMessage(tr("Searching…"));
    const QByteArray qb = q.toUtf8();
    QtConcurrent::run([this, qb] {
        const QByteArray j = takeJson(DZSearchJSON(cstr(qb)));
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject obj = QJsonDocument::fromJson(j).object();
            const int gen = ++m_artGen;

            m_searchTracks.clear();
            for (const QJsonValue &v : obj.value("tracks").toArray())
                m_searchTracks.push_back(parseTrack(v.toObject()));
            fillTrackTable(m_searchTrackTable, m_searchTracks, gen);

            m_searchAlbums.clear();
            m_searchArtists.clear();
            m_searchPlaylists.clear();
            for (const QJsonValue &v : obj.value("albums").toArray())
                m_searchAlbums.push_back(parseAlbum(v.toObject()));
            for (const QJsonValue &v : obj.value("artists").toArray())
                m_searchArtists.push_back(parseArtistInfo(v.toObject()));
            for (const QJsonValue &v : obj.value("playlists").toArray())
                m_searchPlaylists.push_back(parsePlaylist(v.toObject()));

            // kind tags in UserRole: 0 album, 2 artist, 1 playlist (matches the
            // itemActivated router above and the charts grid convention).
            m_searchResults->clear();
            for (int i = 0; i < m_searchAlbums.size(); ++i) {
                const Album &a = m_searchAlbums[i];
                auto *it = new QListWidgetItem(QIcon(placeholderPix(110)), a.name + "\n" + a.artistLine);
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, 0);
                it->setData(Qt::UserRole + 1, i);
                m_searchResults->addItem(it);
                if (!a.artworkUrl.isEmpty())
                    fetchImage(a.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            for (int i = 0; i < m_searchArtists.size(); ++i) {
                const ArtistInfo &a = m_searchArtists[i];
                auto *it = new QListWidgetItem(QIcon(placeholderPix(110)), a.name + "\n" + tr("Artist"));
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, 2);
                it->setData(Qt::UserRole + 1, i);
                m_searchResults->addItem(it);
                if (!a.artworkUrl.isEmpty())
                    fetchImage(a.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            for (int i = 0; i < m_searchPlaylists.size(); ++i) {
                const Playlist &p = m_searchPlaylists[i];
                auto *it = new QListWidgetItem(QIcon(placeholderPix(110)), p.name + "\n" + p.owner);
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, 1);
                it->setData(Qt::UserRole + 1, i);
                m_searchResults->addItem(it);
                if (!p.artworkUrl.isEmpty())
                    fetchImage(p.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            statusBar()->clearMessage();
        }, Qt::QueuedConnection);
    });
}

// ---- favourites (like / unlike) -------------------------------------------

// Paint the like button for the given liked state.
void MainWindow::setLikeButton(bool liked) {
    if (!m_likeBtn)
        return;
    QIcon icon = QIcon::fromTheme(QStringLiteral("emblem-favorite"));
    if (icon.isNull())
        icon = QIcon::fromTheme(QStringLiteral("emblem-favorite-symbolic"));
    if (icon.isNull())
        icon = QIcon::fromTheme(QStringLiteral("starred"));
    m_likeBtn->setIcon(icon);
    m_likeBtn->setText(QString());
    m_likeBtn->setChecked(liked);
    m_likeBtn->setStyleSheet(liked ? QString("QToolButton{color:%1;}").arg(kAccent)
                                   : QString());
    m_likeBtn->setToolTip(liked ? tr("Remove from Liked Songs")
                                : tr("Add to Liked Songs"));
}

// Refresh the heart from the local liked-state mirror for the current track.
void MainWindow::refreshLikeButton() {
    setLikeButton(m_hasCurrent && !m_currentIsEpisode &&
                  m_likedIds.contains(m_current.id));
}

// Transport heart: like/unlike whatever is playing. No is-liked query exists, so
// the intended state is shown immediately and reconciled from the result.
void MainWindow::toggleLikeCurrent() {
    if (!m_hasCurrent || m_current.id.isEmpty()) {
        statusBar()->showMessage(tr("Nothing is playing"), 3000);
        return;
    }
    if (m_currentIsEpisode) {
        statusBar()->showMessage(tr("Podcast episodes can't be liked"), 3000);
        return;
    }
    const bool like = !m_likedIds.contains(m_current.id);
    setLikeButton(like); // optimistic; reconciled by likeTrack's result
    likeTrack(m_current.id, like);
}

// One-shot like/unlike on a worker; updates the local mirror + heart on success.
void MainWindow::likeTrack(const QString &trackId, bool like) {
    if (!m_loggedIn || trackId.isEmpty())
        return;
    const QByteArray idb = trackId.toUtf8();
    QtConcurrent::run([this, idb, trackId, like] {
        const int okRes = like ? DZAddFavorite(cstr(idb)) : DZRemoveFavorite(cstr(idb));
        QMetaObject::invokeMethod(this, [this, okRes, trackId, like] {
            if (okRes) {
                if (like)
                    m_likedIds.insert(trackId);
                else
                    m_likedIds.remove(trackId);
                statusBar()->showMessage(like ? tr("Added to Liked Songs")
                                              : tr("Removed from Liked Songs"),
                                         3000);
            } else {
                statusBar()->showMessage(tr("Couldn't update Liked Songs"), 3000);
            }
            if (m_hasCurrent && m_current.id == trackId)
                refreshLikeButton(); // paint the true state (also reverts a failed toggle)
        }, Qt::QueuedConnection);
    });
}

// ---- download -------------------------------------------------------------

// Save a track to disk (premium-only). The whole job — fetch, Blowfish decrypt
// and file write — happens in the engine, so it runs on a worker and reports
// back on the GUI thread. Empty destDir → the engine's shared default folder
// (see the Downloads setting). Mirrors likeTrack's worker/marshal shape.
void MainWindow::download(const QString &id) {
    if (!m_loggedIn || id.isEmpty())
        return;
    if (!m_premium) { // belt-and-braces: the menu entry is disabled on Free plans
        statusBar()->showMessage(tr("Downloads require a paid Deezer plan"), 4000);
        return;
    }
    statusBar()->showMessage(tr("Downloading…"));
    const QByteArray idb = id.toUtf8();
    QtConcurrent::run([this, idb] {
        // "" destDir → shared default folder. takeJson frees the malloc'd result.
        const QByteArray status = takeJson(DZDownloadTrack(cstr(idb), cstr(QByteArray())));
        const QJsonObject o = QJsonDocument::fromJson(status).object();
        const QString path = o.value("path").toString();
        const QString err  = o.value("error").toString();
        QMetaObject::invokeMethod(this, [this, path, err] {
            if (!path.isEmpty()) {
                statusBar()->showMessage(tr("Saved to %1").arg(path), 6000);
            } else {
                statusBar()->showMessage(tr("Download failed"), 5000);
                QMessageBox::warning(this, tr("Download"),
                                     err.isEmpty() ? tr("Download failed") : err);
            }
        }, Qt::QueuedConnection);
    });
}

// Save the current / a chosen track for OFFLINE playback (premium-only). Unlike
// download() (which exports a file to the shared folder), this stores the track
// in the engine's offline cache. Runs on a worker like every blocking DZ* call;
// on success the returned {"key"} id (plus the requested id, belt-and-braces)
// joins m_offlineIds so the "downloaded" indicator lights up everywhere.
void MainWindow::downloadForOffline(const Track &t) {
    if (!m_loggedIn || t.id.isEmpty())
        return;
    if (!m_premium) { // belt-and-braces: the menu entries are disabled on Free plans
        statusBar()->showMessage(tr("Downloads require a paid Deezer plan"), 4000);
        return;
    }
    if (m_offlineIds.contains(t.id)) {
        statusBar()->showMessage(tr("Already available offline"), 3000);
        return;
    }
    statusBar()->showMessage(tr("Saving for offline…"));
    const QByteArray idb = t.id.toUtf8();
    const QString origId = t.id;
    QtConcurrent::run([this, idb, origId] {
        // takeJson frees the malloc'd result. The fetch + decrypt + store runs
        // entirely engine-side.
        const QByteArray status = takeJson(DZDownloadForOffline(cstr(idb)));
        const QJsonObject o = QJsonDocument::fromJson(status).object();
        const QString key = o.value("key").toString();
        const QString err = o.value("error").toString();
        QMetaObject::invokeMethod(this, [this, key, err, origId] {
            if (err.isEmpty()) {
                if (!key.isEmpty())
                    m_offlineIds.insert(key);
                m_offlineIds.insert(origId);
                refreshOfflineIndicators();
                statusBar()->showMessage(tr("Available offline"), 5000);
            } else {
                statusBar()->showMessage(tr("Offline download failed"), 5000);
                QMessageBox::warning(this, tr("Download for offline"), err);
            }
        }, Qt::QueuedConnection);
    });
}

// Repaint the "downloaded" markers after m_offlineIds changes: the now-playing
// badge, the title cells of every populated track table (in place — no art
// refetch) and the Up-Next panel.
void MainWindow::refreshOfflineIndicators() {
    if (m_offlineBadge)
        m_offlineBadge->setVisible(m_hasCurrent && !m_currentIsEpisode &&
                                   m_offlineIds.contains(m_current.id));
    auto repaint = [this](QTableWidget *tbl, const QVector<Track> &vec) {
        if (!tbl)
            return;
        const int n = qMin(tbl->rowCount(), int(vec.size()));
        for (int i = 0; i < n; ++i)
            if (auto *it = tbl->item(i, 0))
                it->setText(displayTitle(vec.at(i)));
    };
    repaint(m_trackTable,       m_tableTracks);
    repaint(m_searchTrackTable, m_searchTracks);
    repaint(m_artistTopTable,   m_artistTopTracks);
    repaint(m_chartsTrackTable, m_chartsTracks);
    repaint(m_historyTable,     m_historyTracks);
    refreshQueuePanel();
}

// Save a whole album / playlist to disk (premium-only). Like the single-track
// download, the entire batch — fetch, Blowfish decrypt and file writes — runs
// in the engine, so it goes on a worker and reports the summary back on the GUI
// thread. Both return {"saved":N,"failed":N,"dir":"...","error":""}.
void MainWindow::downloadAlbum(const QString &id, const QString &name) {
    downloadBatch(id, name, /*album=*/true);
}
void MainWindow::downloadPlaylist(const QString &id, const QString &name) {
    downloadBatch(id, name, /*album=*/false);
}
void MainWindow::downloadBatch(const QString &id, const QString &name, bool album) {
    if (!m_loggedIn || id.isEmpty())
        return;
    if (!m_premium) { // belt-and-braces: the header button is disabled on Free plans
        statusBar()->showMessage(tr("Downloads require a paid Deezer plan"), 4000);
        return;
    }
    statusBar()->showMessage(album ? tr("Downloading album…")
                                   : tr("Downloading playlist…"));
    const QByteArray idb = id.toUtf8();
    QtConcurrent::run([this, idb, album, name] {
        // takeJson frees the malloc'd result. The batch runs entirely engine-side.
        const QByteArray status = takeJson(album ? DZDownloadAlbum(cstr(idb))
                                                 : DZDownloadPlaylist(cstr(idb)));
        const QJsonObject o = QJsonDocument::fromJson(status).object();
        const int     saved  = o.value("saved").toInt();
        const int     failed = o.value("failed").toInt();
        const QString dir    = o.value("dir").toString();
        const QString err    = o.value("error").toString();
        QMetaObject::invokeMethod(this, [this, name, saved, failed, dir, err] {
            if (saved > 0 && failed == 0) {
                statusBar()->showMessage(
                    tr("Saved %n track(s) to %1", "", saved).arg(dir), 6000);
            } else if (saved > 0) {
                statusBar()->showMessage(
                    tr("Saved %1, %2 failed").arg(saved).arg(failed), 6000);
                QMessageBox::warning(this, tr("Download"),
                    tr("Downloaded \"%1\": %2 saved, %3 failed.")
                        .arg(name).arg(saved).arg(failed) +
                    (err.isEmpty() ? QString() : QStringLiteral("\n") + err));
            } else {
                statusBar()->showMessage(tr("Download failed"), 5000);
                QMessageBox::warning(this, tr("Download"),
                                     err.isEmpty() ? tr("Download failed") : err);
            }
        }, Qt::QueuedConnection);
    });
}

// Header "Download album/playlist" button: dispatch to whichever unit the shared
// track table is currently showing (album takes precedence, then playlist).
void MainWindow::downloadCurrentList() {
    if (!m_currentAlbumId.isEmpty())
        downloadAlbum(m_currentAlbumId, m_currentAlbumName);
    else if (!m_currentPlaylistId.isEmpty())
        downloadPlaylist(m_currentPlaylistId, m_currentPlaylistName);
}

// Show + label the header download button for the current view. Hidden for
// non-unit views (Liked Songs / Flow / Radio / search etc.); shown but greyed
// out with a hint on Free plans, mirroring the single-track menu entry.
void MainWindow::updateListDownloadButton() {
    if (!m_downloadListBtn)
        return;
    const bool album    = !m_currentAlbumId.isEmpty();
    const bool playlist = !m_currentPlaylistId.isEmpty();
    if (!album && !playlist) {
        m_downloadListBtn->setVisible(false);
        return;
    }
    m_downloadListBtn->setText(album ? tr("Download album")
                                     : tr("Download playlist"));
    m_downloadListBtn->setVisible(true);
    m_downloadListBtn->setEnabled(m_premium);
    m_downloadListBtn->setToolTip(m_premium ? QString()
                                            : tr("Requires a paid Deezer plan"));
}

// ---- add to playlist ------------------------------------------------------

// Load the user's playlists (fresh) then show the picker on the GUI thread.
void MainWindow::addTrackToPlaylist(const Track &t) {
    if (!m_loggedIn || t.id.isEmpty())
        return;
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this, t] {
        QVector<Playlist> ps;
        const QJsonObject obj =
            QJsonDocument::fromJson(takeJson(DZPlaylistsJSON())).object();
        for (const QJsonValue &v : obj.value("playlists").toArray())
            ps.push_back(parsePlaylist(v.toObject()));
        QMetaObject::invokeMethod(this, [this, t, ps] {
            statusBar()->clearMessage();
            showAddToPlaylistPicker(t, ps);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::showAddToPlaylistPicker(const Track &t, const QVector<Playlist> &ps) {
    QDialog dlg(this);
    dlg.setWindowTitle(tr("Add to Playlist"));
    auto *v = new QVBoxLayout(&dlg);
    v->addWidget(new QLabel(tr("Add \"%1\" to:").arg(t.name)));
    auto *list = new QListWidget;
    auto *newItem = new QListWidgetItem(QStringLiteral("＋  ") + tr("New playlist…"));
    newItem->setData(Qt::UserRole, -1);
    list->addItem(newItem);
    for (int i = 0; i < ps.size(); ++i) {
        auto *it = new QListWidgetItem(ps[i].name);
        it->setData(Qt::UserRole, i);
        list->addItem(it);
    }
    list->setCurrentRow(0);
    v->addWidget(list, 1);
    auto *bb = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    v->addWidget(bb);
    connect(bb, &QDialogButtonBox::accepted, &dlg, &QDialog::accept);
    connect(bb, &QDialogButtonBox::rejected, &dlg, &QDialog::reject);
    connect(list, &QListWidget::itemActivated, &dlg, &QDialog::accept);
    if (dlg.exec() != QDialog::Accepted)
        return;
    QListWidgetItem *sel = list->currentItem();
    if (!sel)
        return;
    const int idx = sel->data(Qt::UserRole).toInt();

    if (idx < 0) {
        // "New playlist…": prompt, create, then add the track to the new id.
        bool ok = false;
        const QString name = QInputDialog::getText(
            this, tr("New Playlist"), tr("Playlist name:"),
            QLineEdit::Normal, QString(), &ok).trimmed();
        if (!ok || name.isEmpty())
            return;
        const QByteArray nb = name.toUtf8();
        const QByteArray tid = t.id.toUtf8();
        QtConcurrent::run([this, nb, tid, name] {
            const QByteArray j = takeJson(DZCreatePlaylist(cstr(nb)));
            const QString pid = QJsonDocument::fromJson(j).object().value("id").toString();
            int added = 0;
            if (!pid.isEmpty()) {
                const QByteArray pidb = pid.toUtf8();
                added = DZAddToPlaylist(cstr(pidb), cstr(tid));
            }
            QMetaObject::invokeMethod(this, [this, name, pid, added] {
                if (pid.isEmpty())
                    statusBar()->showMessage(tr("Couldn't create playlist"), 3000);
                else
                    statusBar()->showMessage(added
                        ? tr("Added to new playlist \"%1\"").arg(name)
                        : tr("Created \"%1\" but couldn't add the track").arg(name),
                        3000);
            }, Qt::QueuedConnection);
        });
        return;
    }

    if (idx >= ps.size())
        return;
    const QString plName = ps[idx].name;
    const QByteArray pid = ps[idx].id.toUtf8();
    const QByteArray tid = t.id.toUtf8();
    QtConcurrent::run([this, pid, tid, plName] {
        const int added = DZAddToPlaylist(cstr(pid), cstr(tid));
        QMetaObject::invokeMethod(this, [this, added, plName] {
            statusBar()->showMessage(added
                ? tr("Added to \"%1\"").arg(plName)
                : tr("Couldn't add to \"%1\"").arg(plName), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::removeFromCurrentPlaylist(const Track &t, int row) {
    if (m_currentPlaylistId.isEmpty() || t.id.isEmpty())
        return;
    const QString plid = m_currentPlaylistId;
    const QByteArray pid = plid.toUtf8();
    const QByteArray tid = t.id.toUtf8();
    const QString tid2 = t.id;
    QtConcurrent::run([this, pid, tid, plid, tid2, row] {
        const int okRes = DZRemoveFromPlaylist(cstr(pid), cstr(tid));
        QMetaObject::invokeMethod(this, [this, okRes, plid, tid2, row] {
            if (!okRes) {
                statusBar()->showMessage(tr("Couldn't remove from playlist"), 3000);
                return;
            }
            statusBar()->showMessage(tr("Removed from playlist"), 3000);
            // Drop the row locally if the table still shows this playlist + track.
            if (m_currentPlaylistId == plid && row >= 0 && row < m_tableTracks.size() &&
                m_tableTracks[row].id == tid2) {
                m_tableTracks.removeAt(row);
                const int gen = ++m_artGen;
                fillTrackTable(m_trackTable, m_tableTracks, gen);
            }
        }, Qt::QueuedConnection);
    });
}

// ---- playlist management (create / rename / delete) -----------------------

void MainWindow::createPlaylist() {
    if (!m_loggedIn)
        return;
    bool ok = false;
    const QString name = QInputDialog::getText(
        this, tr("New Playlist"), tr("Playlist name:"),
        QLineEdit::Normal, QString(), &ok).trimmed();
    if (!ok || name.isEmpty())
        return;
    const QByteArray nb = name.toUtf8();
    QtConcurrent::run([this, nb, name] {
        const QByteArray j = takeJson(DZCreatePlaylist(cstr(nb)));
        const QString id = QJsonDocument::fromJson(j).object().value("id").toString();
        QMetaObject::invokeMethod(this, [this, name, id] {
            if (id.isEmpty()) {
                statusBar()->showMessage(tr("Couldn't create playlist"), 3000);
            } else {
                statusBar()->showMessage(tr("Created \"%1\"").arg(name), 3000);
                loadPlaylists(); // refresh the grid
            }
        }, Qt::QueuedConnection);
    });
}

void MainWindow::renamePlaylist(const Playlist &p) {
    if (!m_loggedIn || p.id.isEmpty())
        return;
    bool ok = false;
    const QString name = QInputDialog::getText(
        this, tr("Rename Playlist"), tr("New name:"),
        QLineEdit::Normal, p.name, &ok).trimmed();
    if (!ok || name.isEmpty() || name == p.name)
        return;
    const QByteArray idb = p.id.toUtf8();
    const QByteArray nb = name.toUtf8();
    QtConcurrent::run([this, idb, nb, name] {
        const int okRes = DZRenamePlaylist(cstr(idb), cstr(nb));
        QMetaObject::invokeMethod(this, [this, okRes, name] {
            statusBar()->showMessage(okRes
                ? tr("Renamed to \"%1\"").arg(name)
                : tr("Couldn't rename playlist"), 3000);
            if (okRes)
                loadPlaylists();
        }, Qt::QueuedConnection);
    });
}

void MainWindow::deletePlaylist(const Playlist &p) {
    if (!m_loggedIn || p.id.isEmpty())
        return;
    if (QMessageBox::question(this, tr("Delete Playlist"),
            tr("Delete \"%1\"? This cannot be undone.").arg(p.name))
        != QMessageBox::Yes)
        return;
    const QByteArray idb = p.id.toUtf8();
    QtConcurrent::run([this, idb] {
        const int okRes = DZDeletePlaylist(cstr(idb));
        QMetaObject::invokeMethod(this, [this, okRes] {
            statusBar()->showMessage(okRes ? tr("Playlist deleted")
                                           : tr("Couldn't delete playlist"),
                                     3000);
            if (okRes)
                loadPlaylists();
        }, Qt::QueuedConnection);
    });
}

// ---- lyrics + artist pages ------------------------------------------------

// Right-click menu shared by every track table: jump to the row's artist, show
// its lyrics, like it, or add it to a playlist. When the shared table is showing
// a playlist, a "Remove from this playlist" entry appears too. src points at the
// QVector backing the table's rows.
void MainWindow::installTrackMenu(QTableWidget *table, QVector<Track> *src) {
    table->setContextMenuPolicy(Qt::CustomContextMenu);
    connect(table, &QWidget::customContextMenuRequested, this,
            [this, table, src](const QPoint &pos) {
                const int row = table->rowAt(pos.y());
                if (row < 0 || row >= src->size())
                    return;
                const Track t = src->at(row);
                QMenu menu(this);
                QAction *goArtist = menu.addAction(tr("Go to Artist"));
                goArtist->setEnabled(!t.artistId.isEmpty());
                QAction *showLy = menu.addAction(tr("Show Lyrics"));
                QAction *radio  = menu.addAction(tr("Start radio"));
                menu.addSeparator();
                // Up-Next queue actions (mirror the engine queue too).
                QAction *playNext = menu.addAction(tr("Play next"));
                QAction *addQueue = menu.addAction(tr("Add to queue"));
                menu.addSeparator();
                QAction *like  = menu.addAction(tr("Add to Liked Songs"));
                QAction *addPl = menu.addAction(tr("Add to Playlist…"));
                menu.addSeparator();
                // Downloads are premium-only: show the entries always so they're
                // discoverable, but grey them out with a hint on Free plans.
                QAction *dl = menu.addAction(tr("Download"));
                QAction *dlOffline = menu.addAction(tr("Download for offline"));
                if (m_offlineIds.contains(t.id)) {
                    dlOffline->setEnabled(false);
                    dlOffline->setText(tr("Available offline"));
                }
                if (!m_premium) {
                    dl->setEnabled(false);
                    dl->setToolTip(tr("Requires a paid Deezer plan"));
                    if (dlOffline->isEnabled()) {
                        dlOffline->setEnabled(false);
                        dlOffline->setToolTip(tr("Requires a paid Deezer plan"));
                    }
                    menu.setToolTipsVisible(true);
                }
                QAction *removePl = nullptr;
                if (table == m_trackTable && !m_currentPlaylistId.isEmpty())
                    removePl = menu.addAction(tr("Remove from this playlist"));
                QAction *chosen = menu.exec(table->viewport()->mapToGlobal(pos));
                if (chosen == goArtist)
                    openArtist(t.artistId);
                else if (chosen == showLy)
                    openLyricsFor(t.id, t.name + QStringLiteral("   ·   ") + t.artistLine);
                else if (chosen == radio)
                    startTrackRadio(t.id);
                else if (chosen == playNext)
                    queueInsertNext(t);
                else if (chosen == addQueue)
                    queueAppend(t);
                else if (chosen == like)
                    likeTrack(t.id, true);
                else if (chosen == addPl)
                    addTrackToPlaylist(t);
                else if (chosen == dl)
                    download(t.id);
                else if (chosen == dlOffline)
                    downloadForOffline(t);
                else if (removePl && chosen == removePl)
                    removeFromCurrentPlaylist(t, row);
            });
}

// Only the browse pages — home(0), tracks(1), playlists(2), search(3),
// charts(6), podcasts(7), history(9) — are valid "Back" targets, never another
// detour page, so Back from lyrics/artist always lands somewhere sensible.
void MainWindow::rememberReturnPage() {
    const int cur = m_stack->currentIndex();
    if (cur == 0 || cur == 1 || cur == 2 || cur == 3 || cur == 6 || cur == 7 || cur == 9)
        m_returnPage = cur;
}

QWidget *MainWindow::buildLyricsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *top = new QHBoxLayout;
    auto *back = new QToolButton;
    back->setText(QStringLiteral("‹ ") + tr("Back"));
    back->setAutoRaise(true);
    connect(back, &QToolButton::clicked, this,
            [this] { m_stack->setCurrentIndex(m_returnPage); });
    top->addWidget(back);
    m_lyricsTitle = new QLabel(tr("Lyrics"));
    QFont tf = m_lyricsTitle->font();
    tf.setPointSize(tf.pointSize() + 4);
    tf.setBold(true);
    m_lyricsTitle->setFont(tf);
    top->addWidget(m_lyricsTitle, 1);
    v->addLayout(top);

    m_lyricsList = new QListWidget;
    m_lyricsList->setSelectionMode(QAbstractItemView::NoSelection);
    m_lyricsList->setFocusPolicy(Qt::NoFocus);
    m_lyricsList->setWordWrap(true);
    m_lyricsList->setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
    v->addWidget(m_lyricsList, 1);
    return w;
}

QWidget *MainWindow::buildArtistPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *top = new QHBoxLayout;
    auto *back = new QToolButton;
    back->setText(QStringLiteral("‹ ") + tr("Back"));
    back->setAutoRaise(true);
    connect(back, &QToolButton::clicked, this,
            [this] { m_stack->setCurrentIndex(m_returnPage); });
    top->addWidget(back);
    top->addStretch(1);
    v->addLayout(top);

    // Header: avatar + name + fan count.
    auto *head = new QHBoxLayout;
    m_artistAvatar = new QLabel;
    m_artistAvatar->setFixedSize(72, 72);
    m_artistAvatar->setScaledContents(true);
    m_artistAvatar->setPixmap(placeholderPix(72));
    head->addWidget(m_artistAvatar);
    auto *names = new QVBoxLayout;
    m_artistName = new QLabel(tr("Artist"));
    QFont nf = m_artistName->font();
    nf.setPointSize(nf.pointSize() + 6);
    nf.setBold(true);
    m_artistName->setFont(nf);
    m_artistFans = new QLabel;
    names->addWidget(m_artistName);
    names->addWidget(m_artistFans);
    names->addStretch(1);
    head->addLayout(names, 1);
    // "Start radio" for this artist (DZArtistMixJSON → playMix).
    auto *radioBtn = new QPushButton(QString::fromUtf8("\xE2\x96\xB6 ") + tr("Start radio")); // ▶
    radioBtn->setStyleSheet(QString(
        "QPushButton{background:%1;color:white;padding:6px 16px;border-radius:4px;}")
        .arg(kAccent));
    connect(radioBtn, &QPushButton::clicked, this,
            [this] { startArtistRadio(m_currentArtistId); });
    head->addWidget(radioBtn, 0, Qt::AlignTop);
    v->addLayout(head);

    v->addWidget(new QLabel(tr("Top Tracks")));
    m_artistTopTable = makeTrackTable();
    connect(m_artistTopTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_artistTopTracks, row); });
    installTrackMenu(m_artistTopTable, &m_artistTopTracks);
    v->addWidget(m_artistTopTable, 2);

    v->addWidget(new QLabel(tr("Albums")));
    m_artistAlbumsGrid = new QListWidget;
    m_artistAlbumsGrid->setViewMode(QListView::IconMode);
    m_artistAlbumsGrid->setIconSize(QSize(110, 110));
    m_artistAlbumsGrid->setGridSize(QSize(140, 165));
    m_artistAlbumsGrid->setResizeMode(QListView::Adjust);
    m_artistAlbumsGrid->setMovement(QListView::Static);
    m_artistAlbumsGrid->setWordWrap(true);
    connect(m_artistAlbumsGrid, &QListWidget::itemActivated, this,
            [this](QListWidgetItem *it) {
                const int idx = it->data(Qt::UserRole).toInt();
                if (idx >= 0 && idx < m_artistAlbums.size())
                    openAlbum(m_artistAlbums[idx]);
            });
    v->addWidget(m_artistAlbumsGrid, 1);

    v->addWidget(new QLabel(tr("Related Artists")));
    m_artistRelatedGrid = new QListWidget;
    m_artistRelatedGrid->setViewMode(QListView::IconMode);
    m_artistRelatedGrid->setIconSize(QSize(110, 110));
    m_artistRelatedGrid->setGridSize(QSize(140, 165));
    m_artistRelatedGrid->setResizeMode(QListView::Adjust);
    m_artistRelatedGrid->setMovement(QListView::Static);
    m_artistRelatedGrid->setWordWrap(true);
    connect(m_artistRelatedGrid, &QListWidget::itemActivated, this,
            [this](QListWidgetItem *it) {
                const int idx = it->data(Qt::UserRole).toInt();
                if (idx >= 0 && idx < m_artistRelated.size())
                    openArtist(m_artistRelated[idx].id);
            });
    v->addWidget(m_artistRelatedGrid, 1);
    return w;
}

// ---- charts page ----------------------------------------------------------

QWidget *MainWindow::buildChartsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    auto *title = new QLabel(tr("Charts"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

    v->addWidget(new QLabel(tr("Top Tracks")));
    m_chartsTrackTable = makeTrackTable();
    connect(m_chartsTrackTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_chartsTracks, row); });
    installTrackMenu(m_chartsTrackTable, &m_chartsTracks);
    v->addWidget(m_chartsTrackTable, 2);

    v->addWidget(new QLabel(tr("Albums, Artists & Playlists")));
    m_chartsResults = new QListWidget;
    m_chartsResults->setViewMode(QListView::IconMode);
    m_chartsResults->setIconSize(QSize(110, 110));
    m_chartsResults->setGridSize(QSize(140, 165));
    m_chartsResults->setResizeMode(QListView::Adjust);
    m_chartsResults->setMovement(QListView::Static);
    m_chartsResults->setWordWrap(true);
    connect(m_chartsResults, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int kind = it->data(Qt::UserRole).toInt();      // 0 album, 1 playlist, 2 artist
        const int idx  = it->data(Qt::UserRole + 1).toInt();
        if (kind == 0 && idx < m_chartsAlbums.size())
            openAlbum(m_chartsAlbums[idx]);
        else if (kind == 1 && idx < m_chartsPlaylists.size())
            openPlaylist(m_chartsPlaylists[idx]);
        else if (kind == 2 && idx < m_chartsArtists.size())
            openArtist(m_chartsArtists[idx].id);
    });
    v->addWidget(m_chartsResults, 1);
    return w;
}

// ---- podcasts pages -------------------------------------------------------

QWidget *MainWindow::buildPodcastsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    auto *title = new QLabel(tr("Podcasts"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

    auto *top = new QHBoxLayout;
    m_podcastSearchEdit = new QLineEdit;
    m_podcastSearchEdit->setPlaceholderText(tr("Search podcasts…"));
    auto *btn = new QPushButton(tr("Search"));
    top->addWidget(m_podcastSearchEdit, 1);
    top->addWidget(btn);
    v->addLayout(top);

    m_podcastGrid = new QListWidget;
    m_podcastGrid->setViewMode(QListView::IconMode);
    m_podcastGrid->setIconSize(QSize(110, 110));
    m_podcastGrid->setGridSize(QSize(150, 180));
    m_podcastGrid->setResizeMode(QListView::Adjust);
    m_podcastGrid->setMovement(QListView::Static);
    m_podcastGrid->setWordWrap(true);
    connect(m_podcastGrid, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int idx = it->data(Qt::UserRole).toInt();
        if (idx >= 0 && idx < m_podcasts.size())
            openPodcast(m_podcasts[idx]);
    });
    v->addWidget(m_podcastGrid, 1);

    connect(btn, &QPushButton::clicked, this, &MainWindow::runPodcastSearch);
    connect(m_podcastSearchEdit, &QLineEdit::returnPressed, this, &MainWindow::runPodcastSearch);
    return w;
}

QWidget *MainWindow::buildPodcastEpisodesPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *top = new QHBoxLayout;
    auto *back = new QToolButton;
    back->setText(QStringLiteral("‹ ") + tr("Back"));
    back->setAutoRaise(true);
    connect(back, &QToolButton::clicked, this,
            [this] { m_stack->setCurrentIndex(7); }); // back to the shows grid
    top->addWidget(back);
    m_podcastTitle = new QLabel(tr("Episodes"));
    QFont tf = m_podcastTitle->font();
    tf.setPointSize(tf.pointSize() + 4);
    tf.setBold(true);
    m_podcastTitle->setFont(tf);
    top->addWidget(m_podcastTitle, 1);
    v->addLayout(top);

    m_episodeList = new QListWidget;
    m_episodeList->setIconSize(QSize(48, 48));
    m_episodeList->setWordWrap(true);
    m_episodeList->setSelectionMode(QAbstractItemView::SingleSelection);
    connect(m_episodeList, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int idx = it->data(Qt::UserRole).toInt();
        if (idx >= 0 && idx < m_episodes.size())
            playEpisode(m_episodes[idx]);
    });
    v->addWidget(m_episodeList, 1);
    return w;
}

// ---- history page (recently played + listening stats) ---------------------

QWidget *MainWindow::buildHistoryPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *title = new QLabel(tr("Recently played"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

    // Recently played — a normal track table (plays by id via playFrom), so it
    // gets the shared track context menu ("Start radio", "Show Lyrics", …) too.
    m_historyTable = makeTrackTable();
    connect(m_historyTable, &QTableWidget::cellActivated, this,
            [this](int row, int) {
                if (row < 0 || row >= m_historyTracks.size())
                    return;
                const Track &t = m_historyTracks[row];
                if (t.isEpisode) {
                    // Podcast episode: replay via the standalone episode path.
                    // The engine enriches title/show/artwork + reports the real
                    // duration once the stream is prepared, so 0 duration is fine.
                    Episode e;
                    e.id    = t.id;
                    e.title = t.name;
                    playEpisode(e, t.artistLine); // artistLine = the show name
                } else {
                    playFrom(m_historyTracks, row);
                }
            });
    installTrackMenu(m_historyTable, &m_historyTracks);
    v->addWidget(m_historyTable, 2);

    auto *statsTitle = new QLabel(tr("Listening stats"));
    QFont sf = statsTitle->font();
    sf.setPointSize(sf.pointSize() + 2);
    sf.setBold(true);
    statsTitle->setFont(sf);
    v->addWidget(statsTitle);

    m_statsTotal = new QLabel;
    v->addWidget(m_statsTotal);

    // Two columns: top tracks (playable by id) | top artists (informational —
    // the stats carry an artist name but no id, so no navigation).
    auto *cols = new QHBoxLayout;
    auto *ttCol = new QVBoxLayout;
    ttCol->addWidget(new QLabel(tr("Top Tracks")));
    m_statsTopTracks = new QListWidget;
    connect(m_statsTopTracks, &QListWidget::itemActivated, this,
            [this](QListWidgetItem *it) {
                const int idx = it->data(Qt::UserRole).toInt();
                if (idx >= 0 && idx < m_statsTrackList.size())
                    playFrom(m_statsTrackList, idx);
            });
    ttCol->addWidget(m_statsTopTracks);
    cols->addLayout(ttCol, 1);

    auto *taCol = new QVBoxLayout;
    taCol->addWidget(new QLabel(tr("Top Artists")));
    m_statsTopArtists = new QListWidget;
    m_statsTopArtists->setSelectionMode(QAbstractItemView::NoSelection);
    m_statsTopArtists->setFocusPolicy(Qt::NoFocus);
    taCol->addWidget(m_statsTopArtists);
    cols->addLayout(taCol, 1);

    v->addLayout(cols, 1);
    return w;
}

// Load the machine-local listening history (DZHistoryRecentJSON) + stats
// (DZHistoryStatsJSON over the last 30 days) off the GUI thread, then populate
// the table + the two stat lists. Both are cheap local reads but are marshalled
// like every other DZ*JSON call for consistency.
void MainWindow::loadHistory() {
    if (!m_loggedIn)
        return;
    statusBar()->showMessage(tr("Loading…"));
    QtConcurrent::run([this] {
        const QByteArray recentJson = takeJson(DZHistoryRecentJSON(100));
        const QByteArray statsJson  = takeJson(DZHistoryStatsJSON(30));
        QMetaObject::invokeMethod(this, [this, recentJson, statsJson] {
            const int gen = ++m_artGen;

            // Recently played — build playable Track rows (id + display fields;
            // the entry carries no track length, so Duration shows 0:00 and the
            // engine reports the real duration once a row is played).
            m_historyTracks.clear();
            for (const QJsonValue &v : QJsonDocument::fromJson(recentJson).array()) {
                const QJsonObject o = v.toObject();
                Track t;
                t.id         = o.value("trackId").toString();
                t.name       = o.value("title").toString();
                t.artistLine = o.value("artist").toString();
                t.albumName  = o.value("album").toString();
                // kind=="episode" rows are podcast episodes — replayed via the
                // plain-stream episode path, not the encrypted track pipeline.
                t.isEpisode  = o.value("kind").toString() == QLatin1String("episode");
                if (!t.id.isEmpty())
                    m_historyTracks.push_back(t);
            }
            fillTrackTable(m_historyTable, m_historyTracks, gen);

            // Stats — total listening time, top tracks (playable), top artists.
            const QJsonObject stats = QJsonDocument::fromJson(statsJson).object();
            const qint64 totalSec =
                static_cast<qint64>(stats.value("totalSeconds").toDouble());
            m_statsTotal->setText(tr("Listening time (last 30 days): %1h %2m")
                                      .arg(totalSec / 3600)
                                      .arg((totalSec % 3600) / 60));

            m_statsTrackList.clear();
            m_statsTopTracks->clear();
            for (const QJsonValue &v : stats.value("topTracks").toArray()) {
                const QJsonObject o = v.toObject();
                Track t;
                t.id         = o.value("trackId").toString();
                t.name       = o.value("title").toString();
                t.artistLine = o.value("artist").toString();
                auto *it = new QListWidgetItem(
                    t.name + QStringLiteral(" — ") + t.artistLine +
                    QStringLiteral("   ") +
                    tr("(%n play(s))", "", o.value("plays").toInt()));
                it->setData(Qt::UserRole, m_statsTrackList.size());
                m_statsTopTracks->addItem(it);
                m_statsTrackList.push_back(t);
            }
            if (m_statsTrackList.isEmpty())
                m_statsTopTracks->addItem(new QListWidgetItem(tr("No data yet.")));

            m_statsTopArtists->clear();
            const QJsonArray topArtists = stats.value("topArtists").toArray();
            for (const QJsonValue &v : topArtists) {
                const QJsonObject o = v.toObject();
                m_statsTopArtists->addItem(new QListWidgetItem(
                    o.value("artist").toString() + QStringLiteral("   ") +
                    tr("(%n play(s))", "", o.value("plays").toInt())));
            }
            if (topArtists.isEmpty())
                m_statsTopArtists->addItem(new QListWidgetItem(tr("No data yet.")));

            statusBar()->showMessage(
                tr("Recently played — %n track(s)", "", int(m_historyTracks.size())),
                3000);
        }, Qt::QueuedConnection);
    });
}

// ---- podcasts flow --------------------------------------------------------

void MainWindow::runPodcastSearch() {
    if (!m_loggedIn)
        return;
    const QString q = m_podcastSearchEdit->text().trimmed();
    if (q.isEmpty())
        return;
    statusBar()->showMessage(tr("Searching…"));
    const QByteArray qb = q.toUtf8();
    QtConcurrent::run([this, qb] {
        const QByteArray j = takeJson(DZSearchPodcastsJSON(cstr(qb)));
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject obj = QJsonDocument::fromJson(j).object();
            const int gen = ++m_artGen;
            m_podcasts.clear();
            for (const QJsonValue &v : obj.value("podcasts").toArray())
                m_podcasts.push_back(parsePodcast(v.toObject()));
            m_podcastGrid->clear();
            for (int i = 0; i < m_podcasts.size(); ++i) {
                const Podcast &p = m_podcasts[i];
                auto *it = new QListWidgetItem(
                    QIcon(placeholderPix(110)),
                    p.name + "\n" + tr("%n episode(s)", "", p.episodeCount));
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, i);
                m_podcastGrid->addItem(it);
                if (!p.artworkUrl.isEmpty())
                    fetchImage(p.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            statusBar()->showMessage(
                tr("%n podcast(s)", "", int(m_podcasts.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openPodcast(const Podcast &p) {
    m_currentPodcastName = p.name;
    m_podcastTitle->setText(p.name);
    m_stack->setCurrentIndex(8); // podcast episodes page
    m_episodes.clear();
    ++m_artGen; // clear() deletes the old items; drop their in-flight art now
    m_episodeList->clear();
    m_episodeList->addItem(new QListWidgetItem(tr("Loading…")));
    statusBar()->showMessage(tr("Loading…"));
    const QByteArray idb = p.id.toUtf8();
    QtConcurrent::run([this, idb] {
        const QByteArray j = takeJson(DZPodcastEpisodesJSON(cstr(idb)));
        QMetaObject::invokeMethod(this, [this, j] {
            const QJsonObject obj = QJsonDocument::fromJson(j).object();
            const int gen = ++m_artGen;
            m_episodes.clear();
            for (const QJsonValue &v : obj.value("episodes").toArray())
                m_episodes.push_back(parseEpisode(v.toObject()));
            m_episodeList->clear();
            for (int i = 0; i < m_episodes.size(); ++i) {
                const Episode &e = m_episodes[i];
                QString sub = e.releaseDate;
                if (e.durationMs > 0) {
                    if (!sub.isEmpty())
                        sub += QStringLiteral("   ·   ");
                    sub += timeText(e.durationMs);
                }
                auto *it = new QListWidgetItem(QIcon(placeholderPix(48)),
                                               sub.isEmpty() ? e.title
                                                             : e.title + "\n" + sub);
                it->setData(Qt::UserRole, i);
                m_episodeList->addItem(it);
                if (!e.artworkUrl.isEmpty())
                    fetchImage(e.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            48, 48, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            if (m_episodes.isEmpty())
                m_episodeList->addItem(new QListWidgetItem(tr("No episodes.")));
            statusBar()->showMessage(
                tr("%n episode(s)", "", int(m_episodes.size())), 3000);
        }, Qt::QueuedConnection);
    });
}

// Episodes use the plain-stream path (DZPlayEpisode), not the encrypted track
// pipeline, so they sit outside the queue: clearing it makes next/prev and
// auto-advance no-ops while the episode plays.
void MainWindow::playEpisode(const Episode &e, const QString &showName) {
    if (!m_loggedIn || e.id.isEmpty())
        return;
    m_queue.clear();
    m_queueIndex = -1;
    syncQueueToEngine(); // clear the engine queue too (episodes sit outside it)
    Track t;
    t.id         = e.id;
    t.name       = e.title;
    t.artistLine = showName.isEmpty() ? m_currentPodcastName : showName;
    t.artworkUrl = e.artworkUrl;
    t.durationMs = e.durationMs;
    m_current    = t;
    m_hasCurrent = true;
    m_currentIsEpisode = true;
    setNowPlaying(t);
    m_seek->setRange(0, static_cast<int>(qMax<qint64>(1, e.durationMs)));
    m_seek->setValue(0);
    m_posLabel->setText("0:00");
    m_durLabel->setText(timeText(e.durationMs));
    const QByteArray id = e.id.toUtf8();
    const qint64 dur = e.durationMs;
    QtConcurrent::run([id, dur] { DZPlayEpisode(cstr(id), dur); });
}

// ---- lyrics flow ----------------------------------------------------------

// Transport "Lyrics" button: the lyrics follow whatever is playing.
void MainWindow::openLyrics() {
    // When routed via Connect, the remote device owns the queue — ask the engine
    // for the authoritative now-playing rather than relying on m_current, which
    // might lag behind or reflect the last local track.
    if (char *c = DZConnectedDevice()) {
        const bool remote = (*c != '\0');
        DZFree(c);
        if (remote) {
            const QByteArray npJson = takeJson(DZNowPlayingJSON());
            Track np;
            if (!npJson.isEmpty())
                np = parseTrack(QJsonDocument::fromJson(npJson).object());
            if (np.id.isEmpty()) {
                statusBar()->showMessage(
                    tr("Nothing is playing on the remote device"), 3000);
                return;
            }
            m_lyricsFollowsPlayback = true;
            rememberReturnPage();
            m_stack->setCurrentIndex(4);
            loadLyrics(np.id,
                       np.name + QStringLiteral("   ·   ") + np.artistLine);
            return;
        }
    }
    // Local path: use the known current track.
    if (!m_hasCurrent) {
        statusBar()->showMessage(tr("Nothing is playing"), 3000);
        return;
    }
    m_lyricsFollowsPlayback = true;
    rememberReturnPage();
    m_stack->setCurrentIndex(4);
    loadLyrics(m_current.id,
               m_current.name + QStringLiteral("   ·   ") + m_current.artistLine);
}

// Context-menu "Show Lyrics": a specific, possibly-not-playing track. These do
// not auto-refresh when the playing track changes.
void MainWindow::openLyricsFor(const QString &trackId, const QString &title) {
    if (!m_loggedIn || trackId.isEmpty())
        return;
    m_lyricsFollowsPlayback = false;
    rememberReturnPage();
    m_stack->setCurrentIndex(4);
    loadLyrics(trackId, title);
}

// Fetch (or serve from cache) the lyrics for a track and render them.
void MainWindow::loadLyrics(const QString &trackId, const QString &title) {
    m_lyricsRequestedId = trackId;
    m_lyricsTitle->setText(title);

    const auto it = m_lyricsCache.constFind(trackId);
    if (it != m_lyricsCache.constEnd()) {
        renderLyrics(trackId, title, it.value()); // cached — no network
        return;
    }

    // Cache miss: show a placeholder, fetch on a worker (DZLyricsJSON is network).
    m_lyricsList->clear();
    m_lyricsTimes.clear();
    m_lyricsActiveRow = -1;
    m_lyricsIsSynced  = false;
    m_lyricsShownId.clear();
    m_lyricsList->addItem(new QListWidgetItem(tr("Loading…")));

    const int gen = ++m_lyricsGen;
    const QByteArray idb = trackId.toUtf8();
    QtConcurrent::run([this, idb, trackId, title, gen] {
        const LyricsData d = parseLyrics(takeJson(DZLyricsJSON(cstr(idb))));
        QMetaObject::invokeMethod(this, [this, trackId, title, d, gen] {
            if (gen != m_lyricsGen)
                return; // a newer lyrics request superseded this one
            m_lyricsCache.insert(trackId, d);
            renderLyrics(trackId, title, d);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::renderLyrics(const QString &trackId, const QString &title,
                              const LyricsData &d) {
    m_lyricsTitle->setText(title);
    m_lyricsList->clear();
    m_lyricsTimes.clear();
    m_lyricsActiveRow = -1;
    m_lyricsShownId   = trackId;
    m_lyricsIsSynced  = d.isSynced && !d.lines.isEmpty();

    if (m_lyricsIsSynced) {
        for (const LyricsLine &ln : d.lines) {
            auto *item = new QListWidgetItem(
                ln.text.isEmpty() ? QStringLiteral(" ") : ln.text);
            item->setTextAlignment(Qt::AlignHCenter | Qt::AlignVCenter);
            m_lyricsList->addItem(item);
            m_lyricsTimes.push_back(ln.timeMs);
        }
        updateLyricsHighlight(DZPositionMS()); // set the active line right away
        return;
    }

    const QString plain = d.plain.trimmed();
    if (plain.isEmpty()) {
        m_lyricsList->addItem(
            new QListWidgetItem(tr("No lyrics available.")));
        return;
    }
    const QStringList lines = plain.split('\n');
    for (const QString &line : lines) {
        auto *item = new QListWidgetItem(line);
        item->setTextAlignment(Qt::AlignHCenter | Qt::AlignVCenter);
        m_lyricsList->addItem(item);
    }
}

// Highlight the last synced line whose start time has passed, and scroll to it.
// Driven by the existing UI tick (same timer that advances the seek bar).
void MainWindow::updateLyricsHighlight(qint64 posMs) {
    if (!m_lyricsIsSynced || m_lyricsTimes.isEmpty())
        return;
    int active = -1;
    for (int i = 0; i < m_lyricsTimes.size(); ++i) {
        if (m_lyricsTimes[i] <= posMs)
            active = i;
        else
            break;
    }
    if (active == m_lyricsActiveRow)
        return;

    if (m_lyricsActiveRow >= 0 && m_lyricsActiveRow < m_lyricsList->count()) {
        QListWidgetItem *old = m_lyricsList->item(m_lyricsActiveRow);
        QFont f = old->font();
        f.setBold(false);
        old->setFont(f);
        old->setForeground(QBrush()); // restore palette default
    }
    m_lyricsActiveRow = active;
    if (active >= 0 && active < m_lyricsList->count()) {
        QListWidgetItem *it = m_lyricsList->item(active);
        QFont f = it->font();
        f.setBold(true);
        it->setFont(f);
        it->setForeground(QBrush(QColor(kAccent)));
        m_lyricsList->scrollToItem(it, QAbstractItemView::PositionAtCenter);
    }
}

// ---- artist flow ----------------------------------------------------------

void MainWindow::openArtistForCurrent() {
    // When routed via Connect, the remote device owns the queue — ask the engine
    // for the authoritative now-playing (which now carries "artistId") rather than
    // relying on m_current, which might lag or reflect the last local track.
    if (char *c = DZConnectedDevice()) {
        const bool remote = (*c != '\0');
        DZFree(c);
        if (remote) {
            const QByteArray npJson = takeJson(DZNowPlayingJSON());
            Track np;
            if (!npJson.isEmpty())
                np = parseTrack(QJsonDocument::fromJson(npJson).object());
            if (np.id.isEmpty()) {
                statusBar()->showMessage(
                    tr("Nothing is playing on the remote device"), 3000);
                return;
            }
            if (np.artistId.isEmpty()) {
                statusBar()->showMessage(
                    tr("Artist unavailable for this track"), 3000);
                return;
            }
            openArtist(np.artistId);
            return;
        }
    }
    // Local path: use the known current track.
    if (!m_hasCurrent) {
        statusBar()->showMessage(tr("Nothing is playing"), 3000);
        return;
    }
    if (m_current.artistId.isEmpty()) {
        statusBar()->showMessage(
            tr("Artist unavailable for this track"), 3000);
        return;
    }
    openArtist(m_current.artistId);
}

void MainWindow::openArtist(const QString &artistId) {
    if (!m_loggedIn || artistId.isEmpty())
        return;
    m_currentArtistId = artistId; // drives the artist-page "Start radio" button
    rememberReturnPage();
    m_stack->setCurrentIndex(5);
    statusBar()->showMessage(tr("Loading…"));

    // Reset the page to a loading state.
    m_artistName->setText(tr("Loading…"));
    m_artistFans->clear();
    m_artistAvatar->setPixmap(placeholderPix(72));
    m_artistTopTracks.clear();
    m_artistTopTable->clearContents();
    m_artistTopTable->setRowCount(0);
    m_artistAlbums.clear();
    m_artistAlbumsGrid->clear();
    m_artistRelated.clear();
    m_artistRelatedGrid->clear();

    const int gen = ++m_artGen; // also invalidates any in-flight cover art
    const QByteArray idb = artistId.toUtf8();
    QtConcurrent::run([this, idb, gen] {
        const QByteArray j = takeJson(DZArtistProfileJSON(cstr(idb)));
        QMetaObject::invokeMethod(this, [this, j, gen] {
            renderArtist(j, gen);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::renderArtist(const QByteArray &json, int gen) {
    if (gen != m_artGen)
        return; // a newer load (another artist / list reload) took over
    const QJsonObject obj = QJsonDocument::fromJson(json).object();
    if (obj.contains("error")) {
        m_artistName->setText(tr("Artist unavailable"));
        statusBar()->showMessage(tr("Couldn't load artist"), 3000);
        return;
    }

    const ArtistInfo info = parseArtistInfo(obj.value("artist").toObject());
    m_artistName->setText(info.name.isEmpty() ? tr("Artist") : info.name);
    m_artistFans->setText(info.nbFans > 0
        ? tr("%n fan(s)", "", info.nbFans)
        : QString());
    m_artistAvatar->setPixmap(placeholderPix(72));
    if (!info.artworkUrl.isEmpty())
        fetchImage(info.artworkUrl, gen, [this](const QImage &img) {
            m_artistAvatar->setPixmap(QPixmap::fromImage(img).scaled(
                72, 72, Qt::KeepAspectRatio, Qt::SmoothTransformation));
        });

    // Top tracks — playable through the shared play path.
    m_artistTopTracks.clear();
    for (const QJsonValue &v : obj.value("top").toArray())
        m_artistTopTracks.push_back(parseTrack(v.toObject()));
    fillTrackTable(m_artistTopTable, m_artistTopTracks, gen);

    // Albums — open through the existing album-tracks path.
    m_artistAlbums.clear();
    m_artistAlbumsGrid->clear();
    for (const QJsonValue &v : obj.value("albums").toArray())
        m_artistAlbums.push_back(parseAlbum(v.toObject()));
    for (int i = 0; i < m_artistAlbums.size(); ++i) {
        const Album &a = m_artistAlbums[i];
        auto *it = new QListWidgetItem(QIcon(placeholderPix(110)),
                                       a.name + "\n" + a.artistLine);
        it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
        it->setData(Qt::UserRole, i);
        m_artistAlbumsGrid->addItem(it);
        if (!a.artworkUrl.isEmpty())
            fetchImage(a.artworkUrl, gen, [it](const QImage &img) {
                it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                    110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
            });
    }

    // Related artists — clicking opens that artist's page (recurses).
    m_artistRelated.clear();
    m_artistRelatedGrid->clear();
    for (const QJsonValue &v : obj.value("related").toArray())
        m_artistRelated.push_back(parseArtistInfo(v.toObject()));
    for (int i = 0; i < m_artistRelated.size(); ++i) {
        const ArtistInfo &ar = m_artistRelated[i];
        auto *it = new QListWidgetItem(QIcon(placeholderPix(110)), ar.name);
        it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
        it->setData(Qt::UserRole, i);
        m_artistRelatedGrid->addItem(it);
        if (!ar.artworkUrl.isEmpty())
            fetchImage(ar.artworkUrl, gen, [it](const QImage &img) {
                it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                    110, 110, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
            });
    }

    statusBar()->showMessage(info.name, 3000);
}

// ---- track table fill + async art ----------------------------------------

// Row title as shown everywhere: the explicit "E" badge, plus a leading "saved
// offline" glyph (⤓) when the track is available offline.
QString MainWindow::displayTitle(const Track &t) const {
    QString s = badgedTitle(t);
    if (m_offlineIds.contains(t.id))
        s = QString::fromUtf8("\xE2\xA4\x93 ") + s; // ⤓ U+2913 downwards-to-bar
    return s;
}

void MainWindow::fillTrackTable(QTableWidget *table, const QVector<Track> &tracks, int gen) {
    table->clearContents();
    table->setRowCount(tracks.size());
    for (int i = 0; i < tracks.size(); ++i) {
        const Track &t = tracks[i];
        auto *title = new QTableWidgetItem(displayTitle(t));
        title->setIcon(placeholderIcon());
        table->setItem(i, 0, title);
        table->setItem(i, 1, new QTableWidgetItem(t.artistLine));
        table->setItem(i, 2, new QTableWidgetItem(t.albumName));
        auto *dur = new QTableWidgetItem(timeText(t.durationMs));
        dur->setTextAlignment(Qt::AlignRight | Qt::AlignVCenter);
        table->setItem(i, 3, dur);
        if (!t.artworkUrl.isEmpty())
            fetchImage(t.artworkUrl, gen, [table, i](const QImage &img) {
                if (auto *it = table->item(i, 0))
                    it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                        40, 40, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
            });
    }
}

// Download bytes on a worker (DZFetch + QImage::fromData are reentrant), then
// apply on the GUI thread. gen guards against a list having been reloaded since;
// pass gen < 0 to always apply (e.g. the now-playing cover).
void MainWindow::fetchImage(const QString &url, int gen, std::function<void(const QImage &)> apply) {
    const QByteArray u = url.toUtf8();
    if (m_artPool.maxThreadCount() > 4)
        m_artPool.setMaxThreadCount(4); // keep the global pool free for play/browse
    QtConcurrent::run(&m_artPool, [this, u, gen, apply] {
        int len = 0;
        unsigned char *p = DZFetch(cstr(u), &len);
        QImage img;
        if (p) {
            if (len > 0)
                img = QImage::fromData(reinterpret_cast<const uchar *>(p), len);
            DZFreeBytes(p);
        }
        QMetaObject::invokeMethod(this, [this, gen, apply, img] {
            if (gen >= 0 && gen != m_artGen)
                return; // list reloaded — drop stale art
            if (!img.isNull())
                apply(img);
        }, Qt::QueuedConnection);
    });
}

// ---- engine queue sync ----------------------------------------------------

// Mirror the whole GUI queue to the engine (DZQueueSet) and align its cursor to
// the playing row (DZQueueSetIndex). Called on every queue (re)build. Both are
// cheap, local engine-state writes (no network / no remote forward — DZQueueSet
// only parses the array and swaps an in-memory slice), so they run directly on
// the GUI thread like DZSetGapless, keeping the engine cursor in lock-step with
// what is audible. The wire shape matches every list call
// ({id,name,durationMs,artistLine,artistId,artists:[{id,name}],albumName,
// artworkUrl,explicit}); js is a named local so it outlives the copying call.
void MainWindow::syncQueueToEngine() {
    QJsonArray arr;
    for (const Track &t : m_queue)
        arr.push_back(trackToJsonObj(t));
    const QByteArray js = QJsonDocument(arr).toJson(QJsonDocument::Compact);
    DZQueueSet(cstr(js));
    DZQueueSetIndex(m_queueIndex);
}

// Realign just the engine queue cursor after an index-only change (next / prev /
// repeat-one), without re-sending the queue contents.
void MainWindow::syncQueueIndex() {
    DZQueueSetIndex(m_queueIndex);
}

// ---- Up-Next queue editor (right dock) ------------------------------------

// Build the "Up Next" dock: the queue list (double-click to jump, context menu
// for play/move/remove/offline) plus a header Clear and footer move/remove
// buttons acting on the selected row. Added to the right dock area; visible by
// default (toggle via the transport button, the View menu or the title-bar X).
void MainWindow::buildQueueDock() {
    m_queueDock = new QDockWidget(tr("Up Next"), this);
    m_queueDock->setObjectName(QStringLiteral("queueDock")); // for saveState()
    m_queueDock->setAllowedAreas(Qt::LeftDockWidgetArea | Qt::RightDockWidgetArea);

    auto *panel = new QWidget;
    auto *v = new QVBoxLayout(panel);
    v->setContentsMargins(8, 8, 8, 8);
    v->setSpacing(6);

    auto *head = new QHBoxLayout;
    auto *title = new QLabel(tr("Up Next"));
    QFont tf = title->font();
    tf.setBold(true);
    title->setFont(tf);
    head->addWidget(title);
    head->addStretch(1);
    auto *clearBtn = new QToolButton;
    clearBtn->setText(tr("Clear"));
    clearBtn->setAutoRaise(true);
    clearBtn->setToolTip(tr("Clear the upcoming tracks"));
    connect(clearBtn, &QToolButton::clicked, this, &MainWindow::queueClear);
    head->addWidget(clearBtn);
    v->addLayout(head);

    m_queueList = new QListWidget;
    m_queueList->setSelectionMode(QAbstractItemView::SingleSelection);
    m_queueList->setWordWrap(false);
    // Double-click (or Enter) a row to jump straight to it.
    connect(m_queueList, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        queueJumpTo(it->data(Qt::UserRole).toInt());
    });
    connect(m_queueList, &QListWidget::itemDoubleClicked, this, [this](QListWidgetItem *it) {
        queueJumpTo(it->data(Qt::UserRole).toInt());
    });
    // Row context menu.
    m_queueList->setContextMenuPolicy(Qt::CustomContextMenu);
    connect(m_queueList, &QWidget::customContextMenuRequested, this,
            [this](const QPoint &pos) {
                QListWidgetItem *it = m_queueList->itemAt(pos);
                if (!it)
                    return;
                const int r = it->data(Qt::UserRole).toInt();
                if (r < 0 || r >= m_queue.size())
                    return;
                const Track t = m_queue[r];
                QMenu menu(this);
                QAction *play = menu.addAction(tr("Play now"));
                QAction *up   = menu.addAction(tr("Move up"));
                QAction *down = menu.addAction(tr("Move down"));
                up->setEnabled(r > 0);
                down->setEnabled(r < m_queue.size() - 1);
                QAction *rem  = menu.addAction(tr("Remove from queue"));
                rem->setEnabled(r != m_queueIndex); // never yank the playing track
                menu.addSeparator();
                QAction *off = menu.addAction(tr("Download for offline"));
                if (m_offlineIds.contains(t.id)) {
                    off->setEnabled(false);
                    off->setText(tr("Available offline"));
                } else if (!m_premium) {
                    off->setEnabled(false);
                    off->setToolTip(tr("Requires a paid Deezer plan"));
                    menu.setToolTipsVisible(true);
                }
                QAction *chosen = menu.exec(m_queueList->viewport()->mapToGlobal(pos));
                if (chosen == play)       queueJumpTo(r);
                else if (chosen == up)    queueMove(r, r - 1);
                else if (chosen == down)  queueMove(r, r + 1);
                else if (chosen == rem)   queueRemoveAt(r);
                else if (chosen == off)   downloadForOffline(t);
            });
    v->addWidget(m_queueList, 1);

    auto *foot = new QHBoxLayout;
    auto *upBtn   = new QToolButton;
    upBtn->setText(QString::fromUtf8("\xE2\x96\xB2")); // ▲
    upBtn->setToolTip(tr("Move up"));
    auto *downBtn = new QToolButton;
    downBtn->setText(QString::fromUtf8("\xE2\x96\xBC")); // ▼
    downBtn->setToolTip(tr("Move down"));
    auto *remBtn  = new QToolButton;
    remBtn->setText(QString::fromUtf8("\xE2\x9C\x95")); // ✕
    remBtn->setToolTip(tr("Remove from queue"));
    for (QToolButton *b : {upBtn, downBtn, remBtn})
        b->setAutoRaise(true);
    connect(upBtn, &QToolButton::clicked, this, [this] {
        const int r = m_queueList->currentRow();
        if (r > 0)
            queueMove(r, r - 1);
    });
    connect(downBtn, &QToolButton::clicked, this, [this] {
        const int r = m_queueList->currentRow();
        if (r >= 0 && r < m_queue.size() - 1)
            queueMove(r, r + 1);
    });
    connect(remBtn, &QToolButton::clicked, this, [this] {
        const int r = m_queueList->currentRow();
        if (r >= 0 && r != m_queueIndex)
            queueRemoveAt(r);
    });
    foot->addWidget(upBtn);
    foot->addWidget(downBtn);
    foot->addWidget(remBtn);
    foot->addStretch(1);
    v->addLayout(foot);

    m_queueDock->setWidget(panel);
    addDockWidget(Qt::RightDockWidgetArea, m_queueDock);
    resizeDocks({m_queueDock}, {280}, Qt::Horizontal);
    m_queueDock->hide(); // revealed after login (finishLogin) — see the toggle
    refreshQueuePanel(); // empty-state placeholder
}

// Render the Up-Next list. The engine queue (DZQueueJSON) is the display source
// once logged in — it mirrors m_queue, which the GUI keeps in lock-step — with
// m_queue as the authority/fallback (a transient size mismatch means the mirror
// is mid-update, so trust the GUI model). Row data() carries the index used by
// jump/move/remove; the playing row is bold + accent.
void MainWindow::refreshQueuePanel() {
    if (!m_queueList)
        return;
    QVector<Track> rows = m_queue;
    if (m_loggedIn) {
        const QVector<Track> eng = parseQueue(takeJson(DZQueueJSON()));
        if (eng.size() == m_queue.size())
            rows = eng; // engine truth == GUI model
    }
    int cur = m_loggedIn ? DZQueueIndex() : m_queueIndex;
    if (cur < 0 || cur >= rows.size())
        cur = m_queueIndex;

    QSignalBlocker block(m_queueList);
    m_queueList->clear();
    if (rows.isEmpty()) {
        auto *empty = new QListWidgetItem(tr("Queue is empty"));
        empty->setFlags(Qt::NoItemFlags);
        m_queueList->addItem(empty);
        return;
    }
    for (int i = 0; i < rows.size(); ++i) {
        const Track &t = rows[i];
        QString label = displayTitle(t);
        if (!t.artistLine.isEmpty())
            label += QStringLiteral("  ·  ") + t.artistLine;
        auto *it = new QListWidgetItem(label);
        it->setData(Qt::UserRole, i);
        it->setToolTip(label);
        if (i == cur) {
            QFont f = it->font();
            f.setBold(true);
            it->setFont(f);
            it->setForeground(QBrush(QColor(kAccent)));
        }
        m_queueList->addItem(it);
    }
    if (cur >= 0 && cur < m_queueList->count())
        m_queueList->scrollToItem(m_queueList->item(cur));
}

// Point the cursor (GUI + engine) at wherever the actually-playing track sits
// after a queue edit, so a reorder/removal can never desync playback from the
// visible queue. Explicitly writing DZQueueSetIndex is safe regardless of any
// cursor auto-adjust the granular engine op may have applied.
void MainWindow::realignQueueCursor() {
    if (m_hasCurrent) {
        for (int i = 0; i < m_queue.size(); ++i)
            if (m_queue[i].id == m_current.id) {
                m_queueIndex = i;
                break;
            }
    }
    if (m_queueIndex >= m_queue.size())
        m_queueIndex = m_queue.size() - 1;
    if (m_loggedIn)
        DZQueueSetIndex(m_queueIndex);
}

// Double-click / "Play now": start the chosen queue row. playCurrent() re-aligns
// the cursor and refreshes the panel.
void MainWindow::queueJumpTo(int row) {
    if (row < 0 || row >= m_queue.size())
        return;
    m_queueIndex = row;
    playCurrent();
}

// Remove a queued track (never the one playing). DZQueueRemove(i) removes the
// same index in the engine mirror; the cursor is then re-anchored to the current
// track's new row.
void MainWindow::queueRemoveAt(int row) {
    if (row < 0 || row >= m_queue.size() || row == m_queueIndex)
        return;
    DZQueueRemove(row);
    m_queue.removeAt(row);
    realignQueueCursor();
    refreshQueuePanel();
    if (m_queueList)
        m_queueList->setCurrentRow(qBound(0, row, m_queueList->count() - 1));
    statusBar()->showMessage(tr("Removed from queue"), 2000);
}

// Reorder: DZQueueMove(from,to) mirrors QList::move(from,to) on the engine side.
void MainWindow::queueMove(int from, int to) {
    if (from < 0 || from >= m_queue.size() || to < 0 || to >= m_queue.size() ||
        from == to)
        return;
    DZQueueMove(from, to);
    m_queue.move(from, to);
    realignQueueCursor();
    refreshQueuePanel();
    if (m_queueList)
        m_queueList->setCurrentRow(to);
}

// "Play next": insert immediately after the current track. DZQueueInsertNext
// inserts one track JSON after the engine cursor; m_queue mirrors it at
// m_queueIndex+1. With nothing playing yet, fall back to building a one-track
// queue via the whole-queue push.
void MainWindow::queueInsertNext(const Track &t) {
    if (t.id.isEmpty())
        return;
    if (m_queue.isEmpty() || m_queueIndex < 0) {
        m_queue.append(t);
        syncQueueToEngine();
        refreshQueuePanel();
        statusBar()->showMessage(tr("Added to queue"), 2000);
        return;
    }
    const QByteArray js = QJsonDocument(trackToJsonObj(t)).toJson(QJsonDocument::Compact);
    DZQueueInsertNext(cstr(js));
    m_queue.insert(m_queueIndex + 1, t);
    realignQueueCursor();
    refreshQueuePanel();
    statusBar()->showMessage(tr("Playing next"), 2000);
}

// "Add to queue": append to the end. No dedicated engine export — push the whole
// queue (DZQueueSet + DZQueueSetIndex via syncQueueToEngine), which byte-matches
// the mirror to m_queue.
void MainWindow::queueAppend(const Track &t) {
    if (t.id.isEmpty())
        return;
    m_queue.append(t);
    syncQueueToEngine();
    refreshQueuePanel();
    statusBar()->showMessage(tr("Added to queue"), 2000);
}

// Clear the upcoming tracks. Keep whatever is playing as the sole remaining row
// (cursor 0) so playback stays coherent; empty the queue outright when idle.
void MainWindow::queueClear() {
    if (m_hasCurrent && !m_currentIsEpisode) {
        m_queue = {m_current};
        m_queueIndex = 0;
    } else {
        m_queue.clear();
        m_queueIndex = -1;
    }
    syncQueueToEngine();
    refreshQueuePanel();
    statusBar()->showMessage(tr("Queue cleared"), 2000);
}

// ---- playback -------------------------------------------------------------

void MainWindow::playFrom(const QVector<Track> &list, int index) {
    if (index < 0 || index >= list.size())
        return;
    m_queue = list;
    m_queueIndex = index;
    syncQueueToEngine(); // remote controllers see + walk this queue
    playCurrent();
}

void MainWindow::playCurrent() {
    if (m_queueIndex < 0 || m_queueIndex >= m_queue.size())
        return;
    const Track t = m_queue[m_queueIndex];
    m_current = t;
    m_hasCurrent = true;
    m_currentIsEpisode = false;
    setNowPlaying(t);
    m_seek->setRange(0, static_cast<int>(qMax<qint64>(1, t.durationMs)));
    m_seek->setValue(0);
    m_posLabel->setText("0:00");
    m_durLabel->setText(timeText(t.durationMs));
    syncQueueIndex(); // keep the engine cursor on the row we're starting
    const QByteArray id = t.id.toUtf8();
    const qint64 dur = t.durationMs;
    // DZPlay prepares the stream over the network — run it off the GUI thread.
    QtConcurrent::run([id, dur] { DZPlay(cstr(id), dur); });
    // Gapless/crossfade: prime the engine with the deterministic next track.
    preloadNext();
    refreshQueuePanel(); // move the "now playing" highlight to this row
}

void MainWindow::setNowPlaying(const Track &t) {
    if (m_explicitBadge)
        m_explicitBadge->setVisible(t.isExplicit);
    if (m_offlineBadge)
        m_offlineBadge->setVisible(m_offlineIds.contains(t.id));
    m_nowPlaying->setText(t.name + "\n" + t.artistLine);
    m_cover->setPixmap(placeholderPix(56));
    refreshLikeButton(); // reflect liked state for the new track

    // Push the new track to the OS media overlay / lock-screen.
    if (m_mpris) {
        m_mpris->updateMetadata(t.name, t.artistLine, t.albumName, t.artworkUrl,
                                t.durationMs, t.id);
        m_mpris->updateStatus(QStringLiteral("Playing"));
        m_lastStatus = QStringLiteral("Playing");
    }
    const int token = ++m_playGen; // a newer track invalidates this cover
    if (!t.artworkUrl.isEmpty())
        fetchImage(t.artworkUrl, -1, [this, token](const QImage &img) {
            if (token != m_playGen)
                return; // a later track started before this cover arrived
            m_cover->setPixmap(QPixmap::fromImage(img).scaled(
                56, 56, Qt::KeepAspectRatio, Qt::SmoothTransformation));
        });
}

// DZTogglePause is instant locally but a blocking HTTP request when routed to
// a Connect device — never call it on the GUI thread.
void MainWindow::togglePause() {
    QtConcurrent::run([] { DZTogglePause(); });
}

void MainWindow::next() {
    if (m_queue.isEmpty())
        return;
    if (m_shuffle && m_queue.size() > 1) {
        int n = m_queueIndex;
        while (n == m_queueIndex)
            n = QRandomGenerator::global()->bounded(m_queue.size());
        m_queueIndex = n;
    } else if (m_queueIndex + 1 < m_queue.size()) {
        m_queueIndex++;
    } else if (m_repeat == 1) {
        m_queueIndex = 0;
    } else {
        return;
    }
    playCurrent();
}

void MainWindow::prev() {
    if (m_queue.isEmpty())
        return;
    if (m_queueIndex > 0)
        m_queueIndex--;
    playCurrent();
}

// The next index that will play if nothing intervenes — linear next, or wrap to
// 0 under repeat-all. Returns -1 when there is no deterministic next (shuffle,
// repeat-one, or end of a non-repeating queue), in which case nothing is
// preloaded and the engine won't gaplessly swap.
int MainWindow::nextIndexDeterministic() const {
    if (m_shuffle || m_repeat == 2 || m_queue.isEmpty())
        return -1;
    if (m_queueIndex + 1 < m_queue.size())
        return m_queueIndex + 1;
    if (m_repeat == 1)
        return 0; // repeat-all wraps deterministically
    return -1;
}

// Preload the deterministic next track so the engine can swap to it gaplessly /
// with a crossfade when the current one ends. No-op unless that's enabled.
void MainWindow::preloadNext() {
    if (!autoTransitionEnabled())
        return;
    const int ni = nextIndexDeterministic();
    if (ni < 0 || ni >= m_queue.size())
        return;
    const Track t = m_queue[ni];
    if (t.id.isEmpty())
        return;
    const QByteArray id = t.id.toUtf8();
    const qint64 dur = t.durationMs;
    QtConcurrent::run([id, dur] { DZPreload(cstr(id), dur); });
}

// DZSetVolume is instant locally but a blocking HTTP request (15 s timeout)
// when routed to a Connect device, and valueChanged fires for every step of a
// slider drag — send from a single worker that always pushes the newest value,
// coalescing the intermediate ones.
void MainWindow::setVolume(int percent) {
    if (m_mpris)
        m_mpris->updateVolume(percent / 100.0);
    m_pendingVol.store(percent);
    if (m_volInFlight.exchange(true))
        return; // the live sender picks the new value up
    QtConcurrent::run([this] {
        int sent = -1;
        for (;;) {
            const int v = m_pendingVol.load();
            if (v != sent) {
                DZSetVolume(v / 100.0);
                sent = v;
                continue;
            }
            m_volInFlight.store(false);
            // A setVolume() racing between the load and the store above saw
            // the sender as still alive and didn't start one — reclaim it.
            if (m_pendingVol.load() == sent || m_volInFlight.exchange(true))
                return;
        }
    });
}

// ---- OpenDeezer Connect (LAN device picker) -------------------------------

// Cast button: discover OpenDeezer instances on the LAN, then offer a Spotify-
// Connect-style picker. Discovery blocks ~0.7s on the network, so it runs on a
// worker and the modal picker opens with the results (mirroring addTrackToPlaylist).
void MainWindow::openConnectPicker() {
    if (!m_loggedIn)
        return;
    // Re-entrancy guard: ignore extra clicks while a scan is already in flight so
    // a burst of clicks can't stack worker scans (and, in turn, stacked dialogs).
    if (m_connectScanning)
        return;
    m_connectScanning = true;
    // Loading state: disable the button and announce the scan. Discovery ALWAYS
    // re-runs here, so every open reflects the current LAN — never a stale list.
    if (m_connectBtn)
        m_connectBtn->setEnabled(false);
    statusBar()->showMessage(tr("Scanning for devices…"));
    QtConcurrent::run([this] {
        const QVector<ConnectDevice> devs = parseDevices(takeJson(DZDiscoverDevices(700)));
        QString connected;
        if (char *c = DZConnectedDevice()) {
            connected = QString::fromUtf8(c);
            DZFree(c);
        }
        QMetaObject::invokeMethod(this, [this, devs, connected] {
            statusBar()->clearMessage();
            if (m_connectBtn)
                m_connectBtn->setEnabled(true);
            m_connectScanning = false;
            // A brand-new dialog + QListWidget is built from these fresh results
            // every time, so no stale device state can survive between opens.
            showConnectPicker(devs, connected);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::showConnectPicker(const QVector<ConnectDevice> &devices,
                                   const QString &connectedAddr) {
    QDialog dlg(this);
    dlg.setWindowTitle(QStringLiteral("OpenDeezer Connect"));
    auto *v = new QVBoxLayout(&dlg);
    v->addWidget(new QLabel(tr("Play on a device:")));
    auto *list = new QListWidget;

    // Mark the active entry (bold + accent), matching the lyrics-highlight style.
    auto markActive = [](QListWidgetItem *it) {
        QFont f = it->font();
        f.setBold(true);
        it->setFont(f);
        it->setForeground(QBrush(QColor(kAccent)));
    };

    // "This computer" — selecting it returns playback here (DZDisconnectDevice).
    auto *here = new QListWidgetItem(tr("This computer") + QStringLiteral("\n") + tr("Local playback"));
    here->setData(Qt::UserRole, QString());  // empty addr = local
    list->addItem(here);
    QListWidgetItem *active = here;          // current connection (default: local)
    if (connectedAddr.isEmpty())
        markActive(here);

    for (const ConnectDevice &d : devices) {
        QString sub = deviceTypeLabel(d.client);
        if (!d.version.isEmpty())
            sub += QStringLiteral(" · OpenDeezer ") + d.version;
        auto *it = new QListWidgetItem(
            (d.name.isEmpty() ? QStringLiteral("OpenDeezer") : d.name) + "\n" + sub);
        it->setData(Qt::UserRole, d.addr);
        it->setData(Qt::UserRole + 1, d.name);
        list->addItem(it);
        if (!d.addr.isEmpty() && d.addr == connectedAddr) {
            markActive(it);
            active = it;
        }
    }
    if (devices.isEmpty()) {
        auto *none = new QListWidgetItem(tr("No devices found."));
        none->setFlags(Qt::NoItemFlags); // a hint, not a selectable row
        list->addItem(none);
    }

    list->setCurrentItem(active);
    v->addWidget(list, 1);
    auto *bb = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    v->addWidget(bb);
    connect(bb, &QDialogButtonBox::accepted, &dlg, &QDialog::accept);
    connect(bb, &QDialogButtonBox::rejected, &dlg, &QDialog::reject);
    connect(list, &QListWidget::itemActivated, &dlg, &QDialog::accept);
    if (dlg.exec() != QDialog::Accepted)
        return;
    QListWidgetItem *sel = list->currentItem();
    if (!sel || !(sel->flags() & Qt::ItemIsSelectable))
        return;
    const QString addr = sel->data(Qt::UserRole).toString();
    if (addr.isEmpty())
        disconnectDevice();
    else
        connectDevice(addr, sel->data(Qt::UserRole + 1).toString());
}

// Route playback to the chosen device. DZConnectDevice does a network handshake,
// so it runs on a worker; the button + status bar reflect the result.
void MainWindow::connectDevice(const QString &addr, const QString &name) {
    const QByteArray ab = addr.toUtf8();
    const QString label = name.isEmpty() ? addr : name;
    m_connectName = label;
    statusBar()->showMessage(tr("Connecting to %1…").arg(label));
    QtConcurrent::run([this, ab, label] {
        const int ok = DZConnectDevice(cstr(ab));
        QMetaObject::invokeMethod(this, [this, ok, label] {
            if (!ok)
                m_connectName.clear();
            statusBar()->showMessage(ok
                ? tr("Playing on %1").arg(label)
                : tr("Couldn't connect to %1").arg(label), 4000);
            refreshConnectButton();
        }, Qt::QueuedConnection);
    });
}

// Return playback to this computer. DZDisconnectDevice sends a synchronous
// Stop to the remote peer, so it runs on a worker like connectDevice.
void MainWindow::disconnectDevice() {
    statusBar()->showMessage(tr("Disconnecting…"));
    QtConcurrent::run([this] {
        DZDisconnectDevice();
        QMetaObject::invokeMethod(this, [this] {
            m_connectName.clear();
            statusBar()->showMessage(tr("Playing on this computer"), 3000);
            refreshConnectButton();
        }, Qt::QueuedConnection);
    });
}

// Paint the repeat toggle for the given mode (0 off, 1 all, 2 one) and record it
// in m_repeat. Emits nothing to the engine — the click handler commands DZSetRepeat
// and tick() calls this to reconcile the button with engine truth (phone remote /
// control API / routed device). Uses QToolButton::clicked (not toggled), so
// setChecked here never re-fires the cycle handler.
void MainWindow::applyRepeatButton(int mode) {
    m_repeat = mode;
    if (!m_repeatBtn)
        return;
    m_repeatBtn->setIcon(QIcon::fromTheme(
        mode == 2 ? QStringLiteral("media-playlist-repeat-song")
                  : QStringLiteral("media-playlist-repeat")));
    m_repeatBtn->setChecked(mode != 0);
    m_repeatBtn->setToolTip(mode == 0 ? tr("Repeat: off")
                            : mode == 1 ? tr("Repeat: all")
                                        : tr("Repeat: one"));
}

// Set the shuffle toggle without re-commanding the engine. m_shuffleBtn drives
// DZSetShuffle from its toggled() signal, so tick()'s reconcile blocks signals
// while syncing the button to engine truth to avoid a feedback command.
void MainWindow::applyShuffleButton(bool on) {
    m_shuffle = on;
    if (!m_shuffleBtn)
        return;
    QSignalBlocker block(m_shuffleBtn);
    m_shuffleBtn->setChecked(on);
}

// Paint the cast button: accent + the device name in the tooltip when routed to
// a remote device, plain otherwise. The connected address is authoritative.
void MainWindow::refreshConnectButton() {
    if (!m_connectBtn)
        return;
    QString connected;
    if (char *c = DZConnectedDevice()) {
        connected = QString::fromUtf8(c);
        DZFree(c);
    }
    m_castAddr = connected; // cache the truth tick() diffs against
    const bool remote = !connected.isEmpty();
    m_connectBtn->setStyleSheet(remote ? QString("QToolButton{color:%1;}").arg(kAccent)
                                       : QString());
    m_connectBtn->setToolTip(remote
        ? tr("Connected to %1 — choose a device")
              .arg(m_connectName.isEmpty() ? connected : m_connectName)
        : tr("Connect to a device"));

    // Persistent "Playing on <device>" indicator in the status-bar corner while
    // routed to a Connect device; hidden when playing locally. Built lazily on
    // first cast (mirrors the Free-quality hint).
    if (remote) {
        const QString name = m_connectName.isEmpty() ? connected : m_connectName;
        if (!m_castLabel) {
            m_castLabel = new QLabel;
            m_castLabel->setStyleSheet(QString("color:%1;font-weight:bold;").arg(kAccent));
            statusBar()->addPermanentWidget(m_castLabel);
        }
        m_castLabel->setText(tr("Playing on %1").arg(name));
        m_castLabel->show();
    } else if (m_castLabel) {
        m_castLabel->hide();
    }
}

// ---- poll loop ------------------------------------------------------------

void MainWindow::tick() {
    if (!m_loggedIn)
        return;
    const int   st  = DZState();
    const qint64 pos = DZPositionMS();
    const qint64 dur = DZDurationMS();

    if (dur > 0 && m_seek->maximum() != static_cast<int>(dur))
        m_seek->setRange(0, static_cast<int>(dur));
    if (!m_seeking)
        m_seek->setValue(static_cast<int>(qMin<qint64>(pos, m_seek->maximum())));
    m_posLabel->setText(timeText(pos));
    if (dur > 0)
        m_durLabel->setText(timeText(dur));

    m_playBtn->setIcon(style()->standardIcon(
        st == 2 ? QStyle::SP_MediaPause : QStyle::SP_MediaPlay));

    // Reconcile the repeat/shuffle toggles with the engine's truth so external
    // changes (phone remote, control API, a routed Connect device) reflect. The
    // click/toggle handlers command the engine; this reads it back. DZGetRepeat
    // returns "off"/"all"/"one" (malloc'd — free with DZFree).
    if (char *rp = DZGetRepeat()) {
        const QByteArray mode(rp);
        DZFree(rp);
        const int rm = mode == "one" ? 2 : mode == "all" ? 1 : 0;
        if (rm != m_repeat)
            applyRepeatButton(rm);
    }
    const bool sh = (DZGetShuffle() != 0);
    if (sh != m_shuffle)
        applyShuffleButton(sh);

    // Mirror an externally-changed volume (phone remote / control API / routed
    // device) back into the slider while no local send is pending, so the slider
    // tracks reality and the next local nudge can't snap audio to a stale value.
    // Guard against a slider drag in progress. Block signals so setValue doesn't
    // bounce back through setVolume as a redundant command. (Mirrors macOS.)
    if (!m_volInFlight.load() && !m_vol->isSliderDown()) {
        const int v = static_cast<int>(qRound(DZVolume() * 100));
        if (v != m_vol->value()) {
            QSignalBlocker block(m_vol);
            m_vol->setValue(v);
        }
    }

    // Keep the casting indicator honest against the engine's connected-device
    // truth (covers a remote-initiated disconnect). DZConnectedDevice is a cheap
    // cached read; only repaint when it actually changes.
    {
        QString conn;
        if (char *c = DZConnectedDevice()) {
            conn = QString::fromUtf8(c);
            DZFree(c);
        }
        if (conn != m_castAddr)
            refreshConnectButton();
    }

    // Mirror playback status + position to the OS media controls. DZState enum:
    // 0 Stopped, 1 Loading, 2 Playing, 3 Paused, 4 Errored.
    if (m_mpris) {
        const QString status = st == 2 ? QStringLiteral("Playing")
                               : st == 3 ? QStringLiteral("Paused")
                                         : QStringLiteral("Stopped");
        if (status != m_lastStatus) {
            m_mpris->updateStatus(status);
            m_lastStatus = status;
        }
        m_mpris->updatePosition(pos);
    }

    // Keep the now-playing display in sync with the engine's truth: tracks started
    // on this device AND, when routed via OpenDeezer Connect, the remote device's
    // current track. Act only on a real track whose id differs from what's shown;
    // an empty object ("{}", no id) leaves the display untouched (keep last). The
    // id guard means setNowPlaying — and so the artwork refetch — runs only on an
    // actual change, avoiding flicker and redundant cover downloads.
    const QByteArray npJson = takeJson(DZNowPlayingJSON());
    if (!npJson.isEmpty()) {
        const Track nt = parseTrack(QJsonDocument::fromJson(npJson).object());
        if (!nt.id.isEmpty() && nt.id != m_current.id) {
            m_current = nt;
            m_hasCurrent = true;
            m_currentIsEpisode = false;
            setNowPlaying(nt);
        }
    }

    // Follow the engine queue cursor. Once the GUI mirrors its queue to the
    // engine (DZQueueSet + DZQueueSetIndex), the ENGINE owns natural-finish
    // auto-advance and DZFinishedCount stops bumping for those finishes — so the
    // counter-driven block at the bottom won't fire for them. Adopt an
    // engine-driven advance into the GUI's queue pointer (now-playing was
    // already refreshed above from DZNowPlayingJSON) and re-arm the
    // deterministic preload so gapless/crossfade keeps working for later tracks.
    // qi == -1 means no synced queue (e.g. a podcast episode) — leave the
    // DZFinishedCount path in charge there. A user-initiated next/prev keeps the
    // cursor aligned via syncQueueIndex(), so qi == m_queueIndex and this no-ops.
    const int qi = DZQueueIndex();
    if (qi >= 0 && qi < m_queue.size() && qi != m_queueIndex) {
        m_queueIndex = qi;
        preloadNext();
        refreshQueuePanel(); // follow the engine-driven advance in the Up-Next panel
    }

    // Show the actual output format next to the now-playing title.
    if (m_hasCurrent) {
        QString sub = m_current.artistLine;
        if (char *fp = DZFormat()) {
            if (*fp)
                sub += QStringLiteral("   ·   ") + QString::fromUtf8(fp);
            DZFree(fp);
        }
        if (m_explicitBadge)
            m_explicitBadge->setVisible(m_current.isExplicit);
        m_nowPlaying->setText(m_current.name + "\n" + sub);
    }

    // Flag when the engine is only streaming a 30-second preview (e.g. a track
    // not fully available on the current plan / region). Hidden when idle.
    if (m_previewBadge)
        m_previewBadge->setVisible(m_hasCurrent && DZIsPreview() == 1);

    // Reconcile the "offline" badge with the current track (covers a track that
    // was saved for offline while it kept playing).
    if (m_offlineBadge)
        m_offlineBadge->setVisible(m_hasCurrent && !m_currentIsEpisode &&
                                   m_offlineIds.contains(m_current.id));

    // Lyrics page: follow the playing track and keep the synced line highlighted.
    if (m_stack->currentIndex() == 4) {
        if (m_lyricsFollowsPlayback && m_hasCurrent &&
            m_current.id != m_lyricsRequestedId)
            loadLyrics(m_current.id,
                       m_current.name + QStringLiteral("   ·   ") + m_current.artistLine);
        updateLyricsHighlight(pos);
    }

    const int f = DZFinishedCount();
    if (f != m_lastFinished) {
        m_lastFinished = f;
        const int ni = nextIndexDeterministic();
        if (m_repeat == 2) {
            playCurrent(); // repeat-one
        } else if (autoTransitionEnabled() && ni >= 0 && DZState() == 2 /* Playing */) {
            // The engine already swapped to the preloaded next track gaplessly /
            // with a crossfade, so it is still playing. Advance the UI's queue
            // pointer WITHOUT a fresh DZPlay, refresh the now-playing surfaces,
            // and prime the track after this one.
            m_queueIndex = ni;
            m_current = m_queue[m_queueIndex];
            m_hasCurrent = true;
            m_currentIsEpisode = false;
            setNowPlaying(m_current);
            m_seek->setRange(0, static_cast<int>(qMax<qint64>(1, m_current.durationMs)));
            m_durLabel->setText(timeText(m_current.durationMs));
            preloadNext();
            refreshQueuePanel(); // advance the Up-Next highlight
        } else {
            next(); // no preload (or engine stopped) — start the next normally
        }
    }
}
