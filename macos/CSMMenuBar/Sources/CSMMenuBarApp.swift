import SwiftUI

@main
struct CSMMenuBarApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate
    @StateObject private var sessionService = SessionService()

    var body: some Scene {
        MenuBarExtra {
            SessionsPopover()
                .environmentObject(sessionService)
        } label: {
            StatusIcon(status: sessionService.aggregateStatus)
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationWillTerminate(_ notification: Notification) {
        // Process cleanup happens via SessionService.stop()
    }
}
