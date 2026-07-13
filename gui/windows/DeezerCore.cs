// P/Invoke bridge to the Go engine (libdeezercore.dll), the SAME C ABI the C++
// front-end called over extern "C". The engine does login, browse, Blowfish
// decrypt, MP3/FLAC decode and WASAPI playback in-process; this file is the
// marshaling boundary only.
//
// Marshaling rules (must match the Go side exactly):
//   * cdecl, all POD params: CallingConvention = Cdecl.
//   * Go returns C strings (UTF-8) that MUST be freed with DZFree. So every
//     char*-returning export is declared returning IntPtr; TakeJson() converts
//     it with Marshal.PtrToStringUTF8 (NOT PtrToStringAnsi -- Go strings are
//     UTF-8) and then calls DZFree, mirroring the C++ TakeJson helper.
//   * char* params are marshalled as UnmanagedType.LPUTF8Str (UTF-8 to match Go).
//   * DZFetch returns unsigned char* + an int* length: declared IntPtr + out int;
//     Fetch() copies the bytes with Marshal.Copy then frees with DZFreeBytes.
//   * long long -> long, double -> double, int -> int.

using System;
using System.Runtime.InteropServices;

namespace OpenDeezer;

internal static class DeezerCore
{
    private const string Dll = "libdeezercore";
    private const CallingConvention Cdecl = CallingConvention.Cdecl;

    // ---- raw exports (mirror libdeezercore.def / the extern "C" block) -------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZInit([MarshalAs(UnmanagedType.LPUTF8Str)] string arl);
    // Why the last DZInit(...) returned falsy: 0 ok, 1 ARL expired/invalid, 2 no internet, 3 other.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZLoginErrorKind();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZUserID();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZFavoritesJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZPlaylistsJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZPlaylistTracksJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZAlbumTracksJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZSearchJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string q);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZPlay([MarshalAs(UnmanagedType.LPUTF8Str)] string trackID, long durationMS);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZPause();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZResume();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZTogglePause();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZStop();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSeek(long ms);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZState();          // 0 stop 1 load 2 play 3 pause 4 err
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern long DZPositionMS();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern long DZDurationMS();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetVolume(double v);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern double DZVolume();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZFinishedCount();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZLastErrorJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZFree(IntPtr s);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZFetch([MarshalAs(UnmanagedType.LPUTF8Str)] string url, out int outLen);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZFreeBytes(IntPtr p);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetQuality(int level);   // 0=MP3_128,1=MP3_320,2=FLAC
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZHighQuality();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZQuality();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZFormat();

    // ---- v0.3 additions ------------------------------------------------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZAccountJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZChartsJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZArtistTopJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZArtistProfileJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZLyricsJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetReplayGain(int on);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZReplayGain();

    // ---- v0.4 additions ------------------------------------------------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZAddFavorite([MarshalAs(UnmanagedType.LPUTF8Str)] string trackID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZRemoveFavorite([MarshalAs(UnmanagedType.LPUTF8Str)] string trackID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZAddToPlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string playlistID, [MarshalAs(UnmanagedType.LPUTF8Str)] string trackID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZRemoveFromPlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string playlistID, [MarshalAs(UnmanagedType.LPUTF8Str)] string trackID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZCreatePlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string title);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZRenamePlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string playlistID, [MarshalAs(UnmanagedType.LPUTF8Str)] string title);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZDeletePlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string playlistID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZFlowJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZSearchPodcastsJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string q);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZPodcastEpisodesJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string podcastID);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZPlayEpisode([MarshalAs(UnmanagedType.LPUTF8Str)] string episodeID, long durationMS);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZAudioDevicesJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSetAudioDevice([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZCurrentAudioDevice();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetGapless(int on);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZGapless();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetCrossfadeMS(int ms);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZCrossfadeMS();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZPreload([MarshalAs(UnmanagedType.LPUTF8Str)] string trackID, long durationMS);

    // ---- OpenDeezer Connect (LAN device transfer) ---------------------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetClientInfo([MarshalAs(UnmanagedType.LPUTF8Str)] string client, [MarshalAs(UnmanagedType.LPUTF8Str)] string device);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZDiscoverDevices(int timeoutMS);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZConnectDevice([MarshalAs(UnmanagedType.LPUTF8Str)] string addr);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZDisconnectDevice();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZConnectedDevice();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZNowPlayingJSON();

