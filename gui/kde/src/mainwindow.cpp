#include "mainwindow.h"
#include "logindialog.h"
#include "mpris.h"
#include "settingsdialog.h"

#include <QAction>
#include <QAbstractItemView>
#include <QApplication>
#include <QBrush>
#include <QCloseEvent>
#include <QColor>
#include <QDialog>
#include <QDialogButtonBox>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QFont>
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
#include <QPushButton>
#include <QRandomGenerator>
#include <QSlider>
#include <QSplitter>
#include <QStackedWidget>
#include <QStatusBar>
#include <QStringList>
#include <QStyle>
#include <QSystemTrayIcon>
#include <QTableWidget>
#include <QTableWidgetItem>
#include <QTimer>
#include <QToolButton>
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

namespace {

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
    const QJsonArray as = o.value("artists").toArray();
    if (!as.isEmpty())
        t.artistId = as.first().toObject().value("id").toString();
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

    // Load persisted settings (config dir lives alongside arl.txt).
    const QString cfg = settingsPath();
    QDir().mkpath(QFileInfo(cfg).absolutePath());
    m_quality = SettingsDialog::loadQuality(cfg);
    m_closeToTray = SettingsDialog::loadCloseToTray(cfg);

    buildMenu();
    buildSidebar();

    m_stack = new QStackedWidget;
    m_stack->addWidget(buildTracksPage());          // index 0
    m_stack->addWidget(buildPlaylistsPage());       // index 1
    m_stack->addWidget(buildSearchPage());          // index 2
    m_stack->addWidget(buildLyricsPage());          // index 3
    m_stack->addWidget(buildArtistPage());          // index 4
    m_stack->addWidget(buildChartsPage());          // index 5
    m_stack->addWidget(buildPodcastsPage());        // index 6
    m_stack->addWidget(buildPodcastEpisodesPage()); // index 7

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

    // The whole app lives in a top-level stack so a Free account can be gated
    // behind a blocking "Premium required" page without tearing down the live
    // widgets (see showFreeAccountBlock).
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

