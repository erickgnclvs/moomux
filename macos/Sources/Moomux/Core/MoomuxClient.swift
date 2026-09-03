import Foundation

/// The Swift half of `internal/ipc`. One JSON request line in, one JSON
/// response line out, connection closed — except `Watch`, which streams
/// snapshot lines until the client hangs up.
///
/// The Go `ipc.Client` is the reference implementation; keep the two honest
/// against each other. Anything this cannot do is a hole in the socket
/// boundary, not a reason to link the Go core.
public final class MoomuxClient: Sendable {

    public enum Failure: Error, LocalizedError {
        /// An error the server returned for a call it understood.
        case server(String)
        case emptyResponse
        case disconnected

        public var errorDescription: String? {
            switch self {
            case let .server(message): return message
            case .emptyResponse: return "the server closed the connection without answering"
            case .disconnected: return "the status stream ended"
            }
        }
    }

    public let socketPath: String

    public init(socketPath: String = MoomuxClient.defaultSocketPath) {
        self.socketPath = socketPath
    }

    /// Mirrors `ipc.DefaultSocket`. `NSHomeDirectory()` is the real home only
    /// because this app is not sandboxed — a sandboxed build would get its
    /// container here and never find the socket.
    public static var defaultSocketPath: String {
        (NSHomeDirectory() as NSString)
            .appendingPathComponent(".local/share/moomux/moomux.sock")
    }

    // MARK: - Wire shapes

    /// The subset of `ipc.Args` this app sends. Everything is optional and
    /// `omitempty` on the Go side, so unset fields simply do not appear.
    ///
    /// Every field has to stay Optional. A non-optional `Bool` would encode
    /// `false` on every call, and `open_terminal:false` on a call that never
    /// meant to say anything about it is a different message.
    struct Args: Encodable {
        var id: String?
        var name: String?
        var project: String?
        var agent: String?
        var branch: String?
        var ticket: String?
        var pr: String?
        var prompt: String?
        var model: String?
        var thinking: String?
        var tmuxSession: String?
        var delta: Int?
        var dangerous: Bool?
        var openTerminal: Bool?
        var autoSubmit: Bool?
        var on: Bool?

        // Three of `ipc.Args`' json tags are snake_case; the rest already match
        // their property names. A missing entry here is invisible in both
        // directions — Go ignores the unknown key and uses the zero value.
        enum CodingKeys: String, CodingKey {
            case id, name, project, agent, branch, ticket, pr, prompt, model, thinking, delta
            case dangerous, on
            case tmuxSession = "tmux_session"
            case openTerminal = "open_terminal"
            case autoSubmit = "auto_submit"
        }
    }

    private struct Request: Encodable {
        let method: String
        var args: Args?
    }

    /// `ipc.Result` plus the error fields, all optional — one union type, the
    /// same trade the Go side makes.
    private struct Response: Decodable {
        var result: CallResult?
        var err: String?
        var code: String?
    }

    struct CallResult: Decodable {
        /// The updated row, from every mutating setter. Discarded today — see
        /// the mutations below.
        var session: Session?
        var sessions: [Session]?
        var strings: [String]?
        var alive: [String: Bool]?
        var cfg: Config?
        var hint: String?
        var ok: Bool?
        var dirty: Bool?
        var unpushed: Bool?
        var files: Int?
        var commits: Int?
        var pr: PRInfo?
    }

    /// What the core can say about a session's worktree and its pull request.
    ///
    /// `known` is the `ok` the Go side returns from all three calls: false for
    /// an unknown session, a plain (non-git) project, or a lookup that failed.
    /// Kept as a flag rather than making the whole thing optional so "we asked
    /// and the answer is nothing" is distinguishable from "we never asked".
    public struct SessionStatus: Equatable, Sendable {
        public var known = false
        public var dirty = false
        public var unpushed = false
        public var filesChanged = 0
        public var unpushedCommits = 0
        public var pr: PRInfo?

        /// Empty when the worktree is clean, so the row can be hidden.
        public var changeSummary: String {
            var parts: [String] = []
            if filesChanged > 0 {
                parts.append("\(filesChanged) file\(filesChanged == 1 ? "" : "s") changed")
            } else if dirty {
                parts.append("uncommitted changes")
            }
            if unpushedCommits > 0 {
                parts.append("\(unpushedCommits) commit\(unpushedCommits == 1 ? "" : "s") unpushed")
            } else if unpushed {
                parts.append("unpushed commits")
            }
            return parts.joined(separator: ", ")
        }
    }

    // MARK: - Calls
    //
    // These block. Call them off the main actor.

    @discardableResult
    private func call(_ method: String, _ args: Args? = nil) throws -> CallResult {
        let socket = try UnixSocket(path: socketPath)
        defer { socket.close() }
        try socket.write(Wire.encoder.encode(Request(method: method, args: args)))
        let data = try socket.readToEnd()
        guard !data.isEmpty else { throw Failure.emptyResponse }
        let response = try Wire.decoder.decode(Response.self, from: data)
        if let err = response.err, !err.isEmpty { throw Failure.server(err) }
        return response.result ?? CallResult()
    }

