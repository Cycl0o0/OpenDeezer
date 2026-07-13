// OpenDeezer — native KDE / Qt6 Widgets front-end.
//
// The whole engine (login, browse, Blowfish decrypt, MP3 decode, ALSA playback)
// is the Go core compiled to a C static archive (lib/libdeezercore.a) and linked
// in-process. This file is UI only: a QMainWindow with a QListWidget sidebar, a
// QStackedWidget of content pages (track table / playlist grid / search), and a
// bottom transport bar. Every blocking DZ* call is marshalled onto a worker via
// QtConcurrent::run and the result is pushed back to the GUI thread with
// QMetaObject::invokeMethod.
#pragma once

#include <QMainWindow>
#include <QVector>
#include <QThreadPool>
#include <QHash>
#include <QSet>
#include <QString>
#include <atomic>
#include <functional>

QT_BEGIN_NAMESPACE
class QListWidget;
class QStackedWidget;
class QTableWidget;
class QLabel;
class QSlider;
class QToolButton;
class QLineEdit;
class QTimer;
class QImage;
class QByteArray;
class QCloseEvent;
class QSystemTrayIcon;
class QFrame;
class QVBoxLayout;
class QPushButton;
class QDockWidget;
QT_END_NAMESPACE

class MprisController;

// Wire models — mirror the JSON emitted by corelib (jTrack/jAlbum/jPlaylist).
struct Track {
    QString id, name, artistLine, albumName, artworkUrl;
    QString artistId;            // jTrack.artists[0].id — drives the artist view
    qint64  durationMs = 0;
    bool    isExplicit = false;  // jTrack.explicit — shows the "E" badge
    bool    isEpisode  = false;  // history rows only: replay via the episode path
};
struct Album {
    QString id, name, artistLine, artworkUrl;
};
struct Playlist {
    QString id, name, owner, artworkUrl;
    int     trackCount = 0;
};
// jArtistInfo: {id,name,artworkUrl,nbFans}.
struct ArtistInfo {
    QString id, name, artworkUrl;
    int     nbFans = 0;
};
// jPodcast: {id,name,description,artworkUrl,episodeCount}.
struct Podcast {
    QString id, name, description, artworkUrl;
    int     episodeCount = 0;
};
// jEpisode: {id,title,description,artworkUrl,durationMs,releaseDate}.
struct Episode {
    QString id, title, description, artworkUrl, releaseDate;
    qint64  durationMs = 0;
};
// One timed line of synced lyrics ({timeMs,text}).
struct LyricsLine {
    qint64  timeMs = 0;
    QString text;
};
// DZLyricsJSON result: {plain, synced:[{timeMs,text}], isSynced}.
struct LyricsData {
    bool                isSynced = false;
    QString             plain;
    QVector<LyricsLine> lines;   // populated only when isSynced
};
// One OpenDeezer Connect device from DZDiscoverDevices ({name,addr,client,version}).
// addr is the control-API host:port; client maps to a friendly device type.
struct ConnectDevice {
    QString name, addr, client, version;
};

class MainWindow : public QMainWindow {
    Q_OBJECT
public:
    explicit MainWindow(QWidget *parent = nullptr);
    ~MainWindow() override;

protected:
    void closeEvent(QCloseEvent *event) override;

private:
    // ---- UI construction ----
    void          buildMenu();
    void          buildSidebar();
    QWidget      *buildHomePage();
    QWidget      *buildTracksPage();
    QWidget      *buildPlaylistsPage();
    QWidget      *buildSearchPage();
    QWidget      *buildLyricsPage();
    QWidget      *buildArtistPage();
    QWidget      *buildChartsPage();
    QWidget      *buildPodcastsPage();
    QWidget      *buildPodcastEpisodesPage();
    QWidget      *buildHistoryPage();          // recently played + listening stats
    QWidget      *buildTransport();
    QTableWidget *makeTrackTable();
    // Right-click track menu: "Go to Artist" / "Show Lyrics" / "Add to Liked
    // Songs" / "Add to Playlist…" (and "Remove from this playlist" when the
    // shared table is showing a playlist); src points at the QVector backing
    // that table's rows.
    void          installTrackMenu(QTableWidget *table, QVector<Track> *src);

