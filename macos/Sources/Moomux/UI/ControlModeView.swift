import SwiftTerm
import SwiftUI

/// A session's tmux panes as real native views, over control mode.
///
/// The layout comes from tmux (`%layout-change`), and every pane in it already
/// carries absolute cell coordinates — so each pane is just a proportional
/// frame of the container, and there are no nested split views to keep in sync.
/// The container's cell grid *is* tmux's window grid, so those proportions land
/// on exact cell boundaries.
struct ControlModeView: View {
    @Environment(AppState.self) private var app
    let session: Session
    let tmuxPath: String
    /// The client went away: detached, the session ended, or tmux refused.
    /// The reason, when tmux gave one, is worth showing.
    var onExit: (String?) -> Void

    @State private var client: TmuxControlClient?
    @State private var layout: TmuxWindowLayout?
    @State private var activePane: String?
    @State private var windows: [TmuxWindow] = []
    @State private var failure: String?

    var body: some View {
        VStack(spacing: 0) {
            // Only worth its vertical space once there is somewhere to go. The
            // `CellRuler` sits inside the GeometryReader below, so whatever the
            // bar costs is already off the size reported to tmux.
            if windows.count > 1, let client {
                Picker("Window", selection: Binding(
                    get: { windows.first(where: \.active)?.id ?? "" },
                    set: { client.selectWindow($0) })) {
                    ForEach(windows) { window in
                        Text("\(window.index): \(window.name)").tag(window.id)
                    }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                Divider()
            }
            panesArea
        }
        .onDisappear {
            client?.stop()
            client = nil
            app.controlClient = nil
        }
    }

    private var panesArea: some View {
        GeometryReader { geometry in
            ZStack {
                if let client, let layout, layout.width > 0, layout.height > 0 {
                    panes(client: client, layout: layout, in: geometry.size)
                } else if let failure {
                    ContentUnavailableView("Control mode failed", systemImage: "bolt.horizontal",
                                           description: Text(failure))
                } else {
                    ProgressView("Attaching…")
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }

                // The ruler. A terminal view is the only thing that knows how
                // many cells fit in a given number of points — its font metrics
                // are internal — so one invisible full-size view answers that
                // question, and its size is what we report to tmux as the
                // client size. Cheaper than reimplementing font measurement,
                // and it can never disagree with the panes.
                CellRuler { cols, rows in
                    if let client {
                        client.setSize(cols: cols, rows: rows)
                    } else {
                        attach(cols: cols, rows: rows)
                    }
                }
                .allowsHitTesting(false)
                .opacity(0)
            }
        }
    }

    private func panes(client: TmuxControlClient, layout: TmuxWindowLayout,
                       in size: CGSize) -> some View {
        // The container divided by tmux's grid, not the terminal's own font
        // metrics. This is deliberately the *self-consistent* measure: the
        // panes and gaps sum to exactly the container, and it can only ever
        // round a pane a hair wide — where a hair narrow rewraps every long
        // line one character early, and a hair wide just leaves the last
        // column unused. `getOptimalFrameSize` looks like the exact answer and
        // is not: it reports the terminal's cols, which lag a resize.
        let cell = CGSize(width: size.width / CGFloat(layout.width),
                          height: size.height / CGFloat(layout.height))
        // SwiftUI rounds a fractional frame *down* to device pixels, and
        // SwiftTerm then floors width/cellWidth — two floors in a row is what
        // costs the column. Half a point survives both.
        let slack: CGFloat = 0.5
        return ForEach(layout.panes) { pane in
            PaneView(client: client, paneID: pane.id)
                .frame(width: CGFloat(pane.width) * cell.width + slack,
                       height: CGFloat(pane.height) * cell.height + slack)
                .overlay {
                    // tmux leaves a one-cell gap between panes for its own
                    // divider; nothing draws it here, so a border on the active
                    // pane is what shows where keystrokes are going.
                    if pane.id == activePane {
                        Rectangle().strokeBorder(Color.accentColor.opacity(0.6), lineWidth: 1)
                    }
                }
                .position(x: (CGFloat(pane.x) + CGFloat(pane.width) / 2) * cell.width,
                          y: (CGFloat(pane.y) + CGFloat(pane.height) / 2) * cell.height)
        }
    }

    private func attach(cols: Int, rows: Int) {
        guard client == nil else { return }
        let client = TmuxControlClient(tmuxPath: tmuxPath, session: session.tmuxSession,
                                       size: (cols, rows))
        client.onLayoutChange = { [weak client] in
            layout = client?.layout
            activePane = client?.activePane
            windows = client?.windows ?? []
        }
        client.onStatusChange = { status in
            if case let .exited(reason) = status {
                failure = reason
                onExit(reason)
            }
        }
        client.start()
        self.client = client
        app.controlClient = client
    }
}

// MARK: - One pane

private struct PaneView: NSViewRepresentable {
    let client: TmuxControlClient
    let paneID: String

    func makeNSView(context: Context) -> TerminalView {
        // Built with the scrollback up front: SwiftTerm's default cap is 500
        // lines, half of what the first paint restores, and the excess would be
        // fed and silently dropped.
        let view = PaneTerminalView(
            frame: .init(x: 0, y: 0, width: 400, height: 300),
            font: .monospacedSystemFont(ofSize: 12, weight: .regular),
            options: TerminalOptions(scrollback: TmuxControlClient.historyLines))
        view.onFocused = { [weak client] in client?.selectPane(paneID) }
        view.terminalDelegate = context.coordinator
        client.register(view, for: paneID)
        return view
    }

