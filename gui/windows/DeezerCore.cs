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

    // {current,latest,hasUpdate,url,notes}; network failure -> HasUpdate=false.
    internal static UpdateInfo CheckUpdate() => Wire.ParseUpdateInfo(TakeJson(DZCheckUpdateJSON()));
}
