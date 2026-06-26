#include "mainwindow.h"
#include "mpris.h"
#include "settingsdialog.h"

#include <QAction>
#include <QAbstractItemView>
#include <QApplication>
#include <QCloseEvent>
#include <QColor>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QFont>
#include <QHBoxLayout>
#include <QHeaderView>
#include <QIcon>
#include <QImage>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QKeySequence>
#include <QLabel>
#include <QLineEdit>
#include <QListView>
#include <QListWidget>
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
    return t;
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

// A themed app icon, falling back to a Deezer-purple disc with a white note so
// the tray entry and window are recognisable even without an installed theme.
QIcon appIcon() {
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
    m_stack->addWidget(buildTracksPage());    // index 0
    m_stack->addWidget(buildPlaylistsPage()); // index 1
    m_stack->addWidget(buildSearchPage());    // index 2

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
    setCentralWidget(central);

    // One GUI-thread poll timer drives the seek bar, the play/pause icon and
    // auto-advance. Only cheap, non-blocking DZ* state reads happen here.
    m_poll = new QTimer(this);
    m_poll->setInterval(300);
    connect(m_poll, &QTimer::timeout, this, &MainWindow::tick);

    setupMpris();   // session-bus media controls / now-playing
    setupTray();    // background playback + close-to-tray

    statusBar()->showMessage("Logging in…");
    startLogin();
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
    SettingsDialog dlg(settingsPath(), this);
    connect(&dlg, &SettingsDialog::qualityChanged, this, &MainWindow::applyQuality);
    connect(&dlg, &SettingsDialog::closeToTrayChanged, this,
            [this](bool on) { m_closeToTray = on; });
    dlg.exec();
}

void MainWindow::applyQuality(int level) {
    m_quality = level;
    DZSetQuality(level);
    const char *names[] = {"Normal (MP3 128)", "High (MP3 320)", "HiFi (FLAC)"};
    statusBar()->showMessage(QStringLiteral("Quality: ") +
                             names[level < 0 ? 0 : (level > 2 ? 2 : level)], 3000);
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
        QMessageBox::about(
            this, "About OpenDeezer",
            "<h3>OpenDeezer 0.2.0</h3>"
            "<p>An open source reimplementation of Deezer.</p>"
            "<p>Native KDE / Qt6 client. The engine (login, browse, Blowfish"
            " decrypt, MP3 decode, playback) is a Go core linked in-process.</p>"
            "<p>By <b>Cycl0o0</b>.<br>Licensed under <b>AGPL-3.0</b>.</p>");
    });
}

// ---- sidebar --------------------------------------------------------------

void MainWindow::buildSidebar() {
    m_sidebar = new QListWidget;
    m_sidebar->setMaximumWidth(240);
    m_sidebar->addItem(QStringLiteral("♥  Liked Songs"));
    m_sidebar->addItem(QStringLiteral("☰  Playlists"));
    m_sidebar->addItem(QStringLiteral("⌕  Search"));
    connect(m_sidebar, &QListWidget::currentRowChanged, this, &MainWindow::onSidebarChanged);
}

void MainWindow::onSidebarChanged(int row) {
    switch (row) {
    case 0:
        m_stack->setCurrentIndex(0);
        loadFavorites();
        break;
    case 1:
        m_stack->setCurrentIndex(1);
        loadPlaylists();
        break;
    case 2:
        m_stack->setCurrentIndex(2);
        if (m_searchEdit)
            m_searchEdit->setFocus();
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
    v->addWidget(m_trackTable, 1);
    return w;
}

QWidget *MainWindow::buildPlaylistsPage() {
    auto *w = new QWidget;
    auto *v = new QVBoxLayout(w);
    auto *title = new QLabel("Your Playlists");
    QFont f = title->font();
    f.setPointSize(f.pointSize() + 6);
    f.setBold(true);
    title->setFont(f);
    v->addWidget(title);

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
        statusBar()->showMessage("No ARL found");
        QMessageBox::warning(this, "OpenDeezer",
                             "No ARL found.\nSet $DEEZER_ARL or write"
                             " ~/.config/opendeezer/arl.txt");
        return;
    }
    const QByteArray ab = arl.toUtf8();
    // DZInit blocks on the network — never on the GUI thread.
    QtConcurrent::run([this, ab] {
        const int ok = DZInit(cstr(ab));
        QMetaObject::invokeMethod(this, [this, ok] {
            if (ok) {
                m_loggedIn = true;
                m_lastFinished = DZFinishedCount();
                m_vol->setValue(static_cast<int>(qRound(DZVolume() * 100)));
                applyQuality(m_quality);   // apply persisted quality on startup
                m_poll->start();
                m_sidebar->setCurrentRow(0); // triggers loadFavorites()
                statusBar()->showMessage("Connected", 3000);
            } else {
                statusBar()->showMessage("Login failed");
                QMessageBox::critical(this, "OpenDeezer",
                                      "Login failed — invalid or expired ARL.");
            }
        }, Qt::QueuedConnection);
    });
}

// ---- browse ---------------------------------------------------------------

void MainWindow::loadFavorites() {
    if (!m_loggedIn)
        return;
    m_tracksHeader->setText("Liked Songs");
    statusBar()->showMessage("Loading liked songs…");
    QtConcurrent::run([this] {
        const QVector<Track> tracks = parseTracks(takeJson(DZFavoritesJSON()));
        QMetaObject::invokeMethod(this, [this, tracks] {
            const int gen = ++m_artGen;
            m_tableTracks = tracks;
            fillTrackTable(m_trackTable, tracks, gen);
            statusBar()->showMessage(QString("Liked Songs — %1 tracks").arg(tracks.size()), 3000);
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

// ---- track table fill + async art ----------------------------------------

void MainWindow::fillTrackTable(QTableWidget *table, const QVector<Track> &tracks, int gen) {
    table->clearContents();
    table->setRowCount(tracks.size());
    for (int i = 0; i < tracks.size(); ++i) {
        const Track &t = tracks[i];
        auto *title = new QTableWidgetItem(t.name);
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
    setNowPlaying(t);
    m_seek->setRange(0, static_cast<int>(qMax<qint64>(1, t.durationMs)));
    m_seek->setValue(0);
    m_posLabel->setText("0:00");
    m_durLabel->setText(timeText(t.durationMs));
    const QByteArray id = t.id.toUtf8();
    const qint64 dur = t.durationMs;
    // DZPlay prepares the stream over the network — run it off the GUI thread.
    QtConcurrent::run([id, dur] { DZPlay(cstr(id), dur); });
}

void MainWindow::setNowPlaying(const Track &t) {
    m_nowPlaying->setText(t.name + "\n" + t.artistLine);
    m_cover->setPixmap(placeholderPix(56));

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
        m_nowPlaying->setText(m_current.name + "\n" + sub);
    }

    const int f = DZFinishedCount();
    if (f != m_lastFinished) {
        m_lastFinished = f;
        if (m_repeat == 2)
            playCurrent(); // repeat-one
        else
            next();
    }
}