    func updateNSView(_ view: TerminalView, context: Context) {}

    static func dismantleNSView(_ view: TerminalView, coordinator: Coordinator) {
        coordinator.client.unregister(coordinator.paneID)
    }

    func makeCoordinator() -> Coordinator { Coordinator(client: client, paneID: paneID) }

    final class Coordinator: TerminalDelegateBase {
        let client: TmuxControlClient
        let paneID: String

        init(client: TmuxControlClient, paneID: String) {
            self.client = client
            self.paneID = paneID
        }

        /// Everything typed into this view goes to *this* pane by id, not to
        /// whatever tmux considers active — which is what makes clicking into a
        /// pane and typing behave the way the window looks.
        override func send(source: TerminalView, data: ArraySlice<UInt8>) {
            MainActor.assumeIsolated { client.sendKeys(data, to: paneID) }
        }

        /// tmux is the authority on pane size — the view follows the frame it
        /// is given, and the ruler is what tells tmux how much room there is.
        /// This is only the cue that the view now has a real grid to paint on.
        override func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
            guard newCols > 0, newRows > 0 else { return }
            MainActor.assumeIsolated { client.paint(paneID) }
        }
    }
}

/// Hides SwiftTerm's scroller, which is not cosmetic.
///
/// In SwiftTerm 1.20.0 `reservedScrollerWidth` is `scroller?.isHidden == true ? 0
/// : scrollerWidth` — it ignores `scrollerStyle` entirely, and nothing in the
/// library ever hides the scroller. So **every** view silently reserves ~17pt,
/// and `getEffectiveWidth` takes it off before dividing by the cell width.
///
/// 17pt is noise in a 1188pt ruler (156 columns either way) and a whole column
/// in a 590pt pane, so panes came out one column narrower than the pane tmux
/// was formatting for and every full-width line wrapped a character early.
/// Hiding it makes the reservation zero and the two agree. tmux owns scrollback
/// through copy-mode, so the scroller had no job here anyway.
///
/// Note the version: an earlier reading of this same property came from
/// SwiftTerm's `main`, where it also checks `scrollerStyle == .legacy` and
/// setting `.overlay` would have been enough. Read `.build/checkouts`, not a
/// fresh clone.
class NoScrollerTerminalView: TerminalView {
    override func didAddSubview(_ subview: NSView) {
        super.didAddSubview(subview)
        if subview is NSScroller { subview.isHidden = true }
    }
}

/// `mouseDown` and `viewDidMoveToWindow` are the two SwiftTerm entry points
/// marked `open` rather than `public`, which is what makes click-to-select and
/// focus-on-appear possible at all.
private final class PaneTerminalView: NoScrollerTerminalView {
    /// Tell tmux this pane is the active one. Called for both ways a pane can
    /// take the keyboard, so the highlighted border and the pane that actually
    /// receives typing can never disagree.
    var onFocused: () -> Void = {}

    override func mouseDown(with event: NSEvent) {
        onFocused()
        super.mouseDown(with: event)
    }

    /// The first pane to appear takes the keyboard, so attaching and typing
    /// works without clicking first — SwiftUI otherwise leaves first responder
    /// on the sidebar list. Only when no sibling pane already has it, so this
    /// never steals focus back from the pane the user actually clicked.
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        guard window != nil else { return }
        DispatchQueue.main.async { [weak self] in
            guard let self, let window = self.window else { return }
            if window.firstResponder is PaneTerminalView { return }
            window.makeFirstResponder(self)
            self.onFocused()
        }
    }
}

// MARK: - Cell ruler

/// Reports how many cells fit in whatever space it is given, and how big one
/// cell is. A terminal view is the only thing that can answer either question —
/// its font metrics are internal — so this is one invisible view used as a
/// measuring stick, which can never disagree with the panes beside it.
private struct CellRuler: NSViewRepresentable {
    let onSize: (Int, Int) -> Void

    func makeNSView(context: Context) -> TerminalView {
        let view = NoScrollerTerminalView(frame: .init(x: 0, y: 0, width: 400, height: 300))
        view.terminalDelegate = context.coordinator
        view.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        return view
    }

    func updateNSView(_ view: TerminalView, context: Context) {
        context.coordinator.onSize = onSize
    }

    func makeCoordinator() -> Coordinator { Coordinator(onSize: onSize) }

    final class Coordinator: TerminalDelegateBase {
        var onSize: (Int, Int) -> Void

        init(onSize: @escaping (Int, Int) -> Void) { self.onSize = onSize }

        override func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
            guard newCols > 0, newRows > 0 else { return }
            onSize(newCols, newRows)
        }
    }
}

/// `TerminalViewDelegate` has a dozen members. SwiftTerm supplies defaults for
/// most of them, but not all — `rangeChanged` in particular — so this collects
/// the no-ops in one place and each coordinator overrides only what it needs.
class TerminalDelegateBase: NSObject, TerminalViewDelegate {
    func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {}
    func setTerminalTitle(source: TerminalView, title: String) {}
    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    func send(source: TerminalView, data: ArraySlice<UInt8>) {}
    func scrolled(source: TerminalView, position: Double) {}
    func rangeChanged(source: TerminalView, startY: Int, endY: Int) {}
}
