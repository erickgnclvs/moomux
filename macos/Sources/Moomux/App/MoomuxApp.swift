import AppKit
import SwiftUI

@main
struct MoomuxApp: App {
    @State private var app = MoomuxApp.bootstrap()

    /// A property initialiser is the earliest hook a SwiftUI `App` gives us,
    /// and `--selftest` has to exit before any window comes up.
    @MainActor
    private static func bootstrap() -> AppState {
        SelfTest.runIfRequested() // exits the process when --selftest is passed
        let socket = socketPathArgument() ?? MoomuxClient.defaultSocketPath
        return AppState(client: MoomuxClient(socketPath: socket))
    }

    /// `--socket <path>`, matching `moomux serve -socket` / `moomux ui -socket`.
    private static func socketPathArgument() -> String? {
        let args = CommandLine.arguments
        guard let flag = args.firstIndex(of: "--socket"), args.indices.contains(flag + 1) else {
            return nil
        }
        return args[flag + 1]
    }

    var body: some Scene {
        Window("moomux", id: "main") {
            RootView().environment(app)
        }
        .defaultSize(width: 900, height: 620)

        MenuBarExtra {
            MenuBarContent().environment(app)
        } label: {
            // The count is the payload: it is what makes this worth having
            // over the TUI, which cannot be seen from another app.
            Image(systemName: "cup.and.saucer")
            if app.needsInputCount > 0 { Text("\(app.needsInputCount)") }
        }
        .menuBarExtraStyle(.window)
    }
}
