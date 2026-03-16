import SwiftUI

@main
struct CSMMenuBarApp: App {
    @StateObject private var sessionService = SessionService()

    var body: some Scene {
        MenuBarExtra {
            SessionsPopover()
                .environmentObject(sessionService)
                .onAppear { sessionService.start() }
        } label: {
            StatusIcon(status: sessionService.aggregateStatus)
        }
        .menuBarExtraStyle(.window)
    }
}
