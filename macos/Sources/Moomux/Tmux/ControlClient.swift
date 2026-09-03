import Foundation
import SwiftTerm

/// A tmux control-mode client: `tmux -CC attach`.
///
/// Instead of drawing a screen, tmux emits a line protocol — pane output,
/// layout changes, window events — and takes commands back on stdin. That lets
/// the app render each tmux pane into its own native view and know what the
/// layout actually is, which a plain attach cannot: there, tmux draws its own
/// splits inside one opaque rectangle.
///
/// Control mode still needs a tty (`tcgetattr failed: Inappropriate ioctl for
/// device` over pipes), so this runs tmux under a pseudo-terminal.
/// `SwiftTerm.LocalProcess` is exactly that and nothing more — a forkpty, bytes
/// in, bytes out — with no terminal emulation attached.
@MainActor
public final class TmuxControlClient {

    public enum Status: Equatable {
        case connecting
        case attached
        /// The client is gone: detached, session killed, or tmux failed.
        case exited(String?)
    }

    /// Lines of tmux history pulled into a pane's scroll buffer on its first
    /// paint. Pane views must be built with `TerminalOptions(scrollback:)` set
    /// to this — anything past that cap is fed and silently discarded by
    /// SwiftTerm's circular buffer, with no error anywhere.
    public static let historyLines = 1000

    public private(set) var status: Status = .connecting {
        didSet { if status != oldValue { onStatusChange(status) } }
    }
    /// The active window's panes, in cells. nil until the first layout arrives.
    public private(set) var layout: TmuxWindowLayout?
    public private(set) var activePane: String?

    public var onStatusChange: (Status) -> Void = { _ in }
    public var onLayoutChange: () -> Void = {}

    private let tmuxPath: String
    private let sessionName: String

    private var process: LocalProcess?
    private var parser = TmuxLineParser()
    private var lineBuffer: [UInt8] = []
    /// Completions for commands we have sent, in order. tmux answers commands
    /// in the order they arrive, so position is identity.
    private var pending: [([String], Bool) -> Void] = []
    private var views: [String: TerminalView] = [:]
    /// Panes whose scrollback has already been streamed in. A repaint after a
    /// resize is visible-only: re-sending a thousand lines per pane per frame
    /// of a window drag is the cost, and tmux's rewrapped history would have to
    /// be diffed against ours anyway.
    private var historyPainted: Set<String> = []
    private var windowID: String?
    private var size: (cols: Int, rows: Int)
    /// (0, 0) until the first `refresh-client -C` goes out.
    private var sentSize = (cols: 0, rows: 0)
    private var ready = false

    /// - Parameter size: the size to attach at, sent as soon as the client is
    ///   listening. A control-mode client does **not** take its size from the
    ///   pty the way an ordinary one does — `list-clients` reports it as `80x`
    ///   until told otherwise — so `refresh-client -C` is the only lever, and
    ///   until it lands the panes are laid out against the wrong grid and every
    ///   long line rewraps.
    public init(tmuxPath: String, session: String, size: (cols: Int, rows: Int)) {
        self.tmuxPath = tmuxPath
        self.sessionName = session
        self.size = size
    }

    // MARK: Lifecycle

    public func start() {
        let bridge = Bridge(client: self, size: size)
        self.bridge = bridge
        let process = LocalProcess(delegate: bridge, dispatchQueue: .main)
        self.process = process
        process.startProcess(
            executable: tmuxPath,
            args: ["-u", "-CC", "attach", "-t", sessionName],
            environment: Terminal.getEnvironmentVariables(termName: "xterm-256color"))
    }

    public func stop() {
        process?.terminate()
        process = nil
    }

    private var bridge: Bridge?

    /// `LocalProcessDelegate` is not `@MainActor`, and making the whole client
    /// nonisolated to satisfy it would spread across every caller. The queue is
    /// `.main`, so this hops back with no thread change.
    private final class Bridge: LocalProcessDelegate {
        weak var client: TmuxControlClient?
        let size: (cols: Int, rows: Int)

        init(client: TmuxControlClient, size: (cols: Int, rows: Int)) {
            self.client = client
            self.size = size
        }

        func dataReceived(slice: ArraySlice<UInt8>) {
            MainActor.assumeIsolated { client?.received(slice) }
        }

        func processTerminated(_ source: LocalProcess, exitCode: Int32?) {
            MainActor.assumeIsolated { client?.status = .exited(nil) }
        }

        func getWindowSize() -> winsize {
            winsize(ws_row: UInt16(size.rows), ws_col: UInt16(size.cols),
                    ws_xpixel: 0, ws_ypixel: 0)
        }
    }

    // MARK: Reading

