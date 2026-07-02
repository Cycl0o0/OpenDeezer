#include "settingsdialog.h"

#include <QCheckBox>
#include <QComboBox>
#include <QCoreApplication>
#include <QDesktopServices>
#include <QDialogButtonBox>
#include <QFormLayout>
#include <QGroupBox>
#include <QHBoxLayout>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QLabel>
#include <QLineEdit>
#include <QPointer>
#include <QPushButton>
#include <QSettings>
#include <QSlider>
#include <QTimer>
#include <QUrl>
#include <QVBoxLayout>
#include <QtConcurrent>

#include <utility> // std::as_const

// The Go engine's C API — only the remote-control calls are needed here
// (DZFree, to release DZControlConfigJSON's result, comes along with the
// header). Same include the other secondary KDE source files use.
extern "C" {
#include "libdeezercore.h"
}

// Remote-control settings (control API + phone remote). Redeclared here (like
// mainwindow.cpp does for its own additions) so the dialog still builds
// against an older generated header; identical redeclarations are harmless.
extern "C" char *DZControlConfigJSON(void); // {"enabled","addr","token","lan","running"}
extern "C" void  DZSetControlConfig(int enabled, char *addr, char *token);
extern "C" void  DZWebRemoteSetEnabled(int on);   // 1=enable, 0=disable
extern "C" char *DZWebRemoteInfoJSON(void);       // {"enabled":bool,...}

// v1.5.1 addition. Checks GitHub for a newer release; never downloads or
// installs anything. Result is a malloc'd JSON string — free with DZFree.
extern "C" char *DZCheckUpdateJSON(void); // {"current","latest","hasUpdate","url","notes"}

// Sleep timer. Pause after `minutes` (auto fade-out), or when the current track
// ends if endOfTrack != 0; minutes<=0 & endOfTrack==0 cancels. Applied live on
// change (like the remote-control group) — it's a transient engine action, not
// a persisted playback setting.
extern "C" void      DZSetSleepTimer(int minutes, int endOfTrack);
extern "C" void      DZCancelSleepTimer(void);
extern "C" int       DZSleepTimerActive(void);        // 1/0
extern "C" int       DZSleepTimerEndOfTrack(void);    // 1/0
extern "C" long long DZSleepTimerRemainingMS(void);

// Equalizer + mono downmix (v1.7). DSP, persistence (the engine's eq.json)
// and the band/preset tables all live in the core; the dialog only mirrors
// the state and applies edits live, like the remote-control group.
extern "C" char *DZEQJSON(void);        // {"enabled","mono","preampDb","gainsDb":[10],"preset","bands":[10],"presets":[...]}
extern "C" int   DZSetEQJSON(char *js); // partial update, every key optional; 1 = ok

namespace {
const char *kKeyQuality    = "audio/qualityLevel"; // int: 0=128, 1=320, 2=FLAC
const char *kKeyReplayGain = "audio/replayGain";   // bool: loudness normalization
const char *kKeyTray       = "behavior/closeToTray";
const char *kKeyDevice     = "audio/outputDevice"; // string: device id ("" = default)
const char *kKeyGapless    = "audio/gapless";      // bool: gapless playback
const char *kKeyCrossfade  = "audio/crossfadeMs";  // int: 0/3000/6000/12000

QSettings openIni(const QString &path) { return QSettings(path, QSettings::IniFormat); }

// Take ownership of a malloc'd C string from a DZ*JSON call, copy it into a
// QByteArray and release the C buffer with DZFree (mirrors mainwindow.cpp).
QByteArray takeJson(char *s) {
    QByteArray b;
    if (s) {
        b = QByteArray(s);
        DZFree(s);
    }
    return b;
}

// Compact band-frequency label under an EQ slider: "31", "63", … "1k", "16k".
QString eqBandLabel(double hz) {
    if (hz >= 1000)
        return QString::number(hz / 1000) + QStringLiteral("k");
    return QString::number(int(hz));
}

// Preset combo text from the core's wire name: "bass-boost" -> "Bass Boost".
QString eqPresetLabel(const QString &name) {
    QStringList words = name.split(QLatin1Char('-'));
    for (QString &w : words)
        if (!w.isEmpty())
            w[0] = w[0].toUpper();
    return words.join(QLatin1Char(' '));
}
} // namespace