    statusBar()->showMessage("Logging in…");
    // Defer to the event loop: startLogin() may exec() the modal login dialog,
    // and running that nested loop from inside the constructor (before the main
    // window is shown / app.exec() runs) blocks construction so no window ever
    // appears. singleShot(0) fires it after the window is up.
    QTimer::singleShot(0, this, &MainWindow::startLogin);
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
    connect(m_mpris, &MprisController::playRequested,  this, [this] { DZResume(); });
    connect(m_mpris, &MprisController::pauseRequested, this, [this] { DZPause(); });
    connect(m_mpris, &MprisController::stopRequested,  this, [this] { DZStop(); });
    connect(m_mpris, &MprisController::seekRequested, this, [this](qlonglong offUs) {
        // MPRIS Seek is relative (µs); the engine seeks to an absolute ms.
        const qint64 target = qMax<qint64>(0, DZPositionMS() + offUs / 1000);
        DZSeek(target);
        m_mpris->notifySeeked(target);
    });
    connect(m_mpris, &MprisController::setPositionRequested, this, [this](qlonglong posUs) {
        const qint64 ms = qMax<qint64>(0, posUs / 1000);
        DZSeek(ms);
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
    auto *restore = menu->addAction(QStringLiteral("Show OpenDeezer"));
    connect(restore, &QAction::triggered, this, [this] {
        showNormal();
        raise();
        activateWindow();
    });
    menu->addSeparator();
    auto *quit = menu->addAction(QStringLiteral("Quit"));
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

    SettingsDialog dlg(settingsPath(), devices, curDevice, this);
    connect(&dlg, &SettingsDialog::qualityChanged, this, &MainWindow::applyQuality);
    connect(&dlg, &SettingsDialog::replayGainChanged, this, &MainWindow::applyReplayGain);
    connect(&dlg, &SettingsDialog::closeToTrayChanged, this,
            [this](bool on) { m_closeToTray = on; });
    connect(&dlg, &SettingsDialog::outputDeviceChanged, this, &MainWindow::applyAudioDevice);
    connect(&dlg, &SettingsDialog::gaplessChanged, this, &MainWindow::applyGapless);
    connect(&dlg, &SettingsDialog::crossfadeChanged, this, &MainWindow::applyCrossfade);
    dlg.exec();
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
    const char *names[] = {"Normal (MP3 128)", "High (MP3 320)", "HiFi (FLAC)"};
    QString msg = QStringLiteral("Quality: ") +
                  names[level < 0 ? 0 : (level > 2 ? 2 : level)];
    // Note when the chosen tier exceeds the account's entitlement; the engine
    // transparently falls back, so this is informational only.
    if (m_haveAccount) {
        if (level >= 2 && !m_canHifi)
            msg += QStringLiteral(" — your plan has no HiFi; the engine will fall back");
        else if (level >= 1 && !m_canHq)
            msg += QStringLiteral(" — your plan has no High quality; the engine will fall back");
    }
    statusBar()->showMessage(msg, 4000);
}

void MainWindow::applyReplayGain(bool on) {
    m_replayGain = on;
    DZSetReplayGain(on ? 1 : 0);
    statusBar()->showMessage(on ? QStringLiteral("ReplayGain: on")
                                : QStringLiteral("ReplayGain: off"), 3000);
}

// Switching the output device reinitialises the audio backend, which can briefly
// block — do it off the GUI thread.
void MainWindow::applyAudioDevice(const QString &deviceId) {
    const QByteArray idb = deviceId.toUtf8();
    QtConcurrent::run([this, idb] {
        const int ok = DZSetAudioDevice(cstr(idb));
        QMetaObject::invokeMethod(this, [this, ok] {
            statusBar()->showMessage(ok ? QStringLiteral("Output device changed")
                                        : QStringLiteral("Couldn't change output device"),
                                     3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::applyGapless(bool on) {
    m_gapless = on;
    DZSetGapless(on ? 1 : 0);
    statusBar()->showMessage(on ? QStringLiteral("Gapless: on")
                                : QStringLiteral("Gapless: off"), 3000);
    // Keep the next-track preload in sync with the new setting.
    preloadNext();
}

void MainWindow::applyCrossfade(int ms) {
    m_crossfadeMs = ms;
    DZSetCrossfadeMS(ms);
    statusBar()->showMessage(ms > 0
        ? QStringLiteral("Crossfade: %1s").arg(ms / 1000)
        : QStringLiteral("Crossfade: off"), 3000);
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
                                QStringLiteral("Still playing in the background."),
                                appIcon(), 4000);
            m_trayHintShown = true;
        }
        return;
    }
    DZStop();
    QMainWindow::closeEvent(event);
    qApp->quit();
}

// ---- menu -----------------------------------------------------------------

void MainWindow::buildMenu() {
    auto *file = menuBar()->addMenu("&File");
    // Reachable even when already auto-logged-in from a stored ARL — opens the
    // Deezer web-login dialog on demand (sign in / switch account).
    auto *login = file->addAction("&Log in / Switch account…");
    connect(login, &QAction::triggered, this, [this] { promptLogin(); });
    file->addSeparator();
    auto *settings = file->addAction("&Settings…");
    settings->setShortcut(QKeySequence::Preferences);
    connect(settings, &QAction::triggered, this, &MainWindow::openSettings);
    file->addSeparator();
    auto *quit = file->addAction("&Quit");
    quit->setShortcut(QKeySequence::Quit);
    connect(quit, &QAction::triggered, this, &MainWindow::quitApp);

    auto *help = menuBar()->addMenu("&Help");
    auto *about = help->addAction("&About OpenDeezer");
    connect(about, &QAction::triggered, this, [this] {
        QString text =
            "<h3>OpenDeezer 0.6.0</h3>"
            "<p>An open source reimplementation of Deezer.</p>"
            "<p>Native KDE / Qt6 client. The engine (login, browse, Blowfish"
            " decrypt, MP3 decode, playback) is a Go core linked in-process.</p>";
        // Show the signed-in account tier (from DZAccountJSON) when available.
        if (m_haveAccount && !m_accountName.isEmpty())
            text += QStringLiteral("<p>Signed in as <b>%1</b> · %2</p>")
                        .arg(m_accountName.toHtmlEscaped(),
                             m_accountOffer.toHtmlEscaped());
        text += "<p>By <b>Cycl0o0</b>.<br>Licensed under <b>AGPL-3.0</b>.</p>";
        QMessageBox::about(this, "About OpenDeezer", text);
    });
}

// ---- sidebar --------------------------------------------------------------

void MainWindow::buildSidebar() {
    m_sidebar = new QListWidget;
    m_sidebar->setMaximumWidth(240);
    m_sidebar->addItem(QStringLiteral("♥  Liked Songs")); // 0
    m_sidebar->addItem(QStringLiteral("⚡  Flow"));         // 1
    m_sidebar->addItem(QStringLiteral("☰  Playlists"));    // 2
    m_sidebar->addItem(QStringLiteral("⌕  Search"));       // 3
    m_sidebar->addItem(QStringLiteral("★  Charts"));       // 4
    m_sidebar->addItem(QStringLiteral("◉  Podcasts"));     // 5
    connect(m_sidebar, &QListWidget::currentRowChanged, this, &MainWindow::onSidebarChanged);
}

void MainWindow::onSidebarChanged(int row) {
    switch (row) {
    case 0:
        m_stack->setCurrentIndex(0);
        loadFavorites();
        break;
    case 1:
        m_stack->setCurrentIndex(0); // Flow loads into the shared track table
        loadFlow();
        break;
    case 2:
        m_stack->setCurrentIndex(1);
        loadPlaylists();
        break;
    case 3:
        m_stack->setCurrentIndex(2);
        if (m_searchEdit)
            m_searchEdit->setFocus();
        break;
    case 4:
        m_stack->setCurrentIndex(5); // dedicated charts page
        loadCharts();
        break;
    case 5:
        m_stack->setCurrentIndex(6); // podcasts shows page
        if (m_podcastSearchEdit)
            m_podcastSearchEdit->setFocus();
        break;
    default:
        break;
    }
}

// ---- pages ----------------------------------------------------------------

QTableWidget *MainWindow::makeTrackTable() {
    auto *t = new QTableWidget(0, 4);
    t->setHorizontalHeaderLabels({"Title", "Artist", "Album", "Duration"});
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
    m_tracksHeader = new QLabel("Liked Songs");
    QFont f = m_tracksHeader->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    m_tracksHeader->setFont(f);
    v->addWidget(m_tracksHeader);

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
    auto *title = new QLabel("Your Playlists");
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    head->addWidget(title);
    head->addStretch(1);
    auto *newBtn = new QPushButton(QStringLiteral("＋ New Playlist"));
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
                QAction *open = menu.addAction(QStringLiteral("Open"));
                QAction *ren  = menu.addAction(QStringLiteral("Rename…"));
                QAction *del  = menu.addAction(QStringLiteral("Delete…"));
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
    m_searchEdit->setPlaceholderText("Search Deezer…");
    auto *btn = new QPushButton("Search");
    top->addWidget(m_searchEdit, 1);
    top->addWidget(btn);
    v->addLayout(top);

    v->addWidget(new QLabel("Tracks"));
    m_searchTrackTable = makeTrackTable();
    connect(m_searchTrackTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_searchTracks, row); });
    installTrackMenu(m_searchTrackTable, &m_searchTracks);
    v->addWidget(m_searchTrackTable, 2);

    v->addWidget(new QLabel("Albums & Playlists"));
    m_searchResults = new QListWidget;
    m_searchResults->setViewMode(QListView::IconMode);
    m_searchResults->setIconSize(QSize(110, 110));
    m_searchResults->setGridSize(QSize(140, 165));
    m_searchResults->setResizeMode(QListView::Adjust);
    m_searchResults->setMovement(QListView::Static);
    m_searchResults->setWordWrap(true);
    connect(m_searchResults, &QListWidget::itemActivated, this, [this](QListWidgetItem *it) {
        const int kind = it->data(Qt::UserRole).toInt();       // 0 album, 1 playlist
        const int idx  = it->data(Qt::UserRole + 1).toInt();
        if (kind == 0 && idx < m_searchAlbums.size())
            openAlbum(m_searchAlbums[idx]);
        else if (kind == 1 && idx < m_searchPlaylists.size())
            openPlaylist(m_searchPlaylists[idx]);
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

    m_nowPlaying = new QLabel("Not playing");
    m_nowPlaying->setMinimumWidth(180);
    h->addWidget(m_nowPlaying, 0);

    // Lyrics / Artist detail for the current track, sitting next to its title.
    auto *lyricsBtn = new QToolButton;
    lyricsBtn->setText(QStringLiteral("Lyrics"));
    lyricsBtn->setAutoRaise(true);
    lyricsBtn->setToolTip(QStringLiteral("Show lyrics for the current track"));
    connect(lyricsBtn, &QToolButton::clicked, this, &MainWindow::openLyrics);
    h->addWidget(lyricsBtn);

    auto *artistBtn = new QToolButton;
    artistBtn->setText(QStringLiteral("Artist"));
    artistBtn->setAutoRaise(true);
    artistBtn->setToolTip(QStringLiteral("Open the current track's artist"));
    connect(artistBtn, &QToolButton::clicked, this, &MainWindow::openArtistForCurrent);
    h->addWidget(artistBtn);

    // Heart toggle: like/unlike the current track (DZAddFavorite/DZRemoveFavorite).
    m_likeBtn = new QToolButton;
    m_likeBtn->setAutoRaise(true);
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
    h->addWidget(m_seek, 1);
    m_durLabel = new QLabel("0:00");
    h->addWidget(m_durLabel);

    connect(m_seek, &QSlider::sliderPressed, this, [this] { m_seeking = true; });
    connect(m_seek, &QSlider::sliderReleased, this, [this] {
        m_seeking = false;
        DZSeek(m_seek->value());
        if (m_mpris)
            m_mpris->notifySeeked(m_seek->value()); // discontinuous jump
    });
    connect(m_seek, &QSlider::valueChanged, this, [this](int v) {
        if (m_seeking)
            m_posLabel->setText(timeText(v));
    });

    m_shuffleBtn = new QToolButton;
    m_shuffleBtn->setText("Shuffle");
    m_shuffleBtn->setCheckable(true);
    m_shuffleBtn->setAutoRaise(true);
    connect(m_shuffleBtn, &QToolButton::toggled, this, [this](bool on) { m_shuffle = on; });
    h->addWidget(m_shuffleBtn);

    m_repeatBtn = new QToolButton;
    m_repeatBtn->setText("Repeat: Off");
    m_repeatBtn->setAutoRaise(true);
    connect(m_repeatBtn, &QToolButton::clicked, this, [this] {
        m_repeat = (m_repeat + 1) % 3;
        m_repeatBtn->setText(m_repeat == 0 ? "Repeat: Off"
                             : m_repeat == 1 ? "Repeat: All"
                                             : "Repeat: One");
    });
    h->addWidget(m_repeatBtn);

    h->addWidget(new QLabel("Vol"));
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
            } else {
                // The stored ARL is stale — fall back to the login dialog so the
                // user can re-authenticate without editing files by hand.
                statusBar()->showMessage("Stored login expired — please sign in", 4000);
                promptLogin();
            }
        }, Qt::QueuedConnection);
    });
}

