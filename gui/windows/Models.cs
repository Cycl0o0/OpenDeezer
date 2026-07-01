// Wire models + JSON parsing for OpenDeezer (C# WinUI 3 port of main.cpp).
//
// The Go engine (libdeezercore.dll) returns UTF-8 JSON over its C ABI; these
// types mirror corelib's jTrack / jAlbum / jPlaylist / ... shapes 1:1 and the
// parsers pull the EXACT same keys the C++ front-end used (id, name, durationMs,
// artistLine, albumName, artworkUrl, explicit, artists[0].id, userId, offer,
// canHq, canHifi, loggedIn, premium, synced[].timeMs, ...). System.Text.Json
// (JsonDocument) replaces Windows.Data.Json; behavior is identical.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace OpenDeezer;

// ---- wire models (mirror corelib jTrack/jAlbum/jPlaylist) -------------------
// IsEpisode flags a podcast episode: it shares the queue but plays through the
// plain-stream path (DZPlayEpisode) and skips like / add-to-playlist / preload.
internal sealed class Track
{
    public string Id = "", Name = "", ArtistId = "", ArtistLine = "", AlbumName = "", ArtworkUrl = "";
    public long DurationMs;
    public bool IsEpisode;
    public bool IsExplicit;
}

internal sealed class Album { public string Id = "", Name = "", ArtistLine = "", ArtworkUrl = ""; }

internal sealed class Playlist { public string Id = "", Name = "", Owner = "", ArtworkUrl = ""; public int TrackCount; }

// Premium=false is a Deezer Free account that CANNOT stream on-demand -> the app
// gates itself behind a block message (see ShowBlocked / FinishLogin).
internal sealed class Account { public string UserId = "", Name = "", Offer = ""; public bool CanHq, CanHifi, LoggedIn, Premium; }

internal sealed class Podcast { public string Id = "", Name = "", Description = "", ArtworkUrl = ""; public int EpisodeCount; }

internal sealed class Episode { public string Id = "", Title = "", Description = "", ArtworkUrl = "", ReleaseDate = ""; public long DurationMs; }

// jArtistInfo: {id,name,artworkUrl,nbFans} (related artists + artist header)
internal sealed class ArtistInfo { public string Id = "", Name = "", ArtworkUrl = ""; public long NbFans; }

// DZLyricsJSON: {plain, synced:[{timeMs,text}], isSynced}
internal sealed class LyricLine { public long TimeMs; public string Text = ""; }
internal sealed class Lyrics { public string Plain = ""; public List<LyricLine> Synced = new(); public bool IsSynced; }

// DZArtistProfileJSON: {artist, top:[T], albums:[A], related:[Ar]}
internal sealed class ArtistProfile { public ArtistInfo Artist = new(); public List<Track> Top = new(); public List<Album> Albums = new(); public List<ArtistInfo> Related = new(); }

// DZAudioDevicesJSON -> {"devices":[{id,name,isDefault}]} (id "" = default).
internal sealed class AudioDevice { public string Id = "", Name = ""; public bool IsDefault; }

// DZDiscoverDevices -> [{name,addr,client,version}].
internal sealed class ConnectDevice { public string Name = "", Addr = "", Client = "", Version = ""; }

// DZHomeJSON -> {"topTracks":[T],"topAlbums":[A],"playlists":[P]}
internal sealed class HomeData { public List<Track> TopTracks = new(); public List<Playlist> Playlists = new(); }

// DZCheckUpdateJSON -> {"current","latest","hasUpdate","url","notes"}.
internal sealed class UpdateInfo { public string Current = "", Latest = "", Url = "", Notes = ""; public bool HasUpdate; }

// Persisted settings. quality: 0 Normal,1 High,2 HiFi. audioDevice "" = default. crossfadeMs 0 = off.
internal sealed class Settings
{
    public int Quality = 1;
    public bool CloseToTray = true;
    public bool ReplayGain;
    public bool Gapless;
    public int CrossfadeMs;
    public string AudioDevice = "";
}

// ---- small JsonElement accessors (defaulting, never throwing) ----------------
internal static class JsonExt
{
    public static string Str(this JsonElement e, string name, string def = "")
    {
        if (e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String)
            return v.GetString() ?? def;
        return def;
    }

    public static long Num(this JsonElement e, string name, long def = 0)
    {
        if (e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.Number)
        {
            if (v.TryGetInt64(out var l)) return l;
            if (v.TryGetDouble(out var d)) return (long)d;
        }
        return def;
    }

