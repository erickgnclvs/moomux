import AppKit
import Foundation
import Observation

/// One-way flow, no exceptions:
///
///     MoomuxClient (unix socket) -> AppState -> Views
///
/// A single `@Observable` root store rather than a tree of view models. Views
/// read this and nothing else; nothing derived is stored in a view.
@MainActor
@Observable
public final class AppState {

    public enum Connection: Equatable {
        case connecting
        case connected
        case down(String)

        public var isDown: Bool { if case .down = self { return true }; return false }
    }

    // MARK: Server state

    public private(set) var sessions: [Session] = []
    public private(set) var projects: [String] = []
    /// Session id → tmux session still alive.
    public private(set) var alive: [String: Bool] = [:]
    /// Worktree path → agent state. Keyed by path because that is what the
    /// watcher observes; `state(for:)` does the join.
    public private(set) var states: [String: AgentState] = [:]
    public private(set) var config: Config?
    /// Worktree and PR state, by session id, for sessions that have been
    /// looked at. Deliberately **not** part of the poll loop: each entry costs
    /// a `git status`, a `git log` and a `gh` call over the network, so filling
    /// this for every session every two seconds would hammer the machine and
    /// GitHub both. Fetched when a session is selected, and on refresh.
    public private(set) var statuses: [Session.ID: MoomuxClient.SessionStatus] = [:]

    public private(set) var connection: Connection = .connecting
    /// Set when the status stream drops. Without it the last states keep
    /// rendering, which reads as live rather than frozen.
    public private(set) var statusError: String?

    // MARK: UI state

    public var selectedSessionID: Session.ID?
    public var showArchived = false
    /// Attach with tmux control mode (native panes) rather than a plain
    /// `tmux attach`. The default, because it is the whole reason for a native
    /// app: real per-pane selection and copy, and a layout the app can see.
    ///
    /// The plain attach stays reachable by turning this off — it is the escape
    /// hatch for anything control mode renders badly, and the two are one
    /// `if` apart in `SessionDetail`. Not persisted between runs yet.
    public var useControlMode = true
    /// Whatever the last `OpenSession` told the user to do, if anything.
    public var hint: String?
    /// A mutation the server refused, until the user dismisses it. See
    /// `failed(_:_:)` for why this is not `connection`.
    public var actionError: String?
    /// What a write is doing, while it does it. Only `create` takes long
    /// enough to matter, but every mutation reports here so nothing has to
    /// hold a sheet open waiting for one. Rendered by `ConnectionBadge`.
    public private(set) var busy: String?
    /// Which modal form is up. Presentation state lives on the store rather
    /// than in a view because the Session menu has to open these too, and views
    /// read AppState and nothing else.
    public enum Sheet: Identifiable, Hashable {
        case create
        case rename(Session)
        case tags(Session)
        public var id: Self { self }
    }
    public var sheet: Sheet?
    /// The session the delete confirmation is asking about. Its own field, not
    /// a `Sheet` case: an alert is not a sheet and cannot share the modifier.
    public var pendingDelete: Session?
    /// The live control-mode client, while one is attached. Menu commands need
    /// to reach it, and the menu is the only way to drive tmux's pane
    /// navigation — the prefix key cannot reach tmux in control mode. Weak so
    /// a detach that forgets to clear this cannot keep a tmux client alive.
    public weak var controlClient: TmuxControlClient?

    public let client: MoomuxClient

    private var tasks: [Task<Void, Never>] = []
    /// No view reads this, and `@Observable` fires on any assignment.
    @ObservationIgnored private var notifier: Notifier?

    public init(client: MoomuxClient = MoomuxClient()) {
        self.client = client
    }

    // MARK: Derived

    public func state(for session: Session) -> AgentState {
        states[session.worktreePath] ?? .unknown
    }

    public func isAlive(_ session: Session) -> Bool {
        alive[session.id] ?? false
    }

    /// The reverse of `state(for:)`: the watcher only knows worktree paths.
    public func session(atPath path: String) -> Session? {
        sessions.first { $0.worktreePath == path }
    }

