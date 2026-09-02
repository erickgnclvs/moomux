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

    public private(set) var connection: Connection = .connecting
    /// Set when the status stream drops. Without it the last states keep
    /// rendering, which reads as live rather than frozen.
    public private(set) var statusError: String?

    // MARK: UI state

    public var selectedSessionID: Session.ID?
    public var showArchived = false
    /// Whatever the last `OpenSession` told the user to do, if anything.
    public var hint: String?

    public let client: MoomuxClient

    private var tasks: [Task<Void, Never>] = []

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
                    states = AppState.merge(
                        states, snapshot.states,
                        live: Set(sessions.map(\.worktreePath)))
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
    }

    // MARK: Actions

    public func open(_ session: Session) {
        Task {
            do {
                let hint = try await withoutBlockingTheUI { [client] in
                    try client.openSession(id: session.id)
                }
                self.hint = hint.isEmpty ? nil : hint
                await refresh()
            } catch {
                connection = .down(error.localizedDescription)
            }
        }
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