    public func config() throws -> Config {
        guard let cfg = try call("Config").cfg else { throw Failure.emptyResponse }
        return cfg
    }

    public func sessions() throws -> [Session] {
        try call("Sessions").sessions ?? []
    }

    public func projects() throws -> [String] {
        try call("Projects").strings ?? []
    }

    /// Session id → whether its tmux session is still alive.
    public func tmuxAliveAll() throws -> [String: Bool] {
        try call("TmuxAliveAll").alive ?? [:]
    }

    /// Worktree and PR state for one session.
    ///
    /// Three round trips, and every one of them shells out on the Go side —
    /// `git status`, `git log`, and `gh` over the network for the PR. That is
    /// why this is fetched for the selected session on demand rather than for
    /// every session in the poll loop.
    public func status(id: String) throws -> SessionStatus {
        var status = SessionStatus()
        let worktree = try call("WorktreeStatus", Args(id: id))
        status.known = worktree.ok ?? false
        status.dirty = worktree.dirty ?? false
        status.unpushed = worktree.unpushed ?? false

        let changes = try call("ChangeSummary", Args(id: id))
        if changes.ok == true {
            status.filesChanged = changes.files ?? 0
            status.unpushedCommits = changes.commits ?? 0
        }

        // Only meaningful when a PR is attached; the core returns ok=false
        // otherwise rather than an error.
        let pr = try call("PRStatus", Args(id: id))
        if pr.ok == true { status.pr = pr.pr }
        return status
    }

    /// Attaches the session in the user's terminal. Returns the server's hint —
    /// a user-facing instruction such as "run: tmux attach -t …", not an error.
    @discardableResult
    public func openSession(id: String) throws -> String {
        try call("OpenSession", Args(id: id)).hint ?? ""
    }

    // MARK: - Mutations
    //
    // Ordered as `internal/ipc/server.go` dispatches them, so the two files
    // diff against each other. The `Session` the setters answer with is thrown
    // away on purpose: `AppState.mutate` reloads everything a beat later, and
    // splicing one row in by hand would be a second source of truth.

    /// Kills tmux, removes the worktree and runs the worktree-delete
    /// userscripts. The hint is those scripts' warnings, not an error.
    @discardableResult
    public func deleteSession(id: String) throws -> String {
        try call("DeleteSession", Args(id: id)).hint ?? ""
    }

    /// Leaves the worktree and the session record; only the tmux server side goes.
    public func killTmux(id: String) throws {
        try call("KillTmux", Args(id: id))
    }

    public func rename(id: String, to name: String) throws {
        try call("RenameSession", Args(id: id, name: name))
    }

    /// An empty ticket or PR is how a tag gets cleared, so both are sent as
    /// typed rather than mapped to nil.
    public func setTags(id: String, ticket: String, pr: String) throws {
        try call("SetSessionTags", Args(id: id, ticket: ticket, pr: pr))
    }

    public func setArchived(id: String, _ archived: Bool) throws {
        try call("SetSessionArchived", Args(id: id, on: archived))
    }

    /// ±1, within the project group. A move off either end is a no-op on the Go
    /// side rather than an error.
    public func move(id: String, delta: Int) throws {
        try call("MoveSession", Args(id: id, delta: delta))
    }

    // MARK: - Status stream

    /// Yields a snapshot per watcher tick until the connection drops, then
    /// finishes throwing. Reconnecting is the caller's job — see
    /// `AppState.watchLoop`, which mirrors `ipc.Client.Run`.
    public func watch() -> AsyncThrowingStream<StatusSnapshot, Error> {
        AsyncThrowingStream { continuation in
            let holder = SocketHolder()
            // Closing the fd is what unblocks the read; cancelling the task is
            // not enough, since `bytes.lines` is parked inside read(2).
            continuation.onTermination = { _ in holder.close() }
            Task.detached { [socketPath] in
                do {
                    let socket = try UnixSocket(path: socketPath)
                    guard holder.adopt(socket) else { return continuation.finish() }
                    try socket.write(Wire.encoder.encode(Request(method: "Watch")))
                    for try await line in socket.lines {
                        guard !line.isEmpty else { continue }
                        continuation.yield(
                            try Wire.decoder.decode(StatusSnapshot.self, from: Data(line.utf8)))
                    }
                    // The server only stops sending when it goes away.
                    continuation.finish(throwing: Failure.disconnected)
                } catch {
                    continuation.finish(throwing: error)
                }
            }
        }
    }

    /// Bridges "the stream was torn down" to "close the fd", including the race
    /// where termination lands before the connect finishes.
    private final class SocketHolder: @unchecked Sendable {
        private let lock = NSLock()
        private var socket: UnixSocket?
        private var closed = false

