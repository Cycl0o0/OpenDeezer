// Application entry point. OnLaunched creates + activates the MainWindow. The
// generated Main (XamlGeneratedMain) handles the Windows App SDK bootstrap for
// this unpackaged, self-contained build.

using System;
using System.Runtime.InteropServices;
using Microsoft.UI.Xaml;

namespace OpenDeezer;

public partial class App : Application
{
    private Window? _window;

    public App()
    {
        InitializeComponent();
        // Surface any unhandled XAML/dispatcher exception (coroutines, timers,
        // event handlers) as a dialog instead of a silent stowed-exception crash --
        // mirrors the C++ UnhandledException hook.
        UnhandledException += (_, e) =>
        {
            try { MessageBoxW(IntPtr.Zero, "OpenDeezer hit an unhandled error:\n\n" + e.Message, "OpenDeezer", 0x10); }
            catch { }
        };
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        _window = new MainWindow();
        _window.Activate();
    }

    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern int MessageBoxW(IntPtr hWnd, string text, string caption, uint type);
}