    private func received(_ slice: ArraySlice<UInt8>) {
        lineBuffer.append(contentsOf: slice)
        while let newline = lineBuffer.firstIndex(of: UInt8(ascii: "\n")) {
            var line = Array(lineBuffer[..<newline])
            lineBuffer.removeSubrange(...newline)
            if line.last == UInt8(ascii: "\r") { line.removeLast() }
            // Replacing invalid bytes rather than dropping the line: tmux
            // escapes anything non-printable, so what is left should be UTF-8,
            // and a lone bad byte must not cost a whole screen of output.
            handle(String(decoding: line, as: UTF8.self))
        }
    }

    private func handle(_ line: String) {
        guard let event = parser.parse(line: line) else { return }
        switch event {
        case let .response(_, lines, isError):
            guard !pending.isEmpty else {
                // tmux emits one unsolicited block right after attaching,
                // before %session-changed. Nothing asked for it.
                return
            }
            pending.removeFirst()(lines, isError)

        case let .output(pane, bytes):
            views[pane]?.feed(byteArray: bytes[...])

        case let .layoutChange(window, layout):
            guard window == windowID else { return }
            apply(layout: layout)

        case let .sessionWindowChanged(window):
            windowID = window
            refreshLayout()

        case let .windowPaneChanged(window, pane):
            guard window == windowID else { return }
            activePane = pane
            onLayoutChange()

        case let .windowClose(window):
            // The window we were showing is gone; follow the session to
            // whatever it moved to.
            if window == windowID { refreshLayout() }

        case let .exit(reason):
            status = .exited(reason)

        case .windowRenamed, .unhandled:
            break
        }

        // %session-changed is the first thing tmux says that means "attached
        // and listening". Commands sent before it race the handshake.
        if !ready, line.hasPrefix("%session-changed") {
            ready = true
            status = .attached
            applySize()
            refreshLayout()
        }
    }

    // MARK: Commands

    private func send(_ command: String, then: @escaping ([String], Bool) -> Void = { _, _ in }) {
        guard let process else { return }
        pending.append(then)
        process.send(data: ArraySlice(Array((command + "\n").utf8)))
    }

    private func refreshLayout() {
        send("list-windows -F \"#{window_id} #{window_active} #{window_layout}\"") {
            [weak self] lines, isError in
            guard let self, !isError else { return }
            // Prefer the window we are already showing; otherwise the session's
            // active one.
            let rows = lines.compactMap { line -> (id: String, active: Bool, layout: String)? in
                let parts = line.split(separator: " ", maxSplits: 2).map(String.init)
                guard parts.count == 3 else { return nil }
                return (parts[0], parts[1] == "1", parts[2])
            }
            guard let row = rows.first(where: { $0.id == self.windowID })
                ?? rows.first(where: \.active) else { return }
            self.windowID = row.id
            self.apply(layout: row.layout)
            self.send("display-message -p \"#{pane_id}\"") { [weak self] lines, isError in
                guard let self, !isError, let pane = lines.first, !pane.isEmpty else { return }
                self.activePane = pane
                self.onLayoutChange()
            }
        }
    }

    private func apply(layout: String) {
        guard let parsed = TmuxWindowLayout(layout), parsed != self.layout else { return }
        self.layout = parsed
        onLayoutChange()
    }

    /// Tells tmux how big this client is. Every client on a session shares the
    /// window's size, so this is what letterboxes the user's other terminals —
    /// see `TerminalPane` for the measurements.
    public func setSize(cols: Int, rows: Int) {
        guard cols > 0, rows > 0 else { return }
        size = (cols, rows)
        applySize()
    }

    private func applySize() {
        guard ready, size.cols != sentSize.cols || size.rows != sentSize.rows else { return }
        sentSize = size
        send("refresh-client -C \(size.cols)x\(size.rows)")
        // tmux does not always volunteer a %layout-change for a resize we asked
        // for ourselves, and the panes are wrong until the layout catches up.
        refreshLayout()
    }

    public func sendKeys(_ bytes: ArraySlice<UInt8>, to pane: String) {
        guard !bytes.isEmpty else { return }
        // Hex, not `send-keys -l`: a literal argument would need tmux's quoting
        // rules applied to arbitrary keystrokes, which is how a keypress
        // becomes a command.
        send("send-keys -t \(pane) -H \(hexKeys(bytes))")
    }

    // MARK: Pane commands
    //
    // These exist because **tmux's prefix key cannot work in control mode**.
    // Keystrokes reach a pane through `send-keys -t %id`, which writes straight
    // to that pane's pty — this client never reports a key press to tmux at
    // all, so tmux has no chance to see `C-b` and act on it. Verified: `C-b o`
    // leaves the active pane unchanged and types a literal `o` into the pane.
    // iTerm2's tmux integration has the same property, which is why it offers
    // native menu commands instead. So does this; see `PaneCommands`.

