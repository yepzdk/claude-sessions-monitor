import AppKit
import Combine
import Foundation

private let isoDateFormatter: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return f
}()

private let isoDateFormatterFallback: ISO8601DateFormatter = {
    let f = ISO8601DateFormatter()
    f.formatOptions = [.withInternetDateTime]
    return f
}()

@MainActor
final class SessionService: ObservableObject {
    @Published var sessions: [Session] = []
    @Published var aggregateStatus: SessionStatus = .inactive

    private var timer: Timer?
    private var csmProcess: Process?
    private var ownedProcess = false

    let port: Int
    private let pollInterval: TimeInterval
    private var terminationObserver: NSObjectProtocol?

    init(port: Int = 9847, pollInterval: TimeInterval = 3.0) {
        self.port = port
        self.pollInterval = pollInterval
        Task { @MainActor in
            self.startService()
            self.terminationObserver = NotificationCenter.default.addObserver(
                forName: NSApplication.willTerminateNotification,
                object: nil,
                queue: .main
            ) { [weak self] _ in
                MainActor.assumeIsolated {
                    self?.stop()
                }
            }
        }
    }

    private func startService() {
        Task {
            await ensureServer()
            startPolling()
        }
    }

    func start() {
        // Called externally if needed; no-op if already polling
        guard timer == nil else { return }
        startService()
    }

    func stop() {
        timer?.invalidate()
        timer = nil
        terminateOwnedProcess()
    }

    // MARK: - Server Management

    private func ensureServer() async {
        if await isServerRunning() {
            ownedProcess = false
            return
        }
        startCSMProcess()
        // Wait for the server to become ready
        for _ in 0..<3 {
            try? await Task.sleep(nanoseconds: 500_000_000)
            if await isServerRunning() { return }
        }
    }

    private func isServerRunning() async -> Bool {
        let url = URL(string: "http://localhost:\(port)/api/sessions")!
        var request = URLRequest(url: url)
        request.timeoutInterval = 2
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                return true
            }
        } catch {}
        return false
    }

    private func startCSMProcess() {
        let process = Process()

        // Look for csm bundled next to this executable first
        var executablePath: String?
        let selfPath = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath().path
        let selfDir = (selfPath as NSString).deletingLastPathComponent
        let bundledPath = (selfDir as NSString).appendingPathComponent("csm")
        if FileManager.default.isExecutableFile(atPath: bundledPath) {
            executablePath = bundledPath
        }

        // Fall back to common system locations
        if executablePath == nil {
            let candidates = [
                "/opt/homebrew/bin/csm",
                "/usr/local/bin/csm",
                "\(NSHomeDirectory())/.local/bin/csm",
                "\(NSHomeDirectory())/go/bin/csm",
            ]
            for candidate in candidates {
                if FileManager.default.isExecutableFile(atPath: candidate) {
                    executablePath = candidate
                    break
                }
            }
        }

        // Fall back to PATH lookup via /usr/bin/env
        if let path = executablePath {
            process.executableURL = URL(fileURLWithPath: path)
            process.arguments = ["--web-only", "--port", String(port)]
        } else {
            process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
            process.arguments = ["csm", "--web-only", "--port", String(port)]
        }

        // Silence stdout/stderr from the child process
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice

        do {
            try process.run()
            csmProcess = process
            ownedProcess = true
        } catch {
            // csm not found or failed to start - will poll anyway
            // in case user starts it manually
        }
    }

    private func terminateOwnedProcess() {
        guard ownedProcess, let process = csmProcess, process.isRunning else {
            return
        }
        process.terminate()
        csmProcess = nil
        ownedProcess = false
    }

    // MARK: - Polling

    private func startPolling() {
        // Fire immediately, then repeat
        fetchSessions()
        timer = Timer.scheduledTimer(withTimeInterval: pollInterval, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.fetchSessions()
            }
        }
    }

    private func fetchSessions() {
        Task {
            let url = URL(string: "http://localhost:\(port)/api/sessions")!
            do {
                let (data, _) = try await URLSession.shared.data(from: url)

                let decoder = JSONDecoder()
                decoder.dateDecodingStrategy = .custom { decoder in
                    let container = try decoder.singleValueContainer()
                    let dateString = try container.decode(String.self)
                    if let date = isoDateFormatter.date(from: dateString) {
                        return date
                    }
                    if let date = isoDateFormatterFallback.date(from: dateString) {
                        return date
                    }
                    throw DecodingError.dataCorruptedError(
                        in: container,
                        debugDescription: "Cannot decode date: \(dateString)"
                    )
                }

                if let sessions = try? decoder.decode([Session].self, from: data) {
                    self.sessions = sessions
                    self.aggregateStatus = Self.computeAggregateStatus(sessions)
                }
            } catch {
                // Server not reachable; will retry on next poll
            }
        }
    }

    // MARK: - Aggregate Status

    static func computeAggregateStatus(_ sessions: [Session]) -> SessionStatus {
        var hasWorking = false
        var hasNeedsInput = false
        var hasWaiting = false

        for session in sessions {
            switch session.status {
            case .working:
                hasWorking = true
            case .needsInput:
                hasNeedsInput = true
            case .waiting:
                hasWaiting = true
            case .idle, .inactive:
                break
            }
        }

        if hasWorking { return .working }
        if hasNeedsInput { return .needsInput }
        if hasWaiting { return .waiting }
        return .inactive
    }
}
