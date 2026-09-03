import SwiftUI

/// Semantic colors only — literal hex breaks dark mode.
enum Theme {
    static func color(_ state: AgentState) -> Color {
        switch state {
        case .needsInput: return .orange
        case .working: return .accentColor
        case .done: return .green
        case .parked: return .secondary
        case .unknown: return .secondary
        }
    }

    static let mono = Font.system(size: 12, design: .monospaced)
}

// MARK: - Root

struct RootView: View {
    @Environment(AppState.self) private var app

    var body: some View {
        @Bindable var app = app
        NavigationSplitView {
            SessionList()
                .navigationSplitViewColumnWidth(min: 240, ideal: 300)
        } detail: {
            if let session = app.visibleSessions.first(where: { $0.id == app.selectedSessionID }) {
                SessionDetail(session: session)
            } else {
                ContentUnavailableView("No session selected", systemImage: "square.split.2x1")
            }
        }
        .toolbar {
            ToolbarItem(placement: .status) { ConnectionBadge() }
            ToolbarItem {
                Toggle("Archived", isOn: $app.showArchived)
                    .help("Show archived sessions")
            }
            ToolbarItem {
                Button {
                    Task {
                        await app.refresh()
                        // The expensive per-session state is only ever fetched
                        // on demand, so an explicit refresh is the one place it
                        // should be re-fetched rather than served from cache.
                        if let id = app.selectedSessionID {
                            await app.loadStatus(for: id, force: true)
                        }
                    }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
                .keyboardShortcut("r")
            }
        }
        .task { app.start() }
        // The whole visible surface of `actionError` for now: a refused action
        // has to say so somewhere, and an alert is the least that qualifies.
        .alert("Couldn't do that", isPresented: Binding(
            get: { app.actionError != nil },
            set: { if !$0 { app.actionError = nil } }
        )) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(app.actionError ?? "")
        }
    }
}

// MARK: - Sidebar

private struct SessionList: View {
    @Environment(AppState.self) private var app

    var body: some View {
        @Bindable var app = app
        List(selection: $app.selectedSessionID) {
            ForEach(app.sessionsByProject, id: \.project) { group in
                Section(header: ProjectHeader(name: group.project)) {
                    ForEach(group.sessions) { session in
                        SessionRow(session: session).tag(session.id)
                    }
                }
            }
        }
        .overlay { if app.sessions.isEmpty { EmptyState() } }
    }
}

private struct ProjectHeader: View {
    @Environment(AppState.self) private var app
    let name: String

    var body: some View {
        HStack(spacing: 4) {
            if let emoji = app.emoji(for: name) { Text(emoji) }
            Text(name)
        }
    }
}

private struct SessionRow: View {
    @Environment(AppState.self) private var app
    let session: Session

    var body: some View {
        let state = app.state(for: session)
        HStack(spacing: 8) {
            Image(systemName: state.symbol)
                .foregroundStyle(Theme.color(state))
                .help(state.label)
            VStack(alignment: .leading, spacing: 1) {
                Text(session.name)
                Text(session.branch)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer(minLength: 4)
            // A dead tmux session still has a worktree and still shows here —
            // the dot is the difference between "parked" and "gone".
            if !app.isAlive(session) {
                Image(systemName: "powersleep")
                    .foregroundStyle(.tertiary)
                    .help("no live tmux session")
            }
        }
        .padding(.vertical, 2)
    }
}

private struct EmptyState: View {
    @Environment(AppState.self) private var app

    var body: some View {
        if case let .down(message) = app.connection {
            ContentUnavailableView {
                Label("Can't reach moomux", systemImage: "bolt.horizontal.circle")
            } description: {
                VStack(spacing: 6) {
                    Text(message)
                    Text("moomux serve").font(Theme.mono)
                    Text("Start the core, and this connects on its own.")
                        .foregroundStyle(.secondary)
                }
            }
        } else {
            ContentUnavailableView("No sessions", systemImage: "tray")
        }
    }
}

// MARK: - Detail

private struct SessionDetail: View {
    @Environment(AppState.self) private var app
    let session: Session

    /// Attaching is deliberate, never a side effect of selecting a row: a tmux
    /// client sizes the shared window down to its own dimensions for *every*
    /// other client on that session, so auto-attaching would silently squash
    /// the user's iTerm and phone windows as they browsed the list. See
    /// `TerminalPane` for why this is inherent to a plain attach.
    @State private var attached = false
    @State private var showInfo = false

