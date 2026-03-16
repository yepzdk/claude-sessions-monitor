import SwiftUI

struct SessionsPopover: View {
    @EnvironmentObject var sessionService: SessionService

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Text("Claude Sessions")
                    .font(.headline)
                Spacer()
                statusSummary
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)

            Divider()

            // Session list
            if sessionService.sessions.isEmpty {
                VStack(spacing: 8) {
                    Spacer()
                    Image(systemName: "terminal")
                        .font(.system(size: 28))
                        .foregroundStyle(.tertiary)
                    Text("No sessions found")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    Spacer()
                }
                .frame(maxWidth: .infinity)
            } else {
                ScrollView {
                    LazyVStack(spacing: 0) {
                        ForEach(sessionService.sessions) { session in
                            SessionRow(session: session)
                                .padding(.horizontal, 12)
                            Divider()
                                .padding(.leading, 12)
                        }
                    }
                }
            }

            Divider()

            // Footer
            HStack {
                Button(action: openWebDashboard) {
                    HStack(spacing: 4) {
                        Image(systemName: "globe")
                        Text("Web Dashboard")
                    }
                    .font(.caption)
                }
                .buttonStyle(.plain)
                .foregroundStyle(.blue)

                Spacer()

                Button(action: {
                    NSApplication.shared.terminate(nil)
                }) {
                    Text("Quit")
                        .font(.caption)
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
        }
        .frame(width: 350, height: 400)
    }

    private var statusSummary: some View {
        let counts = Dictionary(grouping: sessionService.sessions, by: \.status)
            .mapValues(\.count)

        return HStack(spacing: 8) {
            if let working = counts[.working], working > 0 {
                statusBadge(count: working, color: .green)
            }
            if let needsInput = counts[.needsInput], needsInput > 0 {
                statusBadge(count: needsInput, color: .yellow)
            }
            if let waiting = counts[.waiting], waiting > 0 {
                statusBadge(count: waiting, color: .blue)
            }
        }
    }

    private func statusBadge(count: Int, color: Color) -> some View {
        HStack(spacing: 3) {
            Circle()
                .fill(color)
                .frame(width: 6, height: 6)
            Text("\(count)")
                .font(.caption2)
                .fontWeight(.medium)
        }
    }

    private func openWebDashboard() {
        if let url = URL(string: "http://localhost:\(sessionService.port)") {
            NSWorkspace.shared.open(url)
        }
    }
}