    // ---- favourites / playlists mutations (v0.4) ----
    void seedFavorites();                                 // liked-ids mirror at login
    void toggleLikeCurrent();                              // heart on the transport
    void likeTrack(const QString &trackId, bool like);    // one-shot like/unlike
    void setLikeButton(bool liked);                       // paint the heart
    void refreshLikeButton();                             // from m_likedIds + current
    void addTrackToPlaylist(const Track &t);              // picker -> DZAddToPlaylist
    void showAddToPlaylistPicker(const Track &t, const QVector<Playlist> &ps);
    void removeFromCurrentPlaylist(const Track &t, int row);
    void download(const QString &id);                     // DZDownloadTrack (premium-only)
    // Save a track for offline playback (premium-only): DZDownloadForOffline on a
    // worker, toast the result and record its {key} id so the "downloaded"
    // indicator (see displayTitle) lights up. refreshOfflineIndicators repaints
    // the markers across the visible track tables, the queue panel and the
    // now-playing badge.
    void downloadForOffline(const Track &t);
    void refreshOfflineIndicators();
    // Batch downloads (premium-only): a whole album / playlist to the shared
    // folder via DZDownloadAlbum / DZDownloadPlaylist, on the same worker shape
    // as download(); downloadBatch is the shared body.
    void downloadAlbum(const QString &id, const QString &name);
    void downloadPlaylist(const QString &id, const QString &name);
    void downloadBatch(const QString &id, const QString &name, bool album);
    void downloadCurrentList();                           // tracks-page header button
    void updateListDownloadButton();                      // show/label that button
    void createPlaylist();                                // DZCreatePlaylist
    void renamePlaylist(const Playlist &p);               // DZRenamePlaylist
    void deletePlaylist(const Playlist &p);               // DZDeletePlaylist (confirm)

    // ---- lyrics view (stack page) ----
    void openLyrics();                                   // current track (transport)
    void openLyricsFor(const QString &trackId, const QString &title);
    void loadLyrics(const QString &trackId, const QString &title);
    void renderLyrics(const QString &trackId, const QString &title,
                      const LyricsData &d);
    void updateLyricsHighlight(qint64 posMs);

    // ---- artist view (stack page) ----
    void openArtistForCurrent();                         // current track's artist
    void openArtist(const QString &artistId);
    void renderArtist(const QByteArray &json, int gen);

    // Remember the browse page to return to from a lyrics/artist detour.
    void rememberReturnPage();

    // ---- flow / browse (all heavy work on a worker thread) ----
    void startLogin();
    void promptLogin();                       // webview / manual-ARL login dialog
    void finishLogin(const QByteArray &acct); // post-login bring-up (shared path)
    void onSidebarChanged(int row);
    void loadHome();
    void loadFavorites();
    void loadFlow();
    // Start radio ("mix"): DZTrackMixJSON / DZArtistMixJSON both return Flow's
    // {tracks:[...]} shape, so playMix reuses the Flow load+auto-play path.
    void startTrackRadio(const QString &trackId);
    void startArtistRadio(const QString &artistId);
    void playMix(const QVector<Track> &tracks);
    void loadHistory();   // recently played + listening stats (history page)
    void loadCharts();
    void loadPlaylists();
    void openPlaylist(const Playlist &p);
    void openAlbum(const Album &a);
    void runSearch();

    // ---- podcasts (v0.4) ----
    void runPodcastSearch();
    void openPodcast(const Podcast &p);
    // Play a podcast episode via the plain-stream path. showName supplies the
    // now-playing "artist" line; empty falls back to m_currentPodcastName (the
    // open podcast's title). History replay passes the show name from the entry.
    void playEpisode(const Episode &e, const QString &showName = QString());

    // ---- track table filling + async cover art ----
    // Row title as shown in every track table / the queue panel: the explicit "E"
    // badge (badgedTitle) plus a leading "downloaded" glyph when the track is
    // available offline (id in m_offlineIds).
    QString displayTitle(const Track &t) const;
    void fillTrackTable(QTableWidget *table, const QVector<Track> &tracks, int gen);
    void fetchImage(const QString &url, int gen, std::function<void(const QImage &)> apply);

    // ---- engine queue sync (so remote controllers see + walk this queue) ----
    // syncQueueToEngine pushes the whole GUI queue (DZQueueSet) and aligns the
    // cursor (DZQueueSetIndex) on every (re)build; syncQueueIndex realigns just
    // the cursor on an index-only change. Once the cursor is aligned the ENGINE
    // owns natural-finish auto-advance (see tick()'s DZQueueIndex follow).
    void syncQueueToEngine();
    void syncQueueIndex();