// Show the Deezer login dialog (embedded webview with automatic arl capture, or
// manual ARL entry). The dialog verifies + persists the ARL with DZInit itself,
// so on Accepted the engine is already logged in; we just bring the app up.
void MainWindow::promptLogin() {
    statusBar()->showMessage("Log in to continue");
    LoginDialog dlg(arlConfigPath(), this);
    if (dlg.exec() == QDialog::Accepted) {
        finishLogin(takeJson(DZAccountJSON()));
    } else {
        statusBar()->showMessage("Not logged in");
    }
}

// Post-login bring-up shared by the auto-login (stored ARL) and dialog paths.
// The engine is already logged in by the time this runs; acct is DZAccountJSON.
void MainWindow::finishLogin(const QByteArray &acct) {
    m_loggedIn = true;
    applyAccount(acct);          // tier + HiFi/HQ entitlements
    // Free accounts can't stream on-demand — gate the whole app behind a blocking
    // "Premium required" page. Both auto-login (stored ARL) and the manual
    // ARL/webview dialog land here, so a Free login can never reach the service.
    if (!m_premium) {
        showFreeAccountBlock();
        return;
    }
    // A Premium account (re-)logged in: make sure the app UI is the visible page.
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
    m_poll->start();
    m_sidebar->setCurrentRow(0); // triggers loadFavorites()
    const QString conn = (m_haveAccount && !m_accountName.isEmpty())
        ? m_accountName + " · " + m_accountOffer
        : QStringLiteral("Connected");
    statusBar()->showMessage(conn, 4000);
}

