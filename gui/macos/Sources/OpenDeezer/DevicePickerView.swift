import SwiftUI

// DevicePickerView — OpenDeezer Connect: lists OpenDeezer instances found on the
// LAN (DZDiscoverDevices) so playback can be routed to one (DZConnectDevice),
// Spotify-Connect style. "This computer" returns playback here
// (DZDisconnectDevice). Once connected, the existing transport drives the chosen
// device, so no other player UI changes are needed.
struct DevicePickerView: View {
    @EnvironmentObject var app: AppState

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().overlay(DZ.hairline)
            content
        }
        .frame(width: 420, height: 480)
        .background(DZ.windowBG)
    }

    private var header: some View {
        HStack(spacing: 12) {
            Image(systemName: "rectangle.connected.to.line.below")
                .font(.system(size: 22)).foregroundStyle(DZ.accent)
            VStack(alignment: .leading, spacing: 2) {
                Text(L("Connect to a Device"))
                    .font(.system(size: 16, weight: .bold)).foregroundStyle(DZ.textPri)
                Text(L("Play on another device on your network"))
                    .font(.system(size: 12)).foregroundStyle(DZ.textSec).lineLimit(1)
            }
            Spacer()
            // Re-run discovery.
            Button { app.discoverDevices() } label: {
                Image(systemName: "arrow.clockwise").foregroundStyle(DZ.accent)
            }
            .buttonStyle(.plain).help(L("Refresh")).disabled(app.devicesLoading)
            Button(L("Done")) { app.showDevicePicker = false }
                .buttonStyle(.glass).tint(DZ.accent)
        }
        .padding(16)
    }

    private var content: some View {
        List {
            // This computer — local playback / disconnect from a remote device.
            SwiftUI.Section {
                deviceRow(symbol: "laptopcomputer", title: L("This computer"),
                          subtitle: L("Play here"), connected: !app.isConnectedRemote) {
                    app.disconnectDevice()
                }
            }

            SwiftUI.Section {
                if app.devicesLoading {
                    HStack(spacing: 10) {
                        ProgressView().controlSize(.small).tint(DZ.accent)
                        Text(L("Looking for devices…"))
                            .font(.system(size: 13)).foregroundStyle(DZ.textSec)
                    }
                    .listRowBackground(Color.clear)
                } else if app.devices.isEmpty {
                    Text(L("No devices found on your network."))
                        .font(.system(size: 13)).foregroundStyle(DZ.textSec)
                        .listRowBackground(Color.clear)
                } else {
                    ForEach(app.devices) { d in
                        deviceRow(symbol: d.symbol,
                                  title: d.name.isEmpty ? d.addr : d.name,
                                  subtitle: "\(d.typeLabel) · OpenDeezer \(d.version)",
                                  connected: d.addr == app.connectedDeviceAddr) {
                            app.connectDevice(d)
                        }
                    }
                }
            } header: {
                Text(L("Devices"))
                    .font(.system(size: 12, weight: .bold)).foregroundStyle(DZ.textSec)
            }
        }
        .listStyle(.inset)
        .scrollContentBackground(.hidden)
    }

    private func deviceRow(symbol: String, title: String, subtitle: String,
                           connected: Bool, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 12) {
                Image(systemName: symbol)
                    .font(.system(size: 18))
                    .foregroundStyle(connected ? DZ.accent : DZ.textPri)
                    .frame(width: 28)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).foregroundStyle(connected ? DZ.accent : DZ.textPri).lineLimit(1)
                    Text(subtitle).font(.caption).foregroundStyle(DZ.textSec).lineLimit(1)
                }
                Spacer()
                if connected {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(DZ.accent)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .listRowBackground(Color.clear)
    }
}