    // ---- Up-Next queue editor (right-side dock) ----
    // buildQueueDock builds the dock (list + Clear / move / remove controls);
    // refreshQueuePanel re-renders it from the engine queue (DZQueueJSON, with the
    // GUI's m_queue as the fallback + authority). The edit ops mutate BOTH the GUI
    // model (m_queue/m_queueIndex — the playback source of truth) and the engine
    // mirror (DZQueueRemove / DZQueueMove / DZQueueInsertNext), then realign the
    // cursor to the actually-playing track so a reorder/removal never desyncs
    // playback. queueJumpTo double-click-plays a row; queueAppend / queueClear go
    // through the whole-queue push (DZQueueSet via syncQueueToEngine).
    void buildQueueDock();
    void refreshQueuePanel();
    void queueJumpTo(int row);
    void queueRemoveAt(int row);
    void queueMove(int from, int to);
    void queueInsertNext(const Track &t);   // "Play next"
    void queueAppend(const Track &t);       // "Add to queue"
    void queueClear();
    void realignQueueCursor();              // cursor -> current track's new row

    // ---- playback ----
    void playFrom(const QVector<Track> &list, int index);
    void playCurrent();
    void togglePause();
    void next();
    void prev();
    void setVolume(int percent);
    void setNowPlaying(const Track &t);
    // Paint the repeat/shuffle toggles for a given engine state. The click/toggle
    // handlers command the engine; tick() calls these to reconcile the buttons
    // with engine truth (phone remote / control API / routed device) — they emit
    // nothing to the engine (shuffle blocks signals to avoid a feedback command).
    void applyRepeatButton(int mode);   // 0 off, 1 all, 2 one
    void applyShuffleButton(bool on);
    void tick();
    // Gapless/crossfade: the deterministic (non-shuffle) next index, and a
    // preload of that track so the engine can swap to it seamlessly on finish.
    int  nextIndexDeterministic() const;
    void preloadNext();
    bool autoTransitionEnabled() const { return m_gapless || m_crossfadeMs > 0; }

    // ---- OpenDeezer Connect (LAN device picker) ----
    void openConnectPicker();                                   // discover, then show
    void showConnectPicker(const QVector<ConnectDevice> &devices,
                           const QString &connectedAddr);
    void connectDevice(const QString &addr, const QString &name);
    void disconnectDevice();                                    // back to this computer
    void refreshConnectButton();                               // paint from DZConnectedDevice

    // ---- OS integration: MPRIS media controls, tray, settings ----
    void setupMpris();
    void setupTray();
    void openSettings();
    void openPhoneRemote();

    // ---- update check (v1.5.1): once per launch, background, non-intrusive.
    // Never blocks startup and never downloads/installs anything — it only
    // offers a link to the GitHub release page.
    void checkForUpdates();
    void showUpdateBanner(const QString &latest, const QString &url,
                          const QString &notes);
    void applyAccount(const QByteArray &json);
    // Login failed because the machine is offline — swap the whole UI to a
    // blocking "No Internet" page whose Retry button re-runs startLogin().
    void showNoInternet();
    void applyQuality(int level);
    void applyReplayGain(bool on);
    void applyAudioDevice(const QString &deviceId);
    void applyGapless(bool on);
    void applyCrossfade(int ms);
    void quitApp();
    QString settingsPath() const;

    // ---- widgets ----
    // Top-level stack: the full app UI (index 0) plus the lazily-appended
    // No-Internet retry page, selected by pointer (setCurrentWidget) so its exact
    // index doesn't matter.
    QStackedWidget*m_rootStack      = nullptr;
    QWidget       *m_noInternetPage = nullptr;   // "No Internet" retry page
    QListWidget   *m_sidebar       = nullptr;
    QStackedWidget*m_stack         = nullptr;

    // "Up Next" queue editor dock (right dock area) + its list of queue rows.
    QDockWidget   *m_queueDock     = nullptr;
    QListWidget   *m_queueList     = nullptr;

    // Central widget's top-level layout — the update banner inserts itself at
    // row 0 above the sidebar/content splitter, and removes itself on dismiss.
    QVBoxLayout   *m_centralLayout = nullptr;
    QFrame        *m_updateBanner  = nullptr;   // non-null while the banner is shown

