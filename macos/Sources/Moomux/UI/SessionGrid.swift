import Foundation
import SwiftTerm
import SwiftUI

/// Every live session at once, as read-only snapshots.
///
/// Deliberately **not** a grid of attached clients. Every tmux client on a
/// session sets the shared window size — measured for a plain attach, for
/// `-CC`, and for grouped sessions — so six live tiles would squash six real
/// agent sessions down to tile size until the grid closed, and would fight the
/// detail pane's own client over `refresh-client -C` where they overlap.
/// `capture-pane` attaches nothing and resizes nothing.
///
/// The price is that a tile cannot be typed into, which is the affordance the
/// size problem makes unaffordable anyway. Clicking one selects the session;
/// Attach is still where a real, full-size, deliberate client comes from.
struct SessionGrid: View {
    @Environment(AppState.self) private var app

    private var tiles: [Session] { app.visibleSessions.filter { app.isAlive($0) } }

    var body: some View {
        ScrollView {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 320), spacing: 12)], spacing: 12) {
                ForEach(tiles) { session in
                    SessionTile(session: session)
                        .onTapGesture {
                            app.selectedSessionID = session.id
                            app.showGrid = false
                        }
                }
            }
            .padding(12)
        }
        .overlay {
            if tiles.isEmpty {
                ContentUnavailableView("No live sessions", systemImage: "square.grid.2x2",
                                       description: Text("A session has to have a tmux session "
                                                         + "running before there is anything to show."))
            }
        }
    }
}

private struct SessionTile: View {
    @Environment(AppState.self) private var app
    let session: Session

    @State private var rows: [String] = []

    var body: some View {
        let state = app.state(for: session)
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 6) {
                Image(systemName: state.symbol).foregroundStyle(Theme.color(state))
                Text(session.name).lineLimit(1)
                Spacer()
                Text(session.project).foregroundStyle(.secondary).lineLimit(1)
            }
            .font(.caption)
            SnapshotTerminal(rows: rows)
                .frame(height: 200)
                .clipShape(RoundedRectangle(cornerRadius: 4))
                .overlay {
                    RoundedRectangle(cornerRadius: 4)
                        .strokeBorder(session.id == app.selectedSessionID
                                      ? Color.accentColor : Color.secondary.opacity(0.3))
                }
        }
        .contentShape(Rectangle())
        .task(id: session.tmuxSession) {
            // What this costs, plainly: one short-lived `tmux capture-pane`
            // process per visible tile every tick. Five seconds rather than the
            // store's two because a snapshot is a glance, not a live terminal,
            // and the process churn is the whole expense. The task belongs to
            // the tile, so closing the grid stops all of it.
            //
            // If a wall of tiles ever makes this bite, the upgrade is one tmux
            // invocation with `;`-joined capture-pane commands — not a client
            // per tile, which is the thing this design exists to avoid.
            while !Task.isCancelled {
                rows = await capture(session: session.tmuxSession)
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }
}

/// `Process` blocks, same rule as every `MoomuxClient` call. A GUI app has
/// launchd's `PATH`, so tmux comes from `ToolPath` and never from a bare name.
private func capture(session: String) async -> [String] {
    guard !session.isEmpty, let tmux = ToolPath.find("tmux") else { return [] }
    let argv = TmuxSnapshot.captureArgv(session: session)
    return await Task.detached(priority: .utility) { () -> [String] in
        let process = Process()
        process.executableURL = URL(fileURLWithPath: tmux)
        process.arguments = argv
        let output = Pipe()
        process.standardOutput = output
        process.standardError = FileHandle.nullDevice
        do { try process.run() } catch { return [] }
        let data = output.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        // A session can die between the poll and the capture. Nothing captured
        // leaves the tile showing its last snapshot, which is the same instinct
        // as `AppState.refresh` keeping the last good list: a failed call must
        // not read as "empty".
        guard process.terminationStatus == 0 else { return [] }
        return String(decoding: data, as: UTF8.self)
            .split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
    }.value
}

/// A terminal used only as a renderer: bytes in, nothing out.
///
/// `NoScrollerTerminalView` and not `TerminalView`: SwiftTerm reserves ~17pt
/// for a scroller it never hides, which is a whole column at this size.
private struct SnapshotTerminal: NSViewRepresentable {
    let rows: [String]

    func makeNSView(context: Context) -> TerminalView {
        let view = NoScrollerTerminalView(frame: .init(x: 0, y: 0, width: 400, height: 200))
        view.terminalDelegate = context.coordinator
        view.font = .monospacedSystemFont(ofSize: 9, weight: .regular)
        context.coordinator.view = view
        return view
    }

    func updateNSView(_ view: TerminalView, context: Context) {
        context.coordinator.rows = rows
        context.coordinator.repaint()
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    /// Truncation needs the tile's column count, and only the terminal knows
    /// it — and only once SwiftUI has given the view a real frame. So the rows
    /// live here and are painted from both sides: a fresh capture, and the
    /// `sizeChanged` that first reveals how wide a tile is. Without the second,
    /// the first paint lands on a zero-frame view and SwiftTerm's soft-reset
    /// wipes it, which is the black-idle-pane bug in miniature.
    final class Coordinator: TerminalDelegateBase {
        weak var view: TerminalView?
        var rows: [String] = []

        override func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
            repaint()
        }

        func repaint() {
            guard let view, !rows.isEmpty else { return }
            let screen = TmuxSnapshot.screen(from: rows, columns: view.getTerminal().cols)
            view.feed(byteArray: Array(screen.utf8)[...])
        }
    }
}