int SettingsDialog::loadQuality(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    int v = s.value(kKeyQuality, 0).toInt(); // default: Normal (MP3_128)
    return v < 0 ? 0 : (v > 2 ? 2 : v);
}

bool SettingsDialog::loadReplayGain(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyReplayGain, false).toBool(); // default: off
}

bool SettingsDialog::loadCloseToTray(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyTray, true).toBool();      // default: keep playing in tray
}

QString SettingsDialog::loadOutputDevice(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyDevice, QString()).toString(); // default: system default
}

bool SettingsDialog::loadGapless(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    return s.value(kKeyGapless, false).toBool();  // default: off
}

int SettingsDialog::loadCrossfadeMs(const QString &iniPath) {
    QSettings s = openIni(iniPath);
    int v = s.value(kKeyCrossfade, 0).toInt();    // default: off
    return v < 0 ? 0 : v;
}

SettingsDialog::SettingsDialog(const QString &iniPath,
                               const QVector<AudioDevice> &devices,
                               const QString &currentDeviceId, QWidget *parent)
    : QDialog(parent), m_iniPath(iniPath), m_initialDevice(currentDeviceId) {
    setWindowTitle(tr("OpenDeezer Settings"));
    setModal(true);

    auto *root = new QVBoxLayout(this);

    // ---- Audio ----
    auto *audioBox  = new QGroupBox(tr("Audio"));
    auto *audioForm = new QFormLayout(audioBox);
    m_quality = new QComboBox;
    m_quality->addItem(tr("Normal — MP3 128 kbps"), 0);
    m_quality->addItem(tr("High — MP3 320 kbps"), 1);
    m_quality->addItem(tr("HiFi — FLAC lossless"), 2);
    m_quality->setCurrentIndex(loadQuality(m_iniPath));
    audioForm->addRow(tr("Streaming quality"), m_quality);
    m_replayGain = new QCheckBox(tr("Normalize loudness (ReplayGain)"));
    m_replayGain->setChecked(loadReplayGain(m_iniPath));
    audioForm->addRow(QString(), m_replayGain);

    // Mono downmix — engine-owned (part of the EQ state, but it works with the
    // equalizer off too), so it sits with the other output options and applies
    // live like the sleep timer. Seeded by refreshEQ() below.
    m_eqMono = new QCheckBox(tr("Mono audio (downmix to one channel)"));
    connect(m_eqMono, &QCheckBox::toggled, this, [this](bool on) {
        if (!m_eqLoading)
            sendEQ({{QStringLiteral("mono"), on}});
    });
    audioForm->addRow(QString(), m_eqMono);

    // Output device — populated from the live device list passed in. The current
    // selection prefers the engine's active device, then the system default.
    m_device = new QComboBox;
    if (devices.isEmpty()) {
        m_device->addItem(tr("System default"), QString());
    } else {
        for (const AudioDevice &d : devices) {
            QString label = d.name.isEmpty() ? tr("System default") : d.name;
            if (d.isDefault)
                label += tr("  (default)");
            m_device->addItem(label, d.id);
        }
        int sel = m_device->findData(currentDeviceId);
        if (sel < 0)
            for (int i = 0; i < devices.size(); ++i)
                if (devices[i].isDefault) { sel = i; break; }
        if (sel >= 0)
            m_device->setCurrentIndex(sel);
    }
    audioForm->addRow(tr("Output device"), m_device);

    // Gapless + crossfade. Crossfade overlaps adjacent tracks; gapless butts
    // them with no silence. Both rely on the engine preloading the next track.
    m_gapless = new QCheckBox(tr("Gapless playback"));
    m_gapless->setChecked(loadGapless(m_iniPath));
    audioForm->addRow(QString(), m_gapless);

    m_crossfade = new QComboBox;
    m_crossfade->addItem(tr("Off"), 0);
    m_crossfade->addItem(tr("3 seconds"), 3000);
    m_crossfade->addItem(tr("6 seconds"), 6000);
    m_crossfade->addItem(tr("12 seconds"), 12000);
    {
        const int xf = loadCrossfadeMs(m_iniPath);
        int sel = m_crossfade->findData(xf);
        m_crossfade->setCurrentIndex(sel < 0 ? 0 : sel);
    }
    audioForm->addRow(tr("Crossfade"), m_crossfade);

    // Sleep timer — pause after N minutes (auto fade-out) or at the end of the
    // current track. Not persisted: it's a live, transient engine action, so it
    // applies immediately on change rather than waiting for OK.
    m_sleepTimer = new QComboBox;
    m_sleepTimer->addItem(tr("Off"), 0);
    m_sleepTimer->addItem(tr("15 minutes"), 15);
    m_sleepTimer->addItem(tr("30 minutes"), 30);
    m_sleepTimer->addItem(tr("45 minutes"), 45);
    m_sleepTimer->addItem(tr("60 minutes"), 60);
    m_sleepTimer->addItem(tr("End of track"), -1);
    // Reflect the engine's current sleep-timer state; a running countdown is
    // snapped up to the nearest preset for display.
    if (DZSleepTimerActive()) {
        if (DZSleepTimerEndOfTrack()) {
            m_sleepTimer->setCurrentIndex(m_sleepTimer->findData(-1));
        } else {
            const long long remMin = (DZSleepTimerRemainingMS() + 59999) / 60000;
            int sel = -1;
            for (int m : {15, 30, 45, 60})
                if (remMin <= m) { sel = m_sleepTimer->findData(m); break; }
            m_sleepTimer->setCurrentIndex(sel < 0 ? m_sleepTimer->findData(60) : sel);
        }
    }
    // Connect after seeding the index so the initial state doesn't re-apply.
    connect(m_sleepTimer, QOverload<int>::of(&QComboBox::currentIndexChanged), this,
            [this](int) { applySleepTimer(); });
    audioForm->addRow(tr("Sleep timer"), m_sleepTimer);
    root->addWidget(audioBox);

    // ---- Equalizer ----
    // Talks to the engine directly and applies on every change: the DSP,
    // persistence (eq.json, debounced engine-side) and preset tables all live
    // in the core, so the dialog only mirrors DZEQJSON and pushes edits via
    // DZSetEQJSON. The group's own checkbox is the EQ on/off switch — Qt
    // greys out the contents when it's unchecked, mono stays in Audio above.
    m_eqBox = new QGroupBox(tr("Equalizer"));
    m_eqBox->setCheckable(true);
    auto *eqLay = new QVBoxLayout(m_eqBox);

    auto *eqPresetRow = new QHBoxLayout;
    eqPresetRow->addWidget(new QLabel(tr("Preset")));
    m_eqPreset = new QComboBox; // populated from the core's list by refreshEQ()
    eqPresetRow->addWidget(m_eqPreset, 1);
    eqLay->addLayout(eqPresetRow);

    // Ten vertical band sliders with the centre frequency underneath. Slider
    // units are dB × 10 so preset gains like 2.5 dB land exactly; band labels
    // come from the core's "bands" table rather than being hardcoded here.
    const QJsonArray eqBandHz =
        QJsonDocument::fromJson(takeJson(DZEQJSON())).object().value("bands").toArray();
    auto *eqBandsRow = new QHBoxLayout;
    for (int i = 0; i < kEQBands; ++i) {
        auto *sl = new QSlider(Qt::Vertical);
        sl->setRange(-120, 120); // ±12 dB
        sl->setPageStep(10);     // 1 dB
        sl->setTickPosition(QSlider::TicksBothSides);
        sl->setTickInterval(60); // marks every 6 dB, including one at 0
        sl->setMinimumHeight(110);
        m_eqSliders[i] = sl;

        auto *hz = new QLabel(eqBandLabel(eqBandHz.at(i).toDouble()));
        hz->setAlignment(Qt::AlignHCenter);
        QFont hzf = hz->font();
        hzf.setPointSize(qMax(1, hzf.pointSize() - 1));
        hz->setFont(hzf);

        auto *col = new QVBoxLayout;
        col->addWidget(sl, 1, Qt::AlignHCenter);
        col->addWidget(hz);
        eqBandsRow->addLayout(col);

        // valueChanged fires per pixel while dragging — mark the band dirty
        // and let the 33 ms timer coalesce the engine calls (~30/s max).
        connect(sl, &QSlider::valueChanged, this, [this, i, sl](int v) {
            sl->setToolTip(tr("%1 dB").arg(v / 10.0));
            if (m_eqLoading)
                return;
            // A manual band edit flips the engine's preset to "custom";
            // mirror that locally without re-firing the preset handler.
            m_eqLoading = true;
            m_eqPreset->setCurrentIndex(m_eqCustomIdx);
            m_eqLoading = false;
            m_eqDirtyBands.insert(i);
            if (!m_eqFlush->isActive())
                m_eqFlush->start();
        });
    }
    eqLay->addLayout(eqBandsRow);

    // Preamp: output trim under the bands, same ±12 dB range and dB×10 units.
    auto *eqPreampRow = new QHBoxLayout;
    eqPreampRow->addWidget(new QLabel(tr("Preamp")));
    m_eqPreamp = new QSlider(Qt::Horizontal);
    m_eqPreamp->setRange(-120, 120);
    m_eqPreamp->setPageStep(10);
    m_eqPreamp->setTickPosition(QSlider::TicksBelow);
    m_eqPreamp->setTickInterval(60);
    eqPreampRow->addWidget(m_eqPreamp, 1);
    eqLay->addLayout(eqPreampRow);
    root->addWidget(m_eqBox);

    m_eqFlush = new QTimer(this);
    m_eqFlush->setSingleShot(true);
    m_eqFlush->setInterval(33);
    connect(m_eqFlush, &QTimer::timeout, this, &SettingsDialog::flushEQ);
    // Don't lose the last ≤33 ms of a drag if the dialog closes mid-flight.
    connect(this, &QDialog::finished, this, [this](int) { flushEQ(); });

    // Seed every EQ control (including the mono checkbox above) from the
    // engine — another client may have changed the state since last time.
    refreshEQ();

    connect(m_eqBox, &QGroupBox::toggled, this, [this](bool on) {
        if (!m_eqLoading)
            sendEQ({{QStringLiteral("enabled"), on}});
    });
    connect(m_eqPreset, QOverload<int>::of(&QComboBox::currentIndexChanged), this,
            [this](int idx) {
        if (m_eqLoading || idx == m_eqCustomIdx)
            return; // "Custom" is a state the engine flips to, not a command
        m_eqDirtyBands.clear(); // stale band edits must not overwrite the preset
        sendEQ({{QStringLiteral("preset"), m_eqPreset->itemData(idx).toString()}});
        refreshEQ(); // the preset changed all ten gains — mirror them
    });
    connect(m_eqPreamp, &QSlider::valueChanged, this, [this](int v) {
        m_eqPreamp->setToolTip(tr("%1 dB").arg(v / 10.0));
        if (m_eqLoading)
            return;
        m_eqPreampDirty = true;
        if (!m_eqFlush->isActive())
            m_eqFlush->start();
    });

    // ---- Behaviour ----
    auto *behBox  = new QGroupBox(tr("Behaviour"));
    auto *behLay  = new QVBoxLayout(behBox);
    m_tray = new QCheckBox(tr("Keep playing in the background "
                                          "(close to tray)"));
    m_tray->setChecked(loadCloseToTray(m_iniPath));
    auto *hint = new QLabel(tr(
        "When enabled, closing the window hides it to the system tray and the "
        "music keeps playing. Use the tray icon to restore or quit."));
    hint->setWordWrap(true);
    QFont hf = hint->font();
    hf.setPointSize(qMax(1, hf.pointSize() - 1));
    hint->setFont(hf);
    behLay->addWidget(m_tray);
    behLay->addWidget(hint);
    root->addWidget(behBox);

    // ---- Remote control ----
    // Unlike the groups above, this talks to the engine directly and applies
    // on every change (it's toggling a live server, not a playback setting).
    auto *remoteBox  = new QGroupBox(tr("Remote control"));
    auto *remoteForm = new QFormLayout(remoteBox);

    m_ctrlEnable = new QCheckBox(tr("Enable remote control"));
    remoteForm->addRow(QString(), m_ctrlEnable);

    m_ctrlLan = new QCheckBox(tr("Allow on local network (LAN)"));
    remoteForm->addRow(QString(), m_ctrlLan);

    m_ctrlToken = new QLineEdit;
    m_ctrlToken->setPlaceholderText(tr("None"));
    remoteForm->addRow(tr("Access token"), m_ctrlToken);

    m_phoneRemote = new QCheckBox(tr("Enable Phone Remote"));
    remoteForm->addRow(QString(), m_phoneRemote);

    auto *remoteHint = new QLabel(tr(
        "Lets another OpenDeezer app or your phone control playback over the "
        "network."));
    remoteHint->setWordWrap(true);
    QFont ref = remoteHint->font();
    ref.setPointSize(qMax(1, ref.pointSize() - 1));
    remoteHint->setFont(ref);
    remoteForm->addRow(remoteHint);
    root->addWidget(remoteBox);

    // Seed both controls from the engine's current state.
    {
        const QJsonObject cfg =
            QJsonDocument::fromJson(takeJson(DZControlConfigJSON())).object();
        m_ctrlEnable->setChecked(cfg.value("enabled").toBool());
        m_ctrlLan->setChecked(cfg.value("lan").toBool());
        m_ctrlToken->setText(cfg.value("token").toString());
        m_appliedCtrlEnable = m_ctrlEnable->isChecked();
        m_appliedCtrlLan    = m_ctrlLan->isChecked();
        m_appliedCtrlToken  = m_ctrlToken->text();
    }
    m_ctrlLan->setEnabled(m_ctrlEnable->isChecked());
    m_ctrlToken->setEnabled(m_ctrlEnable->isChecked());
    {
        const QJsonObject info =
            QJsonDocument::fromJson(takeJson(DZWebRemoteInfoJSON())).object();
        m_phoneRemote->setChecked(info.value("enabled").toBool());
    }

    // Apply live on every change — no need to wait for OK.
    connect(m_ctrlEnable, &QCheckBox::toggled, this, [this](bool on) {
        m_ctrlLan->setEnabled(on);
        m_ctrlToken->setEnabled(on);
        applyControlConfig();
    });
    connect(m_ctrlLan, &QCheckBox::toggled, this, [this] { applyControlConfig(); });
    connect(m_ctrlToken, &QLineEdit::editingFinished, this, [this] { applyControlConfig(); });
    connect(m_phoneRemote, &QCheckBox::toggled, this,
            [](bool on) { DZWebRemoteSetEnabled(on ? 1 : 0); });

    // ---- About ----
    // On-demand release check (mirrors the background one MainWindow runs at
    // startup): never blocks, never downloads/installs anything — Download just
    // opens the GitHub release page in the browser.
    auto *aboutBox = new QGroupBox(tr("About"));
    auto *aboutLay = new QVBoxLayout(aboutBox);
    auto *updRow   = new QHBoxLayout;
    m_checkUpdatesBtn = new QPushButton(tr("Check for Updates"));
    updRow->addWidget(m_checkUpdatesBtn);
    m_updateResult = new QLabel;
    m_updateResult->setWordWrap(true);
    updRow->addWidget(m_updateResult, 1);
    m_updateDownloadBtn = new QPushButton(tr("Download"));
    m_updateDownloadBtn->hide();
    updRow->addWidget(m_updateDownloadBtn);
    aboutLay->addLayout(updRow);
    root->addWidget(aboutBox);

    connect(m_checkUpdatesBtn, &QPushButton::clicked, this, &SettingsDialog::checkForUpdates);
    connect(m_updateDownloadBtn, &QPushButton::clicked, this, [this] {
        if (!m_updateUrl.isEmpty())
            QDesktopServices::openUrl(QUrl(m_updateUrl));
    });

    auto *buttons = new QDialogButtonBox(QDialogButtonBox::Ok | QDialogButtonBox::Cancel);
    // Deezer-purple accent on the default action.
    buttons->button(QDialogButtonBox::Ok)->setStyleSheet(
        QStringLiteral("QPushButton{background:#A238FF;color:white;"
                       "padding:5px 16px;border-radius:4px;}"));
    root->addWidget(buttons);

    connect(buttons, &QDialogButtonBox::accepted, this, [this] { save(); accept(); });
    connect(buttons, &QDialogButtonBox::rejected, this, &QDialog::reject);
}

