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
/// panes with. Every one of them is a no-op with nothing attached, so every one
/// is disabled there — individually, because `Commands` has no `.disabled` and
/// applying one to a `CommandMenu` does not compile. Same shape as
/// `SessionCommands` below.
struct PaneCommands: Commands {
    let app: AppState

    @MainActor private var detached: Bool { app.controlClient == nil }

    var body: some Commands {
        CommandMenu("Pane") {
            Button("Next Pane") { app.controlClient?.nextPane() }
                .keyboardShortcut("]", modifiers: .command)
                .disabled(detached)
            Button("Previous Pane") { app.controlClient?.previousPane() }
                .keyboardShortcut("[", modifiers: .command)
                .disabled(detached)
            Divider()
            Button("Split Right") { app.controlClient?.splitRight() }
                .keyboardShortcut("d", modifiers: .command)
                .disabled(detached)
            Button("Split Down") { app.controlClient?.splitDown() }
                .keyboardShortcut("d", modifiers: [.command, .shift])
                .disabled(detached)
            Button("Zoom Pane") { app.controlClient?.toggleZoom() }
                .keyboardShortcut(.return, modifiers: [.command, .shift])
                .disabled(detached)
            Divider()
            Button("Next Window") { app.controlClient?.nextWindow() }
                .keyboardShortcut("]", modifiers: [.command, .shift])
                .disabled(detached)
            Button("Previous Window") { app.controlClient?.previousWindow() }
                .keyboardShortcut("[", modifiers: [.command, .shift])
                .disabled(detached)
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
        // Over every session, not the visible slice: a search can select an
        // archived row and the menu has to keep acting on it.
        app.session(id: app.selectedSessionID)
    }

    var body: some Commands {
        // Replacing .newItem drops the dead "New Window" AppKit puts there for
        // a single-Window scene, so ⌘N means the only thing it can mean.
        CommandGroup(replacing: .newItem) {
            Button("New Session…") { app.sheet = .create }
                .keyboardShortcut("n")
        }
        // ⌘, is where every Mac app keeps this, and replacing the group is
        // what puts it in the app menu rather than in a menu of our own. A
        // sheet and not a `Settings` scene: `app.sheet` is how every other form
        // here is opened, and one path is fewer than two.
        CommandGroup(replacing: .appSettings) {
            Button("Settings…") { app.sheet = .settings }
                .keyboardShortcut(",", modifiers: .command)
        }
        // SwiftUI gives `NavigationSplitView` a toolbar button and no menu
        // item, and a shortcut needs a menu item — so ⌃⌘S, the system-wide
        // spelling of this, did nothing at all. Verified before and after.
        CommandGroup(after: .sidebar) {
            Button(app.sidebarVisible ? "Hide Sidebar" : "Show Sidebar") {
                app.sidebarVisible.toggle()
            }
            .keyboardShortcut("s", modifiers: [.control, .command])
        }
        CommandMenu("Session") {
            // ⌘F is the find shortcut everywhere, and `f` is the TUI's. The
            // search field lives in the sidebar; this puts the caret in it.
            // Hidden below macOS 15, where `.searchFocused` does not exist and
            // the item could only be a no-op — the field is still clickable.
            if #available(macOS 15, *) {
                Button("Find Session…") { app.focusSearch() }
                    .keyboardShortcut("f", modifiers: .command)
                Divider()
            }
            // ⌘R is Refresh and ⌘↩ is Attach, both already live here.
            // ⌘G was the last free one, and Review is the action most often
            // wanted while attached, where the SessionInfo button is off screen.
            Button("Review Changes") { if let s = selected { app.review(s) } }
                .keyboardShortcut("g", modifiers: .command)
                .disabled(selected.map { !app.canReview($0) } ?? true)
            Divider()
            Button("Edit Session…") { if let s = selected { app.sheet = .edit(s) } }
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
            // Disabled while the core sorts by last-opened: the move would
            // succeed and the next open would undo it, which reads as a bug.
            // The TUI disables shift+↑↓ for the same reason.
            Button("Move Up") { if let s = selected { app.move(s, by: -1) } }
                .keyboardShortcut(.upArrow, modifiers: [.command, .control])
                .disabled(selected == nil || !app.canReorder)
            Button("Move Down") { if let s = selected { app.move(s, by: 1) } }
                .keyboardShortcut(.downArrow, modifiers: [.command, .control])
                .disabled(selected == nil || !app.canReorder)
            Divider()
            Button("Kill tmux") { if let s = selected { app.killTmux(s) } }
                .keyboardShortcut("k", modifiers: [.command, .shift])
                .disabled(selected.map { !app.isAlive($0) } ?? true)
            Button("Delete…") { if let s = selected { app.askDelete(s) } }
                .keyboardShortcut(.delete, modifiers: .command)
                .disabled(selected == nil)
        }
    }
}
