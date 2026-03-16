import SwiftUI

struct SessionRow: View {
    let session: Session

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 8, height: 8)

                Text(session.project)
                    .font(.system(.body, design: .monospaced))
                    .fontWeight(.medium)
                    .lineLimit(1)

                if let branch = session.gitBranch, !branch.isEmpty {
                    Text("@\(branch)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 4)
                        .padding(.vertical, 1)
                        .background(.quaternary)
                        .cornerRadius(3)
                        .lineLimit(1)
                }

                Spacer()

                Text(session.relativeActivity)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }

            if let contextPercent = session.contextPercent, contextPercent > 0 {
                HStack(spacing: 6) {
                    ProgressView(value: min(contextPercent / 100.0, 1.0))
                        .tint(contextColor(contextPercent))
                        .frame(height: 4)

                    Text("\(Int(contextPercent))%")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .frame(width: 30, alignment: .trailing)
                }
            }

            if let message = session.lastMessage, !message.isEmpty {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
        }
        .padding(.vertical, 4)
    }

    private var statusColor: Color {
        switch session.status {
        case .working:
            return .green
        case .needsInput:
            return .yellow
        case .waiting:
            return .blue
        case .idle:
            return .gray
        case .inactive:
            return .gray.opacity(0.5)
        }
    }

    private func contextColor(_ percent: Double) -> Color {
        if percent > 90 { return .red }
        if percent > 75 { return .yellow }
        return .green
    }
}
