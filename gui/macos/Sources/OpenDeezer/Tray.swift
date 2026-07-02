import Foundation
import AppKit

// TrayController gives OpenDeezer a menu-bar (NSStatusItem) presence and the
// "close to tray / keep playing in background" behaviour. When close-to-tray is
// on, hitting the window's close button hides the window (orderOut) and drops
// the Dock icon instead of terminating, so the Go engine keeps playing. The
// tray menu restores the window and quits. It honours the persisted setting.
@MainActor
final class TrayController: NSObject, NSWindowDelegate {
    private var statusItem: NSStatusItem?
    private weak var window: NSWindow?
    // SwiftUI installs its own window delegate; we slot in front of it and
    // transparently forward every selector we don't handle ourselves.
    private weak var previousDelegate: NSWindowDelegate?
    private weak var app: AppState?

    var closeToTray = true

    // MARK: lifecycle

    func attach(app: AppState) {
        self.app = app
        setupStatusItem()
        bindWindow()
    }

    private func bindWindow() {
        guard window == nil else { return }
        guard let w = NSApp.windows.first(where: { $0.canBecomeMain }) ?? NSApp.windows.first else {
            // The window may not exist yet at first launch — retry shortly.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { [weak self] in
                self?.bindWindow()
            }
            return
        }
        window = w
        previousDelegate = w.delegate
        w.delegate = self
    }

    // MARK: status item

    private func setupStatusItem() {
        guard statusItem == nil else { return }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            button.image = NSImage(systemSymbolName: "heart.fill",
                                   accessibilityDescription: "OpenDeezer")
            button.image?.isTemplate = true
            button.toolTip = "OpenDeezer"
        }

        let menu = NSMenu()
        menu.addItem(withTitle: L("Show OpenDeezer"), action: #selector(showWindow), keyEquivalent: "")
            .target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: L("Play / Pause"), action: #selector(playPause), keyEquivalent: "")
            .target = self
        menu.addItem(withTitle: L("Next"), action: #selector(nextTrack), keyEquivalent: "")
            .target = self
        menu.addItem(withTitle: L("Previous"), action: #selector(prevTrack), keyEquivalent: "")
            .target = self
        menu.addItem(.separator())
        menu.addItem(withTitle: L("Quit OpenDeezer"), action: #selector(quit), keyEquivalent: "q")
            .target = self

        item.menu = menu
        statusItem = item
    }

    // MARK: window close → tray

    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if closeToTray {
            hideToTray()
            return false
        }
        return true
    }

    private func hideToTray() {
        window?.orderOut(nil)
        // Drop the Dock icon while living in the tray; restored on reopen.
        NSApp.setActivationPolicy(.accessory)
    }

    @objc private func showWindow() {
        NSApp.setActivationPolicy(.regular)
        if let w = window {
            w.makeKeyAndOrderFront(nil)
        } else {
            bindWindow()
            window?.makeKeyAndOrderFront(nil)
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    // MARK: tray transport — routes to AppState's existing handlers

    @objc private func playPause() { app?.togglePause() }
    @objc private func nextTrack() { app?.next() }
    @objc private func prevTrack() { app?.prev() }
    @objc private func quit() { NSApp.terminate(nil) }

    // MARK: delegate forwarding (preserve SwiftUI's own window delegate)

    override func responds(to aSelector: Selector!) -> Bool {
        if super.responds(to: aSelector) { return true }
        return previousDelegate?.responds(to: aSelector) ?? false
    }

    override func forwardingTarget(for aSelector: Selector!) -> Any? {
        if previousDelegate?.responds(to: aSelector) == true { return previousDelegate }
        return nil
    }
}
