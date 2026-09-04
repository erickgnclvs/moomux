import Foundation

/// A blocking AF_UNIX stream socket.
///
/// Network.framework can reach a unix socket too, but its async connection
/// state machine is a lot of ceremony for "connect, write one line, read the
/// answer" — which is the entire protocol `moomux serve` speaks, since it
/// closes the connection after every call. The one long-lived connection
/// (`Watch`) reads line-delimited JSON, which `FileHandle.bytes.lines` already
/// does.
public final class UnixSocket {

    public enum Failure: Error, LocalizedError {
        case syscall(String, Int32)
        case pathTooLong(String)

        public var errorDescription: String? {
            switch self {
            case let .syscall(what, code):
                return "\(what): \(String(cString: strerror(code)))"
            case let .pathTooLong(path):
                return "socket path is too long for sockaddr_un: \(path)"
            }
        }
    }

    private let handle: FileHandle
    private let lock = NSLock()
    private var closed = false

    public init(path: String) throws {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw Failure.syscall("socket", errno) }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        // sun_path is a fixed 104-byte tuple. A longer path would silently
        // truncate into a connect to some *other* socket, so refuse it.
        guard bytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            Darwin.close(fd)
            throw Failure.pathTooLong(path)
        }
        // sockaddr_un() zero-fills, so copying count bytes leaves the NUL.
        withUnsafeMutableBytes(of: &addr.sun_path) { $0.copyBytes(from: bytes) }

        var rc: Int32 = -1
        withUnsafePointer(to: &addr) { raw in
            raw.withMemoryRebound(to: sockaddr.self, capacity: 1) { sa in
                rc = connect(fd, sa, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0 else {
            let code = errno
            Darwin.close(fd)
            throw Failure.syscall("connect \(path)", code)
        }
        handle = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
    }

    public func write(_ data: Data) throws {
        try handle.write(contentsOf: data)
    }

    /// Reads until the peer closes. The server closes after one response, so
    /// this is the whole answer to a one-shot call.
    public func readToEnd() throws -> Data {
        try handle.readToEnd() ?? Data()
    }

    /// Line-delimited reads, for the `Watch` stream. Go's `json.Encoder.Encode`
    /// terminates every object with a newline, which is what makes this work.
    public var lines: AsyncLineSequence<FileHandle.AsyncBytes> {
        handle.bytes.lines
    }

    /// Closing is how a read in progress is cancelled — `bytes.lines` is parked
    /// inside `read(2)` and will not notice task cancellation on its own. Safe
    /// to call from another thread and more than once.
    public func close() {
        lock.withLock {
            guard !closed else { return }
            closed = true
            try? handle.close()
        }
    }
}