    // home page (stack index 0)
    QLabel        *m_homeGreeting      = nullptr;
    QListWidget   *m_homeTracksRail    = nullptr;
    QListWidget   *m_homePlaylistsRail = nullptr;

    QLabel        *m_tracksHeader  = nullptr;
    QTableWidget  *m_trackTable    = nullptr;
    QListWidget   *m_playlistGrid  = nullptr;
    QLineEdit     *m_searchEdit    = nullptr;
    QTableWidget  *m_searchTrackTable = nullptr;
    QListWidget   *m_searchResults = nullptr;

    // lyrics page
    QLabel        *m_lyricsTitle   = nullptr;
    QListWidget   *m_lyricsList    = nullptr;   // one item per line (synced or plain)

    // artist page
    QLabel        *m_artistName    = nullptr;
    QLabel        *m_artistFans    = nullptr;
    QLabel        *m_artistAvatar  = nullptr;
    QTableWidget  *m_artistTopTable    = nullptr;
    QListWidget   *m_artistAlbumsGrid  = nullptr;
    QListWidget   *m_artistRelatedGrid = nullptr;

    // charts page (tracks table + albums/artists/playlists grid)
    QTableWidget  *m_chartsTrackTable = nullptr;
    QListWidget   *m_chartsResults    = nullptr;

    // podcasts pages
    QLineEdit     *m_podcastSearchEdit = nullptr;
    QListWidget   *m_podcastGrid       = nullptr;   // shows grid
    QLabel        *m_podcastTitle      = nullptr;   // episodes page header
    QListWidget   *m_episodeList       = nullptr;   // episodes of the open show

    // history page (recently played + listening stats)
    QTableWidget  *m_historyTable    = nullptr;     // recently played (play by id)
    QLabel        *m_statsTotal      = nullptr;     // total listening time (30d)
    QListWidget   *m_statsTopTracks  = nullptr;     // top tracks (play by id)
    QListWidget   *m_statsTopArtists = nullptr;     // top artists (informational)

    // "Download album"/"Download playlist" button in the tracks-page header,
    // shown only while an album or playlist is displayed (premium-gated).
    QPushButton   *m_downloadListBtn = nullptr;

    QToolButton *m_prevBtn = nullptr, *m_playBtn = nullptr, *m_nextBtn = nullptr;
    QToolButton *m_likeBtn = nullptr;
    QToolButton *m_connectBtn = nullptr;        // OpenDeezer Connect device picker
    QToolButton *m_shuffleBtn = nullptr, *m_repeatBtn = nullptr;
    QSlider     *m_seek = nullptr, *m_vol = nullptr;
    QLabel      *m_nowPlaying = nullptr, *m_cover = nullptr,
                *m_posLabel = nullptr, *m_durLabel = nullptr;
    QLabel      *m_explicitBadge = nullptr;  // styled "E" tag shown for explicit tracks
    QLabel      *m_previewBadge  = nullptr;  // styled "Preview" tag while DZIsPreview()==1
    QLabel      *m_offlineBadge  = nullptr;  // styled "Offline" tag when the current track is saved offline
    QTimer      *m_poll = nullptr;

    // ---- data ----
    // home page data
    QVector<Track>    m_homeTracks;
    QVector<Playlist> m_homePlaylists;

    QVector<Track>    m_tableTracks;    // rows currently shown in m_trackTable
    QVector<Track>    m_searchTracks;   // rows currently shown in m_searchTrackTable
    QVector<Album>    m_searchAlbums;
    QVector<ArtistInfo> m_searchArtists; // artist tiles in m_searchResults (kind 2)
    QVector<Playlist> m_searchPlaylists;
    QVector<Playlist> m_playlists;

    // charts data (backs m_chartsTrackTable + m_chartsResults)
    QVector<Track>      m_chartsTracks;
    QVector<Album>      m_chartsAlbums;
    QVector<ArtistInfo> m_chartsArtists;
    QVector<Playlist>   m_chartsPlaylists;

    // podcasts data
    QVector<Podcast> m_podcasts;
    QVector<Episode> m_episodes;
    QString          m_currentPodcastName;   // shown as the now-playing "artist"

    // history page data (both play by id through playFrom)
    QVector<Track>   m_historyTracks;        // rows of m_historyTable
    QVector<Track>   m_statsTrackList;       // backs m_statsTopTracks