    // ---- v1.0 additions (repeat/shuffle forwarding to connected remote) -----
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetRepeat(int mode);   // 0=off,1=all,2=one
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetShuffle(int on);    // 0/1

    // ---- web remote (phone pairing) ------------------------------------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZWebRemoteSetEnabled(int on);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZWebRemoteInfoJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZWebRemoteQRPNG(out int outLen);

    // ---- remote control (control API / phone remote settings) ---------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZControlConfigJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetControlConfig(int enabled, [MarshalAs(UnmanagedType.LPUTF8Str)] string addr, [MarshalAs(UnmanagedType.LPUTF8Str)] string token);

    // ---- Home aggregator -------------------------------------------------------
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZHomeJSON();

    // ---- update check (GitHub releases; never downloads/installs anything) ---
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZCheckUpdateJSON();

    // ---- v1.6 additions (sleep timer) ----------------------------------------
    // Pause after `minutes` (with an auto fade-out) or when the current track ends
    // if endOfTrack != 0. minutes<=0 && endOfTrack==0 cancels.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZSetSleepTimer(int minutes, int endOfTrack);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZCancelSleepTimer();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSleepTimerActive();      // 1/0
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSleepTimerEndOfTrack();   // 1/0
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern long DZSleepTimerRemainingMS();

    // ---- v1.7 additions (10-band equalizer / mono downmix) --------------------
    // DZEQJSON -> {enabled,mono,preampDb,gainsDb:[10],preset,bands:[10],presets:[...]}.
    // DZSetEQJSON takes a PARTIAL update (every key optional): enabled, mono,
    // preampDb, gainsDb ([10]), preset, band:{index,gainDb}. Returns 1 on success,
    // 0 if any present key failed (unknown preset, bad band index). EQ state +
    // persistence live in the engine (eq.json) -- the app never saves EQ itself.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZEQJSON();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSetEQJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string js);
    // Discard a preloaded next track once it is no longer the deterministic next
    // (shuffle / repeat toggled after a gapless preload was armed).
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZClearPreload();

    // ---- v1.9 additions (offline track export; PREMIUM-ONLY) -----------------
    // DZDownloadTrack decrypts + writes the full track to destDir and returns
    // malloc'd UTF-8 JSON {"path":"..."} on success or {"error":"..."} on failure
    // (free with DZFree, like every char* export). An empty destDir ("") targets
    // the shared default download folder. DZDownloadDir / DZSetDownloadDir read and
    // change that folder (SetDownloadDir returns 1 ok / 0 fail). DZIsPreview reports
    // whether the CURRENT stream is a 30-second preview rather than the full track.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZDownloadTrack([MarshalAs(UnmanagedType.LPUTF8Str)] string trackID, [MarshalAs(UnmanagedType.LPUTF8Str)] string destDir);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZDownloadDir();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSetDownloadDir([MarshalAs(UnmanagedType.LPUTF8Str)] string path);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZIsPreview();

    // Ad-report opt-out for Deezer Free (Premium has no ads). DZAdsDisabled reports
    // the current state (1 = plays are NOT reported / ads suppressed); DZSetAdsDisabled
    // persists it engine-side and returns 1 on success. See the disclaimer shown next
    // to the Settings toggle -- disabling reporting breaches Deezer's terms of use.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZAdsDisabled();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSetAdsDisabled(int disabled);