    public static bool Bool(this JsonElement e, string name, bool def = false)
    {
        if (e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v))
        {
            if (v.ValueKind == JsonValueKind.True) return true;
            if (v.ValueKind == JsonValueKind.False) return false;
        }
        return def;
    }

    // Returns the named array element, or an Undefined element (caller must check ValueKind).
    public static JsonElement Arr(this JsonElement e, string name)
    {
        if (e.ValueKind == JsonValueKind.Object && e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.Array)
            return v;
        return default;
    }
}

// ---- parsers + small display helpers ----------------------------------------
internal static class Wire
{
    public static string TimeText(long ms)
    {
        if (ms < 0) ms = 0;
        long s = ms / 1000;
        return $"{s / 60}:{s % 60:D2}";
    }

    // "1,234,567 fans" (thousands-grouped); empty when unknown.
    public static string FansText(long n)
    {
        if (n <= 0) return "";
        return n.ToString("N0", CultureInfo.InvariantCulture) + " fans";
    }

    // Map a discovery client/platform id to a friendly device type for the picker.
    public static string ConnectTypeLabel(string client)
    {
        string c = (client ?? "").ToLowerInvariant();
        if (c == "tui") return "Terminal";
        if (c == "darwin" || c == "macos") return "macOS";
        if (c == "windows") return "Windows";
        if (c == "linux" || c == "gnome" || c == "kde") return "Linux";
        return string.IsNullOrEmpty(client) ? "Device" : client;
    }

    // This PC's name, advertised over OpenDeezer Connect discovery.
    public static string ThisDeviceName()
    {
        try { var n = Environment.MachineName; if (!string.IsNullOrEmpty(n)) return n; } catch { }
        return "OpenDeezer for Windows";
    }

    // One jTrack object -> Track. Pulls artists[0].id so a row can open its artist.
    public static Track TrackFromObj(JsonElement o)
    {
        var t = new Track
        {
            Id = o.Str("id"),
            Name = o.Str("name"),
            DurationMs = o.Num("durationMs"),
            ArtistLine = o.Str("artistLine"),
            AlbumName = o.Str("albumName"),
            ArtworkUrl = o.Str("artworkUrl"),
            IsExplicit = o.Bool("explicit"),
        };
        var artists = o.Arr("artists");
        if (artists.ValueKind == JsonValueKind.Array)
            foreach (var a in artists.EnumerateArray()) { t.ArtistId = a.Str("id"); break; }
        return t;
    }

    private static Album AlbumFromObj(JsonElement o)
    {
        var a = new Album { Id = o.Str("id"), Name = o.Str("name"), ArtworkUrl = o.Str("artworkUrl") };
        var artists = o.Arr("artists");
        if (artists.ValueKind == JsonValueKind.Array)
            foreach (var ar in artists.EnumerateArray()) { a.ArtistLine = ar.Str("name"); break; }
        return a;
    }

    private static ArtistInfo ArtistFromObj(JsonElement o) =>
        new() { Id = o.Str("id"), Name = o.Str("name"), ArtworkUrl = o.Str("artworkUrl"), NbFans = o.Num("nbFans") };

    private static JsonDocument? TryParse(string json)
    {
        try { return JsonDocument.Parse(string.IsNullOrEmpty(json) ? "{}" : json); }
        catch { return null; }
    }

