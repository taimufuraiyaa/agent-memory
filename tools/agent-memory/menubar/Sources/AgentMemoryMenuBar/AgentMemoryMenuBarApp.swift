import AppKit
import SwiftUI

@main
struct AgentMemoryMenuBarApp: App {
    @StateObject private var store = MenuStateStore()

    var body: some Scene {
        MenuBarExtra("agent-memory", systemImage: store.binaryAvailable ? "brain" : "brain.head.profile") {
            ContentView(store: store)
                .task {
                    store.refresh()
                }
        }
        .menuBarExtraStyle(.window)

        Settings {
            VStack(alignment: .leading, spacing: 12) {
                Text("agent-memory menubar")
                    .font(.headline)
                Text("Native control surface for dashboard, local service runtime, refresh, and upgrade flows.")
                    .foregroundStyle(.secondary)
            }
            .padding(20)
            .frame(width: 380)
        }
    }
}

private struct ContentView: View {
    @ObservedObject var store: MenuStateStore

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            header
            dashboardGroup
            memoryLifecycleGroup
            utilitiesGroup
        }
        .padding(16)
        .frame(width: 340)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: store.binaryAvailable ? "brain" : "brain.head.profile")
                .foregroundStyle(store.binaryAvailable ? .primary : .secondary)
                .font(.system(size: 16, weight: .semibold))
            
            Text("agent-memory")
                .font(.headline)
            
            Spacer()
            
            if store.isBusy {
                ProgressView().controlSize(.small)
            }
            
            StatusDot(tone: store.binaryAvailable ? .good : .bad)
            Text(store.binaryAvailable ? "CLI ready" : "CLI missing")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    private var dashboardGroup: some View {
        VStack(alignment: .leading, spacing: 6) {
            GroupHeader(title: "Dashboard")

            FunctionRow(
                statusHint: store.dashboardSummary,
                icon: "rectangle.3.group",
                statusTone: tone(for: store.dashboardState),
                title: "Dashboard",
                hasToggle: true,
                isOn: Binding(
                    get: { store.dashboardToggleValue },
                    set: { store.setDashboardEnabled($0) }
                ),
                hasOpenAction: true,
                openAction: store.openDashboard,
                isDisabled: !store.binaryAvailable || store.isBusy || store.dashboardState == .unavailable
            )
        }
    }

    private var memoryLifecycleGroup: some View {
        VStack(alignment: .leading, spacing: 6) {
            GroupHeader(title: "Memory Life Cycle")

            FunctionRow(
                statusHint: store.serviceStatusText,
                icon: "bolt.horizontal.circle",
                statusTone: tone(for: store.memoryServiceState),
                title: "Memory Service",
                hasToggle: true,
                isOn: Binding(
                    get: { store.serviceToggleValue },
                    set: { store.setServiceEnabled($0) }
                ),
                hasOpenAction: store.serviceURL != nil,
                openAction: store.openService,
                isDisabled: !store.binaryAvailable || store.isBusy || store.memoryServiceState == .unavailable
            )
        }
    }

    private var utilitiesGroup: some View {
        VStack(alignment: .leading, spacing: 6) {
            GroupHeader(title: "Utilities")

            FunctionRow(
                statusHint: "Current version \(store.versionText)",
                icon: "arrow.triangle.2.circlepath",
                statusTone: .neutral,
                title: "Upgrade",
                hasToggle: false,
                isOn: .constant(false),
                hasOpenAction: false,
                openAction: {},
                isDisabled: !store.binaryAvailable || store.isBusy,
                action: store.upgrade
            )

            FunctionRow(
                statusHint: "Close menubar controller",
                icon: "power",
                statusTone: .neutral,
                title: "Quit",
                hasToggle: false,
                isOn: .constant(false),
                hasOpenAction: false,
                openAction: {},
                isDisabled: false,
                action: { NSApplication.shared.terminate(nil) }
            )
        }
    }

    private func tone(for state: ServiceState) -> BadgeTone {
        switch state {
        case .running: return .good
        case .stopped: return .neutral
        case .unknown: return .warn
        case .unavailable: return .bad
        }
    }
}

private struct GroupHeader: View {
    let title: String
    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Divider().padding(.vertical, 4)
            Text(title)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
        }
    }
}

private struct FunctionRow: View {
    let statusHint: String
    let icon: String
    let statusTone: BadgeTone
    let title: String
    let hasToggle: Bool
    let isOn: Binding<Bool>
    let hasOpenAction: Bool
    let openAction: () -> Void
    let isDisabled: Bool
    var action: (() -> Void)? = nil

    @State private var isHovered = false
    @State private var isOpenHovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(statusHint)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .padding(.horizontal, 6)

            HStack(spacing: 10) {
                Image(systemName: icon)
                    .frame(width: 16)
                    .foregroundStyle(.secondary)

                StatusDot(tone: statusTone)

                Text(title)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(.primary)

                Spacer()

                if hasOpenAction {
                    Button(action: openAction) {
                        Image(systemName: "safari")
                            .foregroundStyle(isOpenHovered ? .primary : .secondary)
                            .frame(width: 24, height: 24)
                            .background(isOpenHovered ? Color.secondary.opacity(0.1) : Color.clear)
                            .cornerRadius(4)
                    }
                    .buttonStyle(.plain)
                    .onHover { hovering in
                        isOpenHovered = hovering
                    }
                    .padding(.trailing, 4)
                }

                if hasToggle {
                    Toggle("", isOn: isOn)
                        .labelsHidden()
                        .toggleStyle(.switch)
                        .controlSize(.small)
                }
            }
            .padding(.vertical, 6)
            .padding(.horizontal, 6)
            .background(isHovered && !hasToggle && !hasOpenAction ? Color.secondary.opacity(0.1) : Color.clear)
            .cornerRadius(6)
            .onHover { hovering in
                isHovered = hovering
            }
            .onTapGesture {
                if let action = action, !hasToggle, !hasOpenAction {
                    action()
                }
            }
        }
        .padding(.vertical, 2)
        .opacity(isDisabled ? 0.5 : 1.0)
        .disabled(isDisabled)
    }
}

private struct StatusDot: View {
    let tone: BadgeTone
    var body: some View {
        Circle()
            .fill(tone.foreground)
            .frame(width: 8, height: 8)
    }
}

private enum BadgeTone {
    case good, warn, bad, neutral
    var foreground: Color {
        switch self {
        case .good: return .green
        case .warn: return .orange
        case .bad: return .red
        case .neutral: return .secondary
        }
    }
}
