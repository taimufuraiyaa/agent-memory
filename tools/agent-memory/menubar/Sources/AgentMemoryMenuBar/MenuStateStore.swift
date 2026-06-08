import AppKit
import Foundation

enum ServiceState: String {
    case running = "Running"
    case stopped = "Stopped"
    case unavailable = "Unavailable"
    case unknown = "Unknown"
}

@MainActor
final class MenuStateStore: ObservableObject {
    @Published var binaryAvailable = false
    @Published var versionText = "Unknown"
    @Published var dashboardState: ServiceState = .unknown
    @Published var memoryServiceState: ServiceState = .unavailable
    @Published var lastAction = "Idle"
    @Published var dashboardURL: URL?
    @Published var serviceURL: URL?
    @Published var isBusy = false

    private let cli = AgentMemoryCLI()

    var dashboardToggleValue: Bool {
        dashboardState == .running
    }

    var serviceToggleValue: Bool {
        memoryServiceState == .running
    }

    var dashboardSummary: String {
        switch dashboardState {
        case .running:
            return dashboardURL?.absoluteString ?? "Running"
        case .stopped:
            return "Stopped"
        case .unavailable:
            return "Unavailable"
        case .unknown:
            return "State unknown"
        }
    }

    var dashboardStatusText: String {
        dashboardState.rawValue
    }

    var serviceSummary: String {
        switch memoryServiceState {
        case .running:
            return serviceURL?.absoluteString ?? "Running"
        case .stopped:
            return "Stopped"
        case .unavailable:
            return "Service unavailable"
        case .unknown:
            return "Health unconfirmed"
        }
    }

    var serviceStatusText: String {
        memoryServiceState.rawValue
    }

    func refresh() {
        Task {
            await reloadState()
        }
    }

    func startDashboard() {
        Task {
            await self.runAction("Starting dashboard") {
                let urlString = try await self.cli.startDashboard()
                if let urlString, let url = URL(string: urlString) {
                    self.dashboardURL = url
                }
                self.dashboardState = .running
                self.lastAction = self.dashboardURL?.absoluteString ?? "Dashboard started"
            }
        }
    }

    func stopDashboard() {
        Task {
            await self.runAction("Stopping dashboard") {
                try await self.cli.stopDashboard()
                self.dashboardURL = nil
                self.dashboardState = .stopped
                self.lastAction = "Dashboard stopped"
            }
        }
    }

    func setDashboardEnabled(_ enabled: Bool) {
        guard !isBusy else {
            return
        }
        if enabled {
            startDashboard()
        } else {
            stopDashboard()
        }
    }

    func openDashboard() {
        if let dashboardURL {
            NSWorkspace.shared.open(dashboardURL)
            lastAction = "Opened dashboard"
            return
        }
        startDashboard()
    }

    func openService() {
        guard let serviceURL else {
            lastAction = "Service URL not available"
            return
        }
        NSWorkspace.shared.open(serviceURL)
        lastAction = "Opened service URL"
    }

    func upgrade() {
        Task {
            await self.runAction("Upgrading agent-memory") {
                let output = try await self.cli.upgrade()
                self.lastAction = self.compact(output)
                await self.reloadState()
            }
        }
    }

    func startService() {
        Task {
            await self.runAction("Starting memory service") {
                let status = try await self.cli.startService()
                self.applyServiceStatus(status)
                self.lastAction = status.url ?? "Memory service started"
            }
        }
    }

    func stopService() {
        Task {
            await self.runAction("Stopping memory service") {
                let status = try await self.cli.stopService()
                self.applyServiceStatus(status)
                self.lastAction = "Memory service stopped"
            }
        }
    }

    func setServiceEnabled(_ enabled: Bool) {
        guard !isBusy || enabled == serviceToggleValue else {
            return
        }
        if enabled {
            startService()
        } else {
            stopService()
        }
    }

    private func reloadState() async {
        binaryAvailable = await cli.resolveBinary()
        guard binaryAvailable else {
            versionText = "Missing"
            dashboardState = .unknown
            memoryServiceState = .unavailable
            dashboardURL = nil
            serviceURL = nil
            lastAction = "agent-memory not found on PATH"
            return
        }

        do {
            let version = try await cli.fetchVersion()
            versionText = version.version
        } catch {
            versionText = "Unknown"
            lastAction = compact(error.localizedDescription)
        }

        guard await cli.detectServeAvailability() else {
            memoryServiceState = .unavailable
            serviceURL = nil
            return
        }
        do {
            let status = try await cli.fetchServiceStatus()
            applyServiceStatus(status)
        } catch {
            memoryServiceState = .unknown
            lastAction = compact(error.localizedDescription)
        }

        do {
            let dStatus = try await cli.fetchDashboardStatus()
            applyDashboardStatus(dStatus)
        } catch {
            dashboardState = .unknown
        }
    }

    private func runAction(_ pending: String, operation: @escaping @MainActor () async throws -> Void) async {
        if isBusy {
            return
        }
        isBusy = true
        lastAction = pending
        defer { isBusy = false }

        do {
            try await operation()
        } catch {
            lastAction = compact(error.localizedDescription)
        }
    }

    private func compact(_ text: String) -> String {
        let line = text
            .replacingOccurrences(of: "\r\n", with: "\n")
            .split(separator: "\n")
            .map(String.init)
            .first ?? text
        return String(line.prefix(120))
    }

    private func applyServiceStatus(_ status: ServiceStatus) {
        if let url = status.url.flatMap(URL.init(string:)) {
            serviceURL = url
        } else {
            serviceURL = nil
        }
        if status.running {
            memoryServiceState = status.healthy ? .running : .unknown
        } else {
            memoryServiceState = .stopped
        }
    }

    private func applyDashboardStatus(_ status: ServiceStatus) {
        if let url = status.url.flatMap(URL.init(string:)) {
            dashboardURL = url
        } else {
            dashboardURL = nil
        }
        if status.running {
            dashboardState = status.healthy ? .running : .unknown
        } else {
            dashboardState = .stopped
        }
    }
}
