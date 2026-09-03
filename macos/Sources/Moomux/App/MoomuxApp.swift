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
        .commands { PaneCommands(app: app) }

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

/// Pane navigation as real menu commands.
///
/// Not a nicety: in control mode tmux's prefix key **cannot** reach tmux.
/// Keystrokes go to a pane through `send-keys`, so tmux never sees `C-b` and
/// every `prefix`-based binding is dead — `C-b o` types a literal `o`. These
/// commands are the replacement, and they are also the only reason a menu
/// exists. iTerm2's tmux integration made the same trade.
///
/// Shortcuts follow iTerm2's, since that is the other app people drive tmux
/// panes with. They do nothing outside control mode.
struct PaneCommands: Commands {
    let app: AppState

    var body: some Commands {
        CommandMenu("Pane") {
            Button("Next Pane") { app.controlClient?.nextPane() }
                .keyboardShortcut("]", modifiers: .command)
            Button("Previous Pane") { app.controlClient?.previousPane() }
                .keyboardShortcut("[", modifiers: .command)
            Divider()
            Button("Split Right") { app.controlClient?.splitRight() }
                .keyboardShortcut("d", modifiers: .command)
            Button("Split Down") { app.controlClient?.splitDown() }
                .keyboardShortcut("d", modifiers: [.command, .shift])
            Button("Zoom Pane") { app.controlClient?.toggleZoom() }
                .keyboardShortcut(.return, modifiers: [.command, .shift])
        }
    }
}
