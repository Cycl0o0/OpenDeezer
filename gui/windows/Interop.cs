// Win32 + WinRT interop the WindowsAppSDK projection does not expose directly:
//   * the tray icon (Shell_NotifyIcon) + a message-only window for its callbacks,
//   * SystemMediaTransportControls.GetForWindow(hwnd) -- the SMTC overlay is
//     acquired through the classic-COM ISystemMediaTransportControlsInterop on
//     the runtimeclass activation factory (there is no projected GetForWindow
//     on a desktop HWND, unlike CoreWindow).

using System;
using System.Runtime.InteropServices;
using Windows.Media;
using WinRT;

namespace OpenDeezer;

internal static class NativeMethods
{
    // ---- window messages / constants ----------------------------------------
    internal const uint WM_TRAYCALLBACK = 0x8000 + 1; // WM_APP + 1 (matches the C++ kTrayCallback)
    internal const uint WM_COMMAND = 0x0111;
    internal const int WM_LBUTTONDBLCLK = 0x0203;
    internal const int WM_RBUTTONUP = 0x0205;
    internal const int WM_CONTEXTMENU = 0x007B;

    internal const uint NIM_ADD = 0x00000000;
    internal const uint NIM_DELETE = 0x00000002;
    internal const uint NIF_MESSAGE = 0x00000001;
    internal const uint NIF_ICON = 0x00000002;
    internal const uint NIF_TIP = 0x00000004;

    internal const uint MF_STRING = 0x00000000;
    internal const uint MF_SEPARATOR = 0x00000800;
    internal const uint TPM_RIGHTBUTTON = 0x0002;

    internal const int MENU_RESTORE = 1001;
    internal const int MENU_QUIT = 1002;
    internal const uint TRAY_UID = 1;

    internal static readonly IntPtr HWND_MESSAGE = new(-3);
    internal static readonly IntPtr IDI_APPLICATION = new(32512);

    internal delegate IntPtr WndProcDelegate(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    internal struct WNDCLASSEXW
    {
        public uint cbSize;
        public uint style;
        public IntPtr lpfnWndProc;
        public int cbClsExtra;
        public int cbWndExtra;
        public IntPtr hInstance;
        public IntPtr hIcon;
        public IntPtr hCursor;
        public IntPtr hbrBackground;
        [MarshalAs(UnmanagedType.LPWStr)] public string? lpszMenuName;
        [MarshalAs(UnmanagedType.LPWStr)] public string? lpszClassName;
        public IntPtr hIconSm;
    }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    internal struct NOTIFYICONDATAW
    {
        public uint cbSize;
        public IntPtr hWnd;
        public uint uID;
        public uint uFlags;
        public uint uCallbackMessage;
        public IntPtr hIcon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string szTip;
        public uint dwState;
        public uint dwStateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string szInfo;
        public uint uVersionOrTimeout;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)] public string szInfoTitle;
        public uint dwInfoFlags;
        public Guid guidItem;
        public IntPtr hBalloonIcon;
    }

    [StructLayout(LayoutKind.Sequential)]
    internal struct POINT { public int X; public int Y; }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode)] internal static extern IntPtr GetModuleHandleW(string? lpModuleName);

    [DllImport("user32.dll", CharSet = CharSet.Unicode)] internal static extern ushort RegisterClassExW(ref WNDCLASSEXW wc);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    internal static extern IntPtr CreateWindowExW(uint exStyle, string className, string windowName, uint style,
        int x, int y, int w, int h, IntPtr parent, IntPtr menu, IntPtr hInstance, IntPtr param);
    [DllImport("user32.dll")] internal static extern IntPtr DefWindowProcW(IntPtr hWnd, uint msg, IntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll")] internal static extern bool SetForegroundWindow(IntPtr hWnd);
    [DllImport("user32.dll")] internal static extern bool GetCursorPos(out POINT pt);
    [DllImport("user32.dll")] internal static extern IntPtr CreatePopupMenu();
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] internal static extern bool AppendMenuW(IntPtr hMenu, uint flags, IntPtr id, string? item);
    [DllImport("user32.dll")] internal static extern bool TrackPopupMenu(IntPtr hMenu, uint flags, int x, int y, int reserved, IntPtr hWnd, IntPtr rect);
    [DllImport("user32.dll")] internal static extern bool DestroyMenu(IntPtr hMenu);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] internal static extern IntPtr LoadIconW(IntPtr hInstance, IntPtr lpIconName);

    [DllImport("shell32.dll", CharSet = CharSet.Unicode)] internal static extern bool Shell_NotifyIconW(uint msg, ref NOTIFYICONDATAW data);
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)] internal static extern IntPtr ExtractIconW(IntPtr hInst, string exeFileName, uint iconIndex);
}

// SystemMediaTransportControls for a desktop HWND. The interop interface lives on
// the runtimeclass activation factory; we grab it via RoGetActivationFactory, call
// GetForWindow, then project the returned ABI pointer back to the WinRT type.
internal static class Smtc
{
    [DllImport("combase.dll", CharSet = CharSet.Unicode)]
    private static extern int WindowsCreateString(string src, int length, out IntPtr hstring);
    [DllImport("combase.dll")] private static extern int WindowsDeleteString(IntPtr hstring);
    [DllImport("combase.dll")] private static extern int RoGetActivationFactory(IntPtr activatableClassId, [In] ref Guid iid, out IntPtr factory);

    [ComImport, Guid("ddb0472d-c911-4a1f-86d9-dc3d71a95f5a"), InterfaceType(ComInterfaceType.InterfaceIsIInspectable)]
    private interface ISystemMediaTransportControlsInterop
    {
        [PreserveSig] int GetForWindow(IntPtr appWindow, [In] ref Guid riid, out IntPtr mediaTransportControl);
    }

    // IID of ISystemMediaTransportControls (the runtimeclass default interface).
    private static readonly Guid IID_ISystemMediaTransportControls = new("99FA3FF4-1742-42A6-902E-087D41F965EC");

    internal static SystemMediaTransportControls? GetForWindow(IntPtr hwnd)
    {
        const string cls = "Windows.Media.SystemMediaTransportControls";
        IntPtr hClass = IntPtr.Zero;
        try
        {
            if (WindowsCreateString(cls, cls.Length, out hClass) < 0) return null;
            Guid interopIID = typeof(ISystemMediaTransportControlsInterop).GUID;
            if (RoGetActivationFactory(hClass, ref interopIID, out IntPtr factoryPtr) < 0 || factoryPtr == IntPtr.Zero)
                return null;
            // NOTE: factoryPtr / smtcPtr refs are intentionally not released here.
            // Both are process-lifetime singletons; skipping the releases avoids any
            // risk of an over-release while keeping the leak to a single ref each.
            var interop = (ISystemMediaTransportControlsInterop)Marshal.GetObjectForIUnknown(factoryPtr);
            Guid smtcIID = IID_ISystemMediaTransportControls;
            if (interop.GetForWindow(hwnd, ref smtcIID, out IntPtr smtcPtr) < 0 || smtcPtr == IntPtr.Zero)
                return null;
            return MarshalInspectable<SystemMediaTransportControls>.FromAbi(smtcPtr);
        }
        catch
        {
            return null;
        }
        finally
        {
            if (hClass != IntPtr.Zero) WindowsDeleteString(hClass);
        }
    }
}