void SettingsDialog::save() {
    const int     level = m_quality->currentData().toInt();
    const bool    rg    = m_replayGain->isChecked();
    const bool    tray  = m_tray->isChecked();
    const QString dev   = m_device->currentData().toString();
    const bool    gap   = m_gapless->isChecked();
    const int     xf    = m_crossfade->currentData().toInt();

    QSettings s = openIni(m_iniPath);
    s.setValue(kKeyQuality, level);
    s.setValue(kKeyReplayGain, rg);
    s.setValue(kKeyTray, tray);
    s.setValue(kKeyDevice, dev);
    s.setValue(kKeyGapless, gap);
    s.setValue(kKeyCrossfade, xf);
    s.sync();

    emit qualityChanged(level);
    emit replayGainChanged(rg);
    emit closeToTrayChanged(tray);
    // Re-applying the same output device restarts audio with an audible glitch,
    // so only emit when it actually changed.
    if (dev != m_initialDevice)
        emit outputDeviceChanged(dev);
    emit gaplessChanged(gap);
    emit crossfadeChanged(xf);

    // The remote-control group already applies itself live on every change;
    // this just catches a token edit still pending when OK is pressed. Only
    // re-apply when something actually differs: DZSetControlConfig restarts
    // the control server, which kills an active Phone Remote session and
    // persists the control API as enabled.
    if (m_ctrlEnable->isChecked() != m_appliedCtrlEnable ||
        m_ctrlLan->isChecked() != m_appliedCtrlLan ||
        m_ctrlToken->text() != m_appliedCtrlToken)
        applyControlConfig();
}