        /// Takes ownership. Returns false if close already happened, in which
        /// case the socket is closed here and the caller should give up.
        func adopt(_ socket: UnixSocket) -> Bool {
            lock.withLock {
                if closed {
                    socket.close()
                    return false
                }
                self.socket = socket
                return true
            }
        }

        func close() {
            let socket: UnixSocket? = lock.withLock {
                closed = true
                defer { self.socket = nil }
                return self.socket
            }
            socket?.close()
        }
    }

    // MARK: Checks

    public static func demo() {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys

        // Unset args must vanish, not serialize as null: the Go side decodes
        // into a struct union where an explicit null is fine but an unexpected
        // key is not, and `Watch` is dispatched on method alone.
        let watch = String(decoding: try! encoder.encode(Request(method: "Watch")), as: UTF8.self)
        assert(watch == #"{"method":"Watch"}"#, watch)

        let open = String(
            decoding: try! encoder.encode(Request(method: "OpenSession", args: Args(id: "s1"))),
            as: UTF8.self)
        assert(open == #"{"args":{"id":"s1"},"method":"OpenSession"}"#, open)

        // An empty tag is how a tag is cleared, so it has to go over the wire
        // as "" rather than being dropped — and every field nobody set must be
        // absent, never null.
        let tags = String(
            decoding: try! encoder.encode(
                Request(method: "SetSessionTags", args: Args(id: "s1", ticket: "T-1", pr: ""))),
            as: UTF8.self)
        assert(tags == #"{"args":{"id":"s1","pr":"","ticket":"T-1"},"method":"SetSessionTags"}"#, tags)
        assert(!tags.contains("name"), "unset fields must vanish")

        // Unarchiving sends `on:false` explicitly. Go's omitempty would drop it
        // and the zero value is the same false, so both spellings are correct —
        // this pins which one we send, so a reader isn't left guessing.
        let unarchive = String(
            decoding: try! encoder.encode(
                Request(method: "SetSessionArchived", args: Args(id: "s1", on: false))),
            as: UTF8.self)
        assert(unarchive == #"{"args":{"id":"s1","on":false},"method":"SetSessionArchived"}"#, unarchive)

        // The three keys `ipc.Args` spells differently from their property
        // names. Checked on a bare Args because no one call sends all three;
        // a missing CodingKeys entry is otherwise silent, and Go would read the
        // zero value while the UI looked merely broken.
        let snake = String(
            decoding: try! encoder.encode(
                Args(tmuxSession: "moomux-x", openTerminal: true, autoSubmit: true)),
            as: UTF8.self)
        assert(snake == #"{"auto_submit":true,"open_terminal":true,"tmux_session":"moomux-x"}"#, snake)

        // A mutating call answers with the updated session, not a bare ok.
        let renamed = #"{"result":{"session":{"id":"p:new","name":"new","project":"p"}}}"#
        let rr = try! Wire.decoder.decode(Response.self, from: Data(renamed.utf8))
        assert(rr.result?.session?.name == "new")

        // A server-side error arrives as a string beside an empty result; it
        // must surface as a thrown error rather than as "no sessions".
        let failed = """
        {"result":{},"err":"tmux: no server running","code":""}
        """
        let response = try! Wire.decoder.decode(Response.self, from: Data(failed.utf8))
        assert(response.err == "tmux: no server running")
        assert(response.result?.sessions == nil)

        // The status calls share one Result union, so an absent field must not
        // read as a zero: `ok:false` with no counts means "don't know", which
        // is different from "clean".
        let statusJSON = #"{"result":{"dirty":true,"ok":true,"files":3,"commits":2}}"#
        let st = try! Wire.decoder.decode(Response.self, from: Data(statusJSON.utf8))
        assert(st.result?.dirty == true)
        assert(st.result?.unpushed == nil, "omitempty means absent, not false")
        assert(st.result?.files == 3 && st.result?.commits == 2)

        var summary = SessionStatus(known: true, dirty: true, unpushed: true,
                                    filesChanged: 3, unpushedCommits: 1)
        assert(summary.changeSummary == "3 files changed, 1 commit unpushed", summary.changeSummary)
        summary = SessionStatus(known: true, dirty: true, unpushed: false)
        assert(summary.changeSummary == "uncommitted changes", summary.changeSummary)
        assert(SessionStatus(known: true).changeSummary.isEmpty, "a clean worktree says nothing")

        let ok = """
        {"result":{"strings":["moomux","site"],"alive":{"a":true,"b":false}}}
        """
        let good = try! Wire.decoder.decode(Response.self, from: Data(ok.utf8))
        assert(good.err == nil)
        assert(good.result?.strings == ["moomux", "site"])
        assert(good.result?.alive == ["a": true, "b": false])
    }
}
