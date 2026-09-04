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
            // The grid replaces the detail column, not the window, so the
            // sidebar and the toolbar keep working while it is up.
            if app.showGrid {
                SessionGrid()
            } else if let session = app.visibleSessions.first(where: { $0.id == app.selectedSessionID }) {
                SessionDetail(session: session)
            } else {
                ContentUnavailableView("No session selected", systemImage: "square.split.2x1")
            }
        }
        .toolbar {
            ToolbarItem(placement: .status) { ConnectionBadge() }
            ToolbarItem {
                Button {
                    app.sheet = .create
                } label: {
                    Label("New Session", systemImage: "plus")
                }
                // ⌘N lives on the File menu item, not here: two views claiming
                // the same shortcut is ambiguous and only one of them wins.
                .help("New session (⌘N)")
            }
            ToolbarItem {
                Toggle(isOn: $app.showGrid) { Label("Grid", systemImage: "square.grid.2x2") }
                    // ⌘G is Review Changes; the grid is the shifted one.
                    .keyboardShortcut("g", modifiers: [.command, .shift])
                    .help("Every live session at once, read-only. Click one to open it.")
            }
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
        // A hint is what the *last action* had to say, and it is rendered in a
        // session's Info pane. Nothing else clears it, so without this an
        // "Opened a review window in moomux-a-1f2e." stays pinned under session
        // B indefinitely, describing something that happened to session A.
        .onChange(of: app.selectedSessionID) { _, _ in app.hint = nil }
        // Every modal hangs off the root, not off a row: the Session menu can
        // fire any of them with no row on screen at all.
        .sheet(item: $app.sheet) { sheet in
            switch sheet {
            case .create:
                NewSessionSheet()
            case let .rename(session):
                TextFieldSheet(title: "Rename “\(session.name)”",
                               labels: ["Name"], values: [session.name]) {
                    app.rename(session, to: $0[0])
                }
            case let .tags(session):
                TextFieldSheet(title: "Tags for “\(session.name)”",
                               labels: ["Ticket", "PR"],
                               values: [session.ticket ?? "", session.pr ?? ""]) {
                    app.setTags(session, ticket: $0[0], pr: $0[1])
                }
            }
        }
        .alert("Delete session?", isPresented: Binding(
            get: { app.pendingDelete != nil },
            set: { if !$0 { app.pendingDelete = nil } }
        ), presenting: app.pendingDelete) { session in
            Button("Delete", role: .destructive) { app.delete(session) }
            Button("Cancel", role: .cancel) {}
        } message: { session in
            Text(deleteWarning(for: session))
        }
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

    /// Reuses the status already fetched for the selected session rather than
    /// paying for a fresh `WorktreeStatus` round trip inside an alert — the same
    /// warning the TUI's confirm dialog shows, at no extra cost. A session that
    /// was never selected has no status, and then the alert is the base text.
    private func deleteWarning(for session: Session) -> String {
        let base = "Kills tmux, removes the worktree at \(session.worktreePath), "
            + "and deletes the branch if moomux made it."
        let changes = app.statuses[session.id]?.changeSummary ?? ""
        // First letter only. `.capitalized` title-cases every word, so the
        // server's "2 files changed, 2 commits unpushed" came out as "2 Files
        // Changed, 2 Commits Unpushed" — visibly not the same sentence the
        // detail pane shows.
        guard !changes.isEmpty else { return base }
        return "\(changes.prefix(1).uppercased())\(changes.dropFirst()). \(base)"
    }
}