    /// Sessions the user should look at. The menu bar's whole reason to exist.
    public var needsInputCount: Int {
        visibleSessions.filter { state(for: $0) == .needsInput }.count
    }

    public var visibleSessions: [Session] {
        showArchived ? sessions : sessions.filter { !$0.archived }
    }

    /// Project name → its sessions, in the order the server returned them
    /// (which is the user's manual ordering), projects in config order.
    public var sessionsByProject: [(project: String, sessions: [Session])] {
        let grouped = Dictionary(grouping: visibleSessions, by: \.project)
        let ordered = config?.orderedProjectNames ?? projects
        let known = Set(ordered)
        return (ordered + grouped.keys.filter { !known.contains($0) }.sorted())
            .compactMap { name in
                guard let sessions = grouped[name], !sessions.isEmpty else { return nil }
                return (name, sessions)
            }
    }

    public func emoji(for project: String) -> String? {
        config?.projects[project]?.emoji
    }

    // MARK: Lifecycle

    public func start() {
        guard tasks.isEmpty else { return }
        // Here rather than in `init`: `--selftest` exits inside `bootstrap()`
        // from an unbundled binary, where reaching the notification center at
        // all would trap.
        notifier = Notifier(app: self)
        tasks = [
            Task { [weak self] in await self?.pollLoop() },
            Task { [weak self] in await self?.watchLoop() },
        ]
    }

    public func stop() {
        tasks.forEach { $0.cancel() }
        tasks = []
    }