    // Liked-songs ids known to the UI. There is no is-liked query, so this is a
    // best-effort local mirror: seeded from the Liked Songs view and updated on
    // every like/unlike so the heart reflects state for known tracks.
    QSet<QString> m_likedIds;
    // Track ids saved for offline playback, learned from the {key} DZDownloadForOffline
    // returns. Like m_likedIds this is a best-effort per-session mirror (there is no
    // bulk "list offline ids" query); it drives the "downloaded" indicator.
    QSet<QString> m_offlineIds;
    QString       m_currentPlaylistId;       // set while the shared table shows a playlist
    QString       m_currentPlaylistName;     // its display name (batch-download message)
    QString       m_currentAlbumId;          // set while the shared table shows an album
    QString       m_currentAlbumName;        // its display name (batch-download message)

    // lyrics state
    QHash<QString, LyricsData> m_lyricsCache;       // parsed lyrics, keyed by track id
    QVector<qint64>            m_lyricsTimes;        // per-row start time (synced only)
    QString m_lyricsShownId;        // track currently rendered in the lyrics page
    QString m_lyricsRequestedId;    // most recent fetch target (guards re-fetch)
    bool    m_lyricsIsSynced = false;
    bool    m_lyricsFollowsPlayback = false; // auto-refetch when the track changes
    int     m_lyricsActiveRow = -1;          // highlighted line, or -1
    int     m_lyricsGen       = 0;           // guards async lyrics results

    // artist state
    QString             m_currentArtistId;   // artist on the artist page ("Start radio")
    QVector<Track>      m_artistTopTracks;
    QVector<Album>      m_artistAlbums;
    QVector<ArtistInfo> m_artistRelated;

    int m_returnPage = 0;           // stack index to restore from lyrics/artist

    QVector<Track> m_queue;             // the playing queue
    int            m_queueIndex = -1;
    Track          m_current;
    bool           m_hasCurrent = false;
    bool           m_currentIsEpisode = false; // podcast episode (plain-stream path)

    bool m_loggedIn   = false;
    bool m_seeking    = false;          // true while the user drags the seek slider
    int  m_lastFinished = 0;            // last DZFinishedCount() seen (auto-advance)
    int  m_artGen     = 0;              // bumped on every list reload to drop stale art
    int  m_playGen    = 0;              // bumped per track start; guards now-playing cover
    bool m_shuffle    = false;
    int  m_repeat     = 0;              // 0 off, 1 all, 2 one

    // Volume sender state (see setVolume): latest requested percent + whether
    // a sender worker is alive. Atomics — read/written from worker threads.
    std::atomic<int>  m_pendingVol{100};
    std::atomic<bool> m_volInFlight{false};

    // ---- OS integration state ----
    MprisController *m_mpris       = nullptr;   // session-bus media controls
    QSystemTrayIcon *m_tray        = nullptr;   // background / close-to-tray
    QString          m_lastStatus;              // dedupe MPRIS PlaybackStatus
    QString          m_connectName;             // friendly name of the connected device
    bool             m_connectScanning = false; // a Connect discovery scan is in flight
    QString          m_castAddr;                // last DZConnectedDevice() seen (tick diff)
    QLabel          *m_castLabel = nullptr;     // status-bar "Playing on <device>" indicator
    int              m_quality     = 0;         // 0 Normal, 1 High, 2 HiFi
    bool             m_replayGain  = false;     // loudness normalization (DZReplayGain)
    bool             m_closeToTray = true;      // honour close-to-tray setting
    bool             m_gapless     = false;     // gapless playback (DZGapless)
    int              m_crossfadeMs = 0;         // crossfade duration ms (DZCrossfadeMS)

    // ---- account tier (DZAccountJSON) ----
    QString m_accountName, m_accountOffer;      // shown in About / status bar
    bool    m_canHq       = false;              // plan allows MP3 320
    bool    m_canHifi     = false;              // plan allows FLAC
    bool    m_premium     = false;              // paid plan (gates Download; drives previews)
    bool    m_haveAccount = false;              // DZAccountJSON parsed OK
    QLabel *m_freeHint    = nullptr;            // status-bar "Free · standard quality (128 kbps)" hint
    bool             m_forceQuit   = false;     // set by an explicit Quit
    bool             m_trayHintShown = false;   // first hide-to-tray notice

    // Cover-art fetches run on a dedicated bounded pool so a burst of downloads
    // never starves playback/browse work on the global pool.
    QThreadPool m_artPool;
};