    public func nextPane() { send("select-pane -t \(sessionName):.+") }
    public func previousPane() { send("select-pane -t \(sessionName):.-") }
    public func splitRight() { send("split-window -h -t \(sessionName):.") }
    public func splitDown() { send("split-window -v -t \(sessionName):.") }
    public func toggleZoom() { send("resize-pane -Z -t \(sessionName):.") }

    public func selectPane(_ pane: String) {
        guard pane != activePane else { return }
        activePane = pane
        send("select-pane -t \(pane)")
    }

    // MARK: Pane views

    /// Views register themselves so `%output` can be fed straight in.
    ///
    /// Deliberately not routed through `@Observable` state: pane output is a
    /// byte stream arriving many times a second, and putting it in the store
    /// would re-render the whole view tree per chunk. The store carries the
    /// layout; the bytes go point to point.
    public func register(_ view: TerminalView, for pane: String) {
        views[pane] = view
    }

    /// Repaints a pane from tmux's copy of its screen.
    ///
    /// Called when the view learns its size, never at registration: SwiftUI
    /// hands a new `NSView` a zero frame, and the terminal soft-resets when it
    /// is later told its real grid — so anything painted before that is wiped.
    /// A pane producing output recovers on its own and hides the bug; an idle
    /// shell pane just stays black, which is how this was found.
    ///
    /// The first paint of a pane also restores `historyLines` of scrollback, so
    /// SwiftTerm's own wheel and trackpad scrolling has something to scroll
    /// through. Later repaints are visible-only — see `historyPainted`.
    public func paint(_ pane: String) {
        guard views[pane] != nil else { return }
        // Where the cursor is and whether the pane is on the alternate screen
        // both decide the shape of the feed, so ask before capturing.
        send("display-message -p -t \(pane) \"#{cursor_x} #{cursor_y} #{alternate_on}\"") {
            [weak self] lines, isError in
            guard let self, !isError else { return }
            let f = (lines.first ?? "").split(separator: " ").compactMap { Int($0) }
            let cursor = f.count == 3 ? (x: f[0], y: f[1]) : (x: 0, y: 0)
            // Neither tmux nor SwiftTerm keeps scrollback for the alternate
            // screen, so history fed there would trash a full-screen program's
            // display and then be thrown away on the next 1049 switch.
            let history = f.count == 3 && f[2] == 0 && !self.historyPainted.contains(pane)
            // No `-J`: plain `capture-pane` returns screen rows already wrapped
            // to the pane width, one entry per row, which is what makes the
            // arithmetic in `paintSequence` exact. `-J` would join them.
            self.send("capture-pane -p -e\(history ? " -S -\(Self.historyLines)" : "") -t \(pane)") {
                [weak self] lines, isError in
                guard let self, let view = self.views[pane], !isError else { return }
                if history { self.historyPainted.insert(pane) }
                let bytes = Self.paintSequence(lines: lines, cursor: cursor,
                                               clearScrollback: history).utf8
                view.feed(byteArray: Array(bytes)[...])
            }
        }
    }

    /// The bytes that turn a `capture-pane` reply into a painted pane.
    ///
    /// Feeding H history lines plus the pane's R screen rows from home into an
    /// R-row terminal leaves exactly H lines in the scroll buffer and tmux's
    /// screen visible: R-1 of the H+R-1 newlines fill the screen, the other H
    /// scroll. `ESC[3J` drops any scrollback from an earlier paint, so a
    /// repaint cannot stack two copies of the history.
    ///
    /// The trailing CUP is a fix folded in, not decoration: `capture-pane`
    /// returns the pane's full height including the blank rows below the
    /// cursor, so without it the local cursor sits at the bottom while tmux's
    /// is up at the prompt, and the first byte of new output scrolls the whole
    /// restored screen away.
    nonisolated static func paintSequence(lines: [String], cursor: (x: Int, y: Int),
                                          clearScrollback: Bool) -> String {
        (clearScrollback ? "\u{1b}[3J" : "")
            + "\u{1b}[H\u{1b}[2J"
            + lines.joined(separator: "\r\n")
            + "\u{1b}[\(cursor.y + 1);\(cursor.x + 1)H"
    }

    public func unregister(_ pane: String) {
        views.removeValue(forKey: pane)
        // A view torn down and rebuilt (window resize, detach/attach) is a
        // fresh, empty scroll buffer, so it has to be filled again.
        historyPainted.remove(pane)
    }

    nonisolated static func demo() {
        let first = paintSequence(lines: ["a", "b"], cursor: (3, 7), clearScrollback: true)
        assert(first.hasPrefix("\u{1b}[3J\u{1b}[H\u{1b}[2J"))
        assert(first.contains("a\r\nb"))
        assert(first.hasSuffix("\u{1b}[8;4H"), "CUP is 1-based: \(first.debugDescription)")
        // A repaint must not drop the scrollback the first paint restored.
        assert(!paintSequence(lines: [], cursor: (0, 0), clearScrollback: false).contains("[3J"))
    }
}
