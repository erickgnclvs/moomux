import SwiftTerm
import SwiftUI

/// A live tmux client, hosted in the app.
///
/// This is substrate A from `docs/native-macos-rewrite.md`: tmux still owns the
/// process, so sessions survive quitting the app, and the very same session is
/// still `tmux attach`-able from a phone. The app is a viewport, not an owner.
///
/// ponytail: a plain `tmux attach`, not control mode (`tmux -CC`). tmux draws
/// its own splits and status line inside this one view and the app cannot see
/// the layout — so no native tabs, no native splits, no per-pane titles. That
/// buys nothing until there is UI to put a layout into.
///
/// The ceiling that does bite: **every client on a session shares one window
/// size**, so while this pane is attached, the user's iTerm window and phone
/// are letterboxed down to whatever this view happens to be. Measured, not
/// assumed — grouped sessions (`new-session -t`) do not fix it, because a group
/// shares the windows themselves, and the size does not spring back when the
/// larger client is used again. It only recovers on detach. That is why
/// attaching is an explicit action rather than a consequence of selecting a row.
///
/// Control mode does **not** fix this either, contrary to what the plan doc
/// assumed: a `-CC` client sets its size with `refresh-client -C` and the
/// window follows it exactly the same way. Measured both ways — see
/// `TmuxControlClient`.
///
/// The terminal widget is deliberately reached only through this file, so
/// swapping SwiftTerm for libghostty later is one file, as the plan assumes.
struct TerminalPane: NSViewRepresentable {

    let executable: String
    /// The tmux session name to attach to, e.g. `moomux-macos-1a2b`.
    let tmuxSession: String
    /// Called when the tmux client exits — detached, or the session went away.
    var onExit: (Int32?) -> Void = { _ in }

    func makeCoordinator() -> Coordinator { Coordinator(onExit: onExit) }

    /// A terminal nobody can type into is not a terminal, and SwiftUI leaves
    /// first responder on the sidebar list when this pane appears — the
    /// accessibility API reported `AXOutline` as focused, and keystrokes went
    /// to the list. Taking focus from `updateNSView` does not work: the view
    /// has no window yet the one time that runs, so this hooks the moment it
    /// gets one. With it, characters, control keys and the tmux prefix all
    /// reach the client (checked against `capture-pane` and `client_prefix`).
    final class AttachedTerminalView: LocalProcessTerminalView {
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard window != nil else { return }
            // After SwiftUI finishes installing the pane, not during.
            DispatchQueue.main.async { [weak self] in
                guard let self, let window = self.window else { return }
                window.makeFirstResponder(self)
            }
        }
    }

    func makeNSView(context: Context) -> LocalProcessTerminalView {
        let view = AttachedTerminalView(frame: .init(x: 0, y: 0, width: 640, height: 400))
        view.processDelegate = context.coordinator
        // `-u` forces UTF-8: the client's environment here is SwiftTerm's
        // minimal one, so tmux cannot infer it from LANG the way a login shell
        // would. Without it, box drawing and any non-ASCII output corrupt.
        view.startProcess(executable: executable, args: ["-u", "attach", "-t", tmuxSession])
        return view
    }

    func updateNSView(_ view: LocalProcessTerminalView, context: Context) {
        context.coordinator.onExit = onExit
    }

    /// Terminating kills the *client*, which is what detaching is. The tmux
    /// session, its panes and whatever the agent is doing all survive — that is
    /// the entire reason this app attaches instead of owning a PTY itself.
    static func dismantleNSView(_ view: LocalProcessTerminalView, coordinator: Coordinator) {
        coordinator.onExit = { _ in } // the view is going away; nothing to tell
        view.terminate()
    }

    final class Coordinator: NSObject, LocalProcessTerminalViewDelegate {
        var onExit: (Int32?) -> Void

        init(onExit: @escaping (Int32?) -> Void) { self.onExit = onExit }

        func processTerminated(source: TerminalView, exitCode: Int32?) {
            onExit(exitCode)
        }

        // tmux redraws itself on SIGWINCH, so there is nothing to do for a
        // resize, and the window title is the app's own.
        func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}
        func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}
        func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    }
}

/// The terminal for one session. Only reached once the user has deliberately
/// attached, so the "nothing to attach to" cases live on the button instead.
struct SessionTerminal: View {
    let session: Session
    /// The tmux client went away — the user pressed the prefix key and `d`, or
    /// the session ended under them.
    var onDetach: () -> Void

    var body: some View {
        if let tmux = ToolPath.find("tmux") {
            TerminalPane(executable: tmux, tmuxSession: session.tmuxSession) { _ in
                // SwiftTerm reports termination off the main thread.
                Task { @MainActor in onDetach() }
            }
            // Rebuild — so detach and re-attach — if the session changes under
            // us. Without this SwiftUI reuses the view and leaves you looking at
            // the previous session's terminal.
            .id(session.id)
        } else {
            ContentUnavailableView {
                Label("Can't find tmux", systemImage: "terminal")
            } description: {
                Text("""
                    An app launched from Finder doesn't inherit your shell's PATH, and tmux \
                    isn't in any of the usual places either.
                    """)
            }
        }
    }
}
