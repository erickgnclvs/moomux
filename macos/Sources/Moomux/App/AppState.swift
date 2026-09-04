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
    /// Which agents the core can launch, and what to offer in a model or
    /// thinking-level picker for each. Fetched once — it is a static table in
    /// `internal/app`, served over the socket precisely so a front end never
    /// keeps its own copy to drift.
    public private(set) var agentOptions: [AgentOption] = []
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
    /// Replace the detail column with a read-only snapshot of every live
    /// session. Snapshots and not clients: an attached client sizes the shared
    /// tmux window for everyone, so a grid of six would letterbox six real
    /// sessions. Clicking a tile selects it; Attach is still the full-size,
    /// deliberate thing. No snapshot text lives in the store — see `SessionGrid`.
    public var showGrid = false
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
        /// Name, agent and the dangerous flag together — `internal/tui`'s
        /// edit-session form is one dialog over the same three fields, and
        /// `RenameSession` no-ops on an unchanged name, so there is nothing to
        /// gain from splitting them into two sheets and two shortcuts.
        case edit(Session)
        case tags(Session)
        /// Settings *and* project management, on two tabs.
        ///
        /// The project add/edit form is deliberately not a case here: it is
        /// opened from inside this sheet, and one `.sheet(item:)` modifier can
        /// only present one thing — a nested form needs its own presentation on
        /// the settings sheet itself. Which also means there is exactly one
        /// place projects are managed from, and one host for the dialogs that
        /// managing them raises.
        case settings
        public var id: Self { self }
    }
    public var sheet: Sheet?
    /// The session the delete confirmation is asking about. Its own field, not
    /// a `Sheet` case: an alert is not a sheet and cannot share the modifier.
    public var pendingDelete: Session?
    /// The project the remove confirmation is asking about. Same reason.
    public var pendingProjectDelete: String?

    /// A project whose repo path turned out not to be a git repository. Not an
    /// error — a question, and these are its two answers (`initProject` /
    /// `addPlainProject`), which is exactly what the TUI's init-choice dialog
    /// asks. Carries the form's own `Project` so answering does not need the
    /// sheet to still be open.
    public struct PendingProject: Identifiable, Hashable, Sendable {
        public let name: String
        public let project: Project
        public var id: String { name }
    }
    public var pendingProjectInit: PendingProject?
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

    /// `review(_:)` needs a live tmux session to add a window to, and something
    /// to diff. A no-worktree git project still diffs fine, so the predicate is
    /// `isPlain` and not `usesWorktree`.
    public func canReview(_ session: Session) -> Bool {
        isAlive(session) && config?.projects[session.project]?.isPlain != true
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

    /// Manual reordering is meaningless while the core sorts by last-opened —
    /// the next open would undo it. The TUI disables shift+↑↓ for the same
    /// reason rather than letting a move silently do nothing.
    public var canReorder: Bool { config?.sortRecentFirst != true }

    // MARK: Agent table
    //
    // These three mirror `internal/tui`'s `agentNames` / `modelNamesFor` /
    // `thinkingNamesFor` exactly, fallbacks included, over the same table the
    // TUI reads. The point is that neither side owns a copy of the *contents*.

    public var agentNames: [String] {
        // A core that answered with nothing at all would otherwise leave every
        // picker empty and unselectable; claude is what the TUI falls back to.
        agentOptions.isEmpty ? ["claude"] : agentOptions.map(\.name)
    }

    /// The model choices for `agent`, falling back to claude's list. Empty
    /// means the agent has no fixed list worth offering (opencode) and the
    /// control should be a free-text field instead of a picker.
    public func models(for agent: String) -> [String] {
        if let own = agentOptions.first(where: { $0.name == agent })?.models, !own.isEmpty {
            return own
        }
        return agentOptions.first { $0.name == "claude" }?.models ?? []
    }

    /// The thinking-level choices for `agent`, falling back to claude's.
    public func thinking(for agent: String) -> [String] {
        agentOptions.first { $0.name == agent }?.thinking
            ?? agentOptions.first { $0.name == "claude" }?.thinking
            ?? []
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
            Task { [weak self] in await self?.loadAgentOptions() },
        ]
    }

    /// Retries until it lands: the app can start before `moomux serve` does,
    /// and without the table the new-session form has no pickers at all. Once
    /// fetched it is never refetched — the Go side's table is a `var` in the
    /// binary, so it cannot change without a restart of the core.
    private func loadAgentOptions() async {
        while !Task.isCancelled, agentOptions.isEmpty {
            if let options = try? await withoutBlockingTheUI({ [client] in
                try client.agentOptions()
            }), !options.isEmpty {
                agentOptions = options
                return
            }
            try? await Task.sleep(for: .seconds(2))
        }
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

    /// The quiet half of the notification surface: no sound, no banner, just a
    /// count that is there when you look. It is **not** free of authorization —
    /// macOS drops `badgeLabel` on the floor unless the app has the badge
    /// permission, which is why `Notifier` asks for `.badge` alongside `.alert`.
    /// `nil` rather than "0" — a zero badge is still a badge.
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

        // The review window's command line. Measured against a real worktree:
        // the merge-base form catches committed *and* uncommitted work, the
        // fallbacks fire on exit 128 from an unresolvable ref, and the status
        // line is what makes an untracked file and a clean tree both visible.
        let script = reviewScript(base: "main")
        assert(script.hasPrefix("git diff --merge-base 'origin/main' 2>/dev/null"
                                + " || git diff --merge-base 'main' 2>/dev/null"
                                + " || git diff HEAD; git status --short --branch;"), script)
        assert(script.hasSuffix(#"exec "${SHELL:-/bin/sh}""#), script)
        // A branch name out of the user's config is interpolated into a shell
        // command, so it is quoted rather than trusted.
        assert(reviewScript(base: "a'b").contains(#"'origin/a'\''b'"#), reviewScript(base: "a'b"))

        // The thinking level reaches claude and opencode as a prompt phrase,
        // because neither has a launch-time flag for it. Getting this wrong is
        // invisible: the session is created either way and simply does not
        // think as hard as the user asked it to.
        assert(thinkingPromptPrefix("ultrathink") == "ultrathink: ")
        assert(thinkingPromptPrefix("default").isEmpty, #""default" means prepend nothing"#)
        assert(thinkingPromptPrefix("").isEmpty)

        assert(promptExtras(ticket: "T-1", pr: "").isEmpty == false)
        assert(promptExtras(ticket: "T-1", pr: "http://p/1") == "Ticket: T-1\nPR: http://p/1")
        assert(promptExtras(ticket: "", pr: "http://p/1") == "PR: http://p/1")
        assert(promptExtras(ticket: "", pr: "").isEmpty, "no tags adds no lines")

        assert(joinHint("a", "b") == "a\nb")
        assert(joinHint("", "b") == "b" && joinHint("a", "") == "a")

        // The agent table's fallbacks, which decide what every picker in the
        // new-session form contains. Same rules as `internal/tui`'s
        // agentNames / modelNamesFor / thinkingNamesFor.
        MainActor.assumeIsolated {
            let app = AppState()
            // A core that never answered must still leave the form usable.
            assert(app.agentNames == ["claude"], "\(app.agentNames)")
            assert(app.models(for: "claude").isEmpty)

            app.agentOptions = [
                AgentOption(name: "claude", models: ["default", "opus"],
                            thinking: ["default", "ultrathink"]),
                AgentOption(name: "codex", models: ["default", "gpt"], thinking: ["default", "high"]),
                AgentOption(name: "opencode", thinking: ["default", "think"]),
            ]
            assert(app.agentNames == ["claude", "codex", "opencode"])
            assert(app.models(for: "codex") == ["default", "gpt"])
            // opencode has no list of its own, so it falls back to claude's —
            // which is why its model control is a free-text field in the form
            // rather than this picker.
            assert(app.models(for: "opencode") == ["default", "opus"])
            assert(app.models(for: "nonesuch") == ["default", "opus"], "unknown agents fall back")
            assert(app.thinking(for: "codex") == ["default", "high"])
            assert(app.thinking(for: "nonesuch") == ["default", "ultrathink"])

            // Manual reordering is off while the core sorts by last-opened.
            assert(app.canReorder, "no config yet must not disable reordering")
        }

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

    /// Creates a session, then does the four things the TUI does *after*
    /// `CreateSession` returns — and this method exists rather than the sheet
    /// calling the client directly because every one of them is a semantic the
    /// core does not apply and a second front end would otherwise get wrong:
    ///
    /// 1. A changed auto-submit toggle is remembered as the new default, best
    ///    effort — the TUI writes it before creating, and a failed config save
    ///    must not block a session.
    /// 2. A PR tag is set afterwards, because `CreateSession` takes a ticket
    ///    and not a PR.
    /// 3. The thinking level reaches claude and opencode as a phrase prepended
    ///    to the first prompt — neither has a launch-time flag for it. codex is
    ///    the exception: its level is a real `-c model_reasoning_effort` value
    ///    that the core already applied, so prepending it there would say it
    ///    twice.
    /// 4. Ticket and PR are appended to the prompt as their own lines, so the
    ///    agent knows what it is working on.
    ///
    /// `dangerous` is the caller's to compute: `App.CreateSession` takes it as
    /// a plain bool with no fallback to the project, so leaving it out creates
    /// sessions in a `dangerous = true` project *without*
    /// `--dangerously-skip-permissions`. Everything else may be "" for "let the
    /// core default it".
    public func create(project: String, name: String, existingBranch: String = "",
                       baseBranch: String = "", agent: String = "", dangerous: Bool,
                       model: String = "", thinking: String = "", ticket: String = "",
                       pr: String = "", prompt: String, autoSubmit: Bool = false) {
        let rememberAutoSubmit = autoSubmit != (config?.autoSubmitDefault ?? false)
        mutate("Creating session") { client in
            if rememberAutoSubmit {
                // Best effort, exactly as the TUI treats it: remembering a
                // toggle is not worth failing a session creation over.
                try? client.setAutoSubmitDefault(autoSubmit)
            }
            let (session, created) = try client.createSession(
                project: project, name: name, agent: agent, existingBranch: existingBranch,
                ticket: ticket, dangerous: dangerous, baseBranch: baseBranch,
                model: model, thinking: thinking)
            // The worktree and the tmux session exist from here on, so nothing
            // below may be reported as a failed creation — it would invite a
            // retry that answers "session already exists".
            var hint = created
            if !pr.isEmpty {
                do {
                    try client.setTags(id: session.id, ticket: ticket, pr: pr)
                } catch {
                    hint = joinHint(hint, "couldn't set PR tag: \(error.localizedDescription)")
                }
            }
            guard !prompt.isEmpty else { return hint }
            var prompt = prompt
            if agent != "codex" {
                prompt = AppState.thinkingPromptPrefix(thinking) + prompt
            }
            let extras = AppState.promptExtras(ticket: ticket, pr: pr)
            if !extras.isEmpty { prompt += "\n\n" + extras }
            // Recorded on the session as well as typed into the pane, so the
            // detail pane's "First prompt" has something to show. Best-effort,
            // exactly as the TUI treats it.
            try? client.setPrompt(id: session.id, prompt: prompt)
            do {
                try client.startFirstPrompt(tmuxSession: session.tmuxSession,
                                            prompt: prompt, autoSubmit: autoSubmit)
            } catch {
                // The worktree and the tmux session already exist by now. A
                // prompt that never landed is a hint, not a failed creation —
                // reporting it as one would tell the user to try again and
                // hand them "session already exists".
                return joinHint(hint, "couldn't send first prompt: \(error.localizedDescription)")
            }
            return hint
        }
    }

    /// `internal/tui`'s `thinkingPromptPrefix`: there is no CLI flag for
    /// extended-thinking effort on claude or opencode, so the level goes in as
    /// the same magic words a user would type. "default" prepends nothing.
    nonisolated static func thinkingPromptPrefix(_ level: String) -> String {
        (level.isEmpty || level == "default") ? "" : level + ": "
    }

    /// `internal/tui`'s `newFormPromptExtras`: the ticket and PR as their own
    /// lines under the first prompt, so the agent is told what it is on.
    nonisolated static func promptExtras(ticket: String, pr: String) -> String {
        var lines: [String] = []
        if !ticket.isEmpty { lines.append("Ticket: " + ticket) }
        if !pr.isEmpty { lines.append("PR: " + pr) }
        return lines.joined(separator: "\n")
    }

    public func open(_ session: Session) {
        mutate("Open") { try $0.openSession(id: session.id) }
    }

    /// Reviewing a session's changes opens `git diff` in a new tmux **window**
    /// of that session, rather than rendering a patch natively — see the
    /// "Deliberately not done" note for why there is no patch viewer here.
    ///
    /// Not through `mutate`: this never touches the socket. `tmux new-window`
    /// from a second client is also the one path that works whether or not the
    /// app is attached, so there is no branch on `controlClient`. Attached, the
    /// window arrives as a tab; detached, it is waiting in the user's own
    /// terminal, which is what the hint says.
    public func review(_ session: Session) {
        guard let tmux = ToolPath.find("tmux") else {
            actionError = "Review failed: can't find a tmux binary."
            return
        }
        let base = config?.projects[session.project]?.baseBranch ?? "main"
        let script = AppState.reviewScript(base: base)
        let target = "\(session.tmuxSession):review"
        let create = ["new-window", "-t", session.tmuxSession,
                      "-c", session.worktreePath,
                      // -n also turns automatic-rename off for the window, so the
                      // tab keeps saying "review" and not "zsh".
                      "-n", "review", script]
        // Reviewing twice reuses the window rather than stacking a second one
        // called "review" with nothing to tell it from the first — the old one
        // holds a finished diff, which is exactly what is being replaced.
        // `respawn-window` and not kill-then-create: killing the last window of
        // a session kills the session. It does not select, hence the second
        // command; `new-window` does.
        let reuse = ["respawn-window", "-k", "-t", target,
                     "-c", session.worktreePath, script]
        Task {
            do {
                try await withoutBlockingTheUI {
                    do {
                        try ToolPath.run(tmux, reuse)
                        try ToolPath.run(tmux, ["select-window", "-t", target])
                    } catch {
                        try ToolPath.run(tmux, create)  // no review window yet
                    }
                }
                hint = "Opened a review window in \(session.tmuxSession)."
            } catch {
                failed("Review", error)
            }
        }
    }

    /// The shell line a review window runs.
    ///
    /// `git diff --merge-base` is everything not yet on the base branch —
    /// commits since the merge base *and* uncommitted work — in one command.
    /// `origin/` first because a local base branch goes stale in a worktree
    /// checkout, then the local one, then a plain `HEAD` diff for a project
    /// with neither. The `git status` line is not decoration: untracked files
    /// are invisible to every diff, an agent's new files are usually untracked,
    /// and `--branch` guarantees at least one line of output so a clean
    /// worktree reads as "nothing to review" rather than as a window that
    /// failed to run anything.
    ///
    /// No `--color` and no `| less`: output goes straight to a tty, so git
    /// colours and pages it with the user's own pager — a configured `delta`
    /// is honoured, which is most of the argument for reviewing here at all.
    /// It ends in a shell so the window survives the pager and is somewhere to
    /// run `git add -p` from.
    nonisolated static func reviewScript(base: String) -> String {
        let quoted = { (ref: String) in
            "'" + ref.replacingOccurrences(of: "'", with: #"'\''"#) + "'"
        }
        return "git diff --merge-base \(quoted("origin/" + base)) 2>/dev/null"
            + " || git diff --merge-base \(quoted(base)) 2>/dev/null"
            + " || git diff HEAD; git status --short --branch;"
            + #" exec "${SHELL:-/bin/sh}""#
    }

    /// Both halves of the edit-session form, in the TUI's order: the rename
    /// first (it no-ops when the name is unchanged, and fails loudly on a
    /// collision, which must stop the agent change too), then the agent.
    public func edit(_ session: Session, name: String, agent: String, dangerous: Bool) {
        mutate("Save session") { client in
            try client.rename(id: session.id, to: name)
            guard agent != session.agentName || dangerous != session.dangerous else { return nil }
            try client.setAgent(id: session.id, agent: agent, dangerous: dangerous)
            return "\(name) will launch \(agent) next time it's opened."
        }
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

    // MARK: Projects

    /// The core insists the repo path already be a git repository, and answers
    /// "it isn't" as a sentinel rather than a plain failure — so this is the
    /// one write that can end in a question. `pendingProjectInit` is that
    /// question; `initProject` and `addPlainProject` are its answers.
    public func addProject(name: String, _ project: Project) {
        Task {
            busy = "Add project"
            defer { busy = nil }
            do {
                try await withoutBlockingTheUI { [client] in
                    try client.addProject(name: name, project)
                }
                await refresh()
            } catch MoomuxClient.Failure.notGitRepo {
                pendingProjectInit = PendingProject(name: name, project: project)
            } catch {
                failed("Add project", error)
            }
        }
    }

    /// `mkdir -p`, `git init`, one empty commit, then the project is saved as a
    /// git one — the "i" answer in the TUI's init-choice dialog.
    public func initProject(name: String, _ project: Project) {
        mutate("Init repo") { client in
            try client.initProjectAndAdd(name: name, project)
            return "Initialized a git repo at \(project.repo) and added \(name)."
        }
    }

    /// The "s" answer: no git at all, so no worktrees and no branches — every
    /// session runs in the folder itself.
    public func addPlainProject(name: String, _ project: Project) {
        mutate("Add project") { client in
            try client.addPlainProject(name: name, project)
            return "Added \(name) as a plain folder — no worktrees or branches."
        }
    }

    public func updateProject(name: String, _ project: Project) {
        mutate("Save project") { try $0.updateProject(name: name, project); return nil }
    }

    /// Config only — the repository on disk is untouched. The core refuses
    /// while the project still has sessions, archived ones included, and that
    /// refusal is the message the user sees.
    public func removeProject(name: String) {
        mutate("Remove project") { try $0.removeProject(name: name); return nil }
    }

    public func moveProject(name: String, by delta: Int) {
        mutate("Move project") { try $0.moveProject(name: name, delta: delta); return nil }
    }

    // MARK: Settings
    //
    // Each is one socket call that rewrites one field of the shared config, so
    // a change here shows up in the TUI's settings screen and vice versa.

    public func setAutoSubmitDefault(_ on: Bool) {
        mutate("Save setting") { try $0.setAutoSubmitDefault(on); return nil }
    }

    public func setSortRecentFirst(_ on: Bool) {
        mutate("Save setting") { try $0.setSortRecentFirst(on); return nil }
    }

    public func setAutoTmux(_ on: Bool) {
        mutate("Save setting") { try $0.setAutoTmux(on); return nil }
    }

    public func setCompactDetail(_ on: Bool) {
        mutate("Save setting") { try $0.setCompactDetail(on); return nil }
    }

    /// The TUI's palette, edited from here because it is one config file. This
    /// app draws itself with semantic colors and is unaffected by either value.
    public func setTheme(_ theme: String, appearance: String) {
        mutate("Save theme") { try $0.setTheme(theme, appearance: appearance); return nil }
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

/// Two notes on one line, dropping whichever half is empty. `internal/app`'s
/// `joinHint`, for the same reason: a create that succeeded but could not send
/// its prompt has two things to say and one place to say them.
func joinHint(_ a: String, _ b: String) -> String {
    if a.isEmpty { return b }
    if b.isEmpty { return a }
    return a + "\n" + b
}

/// Every `MoomuxClient` call is a blocking socket read. Running one on the main
/// actor freezes the window for as long as the Go side takes — which, for
/// anything touching git or tmux, is seconds.
private func withoutBlockingTheUI<T: Sendable>(
    _ work: @Sendable @escaping () throws -> T
) async throws -> T {
    try await Task.detached(priority: .userInitiated, operation: work).value
}
