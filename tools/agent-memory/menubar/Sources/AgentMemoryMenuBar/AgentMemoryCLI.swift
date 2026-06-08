import Foundation

struct CommandResult {
    let stdout: String
    let stderr: String
    let exitCode: Int32

    var succeeded: Bool {
        exitCode == 0
    }
}

struct VersionEnvelope: Decodable {
    let data: VersionInfo
}

struct ServiceEnvelope: Decodable {
    let data: ServiceStatus
}

struct VersionInfo: Decodable {
    let version: String
    let path: String?
}

struct ServiceStatus: Decodable {
    let running: Bool
    let healthy: Bool
    let pid: Int?
    let workspace: String?
    let addr: String?
    let url: String?
}

enum AgentMemoryCLIError: LocalizedError {
    case binaryNotFound
    case commandFailed(String)

    var errorDescription: String? {
        switch self {
        case .binaryNotFound:
            return "agent-memory not found on PATH"
        case .commandFailed(let message):
            return message
        }
    }
}

final class AgentMemoryCLI {
    private lazy var binaryPath: String? = resolveBinaryPath()

    func resolveBinary() async -> Bool {
        let result = await run(["--help"])
        return !isBinaryMissing(result)
    }

    func fetchVersion() async throws -> VersionInfo {
        let result = try await checked(["version", "--format", "json"])
        let data = Data(result.stdout.utf8)
        return try JSONDecoder().decode(VersionEnvelope.self, from: data).data
    }

    func detectServeAvailability() async -> Bool {
        do {
            _ = try await fetchServiceStatus()
            return true
        } catch AgentMemoryCLIError.commandFailed(let message) {
            return !message.contains("unknown command")
        } catch {
            return false
        }
    }

    func startDashboard() async throws -> String? {
        let result = try await checked(["dashboard", "--start", "--no-open"])
        return firstURL(in: result.stdout) ?? firstURL(in: result.stderr)
    }

    func stopDashboard() async throws {
        do {
            _ = try await checked(["dashboard", "--stop"])
        } catch AgentMemoryCLIError.commandFailed(let message) where isMissingPID(message) {
            return
        }
    }

    func fetchDashboardStatus() async throws -> ServiceStatus {
        let result = try await checked(["dashboard", "--status", "--format", "json"])
        let data = Data(result.stdout.utf8)
        return try JSONDecoder().decode(ServiceEnvelope.self, from: data).data
    }

    func upgrade() async throws -> String {
        let result = try await checked(["upgrade", "--yes"])
        return readableOutput(from: result)
    }

    func fetchServiceStatus() async throws -> ServiceStatus {
        let result = try await checked(["serve", "--status", "--format", "json"])
        let data = Data(result.stdout.utf8)
        return try JSONDecoder().decode(ServiceEnvelope.self, from: data).data
    }

    func startService() async throws -> ServiceStatus {
        let result = try await checked(["serve", "--start", "--no-open", "--format", "json"])
        let data = Data(result.stdout.utf8)
        return try JSONDecoder().decode(ServiceEnvelope.self, from: data).data
    }

    func stopService() async throws -> ServiceStatus {
        do {
            let result = try await checked(["serve", "--stop", "--format", "json"])
            let data = Data(result.stdout.utf8)
            return try JSONDecoder().decode(ServiceEnvelope.self, from: data).data
        } catch AgentMemoryCLIError.commandFailed(let message) where isMissingPID(message) {
            return ServiceStatus(running: false, healthy: false, pid: nil, workspace: nil, addr: nil, url: nil)
        }
    }

    private func run(_ args: [String]) async -> CommandResult {
        await withCheckedContinuation { continuation in
            let process = Process()
            if let binaryPath {
                process.executableURL = URL(fileURLWithPath: binaryPath)
                process.arguments = args
            } else {
                process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
                process.arguments = ["agent-memory"] + args
            }

            let stdoutPipe = Pipe()
            let stderrPipe = Pipe()
            process.standardOutput = stdoutPipe
            process.standardError = stderrPipe

            do {
                try process.run()
            } catch {
                continuation.resume(returning: CommandResult(stdout: "", stderr: error.localizedDescription, exitCode: 127))
                return
            }

            process.terminationHandler = { proc in
                let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
                let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()
                let stdout = String(data: stdoutData, encoding: .utf8) ?? ""
                let stderr = String(data: stderrData, encoding: .utf8) ?? ""
                let result = CommandResult(stdout: stdout, stderr: stderr, exitCode: proc.terminationStatus)
                continuation.resume(returning: result)
            }
        }
    }

    func checked(_ args: [String]) async throws -> CommandResult {
        let result = await run(args)
        if isBinaryMissing(result) {
            throw AgentMemoryCLIError.binaryNotFound
        }
        if !result.succeeded {
            throw AgentMemoryCLIError.commandFailed(readableOutput(from: result))
        }
        return result
    }

    private func isBinaryMissing(_ result: CommandResult) -> Bool {
        if result.succeeded {
            return false
        }
        let combined = (result.stdout + "\n" + result.stderr).lowercased()
        return result.exitCode == 127 &&
            (combined.contains("agent-memory") && (combined.contains("not found") || combined.contains("no such file")))
    }

    private func isMissingPID(_ message: String) -> Bool {
        let m = message.lowercased()
        return m.contains("no such file or directory") || m.contains("cannot find the file")
    }

    private func resolveBinaryPath() -> String? {
        let env = ProcessInfo.processInfo.environment
        if let explicit = env["AGENT_MEMORY_BINARY"], !explicit.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return explicit
        }

        if let resourceURL = Bundle.main.resourceURL {
            let bundled = resourceURL.appendingPathComponent("bin/agent-memory").path
            if FileManager.default.isExecutableFile(atPath: bundled) {
                return bundled
            }
        }

        return nil
    }

    private func readableOutput(from result: CommandResult) -> String {
        let out = result.stdout.trimmingCharacters(in: .whitespacesAndNewlines)
        let err = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
        if !out.isEmpty {
            return out
        }
        if !err.isEmpty {
            return err
        }
        return "Command exited with code \(result.exitCode)"
    }

    private func firstURL(in text: String) -> String? {
        let detector = try? NSDataDetector(types: NSTextCheckingResult.CheckingType.link.rawValue)
        let range = NSRange(location: 0, length: text.utf16.count)
        let match = detector?.firstMatch(in: text, options: [], range: range)
        return match?.url?.absoluteString
    }
}
