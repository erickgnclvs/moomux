import Foundation

// tmux's control-mode wire format, as a pure function of the lines that arrive.
// `man tmux`, CONTROL MODE:
//
//   - A command sent on stdin produces exactly one block on stdout: `%begin
//     <time> <number> <flags>`, the output, then `%end` or `%error` with the
//     same three arguments.
//   - Notifications start with `%` and "will never occur inside an output
//     block", which is what makes this parser a two-state machine and not
//     something worse.
//
// Everything here is pure so `demo()` can check it against captured real
// output. The transport that feeds it lives in `ControlClient.swift`.

/// One thing tmux told us.
public enum TmuxEvent: Equatable {
    /// The reply to the n-th command we sent, in order.
    case response(number: Int, lines: [String], isError: Bool)
    case output(pane: String, bytes: [UInt8])
    case layoutChange(window: String, layout: String)
    case sessionWindowChanged(window: String)
    case windowPaneChanged(window: String, pane: String)
    case windowRenamed(window: String, name: String)
    case windowAdd(window: String)
    case windowClose(window: String)
    /// The client is going away — detached, session killed, or an error.
    case exit(reason: String?)
    /// A notification this app does not act on. Kept rather than dropped so a
    /// trace shows what tmux is actually saying.
    case unhandled(String)
}

/// Feed it lines, get events. One instance per connection.
public struct TmuxLineParser {

    private var blockNumber: Int?
    private var blockLines: [String] = []

    public init() {}

    public mutating func parse(line: String) -> TmuxEvent? {
        // Inside a block, only %end/%error can end it; everything else is
        // literal output, including lines that happen to start with '%'.
        if blockNumber != nil {
            if line.hasPrefix("%end ") || line.hasPrefix("%error ") {
                let number = blockNumber ?? 0
                let lines = blockLines
                blockNumber = nil
                blockLines = []
                return .response(number: number, lines: lines, isError: line.hasPrefix("%error"))
            }
            blockLines.append(line)
            return nil
        }

        guard line.hasPrefix("%") else {
            // The pty echoes what we write before tmux puts it in raw mode, and
            // a fresh terminal answers device queries. Neither is our protocol.
            return nil
        }

        let (verb, rest) = split(line)
        switch verb {
        case "%begin":
            blockNumber = Int(field(rest, 1)) ?? 0
            blockLines = []
            return nil
        case "%output":
            // `%output %12 the-escaped-data`; the data may contain spaces, so
            // only the first field is split off.
            let (pane, data) = split(rest)
            return .output(pane: pane, bytes: decodeOutput(data))
        case "%layout-change":
            // `%layout-change @1 <layout> <visible-layout> <flags>`; the second
            // is what the client should draw when a pane is zoomed.
            return .layoutChange(window: field(rest, 0), layout: field(rest, 2))
        case "%session-window-changed":
            return .sessionWindowChanged(window: field(rest, 1))
        case "%window-pane-changed":
            return .windowPaneChanged(window: field(rest, 0), pane: field(rest, 1))
        case "%window-renamed":
            let (window, name) = split(rest)
            return .windowRenamed(window: window, name: name)
        case "%window-add":
            return .windowAdd(window: field(rest, 0))
        case "%window-close":
            return .windowClose(window: field(rest, 0))
        case "%exit":
            return .exit(reason: rest.isEmpty ? nil : rest)
        default:
            return .unhandled(line)
        }
    }

    /// Splits off the first space-separated field, returning it and the rest.
    private func split(_ line: String) -> (String, String) {
        guard let space = line.firstIndex(of: " ") else { return (line, "") }
        return (String(line[..<space]), String(line[line.index(after: space)...]))
    }

    private func field(_ line: String, _ index: Int) -> String {
        let parts = line.split(separator: " ", omittingEmptySubsequences: false)
        return index < parts.count ? String(parts[index]) : ""
    }
}

/// One tmux window, as `list-windows` describes it.
public struct TmuxWindow: Equatable, Identifiable {
    public var id: String       // "@1"
    public var index: Int       // tmux's own numbering, which is what the user sees
    public var name: String
    public var active: Bool
    public var layout: String

    /// Parses `#{window_id} #{window_active} #{window_index} #{window_layout} #{window_name}`.
    /// The name comes last because it is the only field that can contain spaces.
    public static func parse(_ lines: [String]) -> [TmuxWindow] {
        lines.compactMap { line in
            let f = line.split(separator: " ", maxSplits: 4, omittingEmptySubsequences: false)
            guard f.count == 5, let index = Int(f[2]) else { return nil }
            return TmuxWindow(id: String(f[0]), index: index, name: String(f[4]),
                              active: f[1] == "1", layout: String(f[3]))
        }
    }
}

