import SwiftUI

struct StatusIcon: View {
    let status: SessionStatus

    var body: some View {
        Image(systemName: "terminal.fill")
            .symbolRenderingMode(.palette)
            .foregroundStyle(iconColor)
    }

    private var iconColor: Color {
        switch status {
        case .working:
            return .green
        case .needsInput:
            return .yellow
        case .waiting:
            return .blue
        case .idle, .inactive:
            return .gray
        }
    }
}
