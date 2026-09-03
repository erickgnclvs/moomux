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
        .commands {
            PaneCommands(app: app)
            SessionCommands(app: app)
        }

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
            Divider()
            Button("Next Window") { app.controlClient?.nextWindow() }
                .keyboardShortcut("]", modifiers: [.command, .shift])
            Button("Previous Window") { app.controlClient?.previousWindow() }
                .keyboardShortcut("[", modifiers: [.command, .shift])
        }
    }
}

/// The row-level writes, so every one of them has a keyboard shortcut and is
/// reachable without a right-click.
///
/// Each button is disabled on its own: `Commands` has no `.disabled`, and
/// applying one to a `CommandMenu` does not compile.
struct SessionCommands: Commands {
    let app: AppState

    /// The row the menu acts on. The context menu uses the row that was
    /// clicked; the menu bar has only the selection to go on.
    @MainActor
    private var selected: Session? {
        app.visibleSessions.first { $0.id == app.selectedSessionID }
    }

    var body: some Commands {
        CommandMenu("Session") {
            // ⌘R is Refresh and ⌘↩ is Attach, both already live here.
            Button("Rename…") { if let s = selected { app.sheet = .rename(s) } }
                .keyboardShortcut("r", modifiers: [.command, .shift])
                .disabled(selected == nil)
            Button("Tags…") { if let s = selected { app.sheet = .tags(s) } }
                .keyboardShortcut("t", modifiers: .command)
                .disabled(selected == nil)
            Button(selected?.archived == true ? "Unarchive" : "Archive") {
                if let s = selected { app.setArchived(s, !s.archived) }
            }
            .keyboardShortcut("e", modifiers: .command)
            .disabled(selected == nil)
            Divider()
            Button("Move Up") { if let s = selected { app.move(s, by: -1) } }
                .keyboardShortcut(.upArrow, modifiers: [.command, .control])
                .disabled(selected == nil)
            Button("Move Down") { if let s = selected { app.move(s, by: 1) } }
                .keyboardShortcut(.downArrow, modifiers: [.command, .control])
                .disabled(selected == nil)
            Divider()
            Button("Kill tmux") { if let s = selected { app.killTmux(s) } }
                .keyboardShortcut("k", modifiers: [.command, .shift])
                .disabled(selected.map { !app.isAlive($0) } ?? true)
            Button("Delete…") { app.pendingDelete = selected }
                .keyboardShortcut(.delete, modifiers: .command)
                .disabled(selected == nil)
        }
    }
}