/// Decodes a `%output` payload.
///
/// tmux "escapes non-printable characters and backslash as octal \xxx", so a
/// backslash in the stream is always the start of a three-digit escape — there
/// is no `\\` form to worry about. Anything else is a literal byte.
public func decodeOutput(_ text: String) -> [UInt8] {
    var out: [UInt8] = []
    out.reserveCapacity(text.utf8.count)
    let bytes = Array(text.utf8)
    var i = 0
    while i < bytes.count {
        guard bytes[i] == UInt8(ascii: "\\"), i + 3 < bytes.count else {
            out.append(bytes[i])
            i += 1
            continue
        }
        let digits = bytes[(i + 1)...(i + 3)]
        guard digits.allSatisfy({ $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "7") }) else {
            out.append(bytes[i])
            i += 1
            continue
        }
        let value = digits.reduce(0) { $0 * 8 + Int($1 - UInt8(ascii: "0")) }
        // \400 and up cannot be a byte; treat the backslash as literal rather
        // than truncating into the wrong character.
        if value > 0xFF {
            out.append(bytes[i])
            i += 1
        } else {
            out.append(UInt8(value))
            i += 4
        }
    }
    return out
}

/// Encodes bytes for `send-keys -H`, which takes space-separated hex.
///
/// Hex rather than `send-keys -l <literal>`: a literal argument would need
/// tmux's own quoting rules applied to arbitrary keyboard input, including
/// newlines, quotes and semicolons — the exact bug class that turns a
/// keystroke into a command.
public func hexKeys(_ bytes: some Sequence<UInt8>) -> String {
    bytes.map { String(format: "%02x", $0) }.joined(separator: " ")
}

public enum TmuxProtocolChecks {

    public static func demo() {
        var p = TmuxLineParser()

        // A command block, captured from a real `tmux -CC attach`.
        assert(p.parse(line: "%begin 1788394345 32565138 1") == nil)
        assert(p.parse(line: "%955 100x30") == nil, "block content is not a notification")
        let response = p.parse(line: "%end 1788394345 32565138 1")
        assert(response == .response(number: 32565138, lines: ["%955 100x30"], isError: false),
               "\(String(describing: response))")

        // An error block reports as one, with its output intact.
        assert(p.parse(line: "%begin 1 7 1") == nil)
        assert(p.parse(line: "no such window: @99") == nil)
        assert(p.parse(line: "%error 1 7 1")
            == .response(number: 7, lines: ["no such window: @99"], isError: true))

        // Notifications.
        assert(p.parse(line: "%layout-change @571 2a71,100x30,0,0,955 2a71,100x30,0,0,955 *")
            == .layoutChange(window: "@571", layout: "2a71,100x30,0,0,955"),
               "the *visible* layout is the third field — it is what to draw when zoomed")
        assert(p.parse(line: "%session-window-changed $410 @561")
            == .sessionWindowChanged(window: "@561"))
        assert(p.parse(line: "%window-pane-changed @5 %12")
            == .windowPaneChanged(window: "@5", pane: "%12"))
        assert(p.parse(line: "%window-renamed @5 my session name")
            == .windowRenamed(window: "@5", name: "my session name"))
        assert(p.parse(line: "%window-add @9") == .windowAdd(window: "@9"))
        assert(p.parse(line: "%exit") == .exit(reason: nil))
        assert(p.parse(line: "%exit server exited") == .exit(reason: "server exited"))
        assert(p.parse(line: "%sessions-changed") == .unhandled("%sessions-changed"))

        // Echo from the pty and terminal replies are not protocol.
        assert(p.parse(line: "list-panes -F '#{pane_id}'") == nil)
        assert(p.parse(line: "") == nil)

        // %output: the pane id, then a payload that may contain spaces.
        guard case let .output(pane, bytes)? = p.parse(line: #"%output %3 hi \033[0m there"#) else {
            assertionFailure("expected output"); return
        }
        assert(pane == "%3")
        assert(bytes == Array("hi \u{1b}[0m there".utf8), "\(bytes)")

        // Octal decoding: a backslash is always an escape, so a literal
        // backslash arrives as \134 and must come back as one.
        assert(decodeOutput(#"\134"#) == [0x5C])
        assert(decodeOutput(#"a\015\012b"#) == Array("a\r\nb".utf8))
        assert(decodeOutput("plain") == Array("plain".utf8))
        // Malformed tails must not eat the rest of the line or crash.
        assert(decodeOutput(#"\12"#) == Array(#"\12"#.utf8))
        assert(decodeOutput(#"\999"#) == Array(#"\999"#.utf8))
        assert(decodeOutput(#"\777"#) == Array(#"\777"#.utf8), "\\777 is 511, not a byte")
        assert(decodeOutput(#"\377"#) == [0xFF])
        assert(decodeOutput(#"\"#) == [0x5C])

        // UTF-8 survives: tmux does not escape printable multi-byte characters.
        assert(decodeOutput("🐮") == Array("🐮".utf8))

        assert(hexKeys([0x03]) == "03")
        assert(hexKeys(Array("hi".utf8)) == "68 69")

        // list-windows rows. The layout is fixed-shape, the name is the tail.
        let ws = TmuxWindow.parse([
            "@1 1 0 2a71,100x30,0,0,955 my window",
            "@2 0 3 367d,100x30,0,0,956 zsh",
            "garbage"])
        assert(ws.count == 2, "a malformed row is dropped, not fatal")
        assert(ws[0] == TmuxWindow(id: "@1", index: 0, name: "my window",
                                   active: true, layout: "2a71,100x30,0,0,955"),
               "\(ws[0])")
        assert(ws[1].index == 3 && !ws[1].active)
    }
}
