import Foundation

enum SessionStatus: String, Codable, CaseIterable {
    case working = "Working"
    case needsInput = "Needs Input"
    case waiting = "Waiting"
    case idle = "Idle"
    case inactive = "Inactive"
}

struct Session: Codable, Identifiable {
    var id: String { logFile }

    let project: String
    let status: SessionStatus
    let lastActivity: Date
    let task: String
    let summary: String?
    let lastMessage: String?
    let logFile: String
    let isDesktop: Bool?
    let isGhost: Bool?
    let ghostPid: Int?
    let gitBranch: String?
    let hasUnsandboxed: Bool?
    let contextPercent: Double?
    let contextTokens: Int?

    enum CodingKeys: String, CodingKey {
        case project
        case status
        case lastActivity = "last_activity"
        case task
        case summary
        case lastMessage = "last_message"
        case logFile = "log_file"
        case isDesktop = "is_desktop"
        case isGhost = "is_ghost"
        case ghostPid = "ghost_pid"
        case gitBranch = "git_branch"
        case hasUnsandboxed = "has_unsandboxed"
        case contextPercent = "context_percent"
        case contextTokens = "context_tokens"
    }
}

extension Session {
    var relativeActivity: String {
        let interval = Date().timeIntervalSince(lastActivity)
        if interval < 1 {
            return "just now"
        } else if interval < 60 {
            return "\(Int(interval))s ago"
        } else if interval < 3600 {
            return "\(Int(interval / 60))m ago"
        } else if interval < 86400 {
            return "\(Int(interval / 3600))h ago"
        } else {
            return "\(Int(interval / 86400))d ago"
        }
    }

    var isActive: Bool {
        status == .working || status == .needsInput || status == .waiting
    }
}