void SettingsDialog::applyControlConfig() {
    const bool enabled = m_ctrlEnable->isChecked();
    const QByteArray addr  = m_ctrlLan->isChecked() ? QByteArray(":7654") : QByteArray();
    const QByteArray token = m_ctrlToken->text().toUtf8();
    DZSetControlConfig(enabled ? 1 : 0,
                        const_cast<char *>(addr.constData()),
                        const_cast<char *>(token.constData()));
    m_appliedCtrlEnable = enabled;
    m_appliedCtrlLan    = m_ctrlLan->isChecked();
    m_appliedCtrlToken  = m_ctrlToken->text();
}

// Push the chosen sleep-timer preset to the engine live: >0 minutes counts
// down (auto fade-out), <0 pauses at the end of the current track, 0 cancels.
void SettingsDialog::applySleepTimer() {
    const int v = m_sleepTimer->currentData().toInt();
    if (v > 0)
        DZSetSleepTimer(v, 0);
    else if (v < 0)
        DZSetSleepTimer(0, 1);
    else
        DZCancelSleepTimer();
}

// One partial EQ update: serialize the given keys and hand them to the engine
// (every DZSetEQJSON key is optional). EQ setters are cheap atomics, so this
// is a direct call like the other live-apply groups.
void SettingsDialog::sendEQ(const QJsonObject &o) {
    const QByteArray js = QJsonDocument(o).toJson(QJsonDocument::Compact);
    DZSetEQJSON(const_cast<char *>(js.constData()));
}