    // ---- v2.2 additions ------------------------------------------------------
    // Radio mixes: {tracks:[...]} exactly like DZFlowJSON, so the GUI reuses its
    // Flow parsing (Wire.ParseTracks). DZTrackMixJSON keeps the seed track first.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZTrackMixJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZArtistMixJSON([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    // Machine-local listening history. DZHistoryRecentJSON returns a BARE JSON
    // array [{trackId,title,artist,album,startedAt,durationPlayedSec}] (newest
    // first; n<=0 = all). DZHistoryStatsJSON returns
    // {topTracks:[{trackId,title,artist,plays,totalSec}],
    //  topArtists:[{artist,plays,totalSec}], totalSeconds:N} over the last
    // sinceDays (<=0 = all). Both free with DZFree.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZHistoryRecentJSON(int n);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZHistoryStatsJSON(int sinceDays);
    // Batch offline export (PREMIUM-ONLY, blocking -- call off the UI thread like
    // the single-track download). Returns {saved,failed,dir,error} JSON to the
    // shared download folder. Release with DZFree.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZDownloadAlbum([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZDownloadPlaylist([MarshalAs(UnmanagedType.LPUTF8Str)] string id);
    // On-disk raw-stream cache budget in MB (media.json; 0 = off). The cache is
    // attached to the player once at startup, so a change applies NEXT launch.
    // DZSetMediaCacheMB returns 1 on success.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZMediaCacheMB();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZSetMediaCacheMB(int mb);
    // Engine queue sync (GUI queue -> engine, so remote controllers see it on
    // /status and the engine can own auto-advance). DZQueueSet takes a JSON array
    // of tracks ({id,name,durationMs,artistLine,artistId,artists,albumName,
    // artworkUrl,explicit}); only id is required. "[]" clears it. The cursor
    // resets to 0 -> follow with DZQueueSetIndex to point it at the playing row.
    // Once aligned the ENGINE owns natural-finish advance (DZFinishedCount no
    // longer bumps for queue-owned finishes) -- poll DZQueueIndex to keep the GUI
    // cursor aligned. DZQueueSet returns 1 on success, 0 on a parse error;
    // DZQueueIndex returns the engine cursor (-1 when empty/unsynced).
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZQueueSet([MarshalAs(UnmanagedType.LPUTF8Str)] string js);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern void DZQueueSetIndex(int i);
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZQueueIndex();

    // ---- v3.0 additions (engine-truth transport + liked-ids cache) -----------
    // DZGetRepeat / DZGetShuffle report the ENGINE's current repeat/shuffle state;
    // when casting over Connect these mirror the remote device, so the transport
    // bar reconciles its displayed state from them each poll tick (fixes drift and
    // reflects the remote). DZGetRepeat is a plain string ("off"/"all"/"one",
    // free with DZFree like every char*); DZGetShuffle is 0/1.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZGetRepeat();
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern int DZGetShuffle();
    // Bare JSON array of the account's favorite track ids (["123",...]); seeds the
    // GUI liked-ids cache so the now-playing heart reflects real library state.
    [DllImport(Dll, CallingConvention = Cdecl)] internal static extern IntPtr DZFavoriteIDsJSON();

    // ---- helpers -------------------------------------------------------------
    // Own a DZ*JSON / char* result, copy it (UTF-8) and release it with DZFree.
    // Mirrors the C++ TakeJson(char*).
    internal static string TakeJson(IntPtr p)
    {
        if (p == IntPtr.Zero) return "";
        string s = Marshal.PtrToStringUTF8(p) ?? "";
        DZFree(p);
        return s;
    }

    // Fetch raw bytes (cover art): copy outLen bytes then free with DZFreeBytes.
    // Mirrors the C++ FetchBytes.
    internal static byte[] Fetch(string url)
    {
        IntPtr p = DZFetch(url, out int len);
        if (p == IntPtr.Zero) return Array.Empty<byte>();
        byte[] data = len > 0 ? new byte[len] : Array.Empty<byte>();
        if (len > 0) Marshal.Copy(p, data, 0, len);
        DZFreeBytes(p);
        return data;
    }

    // ---- typed convenience wrappers (keep MainWindow code clean) ------------
    internal static Account Account() => Wire.ParseAccount(TakeJson(DZAccountJSON()));
    internal static System.Collections.Generic.List<Track> Favorites() => Wire.ParseTracks(TakeJson(DZFavoritesJSON()));
    internal static System.Collections.Generic.List<Track> Flow() => Wire.ParseTracks(TakeJson(DZFlowJSON()));
    internal static System.Collections.Generic.List<Track> PlaylistTracks(string id) => Wire.ParseTracks(TakeJson(DZPlaylistTracksJSON(id)));
    internal static System.Collections.Generic.List<Track> AlbumTracks(string id) => Wire.ParseTracks(TakeJson(DZAlbumTracksJSON(id)));
    internal static System.Collections.Generic.List<Playlist> Playlists() => Wire.ParsePlaylists(TakeJson(DZPlaylistsJSON()));
    internal static ArtistProfile ArtistProfile(string id) => Wire.ParseArtistProfile(TakeJson(DZArtistProfileJSON(id)));
    internal static Lyrics Lyrics(string id) => Wire.ParseLyrics(TakeJson(DZLyricsJSON(id)));