    var body: some View {
        let state = app.state(for: session)
        Group {
            if attached, app.useControlMode, let tmux = ToolPath.find("tmux") {
                ControlModeView(session: session, tmuxPath: tmux) { reason in
                    attached = false
                    if let reason, !reason.isEmpty { app.hint = reason }
                }
            } else if attached {
                SessionTerminal(session: session, onDetach: { attached = false })
            } else {
                SessionInfo(session: session, onAttach: { attached = true })
            }
        }
        .navigationTitle(session.name)
        .navigationSubtitle(state.label)
        .onChange(of: attached) { _, isAttached in if !isAttached { showInfo = false } }
        .inspector(isPresented: $showInfo) {
            SessionInfo(session: session, onAttach: nil)
                .inspectorColumnWidth(min: 280, ideal: 340, max: 520)
        }
        .toolbar {
            if attached {
                ToolbarItem {
                    Button {
                        attached = false
                    } label: {
                        Label("Detach", systemImage: "rectangle.portrait.and.arrow.right")
                    }
                    .help("Leave the session running and give the size back")
                }
                ToolbarItem {
                    Button {
                        showInfo.toggle()
                    } label: {
                        Label("Info", systemImage: "sidebar.right")
                    }
                    .keyboardShortcut("i")
                    .help("Session details")
                }
            }
        }
        // Selecting another session must not carry the attachment with it.
        .onChange(of: session.id) { _, _ in attached = false }
        .task(id: session.id) { await app.loadStatus(for: session.id) }
    }
}

/// Everything about a session that isn't the terminal itself.
private struct SessionInfo: View {
    @Environment(AppState.self) private var app
    let session: Session
    /// nil when this is the inspector beside a terminal that is already attached.
    var onAttach: (() -> Void)?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let onAttach {
                    HStack {
                        Button(action: onAttach) {
                            Label("Attach", systemImage: "terminal")
                        }
                        .keyboardShortcut(.return)
                        .disabled(!app.isAlive(session) || ToolPath.find("tmux") == nil)
                        .help("Attach this tmux session inside the app")

                        Button {
                            app.open(session)
                        } label: {
                            Label("Open in terminal", systemImage: "arrow.up.forward.app")
                        }
                        // The core's own open path: a real terminal window, and
                        // what revives a session whose tmux is gone.
                        .help("Hand this session to your terminal app, as the TUI does")
                    }
                    Toggle("Native panes", isOn: Binding(
                        get: { app.useControlMode },
                        set: { app.useControlMode = $0 }))
                        .help("tmux control mode: each pane in its own native view. Turn off to attach the whole session as one terminal.")
                    if !app.isAlive(session) {
                        Text("No live tmux session — open it to start one.")
                            .foregroundStyle(.secondary)
                    } else if ToolPath.find("tmux") == nil {
                        Text("Can't find a tmux binary to attach with.")
                            .foregroundStyle(.secondary)
                    }
                }

                if let hint = app.hint {
                    Text(hint).font(Theme.mono).textSelection(.enabled)
                }

                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 6) {
                    Field("Project", session.project)
                    Field("Agent", session.agentName + (session.dangerous ? "  ⚠︎ dangerous" : ""))
                    Field("Branch", session.branch)
                    Field("Worktree", session.worktreePath)
                    Field("tmux", session.tmuxSession
                        + (app.isAlive(session) ? "" : "  (not running)"))
                    if let status = app.statuses[session.id] {
                        if !status.changeSummary.isEmpty {
                            Field("Changes", status.changeSummary)
                        } else if status.known {
                            Field("Changes", "clean")
                        }
                        if let pr = status.pr, !pr.summary.isEmpty {
                            Field("PR status", pr.summary)
                        }
                    }
                    Field("Created", session.createdAt.formatted(date: .abbreviated, time: .shortened))
                    if session.hasBeenOpened {
                        Field("Last opened", session.lastOpened.formatted(.relative(presentation: .named)))
                    }
                    if let ticket = session.ticket, !ticket.isEmpty { Field("Ticket", ticket) }
                    if let pr = session.pr, !pr.isEmpty { Field("PR", pr) }
                }

                if let prompt = session.prompt, !prompt.isEmpty {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("First prompt").font(.headline)
                        Text(prompt).font(Theme.mono).textSelection(.enabled)
                    }
                }
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct Field: View {
    let label: String
    let value: String

    init(_ label: String, _ value: String) {
        self.label = label
        self.value = value
    }

    var body: some View {
        GridRow {
            Text(label).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
            Text(value).font(Theme.mono).textSelection(.enabled)
        }
    }
}

// MARK: - Status

private struct ConnectionBadge: View {
    @Environment(AppState.self) private var app

    var body: some View {
        switch app.connection {
        case .connecting:
            Text("connecting…").foregroundStyle(.secondary)
        case .connected:
            if let error = app.statusError {
                Label(error, systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.orange)
                    .lineLimit(1)
            }
        case let .down(message):
            Label(message, systemImage: "bolt.horizontal.circle")
                .foregroundStyle(.orange)
                .lineLimit(1)
                .help("Is `moomux serve` running?")
        }
    }
}

// MARK: - Menu bar

/// The one thing the TUI structurally cannot have: a live count of sessions
/// waiting on you, visible while you are in another app.
struct MenuBarContent: View {
    @Environment(AppState.self) private var app

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(app.visibleSessions.filter { app.state(for: $0) != .parked }) { session in
                Button {
                    app.open(session)
                } label: {
                    let state = app.state(for: session)
                    HStack {
                        Image(systemName: state.symbol).foregroundStyle(Theme.color(state))
                        Text("\(session.project) · \(session.name)")
                        Spacer()
                        Text(state.label).foregroundStyle(.secondary)
                    }
                }
                .buttonStyle(.plain)
                .padding(.horizontal, 12)
                .padding(.vertical, 5)
            }
            if app.visibleSessions.isEmpty {
                Text(app.connection.isDown ? "moomux serve isn't running" : "No sessions")
                    .foregroundStyle(.secondary)
                    .padding(12)
            }
            Divider().padding(.vertical, 4)
            Button("Quit Moomux") { NSApplication.shared.terminate(nil) }
                .buttonStyle(.plain)
                .padding(.horizontal, 12)
                .padding(.bottom, 8)
        }
        .frame(width: 320)
    }
}