// (Re)seed every EQ control from the engine's snapshot — on construction
// (another client may have changed the state) and after a preset pick
// (the preset rewrote all ten gains). m_eqLoading keeps the handlers from
// echoing the seeded values straight back to the engine.
void SettingsDialog::refreshEQ() {
    const QJsonObject eq = QJsonDocument::fromJson(takeJson(DZEQJSON())).object();
    m_eqLoading = true;
    m_eqBox->setChecked(eq.value("enabled").toBool());
    m_eqMono->setChecked(eq.value("mono").toBool());
    m_eqPreamp->setValue(qRound(eq.value("preampDb").toDouble() * 10));
    const QJsonArray gains = eq.value("gainsDb").toArray();
    for (int i = 0; i < kEQBands && i < gains.size(); ++i)
        m_eqSliders[i]->setValue(qRound(gains.at(i).toDouble() * 10));
    // First call also builds the preset list from the core-owned table, plus
    // a trailing "Custom" entry (what the engine reports after a manual edit).
    if (m_eqPreset->count() == 0) {
        const QJsonArray presets = eq.value("presets").toArray();
        for (const QJsonValue &v : presets)
            m_eqPreset->addItem(eqPresetLabel(v.toString()), v.toString());
        m_eqCustomIdx = m_eqPreset->count();
        m_eqPreset->addItem(tr("Custom"), QStringLiteral("custom"));
    }
    const int sel = m_eqPreset->findData(eq.value("preset").toString());
    m_eqPreset->setCurrentIndex(sel < 0 ? m_eqCustomIdx : sel);
    m_eqLoading = false;
}