    /// Sessions, projects and tmux liveness have no push channel — only the
    /// watcher streams. Two seconds matches what the TUI settled on.
    private func pollLoop() async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: .seconds(2))
        }
    }

    /// Loads (or reloads) worktree and PR state for one session.
    public func loadStatus(for id: Session.ID, force: Bool = false) async {
        guard force || statuses[id] == nil else { return }
        do {
            let status = try await withoutBlockingTheUI { [client] in
                try client.status(id: id)
            }
            statuses[id] = status
        } catch {
            // Leave whatever was there; `connection` already carries the
            // failure, and a missing status row is not worth a second alarm.
        }
    }

    public func refresh() async {
        do {
            let snapshot = try await withoutBlockingTheUI { [client] in
                (sessions: try client.sessions(),
                 projects: try client.projects(),
                 alive: try client.tmuxAliveAll(),
                 config: try client.config())
            }
            sessions = snapshot.sessions
            projects = snapshot.projects
            alive = snapshot.alive
            config = snapshot.config
            connection = .connected
            // Drop status for sessions that are gone, so the cache cannot grow
            // forever or answer for a recreated id.
            let live = Set(snapshot.sessions.map(\.id))
            statuses = statuses.filter { live.contains($0.key) }
            updateDockBadge()  // needsInputCount filters visibleSessions
        } catch {
            // Deliberately leaves the last-good lists in place. A failed call
            // must never read as "everything was deleted" — the Go client
            // caches for the same reason. `connection` is what says so.
            connection = .down(error.localizedDescription)
        }
    }

    /// Forwards the server's snapshot stream, reconnecting until stopped.
    /// Mirrors `ipc.Client.Run`, backoff included.
    private func watchLoop() async {
        var backoff = Duration.milliseconds(200)
        while !Task.isCancelled {
            do {
                for try await snapshot in client.watch() {
                    backoff = .milliseconds(200) // a working connection earns a fast retry
                    // Diffed after the merge, not against the raw snapshot: a
                    // snapshot carries only one agent's paths, so comparing
                    // merged maps is what makes a partial tick a no-op for
                    // everybody else.
                    let previous = states
                    states = AppState.merge(
                        states, snapshot.states,
                        live: Set(sessions.map(\.worktreePath)))
                    notifier?.report(previous: previous, current: states)
                    updateDockBadge()
                    set(statusError: snapshot.err)
                }
                throw MoomuxClient.Failure.disconnected
            } catch {
                guard !Task.isCancelled else { return }
                set(statusError: "status stream lost (\(error.localizedDescription)); reconnecting")
            }
            try? await Task.sleep(for: backoff)
            backoff = min(backoff * 2, .seconds(5))
        }
    }

    /// `@Observable` fires on any assignment, equal or not, so an unchanged
    /// error re-renders every list row once per watcher tick.
    private func set(statusError message: String?) {
        if statusError != message { statusError = message }
    }

    /// Free, needs no authorization, and the whole fallback if notifications
    /// are denied. `nil` rather than "0" — a zero badge is still a badge.
    private func updateDockBadge() {
        let count = needsInputCount
        NSApp.dockTile.badgeLabel = count == 0 ? nil : "\(count)"
    }

    /// Folds one watcher snapshot into the known states.
    ///
    /// Merge, never assign: `watcher.MultiWatcher` fans out one snapshot per
    /// sub-watcher, and each carries only *its own agent's* paths — so
    /// replacing wholesale would wipe every other agent's sessions on every
    /// tick. The prune against live worktree paths is what keeps that from
    /// growing forever; the watcher reports on directories that were never
    /// sessions ("/", the home directory) and on sessions since deleted.
    /// `internal/tui/update.go`'s StatusTickMsg does exactly this.
    nonisolated static func merge(
        _ known: [String: AgentState],
        _ snapshot: [String: AgentState],
        live: Set<String>
    ) -> [String: AgentState] {
        known.merging(snapshot) { _, fresh in fresh }.filter { live.contains($0.key) }
    }

    nonisolated static func demo() {
        let claude = ["/wt/a": AgentState.working]
        let codex = ["/wt/b": AgentState.needsInput]
        let live: Set<String> = ["/wt/a", "/wt/b"]

        // A snapshot from one watcher must not erase the other's sessions.
        var states = merge([:], claude, live: live)
        states = merge(states, codex, live: live)
        assert(states == ["/wt/a": .working, "/wt/b": .needsInput], "\(states)")

        // Fresh values win.
        states = merge(states, ["/wt/a": .done], live: live)
        assert(states["/wt/a"] == .done)
        assert(states["/wt/b"] == .needsInput)

        // Paths that are not live sessions are dropped — the watcher reports
        // on plenty of directories that never were one.
        states = merge(states, ["/": .parked, "/Users/someone": .parked], live: live)
        assert(states.keys.sorted() == ["/wt/a", "/wt/b"], "\(states.keys.sorted())")

        // A deleted session's state goes with it.
        states = merge(states, [:], live: ["/wt/a"])
        assert(states.keys.sorted() == ["/wt/a"])

        // A mutation the server refuses is an answer, not a dead socket. It has
        // to reach the user as its own message and leave `connection` exactly
        // where the poll loop left it, or one declined action blanks the
        // sidebar into "Can't reach moomux". `--selftest` runs on the main
        // thread, from MoomuxApp.bootstrap().
        MainActor.assumeIsolated {
            let app = AppState()
            app.connection = .connected
            app.failed("Rename", MoomuxClient.Failure.server(#"session "x" already exists"#))
            assert(app.actionError == #"Rename failed: session "x" already exists"#,
                   app.actionError ?? "nil")
            assert(app.connection == .connected, "a refused action must not read as a dead socket")
        }
    }

    // MARK: Actions

    /// The one way a write reaches the server. Runs the blocking call off the
    /// main actor, then reloads — `refresh()` is the whole-world poll, so there
    /// is nothing finer-grained to invalidate, and it is what makes a mutation
    /// visible now rather than up to two seconds later.
    ///
    /// A non-nil return replaces `hint`; nil means the action had nothing to
    /// say and leaves whatever is there.
    private func mutate(_ what: String, _ work: @Sendable @escaping (MoomuxClient) throws -> String?) {
        Task {
            busy = what
            defer { busy = nil }
            do {
                if let hint = try await withoutBlockingTheUI({ [client] in try work(client) }) {
                    self.hint = hint.isEmpty ? nil : hint
                }
                await refresh()
            } catch {
                failed(what, error)
            }
        }
    }

    /// A refused action and an unreachable socket are not the same condition
    /// and must not render the same. This used to be `connection = .down(…)`,
    /// which blanked the whole sidebar into "Can't reach moomux" because one
    /// action was declined — and then the next poll, two seconds later, quietly
    /// put it back, so the server's actual reason flashed past unread.
    /// `connection` belongs to the poll loop and to nothing else.
    private func failed(_ what: String, _ error: Error) {
        actionError = "\(what) failed: \(error.localizedDescription)"
    }

    /// Three fields, and that is the whole form.
    ///
    /// Agent, model, thinking level, base branch, "resume this existing
    /// branch", dangerous mode and auto-submit are all deliberately absent: the
    /// core already defaults every one of them from the project's config, and
    /// offering a picker for any of them means shipping a second copy of
    /// `internal/tui`'s `agentNames` / `thinkingNamesFor` / `modelNamesFor`
    /// tables on this side of the socket, to drift silently the next time the
    /// Go side gains an agent. Add a control here when the core can hand over
    /// the list it validates against — not before. The TUI is still the place
    /// to create anything unusual.
    public func create(project: String, name: String, prompt: String) {
        mutate("Creating session") { client in
            let (session, hint) = try client.createSession(project: project, name: name)
            guard !prompt.isEmpty else { return hint }
            // Recorded on the session as well as typed into the pane, so the
            // detail pane's "First prompt" has something to show. Best-effort,
            // exactly as the TUI treats it.
            try? client.setPrompt(id: session.id, prompt: prompt)
            do {
                try client.startFirstPrompt(tmuxSession: session.tmuxSession, prompt: prompt)
            } catch {
                // The worktree and the tmux session already exist by now. A
                // prompt that never landed is a hint, not a failed creation —
                // reporting it as one would tell the user to try again and
                // hand them "session already exists".
                let note = "couldn't send first prompt: \(error.localizedDescription)"
                return hint.isEmpty ? note : hint + "\n" + note
            }
            return hint
        }
    }

    public func open(_ session: Session) {
        mutate("Open") { try $0.openSession(id: session.id) }
    }

    public func rename(_ session: Session, to name: String) {
        mutate("Rename") { try $0.rename(id: session.id, to: name); return nil }
    }

    public func setTags(_ session: Session, ticket: String, pr: String) {
        mutate("Set tags") { try $0.setTags(id: session.id, ticket: ticket, pr: pr); return nil }
    }

    public func setArchived(_ session: Session, _ archived: Bool) {
        mutate(archived ? "Archive" : "Unarchive") {
            try $0.setArchived(id: session.id, archived)
            return nil
        }
    }

    public func move(_ session: Session, by delta: Int) {
        mutate("Move") { try $0.move(id: session.id, delta: delta); return nil }
    }

    public func killTmux(_ session: Session) {
        detachIfShowing(session)
        mutate("Kill tmux") { try $0.killTmux(id: session.id); return nil }
    }

    public func delete(_ session: Session) {
        detachIfShowing(session)
        mutate("Delete") { try $0.deleteSession(id: session.id) }
    }

    /// Killing or deleting the session the detail pane is attached to would
    /// leave a control-mode client talking to a tmux session that is about to
    /// stop existing. Clearing the selection unmounts `SessionDetail`, which
    /// tears the client down — the same path selecting another row takes.
    private func detachIfShowing(_ session: Session) {
        if selectedSessionID == session.id { selectedSessionID = nil }
    }
}

/// Every `MoomuxClient` call is a blocking socket read. Running one on the main
/// actor freezes the window for as long as the Go side takes — which, for
/// anything touching git or tmux, is seconds.
private func withoutBlockingTheUI<T: Sendable>(
    _ work: @Sendable @escaping () throws -> T
) async throws -> T {
    try await Task.detached(priority: .userInitiated, operation: work).value
}