    public static List<Track> ParseTracks(string json)
    {
        var outl = new List<Track>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("tracks");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray()) outl.Add(TrackFromObj(v));
        return outl;
    }

    public static List<Album> ParseAlbums(string json)
    {
        var outl = new List<Album>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("albums");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray()) outl.Add(AlbumFromObj(v));
        return outl;
    }

    public static List<Playlist> ParsePlaylists(string json)
    {
        var outl = new List<Playlist>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("playlists");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray())
                outl.Add(new Playlist
                {
                    Id = v.Str("id"),
                    Name = v.Str("name"),
                    Owner = v.Str("owner"),
                    TrackCount = (int)v.Num("trackCount"),
                    ArtworkUrl = v.Str("artworkUrl"),
                });
        return outl;
    }

    // Charts / browse "artists":[{id,name,artworkUrl,nbFans}] -> standalone artist tiles.
    public static List<ArtistInfo> ParseArtists(string json)
    {
        var outl = new List<ArtistInfo>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("artists");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray()) outl.Add(ArtistFromObj(v));
        return outl;
    }

    public static List<Podcast> ParsePodcasts(string json)
    {
        var outl = new List<Podcast>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("podcasts");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray())
                outl.Add(new Podcast
                {
                    Id = v.Str("id"),
                    Name = v.Str("name"),
                    Description = v.Str("description"),
                    ArtworkUrl = v.Str("artworkUrl"),
                    EpisodeCount = (int)v.Num("episodeCount"),
                });
        return outl;
    }

    public static List<Episode> ParseEpisodes(string json)
    {
        var outl = new List<Episode>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("episodes");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray())
                outl.Add(new Episode
                {
                    Id = v.Str("id"),
                    Title = v.Str("title"),
                    Description = v.Str("description"),
                    ArtworkUrl = v.Str("artworkUrl"),
                    DurationMs = v.Num("durationMs"),
                    ReleaseDate = v.Str("releaseDate"),
                });
        return outl;
    }

    public static Account ParseAccount(string json)
    {
        var a = new Account();
        using var doc = TryParse(json);
        if (doc == null) return a;
        var o = doc.RootElement;
        a.UserId = o.Str("userId");
        a.Name = o.Str("name");
        a.Offer = o.Str("offer");
        a.CanHq = o.Bool("canHq");
        a.CanHifi = o.Bool("canHifi");
        a.LoggedIn = o.Bool("loggedIn");
        a.Premium = o.Bool("premium"); // false = Deezer Free (no on-demand)
        return a;
    }

    public static Lyrics ParseLyrics(string json)
    {
        var ly = new Lyrics();
        using var doc = TryParse(json);
        if (doc == null) return ly;
        var o = doc.RootElement;
        ly.Plain = o.Str("plain");
        ly.IsSynced = o.Bool("isSynced");
        var arr = o.Arr("synced");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray())
                ly.Synced.Add(new LyricLine { TimeMs = v.Num("timeMs"), Text = v.Str("text") });
        return ly;
    }

    public static ArtistProfile ParseArtistProfile(string json)
    {
        var p = new ArtistProfile();
        using var doc = TryParse(json);
        if (doc == null) return p;
        var o = doc.RootElement;
        if (o.ValueKind == JsonValueKind.Object && o.TryGetProperty("artist", out var ar) && ar.ValueKind == JsonValueKind.Object)
            p.Artist = ArtistFromObj(ar);
        var top = o.Arr("top");
        if (top.ValueKind == JsonValueKind.Array) foreach (var v in top.EnumerateArray()) p.Top.Add(TrackFromObj(v));
        var albums = o.Arr("albums");
        if (albums.ValueKind == JsonValueKind.Array) foreach (var v in albums.EnumerateArray()) p.Albums.Add(AlbumFromObj(v));
        var related = o.Arr("related");
        if (related.ValueKind == JsonValueKind.Array) foreach (var v in related.EnumerateArray()) p.Related.Add(ArtistFromObj(v));
        return p;
    }

    // DZCreatePlaylist -> {"id":"..."} (or {"error":"..."}); "" on failure.
    public static string ParseCreatedId(string json)
    {
        using var doc = TryParse(json);
        return doc?.RootElement.Str("id") ?? "";
    }

    public static List<AudioDevice> ParseDevices(string json)
    {
        var outl = new List<AudioDevice>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var arr = doc.RootElement.Arr("devices");
        if (arr.ValueKind == JsonValueKind.Array)
            foreach (var v in arr.EnumerateArray())
                outl.Add(new AudioDevice { Id = v.Str("id"), Name = v.Str("name"), IsDefault = v.Bool("isDefault") });
        return outl;
    }

    // DZDiscoverDevices returns a BARE array [{...}] (or an {"error":...} object,
    // which is not an array -> parses to empty).
    public static List<ConnectDevice> ParseConnectDevices(string json)
    {
        var outl = new List<ConnectDevice>();
        using var doc = TryParse(json);
        if (doc == null) return outl;
        var root = doc.RootElement;
        if (root.ValueKind == JsonValueKind.Array)
            foreach (var v in root.EnumerateArray())
                outl.Add(new ConnectDevice
                {
                    Name = v.Str("name"),
                    Addr = v.Str("addr"),
                    Client = v.Str("client"),
                    Version = v.Str("version"),
                });
        return outl;
    }

    public static UpdateInfo ParseUpdateInfo(string json)
    {
        var u = new UpdateInfo();
        using var doc = TryParse(json);
        if (doc == null) return u;
        var o = doc.RootElement;
        u.Current = o.Str("current");
        u.Latest = o.Str("latest");
        u.HasUpdate = o.Bool("hasUpdate");
        u.Url = o.Str("url");
        u.Notes = o.Str("notes");
        return u;
    }

    public static HomeData ParseHome(string json)
    {
        var h = new HomeData();
        using var doc = TryParse(json);
        if (doc == null) return h;
        var o = doc.RootElement;
        var tracks = o.Arr("topTracks");
        if (tracks.ValueKind == JsonValueKind.Array)
            foreach (var v in tracks.EnumerateArray()) h.TopTracks.Add(TrackFromObj(v));
        var plists = o.Arr("playlists");
        if (plists.ValueKind == JsonValueKind.Array)
            foreach (var v in plists.EnumerateArray())
                h.Playlists.Add(new Playlist
                {
                    Id = v.Str("id"),
                    Name = v.Str("name"),
                    Owner = v.Str("owner"),
                    TrackCount = (int)v.Num("trackCount"),
                    ArtworkUrl = v.Str("artworkUrl"),
                });
        return h;
    }
}

