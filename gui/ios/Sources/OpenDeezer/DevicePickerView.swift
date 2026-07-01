import SwiftUI

/// OpenDeezer Connect device picker — discovers LAN peers and routes
/// playback to them (AirPlay-style, but talking to the app's own control API).
struct DevicePickerView: View {
    @EnvironmentObject private var player: PlayerController
    @Environment(\.dismiss) private var dismiss

    @State private var devices: [Device] = []
    @State private var isScanning = false
    @State private var connectingAddr: String?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Button {
                        Task {
                            await player.disconnect()
                            dismiss()
                        }
                    } label: {
                        HStack {
                            Image(systemName: "iphone")
                                .foregroundStyle(Palette.accent)
                            Text("This iPhone")
                            Spacer()
                            if player.connectedDeviceAddr.isEmpty {
                                Image(systemName: "checkmark").foregroundStyle(Palette.accent)
                            }
                        }
                    }
                }

                Section("Nearby devices") {
                    if devices.isEmpty {
                        HStack {
                            Text(isScanning ? "Searching…" : "No devices found")
                                .foregroundStyle(.secondary)
                            Spacer()
                            if isScanning { ProgressView() }
                        }
                    }
                    ForEach(devices) { device in
                        Button {
                            Task {
                                connectingAddr = device.addr
                                let ok = await player.connect(to: device)
                                connectingAddr = nil
                                if ok { dismiss() }
                            }
                        } label: {
                            HStack {
                                Image(systemName: device.symbol).foregroundStyle(Palette.accent)
                                VStack(alignment: .leading) {
                                    Text(device.name)
                                    Text(device.typeLabel).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if connectingAddr == device.addr {
                                    ProgressView()
                                } else if player.connectedDeviceAddr == device.addr {
                                    Image(systemName: "checkmark").foregroundStyle(Palette.accent)
                                }
                            }
                        }
                        .foregroundStyle(.primary)
                    }
                }
            }
            .navigationTitle("Devices")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { dismiss() }
                }
            }
            .task { await scan() }
            .refreshable { await scan() }
        }
    }

    private func scan() async {
        isScanning = true
        devices = await Engine.discoverDevices(timeoutMs: 1200)
        isScanning = false
    }
}
