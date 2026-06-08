import Foundation

struct CommandResult {
    let stdout: String
    let stderr: String
    let exitCode: Int32
    var succeeded: Bool { exitCode == 0 }
}

struct ServiceEnvelope: Decodable { let data: ServiceStatus }
struct ServiceStatus: Decodable {
    let running: Bool
    let healthy: Bool
    let pid: Int?
    let workspace: String?
    let addr: String?
    let url: String?
}

func run(_ args: [String]) async -> CommandResult {
    await withCheckedContinuation { continuation in
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/Users/time/timebooks/agent-memory/tools/agent-memory/menubar/dist/AgentMemoryMenuBar.app/Contents/Resources/bin/agent-memory")
        process.arguments = args

        let stdoutPipe = Pipe()
        let stderrPipe = Pipe()
        process.standardOutput = stdoutPipe
        process.standardError = stderrPipe

        do { try process.run() } catch {
            continuation.resume(returning: CommandResult(stdout: "", stderr: error.localizedDescription, exitCode: 127))
            return
        }

        process.terminationHandler = { proc in
            let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
            let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()
            let stdout = String(data: stdoutData, encoding: .utf8) ?? ""
            let stderr = String(data: stderrData, encoding: .utf8) ?? ""
            continuation.resume(returning: CommandResult(stdout: stdout, stderr: stderr, exitCode: proc.terminationStatus))
        }
    }
}

Task {
    let res = await run(["dashboard", "--status", "--format", "json"])
    print("STDOUT: \(res.stdout)")
    print("STDERR: \(res.stderr)")
    do {
        let data = Data(res.stdout.utf8)
        let env = try JSONDecoder().decode(ServiceEnvelope.self, from: data)
        print("DECODED: \(env.data)")
    } catch {
        print("DECODE ERROR: \(error)")
    }
    exit(0)
}
RunLoop.main.run()