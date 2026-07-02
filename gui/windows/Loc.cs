// Localization helper for the OpenDeezer Windows client.
//
// All UI is built in code-behind, so the classic WinUI x:Uid markup pattern does
// not apply. Instead strings live in Strings\<locale>\Resources.resw and are
// resolved here through MRT Core (Microsoft.Windows.ApplicationModel.Resources).
//
//   Loc.S("Nav_Home")                       -> "Home" / "首页" / ...
//   Loc.Format("Update_TitleFormat", ver)   -> string.Format with {0}, {1}, ...
//   Loc.Plural("Tracks", n)                  -> CLDR-correct counted noun ("3 titres")
//
// The active language is chosen by the user (Settings > Language) and persisted in
// settings.json. An empty tag ("") means "follow the Windows display language".
// SetLanguage() is called once at startup (MainWindow ctor) BEFORE the UI is built,
// so every literal picks up the right language; changing it later needs a restart
// because the already-built code-behind tree does not re-text itself.

using System;
using System.Globalization;
using Microsoft.UI.Xaml;
using Microsoft.Windows.ApplicationModel.Resources;

namespace OpenDeezer;

internal static class Loc
{
    private static readonly ResourceManager _rm = new();
    private static ResourceContext _ctx = _rm.CreateResourceContext();

    private static string _base = "en";       // base language subtag for plural / RTL rules
    private static CultureInfo _culture = CultureInfo.InvariantCulture; // number formatting
    private static bool _rtl;

    // Supported UI languages: (settings tag, base subtag). "" = follow the OS.
    internal static readonly (string Tag, string Native)[] Languages =
    {
        ("",       "System default"), // replaced with the localized label by the caller
        ("en-US",  "English"),
        ("zh-CN",  "简体中文"),
        ("hi-IN",  "हिन्दी"),
        ("es-ES",  "Español"),
        ("fr-FR",  "Français"),
        ("ar-SA",  "العربية"),
        ("ru-RU",  "Русский"),
    };

    // Point the resource resolver at a specific language (or the OS default when the
    // tag is empty). Recreates the ResourceContext so a stale Language qualifier can
    // never linger. Also derives the base subtag, formatting culture and RTL flag.
    internal static void SetLanguage(string tag)
    {
        _ctx = _rm.CreateResourceContext();
        string resolved;
        if (!string.IsNullOrEmpty(tag))
        {
            _ctx.QualifierValues["Language"] = tag;
            resolved = tag;
        }
        else
        {
            // Follow the OS display language (MRT resolves resources the same way).
            try { resolved = CultureInfo.CurrentUICulture.Name; } catch { resolved = "en-US"; }
        }
        _base = BaseOf(resolved);
        _culture = SafeCulture(resolved);
        _rtl = _base is "ar" or "he" or "fa" or "ur";
    }

    private static string BaseOf(string tag)
    {
        if (string.IsNullOrEmpty(tag)) return "en";
        int dash = tag.IndexOfAny(new[] { '-', '_' });
        return (dash > 0 ? tag[..dash] : tag).ToLowerInvariant();
    }

    private static CultureInfo SafeCulture(string tag)
    {
        try { return string.IsNullOrEmpty(tag) ? CultureInfo.CurrentCulture : new CultureInfo(tag); }
        catch { return CultureInfo.InvariantCulture; }
    }

    internal static bool IsRtl => _rtl;

    // Fully qualified so the enum reference cannot bind to this same-named property.
    internal static Microsoft.UI.Xaml.FlowDirection FlowDirection =>
        _rtl ? Microsoft.UI.Xaml.FlowDirection.RightToLeft : Microsoft.UI.Xaml.FlowDirection.LeftToRight;

    // Look up a resource by key. A missing key returns the key itself (so an
    // untranslated build still renders something identifiable, never blank).
    internal static string S(string key)
    {
        try
        {
            var c = _rm.MainResourceMap.TryGetValue("Resources/" + key, _ctx);
            if (c != null) return c.ValueAsString;
        }
        catch { }
        return key;
    }

    // Positional {0}/{1}/... substitution, formatted in the active culture.
    internal static string Format(string key, params object[] args)
    {
        string fmt = S(key);
        try { return string.Format(_culture, fmt, args); }
        catch { return fmt; }
    }

    // Counted noun with the platform-native plural form. `baseKey` is the noun stem
    // ("Tracks"); the resolved key is baseKey + "_" + <CLDR category>. The count is
    // formatted in the active culture and substituted for {0}.
    internal static string Plural(string baseKey, long n)
    {
        string cat = PluralCategory(_base, n);
        string fmt = S(baseKey + "_" + cat);
        if (fmt == baseKey + "_" + cat) // category missing for this language -> Other
        {
            fmt = S(baseKey + "_Other");
            if (fmt == baseKey + "_Other") fmt = "{0}";
        }
        string num;
        try { num = n.ToString("N0", _culture); } catch { num = n.ToString(CultureInfo.InvariantCulture); }
        try { return string.Format(_culture, fmt, num); }
        catch { return num; }
    }

    // CLDR cardinal plural category (returns a key suffix: One/Two/Few/Many/Other/Zero).
    // Only the categories a language actually uses are ever returned, matching the
    // forms present in that language's Resources.resw.
    private static string PluralCategory(string lang, long n)
    {
        long an = Math.Abs(n);
        switch (lang)
        {
            case "zh":
            case "ja":
            case "ko":
            case "th":
                return "Other"; // no plural distinction
            case "en":
            case "es":
                return an == 1 ? "One" : "Other";
            case "fr":
            case "hi":
                return (an == 0 || an == 1) ? "One" : "Other";
            case "ru":
            {
                long m10 = an % 10, m100 = an % 100;
                if (m10 == 1 && m100 != 11) return "One";
                if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return "Few";
                return "Many"; // 0, 5-9, 11-14 and everything else integral
            }
            case "ar":
            {
                if (an == 0) return "Zero";
                if (an == 1) return "One";
                if (an == 2) return "Two";
                long m100 = an % 100;
                if (m100 >= 3 && m100 <= 10) return "Few";
                if (m100 >= 11 && m100 <= 99) return "Many";
                return "Other"; // n%100 in {0,1,2} with n>=100
            }
            default:
                return an == 1 ? "One" : "Other";
        }
    }
}