    internal static bool Play(string id, long durMs) => DZPlay(id, durMs) != 0;
    internal static bool PlayEpisode(string id, long durMs) => DZPlayEpisode(id, durMs) != 0;

    internal static string Format() => TakeJson(DZFormat());
    internal static string ConnectedDevice() => TakeJson(DZConnectedDevice());
    internal static string CurrentAudioDevice() => TakeJson(DZCurrentAudioDevice());
    internal static string NowPlaying() => TakeJson(DZNowPlayingJSON());
    internal static string WebRemoteInfo() => TakeJson(DZWebRemoteInfoJSON());
    internal static byte[] WebRemoteQRPng()
    {
        IntPtr p = DZWebRemoteQRPNG(out int len);
        if (p == IntPtr.Zero) return Array.Empty<byte>();
        byte[] data = len > 0 ? new byte[len] : Array.Empty<byte>();
        if (len > 0) Marshal.Copy(p, data, 0, len);
        DZFreeBytes(p);
        return data;
    }
    internal static HomeData Home() => Wire.ParseHome(TakeJson(DZHomeJSON()));
    internal static string ControlConfig() => TakeJson(DZControlConfigJSON());
    internal static EQState EQ() => Wire.ParseEQ(TakeJson(DZEQJSON()));

    // Download a track to `dir` ("" = shared default folder); returns the raw
    // {"path":...} / {"error":...} JSON. DownloadDir/SetDownloadDir read + change
    // the default folder; IsPreview flags a 30-second preview of the current track.
    internal static string Download(string id, string dir) => TakeJson(DZDownloadTrack(id, dir));
    internal static string DownloadDir() => TakeJson(DZDownloadDir());
    internal static bool SetDownloadDir(string p) => DZSetDownloadDir(p) == 1;
    internal static bool IsPreview() => DZIsPreview() == 1;

    // Deezer Free ad-report opt-out (see DZAdsDisabled / DZSetAdsDisabled above).
    internal static bool AdsDisabled() => DZAdsDisabled() == 1;
    internal static bool SetAdsDisabled(bool off) => DZSetAdsDisabled(off ? 1 : 0) == 1;

    // {current,latest,hasUpdate,url,notes}; network failure -> HasUpdate=false.
    internal static UpdateInfo CheckUpdate() => Wire.ParseUpdateInfo(TakeJson(DZCheckUpdateJSON()));

    // ---- v2.2 typed wrappers -------------------------------------------------
    // Radio mixes parse exactly like Flow ({tracks:[...]}).
    internal static System.Collections.Generic.List<Track> TrackMix(string id) => Wire.ParseTracks(TakeJson(DZTrackMixJSON(id)));
    internal static System.Collections.Generic.List<Track> ArtistMix(string id) => Wire.ParseTracks(TakeJson(DZArtistMixJSON(id)));
    // Listening history: recent plays (as id-playable Tracks) + aggregate stats.
    internal static System.Collections.Generic.List<Track> HistoryRecent(int n) => Wire.ParseHistoryRecent(TakeJson(DZHistoryRecentJSON(n)));
    internal static HistoryStats HistoryStats(int sinceDays) => Wire.ParseHistoryStats(TakeJson(DZHistoryStatsJSON(sinceDays)));

    // Media (raw-stream) cache budget in MB; SetMediaCacheMB applies next launch.
    internal static int MediaCacheMB() => DZMediaCacheMB();
    internal static bool SetMediaCacheMB(int mb) => DZSetMediaCacheMB(mb) == 1;

    // ---- v3.0 typed wrappers -------------------------------------------------
    // Engine-truth transport state. Repeat maps the engine string onto the 0/1/2
    // the GUI already uses (off/all/one); anything unexpected falls back to off so
    // the display degrades safely. A numeric string is tolerated too.
    internal static int GetRepeat()
    {
        switch (TakeJson(DZGetRepeat()))
        {
            case "one": case "2": return 2;
            case "all": case "1": return 1;
            default: return 0; // "off" / "" / unknown
        }
    }
    internal static bool GetShuffle() => DZGetShuffle() != 0;
    // Favorite track ids (bare JSON id array) for the liked-ids cache.
    internal static System.Collections.Generic.List<string> FavoriteIDs() => Wire.ParseIdArray(TakeJson(DZFavoriteIDsJSON()));
}