// ---- config dir + arl + settings persistence --------------------------------
// SAME on-disk formats as main.cpp so existing logins/settings carry over:
//   %APPDATA%\opendeezer\arl.txt      (raw UTF-8 ARL, no BOM)
//   %APPDATA%\opendeezer\settings.json {quality,closeToTray,replayGain,gapless,crossfadeMs,audioDevice}
internal static class Config
{
    public static string ConfigDir()
    {
        var appdata = Environment.GetEnvironmentVariable("APPDATA");
        if (string.IsNullOrEmpty(appdata)) appdata = Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData);
        return string.IsNullOrEmpty(appdata) ? "" : Path.Combine(appdata, "opendeezer");
    }

    private static string SettingsPath()
    {
        var d = ConfigDir();
        return string.IsNullOrEmpty(d) ? "" : Path.Combine(d, "settings.json");
    }

    // ARL: %DEEZER_ARL% first, then %APPDATA%\opendeezer\arl.txt.
    public static string LoadArl()
    {
        var env = Environment.GetEnvironmentVariable("DEEZER_ARL");
        if (!string.IsNullOrWhiteSpace(env)) return env.Trim();
        try
        {
            var path = Path.Combine(ConfigDir(), "arl.txt");
            if (File.Exists(path)) return File.ReadAllText(path, Encoding.UTF8).Trim();
        }
        catch { }
        return "";
    }

    // Persist a captured/entered ARL to the SAME file LoadArl() reads at startup.
    public static void SaveArl(string arl)
    {
        var d = ConfigDir();
        if (string.IsNullOrEmpty(d)) return;
        try
        {
            Directory.CreateDirectory(d);
            File.WriteAllText(Path.Combine(d, "arl.txt"), arl, new UTF8Encoding(false)); // no BOM
        }
        catch { }
    }

    public static Settings LoadSettings()
    {
        var s = new Settings();
        try
        {
            var path = SettingsPath();
            if (string.IsNullOrEmpty(path) || !File.Exists(path)) return s;
            using var doc = JsonDocument.Parse(File.ReadAllText(path));
            var o = doc.RootElement;
            s.Quality = (int)o.Num("quality", s.Quality);
            s.CloseToTray = o.Bool("closeToTray", s.CloseToTray);
            s.ReplayGain = o.Bool("replayGain", s.ReplayGain);
            s.Gapless = o.Bool("gapless", s.Gapless);
            s.CrossfadeMs = (int)o.Num("crossfadeMs", s.CrossfadeMs);
            s.AudioDevice = o.Str("audioDevice", s.AudioDevice);
        }
        catch { }
        return s;
    }

    public static void SaveSettings(Settings s)
    {
        var dir = ConfigDir();
        if (string.IsNullOrEmpty(dir)) return;
        try
        {
            Directory.CreateDirectory(dir);
            var o = new JsonObject
            {
                ["quality"] = s.Quality,
                ["closeToTray"] = s.CloseToTray,
                ["replayGain"] = s.ReplayGain,
                ["gapless"] = s.Gapless,
                ["crossfadeMs"] = s.CrossfadeMs,
                ["audioDevice"] = s.AudioDevice,
            };
            File.WriteAllText(SettingsPath(), o.ToJsonString());
        }
        catch { }
    }
}