// Gate a Free (non-Premium) account: OpenDeezer streams on-demand, which a Deezer
// Free plan can't do, so swap the whole window to a hardcoded blocking page. The
// live app widgets stay alive on stack page 0 but are unreachable; only the menu
// bar (Quit / Log in to switch account) and the page's Quit button remain. No
// browsing or playback is started.
void MainWindow::showFreeAccountBlock() {
    if (m_poll)
        m_poll->stop();
    DZStop();   // nothing should be playing on a Free account

    const QString offer = m_accountOffer.isEmpty()
        ? QStringLiteral("Deezer Free") : m_accountOffer;
    const QString body =
        QStringLiteral("OpenDeezer needs a Deezer Premium subscription to stream. "
                       "Your account: %1. Subscribe at deezer.com, then restart "
                       "OpenDeezer.").arg(offer);

    // Build the page once; refresh the offer line on later (re-)logins.
    if (!m_blockPage) {
        m_blockPage = new QWidget;
        auto *outer = new QVBoxLayout(m_blockPage);
        outer->addStretch(1);

        auto *title = new QLabel(QStringLiteral("Sorry — your account isn't supported"));
        title->setAlignment(Qt::AlignCenter);
        title->setWordWrap(true);
        QFont tf = title->font();
        tf.setPointSize(tf.pointSize() + 8);
        tf.setBold(true);
        title->setFont(tf);
        title->setStyleSheet(QString("color:%1;").arg(kAccent));
        outer->addWidget(title);

        outer->addSpacing(12);

        m_blockBody = new QLabel(body);
        m_blockBody->setAlignment(Qt::AlignCenter);
        m_blockBody->setWordWrap(true);
        m_blockBody->setMaximumWidth(560);
        // Centre the constrained body within the page.
        auto *bodyRow = new QHBoxLayout;
        bodyRow->addStretch(1);
        bodyRow->addWidget(m_blockBody);
        bodyRow->addStretch(1);
        outer->addLayout(bodyRow);

        outer->addSpacing(24);

        auto *quitBtn = new QPushButton(QStringLiteral("Quit OpenDeezer"));
        connect(quitBtn, &QPushButton::clicked, this, &MainWindow::quitApp);
        auto *btnRow = new QHBoxLayout;
        btnRow->addStretch(1);
        btnRow->addWidget(quitBtn);
        btnRow->addStretch(1);
        outer->addLayout(btnRow);

        outer->addStretch(1);
        m_rootStack->addWidget(m_blockPage);   // index 1
    } else if (m_blockBody) {
        m_blockBody->setText(body);
    }

    m_rootStack->setCurrentWidget(m_blockPage);
    statusBar()->showMessage(
        QStringLiteral("Premium required — %1 can't stream on-demand").arg(offer));
}

