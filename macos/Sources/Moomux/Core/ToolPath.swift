import Foundation

/// Finds a command-line tool the way a terminal would — which a GUI app cannot
/// take for granted.
///
/// An app launched from Finder or Dock inherits launchd's `PATH`
/// (`/usr/bin:/bin:/usr/sbin:/sbin`), not the one in your shell profile, so
/// Homebrew's `tmux` is simply invisible to it. Nothing about this fails
/// loudly: `Process` just reports that the executable does not exist. The plan
/// doc calls this out as the thing that bites every Mac dev tool that shells
/// out, and this is the answer to it.
public enum ToolPath {

    private static let lock = NSLock()
    private nonisolated(unsafe) static var cache: [String: String?] = [:]

    /// The usual install prefixes, searched after `PATH`. Homebrew on Apple
    /// silicon and on Intel, MacPorts, then the system.
    ///
    /// ponytail: a fixed list, not a login-shell probe. It covers Homebrew and
    /// MacPorts, which is nearly everyone. If someone's tmux lives under nix or
    /// asdf, run `$SHELL -lc 'command -v tmux'` once and cache that instead —
    /// correct for everybody, at ~300ms and a subprocess.
    public static let wellKnownDirectories = [
        "/opt/homebrew/bin",
        "/usr/local/bin",
        "/opt/local/bin",
        "/usr/bin",
        "/bin",
    ]

    public static func find(_ name: String) -> String? {
        lock.withLock {
            if let cached = cache[name] { return cached }
            let found = search(name)
            cache[name] = found
            return found
        }
    }

    /// The pure half, so it can be checked without touching the file system.
    static func search(
        _ name: String,
        path: String? = ProcessInfo.processInfo.environment["PATH"],
        directories: [String] = wellKnownDirectories,
        isExecutable: (String) -> Bool = defaultIsExecutable
    ) -> String? {
        // PATH first: when the app is launched from a terminal (`swift run`,
        // `open` from a shell) it is the richer answer, and it lets someone
        // point the app at a specific build.
        let fromPath = (path ?? "").split(separator: ":").map(String.init).filter { !$0.isEmpty }
        return (fromPath + directories)
            .map { $0.hasSuffix("/") ? $0 + name : $0 + "/" + name }
            .first(where: isExecutable)
    }

    static func defaultIsExecutable(_ path: String) -> Bool {
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &isDirectory),
              !isDirectory.boolValue else { return false }
        return FileManager.default.isExecutableFile(atPath: path)
    }

    /// Runs a tool to completion and hands back its stdout, throwing whatever it
    /// said on stderr — so a tmux refusal ("can't find session") reaches the
    /// alert in tmux's own words. `Failure.server` is simply the one error type
    /// here that carries a message.
    ///
    /// Blocking, like every `MoomuxClient` call: run it off the main actor.
    /// stdout is drained before the wait because a full pipe buffer would
    /// deadlock; stderr is read after, which would deadlock in turn if a tool
    /// ever wrote 64KB of complaints — tmux and git do not.
    @discardableResult
    public static func run(_ path: String, _ args: [String]) throws -> String {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = args
        let out = Pipe(), errors = Pipe()
        process.standardOutput = out
        process.standardError = errors
        try process.run()
        let stdout = out.fileHandleForReading.readDataToEndOfFile()
        let stderr = String(decoding: errors.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw MoomuxClient.Failure.server(
                stderr.isEmpty ? "\(URL(fileURLWithPath: path).lastPathComponent) exited "
                    + "\(process.terminationStatus)" : stderr)
        }
        return String(decoding: stdout, as: UTF8.self)
    }

    // MARK: Checks

    public static func demo() {
        let present: Set<String> = ["/opt/homebrew/bin/tmux", "/somewhere/odd/tmux"]
        let exists: (String) -> Bool = { present.contains($0) }

        // Falls through PATH to the well-known prefixes — the Finder-launched case.
        assert(search("tmux", path: "/usr/bin:/bin", isExecutable: exists)
            == "/opt/homebrew/bin/tmux")

        // PATH wins when it has an answer, so a shell-launched app uses the
        // same binary the shell would.
        assert(search("tmux", path: "/somewhere/odd", isExecutable: exists)
            == "/somewhere/odd/tmux")

        // Empty PATH entries are not the root directory.
        assert(search("tmux", path: "::", isExecutable: exists) == "/opt/homebrew/bin/tmux")
        assert(search("tmux", path: nil, isExecutable: exists) == "/opt/homebrew/bin/tmux")
        assert(search("tmux", path: "/trailing/", directories: [],
                      isExecutable: { $0 == "/trailing/tmux" }) == "/trailing/tmux")

        // Missing is nil, not a path that happens not to exist.
        assert(search("nonesuch", path: "/usr/bin", isExecutable: exists) == nil)
    }
}