/// Three fields, deliberately — see `AppState.create` for what is missing and
/// why. Everything else the TUI's new-session dialog asks for is answered by
/// the project's own configuration on the Go side.
private struct NewSessionSheet: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var project = ""
    @State private var name = ""
    @State private var prompt = ""

    private var projects: [String] { app.config?.orderedProjectNames ?? app.projects }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("New session").font(.headline)
            Form {
                Picker("Project", selection: $project) {
                    ForEach(projects, id: \.self) { Text($0).tag($0) }
                }
                TextField("Name", text: $name)
                TextField("First prompt", text: $prompt, axis: .vertical)
                    .lineLimit(3...6)
            }
            .formStyle(.grouped)
            // An empty `TextField` in a grouped Form draws no box at all, so a
            // blank field reads as a static label — the multi-line prompt field
            // as a large blank void. A border is the whole fix.
            .textFieldStyle(.roundedBorder)
            Text("Agent, model and branch come from the project's settings. "
                 + "The TUI's dialog is still the place to override them.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                // Closes at once rather than waiting out the worktree and the
                // userscripts: `app.busy` reports progress in the toolbar, and
                // a refusal lands in the error alert either way.
                Button("Create") {
                    app.create(project: project, name: name, prompt: prompt)
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(project.isEmpty || name.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
        // Seeded from the row being looked at — a second session in the same
        // project is the common case.
        .onAppear {
            project = app.visibleSessions.first { $0.id == app.selectedSessionID }?.project
                ?? projects.first ?? ""
        }
    }
}

/// Rename is one text field and Tags is two, so they are one view.
private struct TextFieldSheet: View {
    @Environment(\.dismiss) private var dismiss
    let title: String
    let labels: [String]
    @State var values: [String]
    let onSave: ([String]) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(title).font(.headline)
            Form {
                ForEach(labels.indices, id: \.self) { i in
                    TextField(labels[i], text: $values[i])
                }
            }
            .formStyle(.grouped)
            // Without a border an empty field is invisible: the Tags sheet with
            // both fields blank looks like two static rows saying "Ticket" and "PR".
            .textFieldStyle(.roundedBorder)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Save") { onSave(values); dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 360)
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
            // Archived rows are only on screen because the Archived toggle is
            // on, and without this they are indistinguishable from live ones —
            // the toggle changes the list and nothing says which rows it added.
            if session.archived {
                Image(systemName: "archivebox")
                    .foregroundStyle(.tertiary)
                    .help("archived")
            }
            // A dead tmux session still has a worktree and still shows here —
            // the dot is the difference between "parked" and "gone".
            if !app.isAlive(session) {
                Image(systemName: "powersleep")
                    .foregroundStyle(.tertiary)
                    .help("no live tmux session")
            }
        }
        .padding(.vertical, 2)
        // Closes over `session`, never over the selection: right-clicking an
        // unselected row has to act on the row you clicked.
        .contextMenu {
            Button("Open in Terminal") { app.open(session) }
            Button("Review Changes") { app.review(session) }
                .disabled(!app.canReview(session))
            Button("Rename…") { app.sheet = .rename(session) }
            Button("Tags…") { app.sheet = .tags(session) }
            Divider()
            Button(session.archived ? "Unarchive" : "Archive") {
                app.setArchived(session, !session.archived)
            }
            Button("Move Up") { app.move(session, by: -1) }
            Button("Move Down") { app.move(session, by: 1) }
            Divider()
            // No confirmation: the worktree survives, the powersleep dot shows
            // the result immediately, and "Open in Terminal" brings it back.
            // Delete is the irreversible one, and the only thing that asks.
            Button("Kill tmux") { app.killTmux(session) }
                .disabled(!app.isAlive(session))
            Button("Delete…", role: .destructive) { app.pendingDelete = session }
        }
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

                        // The Ticket/PR rows below already render the result;
                        // without this the detail pane is a dead end for the
                        // one edit you make while looking at a session.
                        Button {
                            app.sheet = .tags(session)
                        } label: {
                            Label("Tags", systemImage: "tag")
                        }
                        .help("Set this session's ticket and pull request")

                        Button {
                            app.review(session)
                        } label: {
                            Label("Review", systemImage: "plus.forwardslash.minus")
                        }
                        .disabled(!app.canReview(session))
                        .help("Open this worktree's diff in a new tmux window (⌘G)")
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
        // A running write outranks the connection state: creating a session
        // cuts a worktree and runs the worktree-create userscripts, which is
        // tens of seconds of nothing visible happening otherwise. Here rather
        // than in the sheet so every action gets it for free.
        if let busy = app.busy {
            HStack(spacing: 6) {
                ProgressView().controlSize(.small)
                Text("\(busy)…").foregroundStyle(.secondary).lineLimit(1)
            }
        } else {
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
