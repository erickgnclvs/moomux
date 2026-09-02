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
    struct Args: Encodable {
        var id: String?
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
        var sessions: [Session]?
        var strings: [String]?
        var alive: [String: Bool]?
        var cfg: Config?
        var hint: String?
        var ok: Bool?
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

    /// Attaches the session in the user's terminal. Returns the server's hint —
    /// a user-facing instruction such as "run: tmux attach -t …", not an error.
    @discardableResult
    public func openSession(id: String) throws -> String {
        try call("OpenSession", Args(id: id)).hint ?? ""
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

        // A server-side error arrives as a string beside an empty result; it
        // must surface as a thrown error rather than as "no sessions".
        let failed = """
        {"result":{},"err":"tmux: no server running","code":""}
        """
        let response = try! Wire.decoder.decode(Response.self, from: Data(failed.utf8))
        assert(response.err == "tmux: no server running")
        assert(response.result?.sessions == nil)

        let ok = """
        {"result":{"strings":["moomux","site"],"alive":{"a":true,"b":false}}}
        """
        let good = try! Wire.decoder.decode(Response.self, from: Data(ok.utf8))
        assert(good.err == nil)
        assert(good.result?.strings == ["moomux", "site"])
        assert(good.result?.alive == ["a": true, "b": false])
    }
}