// ---- browse ---------------------------------------------------------------

void MainWindow::loadFavorites() {
    if (!m_loggedIn)
        return;
    m_tracksHeader->setText("Liked Songs");
    m_currentPlaylistId.clear();
    statusBar()->showMessage("Loading liked songs…");
    QtConcurrent::run([this] {
        const QVector<Track> tracks = parseTracks(takeJson(DZFavoritesJSON()));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            // These are liked by definition — seed the local heart state.
            for (const Track &t : tracks)
                m_likedIds.insert(t.id);
            refreshLikeButton();
            fillTrackTable(m_trackTable, tracks, gen);
            statusBar()->showMessage(QString("Liked Songs — %1 tracks").arg(tracks.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

// Flow: the user's personalised stream. Loads into the shared track table (like
// Liked Songs) and starts playing from the top.
void MainWindow::loadFlow() {
    if (!m_loggedIn)
        return;
    m_tracksHeader->setText("Flow");
    m_currentPlaylistId.clear();
    statusBar()->showMessage("Loading Flow…");
    QtConcurrent::run([this] {
        const QVector<Track> tracks = parseTracks(takeJson(DZFlowJSON()));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            statusBar()->showMessage(QString("Flow — %1 tracks").arg(tracks.size()), 3000);
            if (!tracks.isEmpty())
                playFrom(tracks, 0); // Flow auto-plays
        }, Qt::QueuedConnection);
    });
}

// Global charts: tracks fill the charts track table; albums, artists and
// playlists fill the grid below (each tile opens its existing detail view).
void MainWindow::loadCharts() {
    if (!m_loggedIn)
        return;
    statusBar()->showMessage("Loading charts…");
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
                addTile(m_chartsArtists[i].name + "\n" + QStringLiteral("Artist"),
                        m_chartsArtists[i].artworkUrl, 2, i);
            for (int i = 0; i < m_chartsPlaylists.size(); ++i)
                addTile(m_chartsPlaylists[i].name + "\n" + m_chartsPlaylists[i].owner,
                        m_chartsPlaylists[i].artworkUrl, 1, i);

            statusBar()->showMessage(
                QString("Charts — %1 tracks").arg(m_chartsTracks.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::loadPlaylists() {
    if (!m_loggedIn)
        return;
    statusBar()->showMessage("Loading playlists…");
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
                    p.name + "\n" + QString::number(p.trackCount) + " tracks");
                it->setTextAlignment(Qt::AlignHCenter | Qt::AlignTop);
                it->setData(Qt::UserRole, i);
                m_playlistGrid->addItem(it);
                if (!p.artworkUrl.isEmpty())
                    fetchImage(p.artworkUrl, gen, [it](const QImage &img) {
                        it->setIcon(QIcon(QPixmap::fromImage(img).scaled(
                            120, 120, Qt::KeepAspectRatio, Qt::SmoothTransformation)));
                    });
            }
            statusBar()->showMessage(QString("%1 playlists").arg(ps.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openPlaylist(const Playlist &p) {
    statusBar()->showMessage("Loading playlist…");
    m_tracksHeader->setText(p.owner.isEmpty() ? p.name : p.name + "   ·   " + p.owner);
    m_currentPlaylistId = p.id; // enables "Remove from this playlist" in the track menu
    const QByteArray id = p.id.toUtf8();
    QtConcurrent::run([this, id] {
        const QVector<Track> tracks = parseTracks(takeJson(DZPlaylistTracksJSON(cstr(id))));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            m_stack->setCurrentIndex(0);
            statusBar()->showMessage(QString("%1 tracks").arg(tracks.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openAlbum(const Album &a) {
    statusBar()->showMessage("Loading album…");
    m_tracksHeader->setText(a.artistLine.isEmpty() ? a.name : a.name + "   ·   " + a.artistLine);
    m_currentPlaylistId.clear(); // album is not a removable-from playlist
    const QByteArray id = a.id.toUtf8();
    QtConcurrent::run([this, id] {
        const QVector<Track> tracks = parseTracks(takeJson(DZAlbumTracksJSON(cstr(id))));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            m_stack->setCurrentIndex(0);
            statusBar()->showMessage(QString("%1 tracks").arg(tracks.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::runSearch() {
    if (!m_loggedIn)
        return;
    const QString q = m_searchEdit->text().trimmed();
    if (q.isEmpty())
        return;
    statusBar()->showMessage("Searching…");
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
            m_searchPlaylists.clear();
            for (const QJsonValue &v : obj.value("albums").toArray())
                m_searchAlbums.push_back(parseAlbum(v.toObject()));
            for (const QJsonValue &v : obj.value("playlists").toArray())
                m_searchPlaylists.push_back(parsePlaylist(v.toObject()));

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
            statusBar()->showMessage("Search complete", 3000);
        }, Qt::QueuedConnection);
    });
}

// ---- favourites (like / unlike) -------------------------------------------

// Paint the heart for the given liked state.
void MainWindow::setLikeButton(bool liked) {
    if (!m_likeBtn)
        return;
    m_likeBtn->setText(liked ? QString::fromUtf8("♥")   // ♥ filled
                             : QString::fromUtf8("♡"));  // ♡ outline
    m_likeBtn->setStyleSheet(liked ? QString("QToolButton{color:%1;}").arg(kAccent)
                                   : QString());
    m_likeBtn->setToolTip(liked ? QStringLiteral("Remove from Liked Songs")
                                : QStringLiteral("Add to Liked Songs"));
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
        statusBar()->showMessage(QStringLiteral("Nothing is playing"), 3000);
        return;
    }
    if (m_currentIsEpisode) {
        statusBar()->showMessage(QStringLiteral("Podcast episodes can't be liked"), 3000);
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
                statusBar()->showMessage(like ? QStringLiteral("Added to Liked Songs")
                                              : QStringLiteral("Removed from Liked Songs"),
                                         3000);
            } else {
                statusBar()->showMessage(QStringLiteral("Couldn't update Liked Songs"), 3000);
            }
            if (m_hasCurrent && m_current.id == trackId)
                refreshLikeButton(); // paint the true state (also reverts a failed toggle)
        }, Qt::QueuedConnection);
    });
}

// ---- add to playlist ------------------------------------------------------

// Load the user's playlists (fresh) then show the picker on the GUI thread.
void MainWindow::addTrackToPlaylist(const Track &t) {
    if (!m_loggedIn || t.id.isEmpty())
        return;
    statusBar()->showMessage("Loading playlists…");
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
    dlg.setWindowTitle(QStringLiteral("Add to Playlist"));
    auto *v = new QVBoxLayout(&dlg);
    v->addWidget(new QLabel(QStringLiteral("Add \"%1\" to:").arg(t.name)));
    auto *list = new QListWidget;
    auto *newItem = new QListWidgetItem(QStringLiteral("＋  New playlist…"));
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
            this, QStringLiteral("New Playlist"), QStringLiteral("Playlist name:"),
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
                    statusBar()->showMessage(QStringLiteral("Couldn't create playlist"), 3000);
                else
                    statusBar()->showMessage(added
                        ? QStringLiteral("Added to new playlist \"%1\"").arg(name)
                        : QStringLiteral("Created \"%1\" but couldn't add the track").arg(name),
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
                ? QStringLiteral("Added to \"%1\"").arg(plName)
                : QStringLiteral("Couldn't add to \"%1\"").arg(plName), 3000);
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
                statusBar()->showMessage(QStringLiteral("Couldn't remove from playlist"), 3000);
                return;
            }
            statusBar()->showMessage(QStringLiteral("Removed from playlist"), 3000);
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
        this, QStringLiteral("New Playlist"), QStringLiteral("Playlist name:"),
        QLineEdit::Normal, QString(), &ok).trimmed();
    if (!ok || name.isEmpty())
        return;
    const QByteArray nb = name.toUtf8();
    QtConcurrent::run([this, nb, name] {
        const QByteArray j = takeJson(DZCreatePlaylist(cstr(nb)));
        const QString id = QJsonDocument::fromJson(j).object().value("id").toString();
        QMetaObject::invokeMethod(this, [this, name, id] {
            if (id.isEmpty()) {
                statusBar()->showMessage(QStringLiteral("Couldn't create playlist"), 3000);
            } else {
                statusBar()->showMessage(QStringLiteral("Created \"%1\"").arg(name), 3000);
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
        this, QStringLiteral("Rename Playlist"), QStringLiteral("New name:"),
        QLineEdit::Normal, p.name, &ok).trimmed();
    if (!ok || name.isEmpty() || name == p.name)
        return;
    const QByteArray idb = p.id.toUtf8();
    const QByteArray nb = name.toUtf8();
    QtConcurrent::run([this, idb, nb, name] {
        const int okRes = DZRenamePlaylist(cstr(idb), cstr(nb));
        QMetaObject::invokeMethod(this, [this, okRes, name] {
            statusBar()->showMessage(okRes
                ? QStringLiteral("Renamed to \"%1\"").arg(name)
                : QStringLiteral("Couldn't rename playlist"), 3000);
            if (okRes)
                loadPlaylists();
        }, Qt::QueuedConnection);
    });
}

void MainWindow::deletePlaylist(const Playlist &p) {
    if (!m_loggedIn || p.id.isEmpty())
        return;
    if (QMessageBox::question(this, QStringLiteral("Delete Playlist"),
            QStringLiteral("Delete \"%1\"? This cannot be undone.").arg(p.name))
        != QMessageBox::Yes)
        return;
    const QByteArray idb = p.id.toUtf8();
    QtConcurrent::run([this, idb] {
        const int okRes = DZDeletePlaylist(cstr(idb));
        QMetaObject::invokeMethod(this, [this, okRes] {
            statusBar()->showMessage(okRes ? QStringLiteral("Playlist deleted")
                                           : QStringLiteral("Couldn't delete playlist"),
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
                QAction *goArtist = menu.addAction(QStringLiteral("Go to Artist"));
                goArtist->setEnabled(!t.artistId.isEmpty());
                QAction *showLy = menu.addAction(QStringLiteral("Show Lyrics"));
                menu.addSeparator();
                QAction *like  = menu.addAction(QStringLiteral("Add to Liked Songs"));
                QAction *addPl = menu.addAction(QStringLiteral("Add to Playlist…"));
                QAction *removePl = nullptr;
                if (table == m_trackTable && !m_currentPlaylistId.isEmpty())
                    removePl = menu.addAction(QStringLiteral("Remove from this playlist"));
                QAction *chosen = menu.exec(table->viewport()->mapToGlobal(pos));
                if (chosen == goArtist)
                    openArtist(t.artistId);
                else if (chosen == showLy)
                    openLyricsFor(t.id, t.name + QStringLiteral("   ·   ") + t.artistLine);
                else if (chosen == like)
                    likeTrack(t.id, true);
                else if (chosen == addPl)
                    addTrackToPlaylist(t);
                else if (removePl && chosen == removePl)
                    removeFromCurrentPlaylist(t, row);
            });
}

// Only the browse pages — tracks(0), playlists(1), search(2), charts(5),
// podcasts(6) — are valid "Back" targets, never another detour page, so Back
// from lyrics/artist always lands somewhere sensible.
void MainWindow::rememberReturnPage() {
    const int cur = m_stack->currentIndex();
    if (cur == 0 || cur == 1 || cur == 2 || cur == 5 || cur == 6)
        m_returnPage = cur;
}

QWidget *MainWindow::buildLyricsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);

    auto *top = new QHBoxLayout;
    auto *back = new QToolButton;
    back->setText(QStringLiteral("‹ Back"));
    back->setAutoRaise(true);
    connect(back, &QToolButton::clicked, this,
            [this] { m_stack->setCurrentIndex(m_returnPage); });
    top->addWidget(back);
    m_lyricsTitle = new QLabel(QStringLiteral("Lyrics"));
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
    back->setText(QStringLiteral("‹ Back"));
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
    m_artistName = new QLabel(QStringLiteral("Artist"));
    QFont nf = m_artistName->font();
    nf.setPointSize(nf.pointSize() + 6);
    nf.setBold(true);
    m_artistName->setFont(nf);
    m_artistFans = new QLabel;
    names->addWidget(m_artistName);
    names->addWidget(m_artistFans);
    names->addStretch(1);
    head->addLayout(names, 1);
    v->addLayout(head);

    v->addWidget(new QLabel(QStringLiteral("Top Tracks")));
    m_artistTopTable = makeTrackTable();
    connect(m_artistTopTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_artistTopTracks, row); });
    installTrackMenu(m_artistTopTable, &m_artistTopTracks);
    v->addWidget(m_artistTopTable, 2);

    v->addWidget(new QLabel(QStringLiteral("Albums")));
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

    v->addWidget(new QLabel(QStringLiteral("Related Artists")));
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
    auto *title = new QLabel(QStringLiteral("Charts"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

    v->addWidget(new QLabel(QStringLiteral("Top Tracks")));
    m_chartsTrackTable = makeTrackTable();
    connect(m_chartsTrackTable, &QTableWidget::cellActivated, this,
            [this](int row, int) { playFrom(m_chartsTracks, row); });
    installTrackMenu(m_chartsTrackTable, &m_chartsTracks);
    v->addWidget(m_chartsTrackTable, 2);

    v->addWidget(new QLabel(QStringLiteral("Albums, Artists & Playlists")));
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
    auto *title = new QLabel(QStringLiteral("Podcasts"));
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

    auto *top = new QHBoxLayout;
    m_podcastSearchEdit = new QLineEdit;
    m_podcastSearchEdit->setPlaceholderText(QStringLiteral("Search podcasts…"));
    auto *btn = new QPushButton(QStringLiteral("Search"));
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
    back->setText(QStringLiteral("‹ Back"));
    back->setAutoRaise(true);
    connect(back, &QToolButton::clicked, this,
            [this] { m_stack->setCurrentIndex(6); }); // back to the shows grid
    top->addWidget(back);
    m_podcastTitle = new QLabel(QStringLiteral("Episodes"));
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

// ---- podcasts flow --------------------------------------------------------

void MainWindow::runPodcastSearch() {
    if (!m_loggedIn)
        return;
    const QString q = m_podcastSearchEdit->text().trimmed();
    if (q.isEmpty())
        return;
    statusBar()->showMessage("Searching podcasts…");
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
                    p.name + "\n" + QString::number(p.episodeCount) + " episodes");
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
                QString("Found %1 podcasts").arg(m_podcasts.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

void MainWindow::openPodcast(const Podcast &p) {
    m_currentPodcastName = p.name;
    m_podcastTitle->setText(p.name);
    m_stack->setCurrentIndex(7);
    m_episodes.clear();
    m_episodeList->clear();
    m_episodeList->addItem(new QListWidgetItem(QStringLiteral("Loading episodes…")));
    statusBar()->showMessage("Loading episodes…");
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
                m_episodeList->addItem(new QListWidgetItem(QStringLiteral("No episodes.")));
            statusBar()->showMessage(
                QString("%1 episodes").arg(m_episodes.size()), 3000);
        }, Qt::QueuedConnection);
    });
}

// Episodes use the plain-stream path (DZPlayEpisode), not the encrypted track
// pipeline, so they sit outside the queue: clearing it makes next/prev and
// auto-advance no-ops while the episode plays.
void MainWindow::playEpisode(const Episode &e) {
    if (!m_loggedIn || e.id.isEmpty())
        return;
    m_queue.clear();
    m_queueIndex = -1;
    Track t;
    t.id         = e.id;
    t.name       = e.title;
    t.artistLine = m_currentPodcastName;
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
    if (!m_hasCurrent) {
        statusBar()->showMessage(QStringLiteral("Nothing is playing"), 3000);
        return;
    }
    m_lyricsFollowsPlayback = true;
    rememberReturnPage();
    m_stack->setCurrentIndex(3);
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
    m_stack->setCurrentIndex(3);
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
    m_lyricsList->addItem(new QListWidgetItem(QStringLiteral("Loading lyrics…")));

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
            new QListWidgetItem(QStringLiteral("No lyrics available.")));
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
    if (!m_hasCurrent) {
        statusBar()->showMessage(QStringLiteral("Nothing is playing"), 3000);
        return;
    }
    if (m_current.artistId.isEmpty()) {
        statusBar()->showMessage(
            QStringLiteral("Artist unavailable for this track"), 3000);
        return;
    }
    openArtist(m_current.artistId);
}

void MainWindow::openArtist(const QString &artistId) {
    if (!m_loggedIn || artistId.isEmpty())
        return;
    rememberReturnPage();
    m_stack->setCurrentIndex(4);
    statusBar()->showMessage(QStringLiteral("Loading artist…"));

    // Reset the page to a loading state.
    m_artistName->setText(QStringLiteral("Loading…"));
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
        m_artistName->setText(QStringLiteral("Artist unavailable"));
        statusBar()->showMessage(QStringLiteral("Could not load artist"), 3000);
        return;
    }

    const ArtistInfo info = parseArtistInfo(obj.value("artist").toObject());
    m_artistName->setText(info.name.isEmpty() ? QStringLiteral("Artist") : info.name);
    m_artistFans->setText(info.nbFans > 0
        ? QLocale().toString(info.nbFans) + QStringLiteral(" fans")
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

void MainWindow::fillTrackTable(QTableWidget *table, const QVector<Track> &tracks, int gen) {
    table->clearContents();
    table->setRowCount(tracks.size());
    for (int i = 0; i < tracks.size(); ++i) {
        const Track &t = tracks[i];
        auto *title = new QTableWidgetItem(badgedTitle(t));
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

// ---- playback -------------------------------------------------------------

void MainWindow::playFrom(const QVector<Track> &list, int index) {
    if (index < 0 || index >= list.size())
        return;
    m_queue = list;
    m_queueIndex = index;
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
    const QByteArray id = t.id.toUtf8();
    const qint64 dur = t.durationMs;
    // DZPlay prepares the stream over the network — run it off the GUI thread.
    QtConcurrent::run([id, dur] { DZPlay(cstr(id), dur); });
    // Gapless/crossfade: prime the engine with the deterministic next track.
    preloadNext();
}

void MainWindow::setNowPlaying(const Track &t) {
    m_nowPlaying->setText(badgedTitle(t) + "\n" + t.artistLine);
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

void MainWindow::togglePause() { DZTogglePause(); }

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

void MainWindow::setVolume(int percent) {
    DZSetVolume(percent / 100.0);
    if (m_mpris)
        m_mpris->updateVolume(percent / 100.0);
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

    // Show the actual output format next to the now-playing title.
    if (m_hasCurrent) {
        QString sub = m_current.artistLine;
        if (char *fp = DZFormat()) {
            if (*fp)
                sub += QStringLiteral("   ·   ") + QString::fromUtf8(fp);
            DZFree(fp);
        }
        m_nowPlaying->setText(badgedTitle(m_current) + "\n" + sub);
    }

    // Lyrics page: follow the playing track and keep the synced line highlighted.
    if (m_stack->currentIndex() == 3) {
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
        } else {
            next(); // no preload (or engine stopped) — start the next normally
        }
    }
}