// Timer-coalesced flush of pending slider edits: one cheap DZSetEQJSON call
// per dirty band (usually just the one being dragged), plus the preamp if it
// moved. Persistence is debounced engine-side — nothing to save here.
void SettingsDialog::flushEQ() {
    for (int i : std::as_const(m_eqDirtyBands))
        sendEQ({{QStringLiteral("band"),
                 QJsonObject{{QStringLiteral("index"), i},
                             {QStringLiteral("gainDb"), m_eqSliders[i]->value() / 10.0}}}});
    m_eqDirtyBands.clear();
    if (m_eqPreampDirty) {
        sendEQ({{QStringLiteral("preampDb"), m_eqPreamp->value() / 10.0}});
        m_eqPreampDirty = false;
    }
}

// On-demand release check: runs DZCheckUpdateJSON off the GUI thread (it hits
// the network) and shows the result inline. Never downloads or installs
// anything — Download just opens the release page in the browser.
void SettingsDialog::checkForUpdates() {
    m_checkUpdatesBtn->setEnabled(false);
    m_updateDownloadBtn->hide();
    m_updateResult->setText(tr("Checking…"));
    m_updateResult->setToolTip(QString());

    // The dialog is a stack local in MainWindow and may be dismissed (OK /
    // Cancel / Escape) — and thus destroyed — before this blocking network
    // check returns. Guard the GUI-thread callback with a QPointer and post it
    // through qApp (which always outlives the dialog) so nothing dereferences a
    // freed dialog: not the queued lambda, and not invokeMethod's receiver.
    QPointer<SettingsDialog> self(this);
    QtConcurrent::run([this, self] {
        const QByteArray j = takeJson(DZCheckUpdateJSON());
        QMetaObject::invokeMethod(qApp, [this, self, j] {
            if (!self)
                return;
            m_checkUpdatesBtn->setEnabled(true);
            const QJsonObject o = QJsonDocument::fromJson(j).object();
            const QString latest = o.value("latest").toString();
            if (o.value("hasUpdate").toBool()) {
                m_updateUrl = o.value("url").toString();
                m_updateResult->setText(
                    tr("OpenDeezer %1 is available.").arg(latest));
                m_updateResult->setToolTip(o.value("notes").toString());
                m_updateDownloadBtn->show();
            } else if (!latest.isEmpty()) {
                m_updateResult->setText(tr("You're up to date (%1).").arg(latest));
            } else {
                m_updateResult->setText(
                    tr("Couldn't check for updates — try again later."));
            }
        });
    });
}
