import Foundation

// The wire types for `moomux serve`. They mirror the Go core's JSON, and the
// Go core spells its keys two different ways, which is the single most
// surprising thing in this file:
//
//   - `session.Session` carries `json:"..."` tags, so it is snake_case.
//   - `config.Config` and `config.Project` carry only *TOML* tags, so Go's
//     JSON encoder falls back to the Go **field names** — `Projects`,
//     `BranchPrefix`, `NoWorktree`.
//
// One `keyDecodingStrategy` therefore cannot serve both, and every type below
// declares explicit `CodingKeys` instead. If a decode starts coming back with
// nil fields, check the Go struct's tags before anything else.
//
// These structs are also deliberately *partial* — they decode the fields this
// app shows and ignore the rest. That is safe in one direction only: nothing
// here is ever re-encoded and sent back. Requests carry scalars (see `Args`),
// never a whole session, so a dropped field cannot round-trip a value away.

// MARK: - Agent state

/// What a session's agent is doing. Mirrors `watcher.State`, which crosses the
/// wire as its raw `int` — so the case order is the wire format. Do not
/// reorder.
public enum AgentState: Int, Sendable, CaseIterable {
    case unknown = 0
    case parked
    case done
    case working
    case needsInput

    public var label: String {
        switch self {
        case .unknown: return "unknown"
        case .parked: return "parked"
        case .done: return "done"
        case .working: return "working"
        case .needsInput: return "needs input"
        }
    }

    /// SF Symbol shown in the list and the menu bar.
    public var symbol: String {
        switch self {
        case .unknown: return "questionmark.circle"
        case .parked: return "moon.zzz"
        case .done: return "checkmark.circle"
        case .working: return "circle.dotted"
        case .needsInput: return "exclamationmark.circle.fill"
        }
    }
}

// MARK: - Session

public struct Session: Decodable, Identifiable, Hashable, Sendable {
    public var id: String
    public var project: String
    public var name: String
    public var branch: String
    public var worktreePath: String
    public var tmuxSession: String
    public var createdAt: Date
    public var agent: String?
    public var dangerous: Bool
    public var ticket: String?
    public var pr: String?
    public var prompt: String?
    public var archived: Bool
    public var lastOpened: Date

    /// The agent actually used. Sessions created before moomux had a picker
    /// have no `agent` at all, and the Go side defaults them the same way.
    public var agentName: String { (agent?.isEmpty == false) ? agent! : "claude" }

    /// `last_opened` is a `time.Time`, and Go's `omitempty` does nothing for a
    /// struct — so a never-opened session arrives as the year-1 zero time
    /// rather than as an absent key.
    public var hasBeenOpened: Bool { lastOpened > Wire.goZeroTimeCutoff }

    enum CodingKeys: String, CodingKey {
        case id, project, name, branch, agent, dangerous, ticket, pr, prompt, archived
        case worktreePath = "worktree_path"
        case tmuxSession = "tmux_session"
        case createdAt = "created_at"
        case lastOpened = "last_opened"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        project = try c.decodeIfPresent(String.self, forKey: .project) ?? ""
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        branch = try c.decodeIfPresent(String.self, forKey: .branch) ?? ""
        worktreePath = try c.decodeIfPresent(String.self, forKey: .worktreePath) ?? ""
        tmuxSession = try c.decodeIfPresent(String.self, forKey: .tmuxSession) ?? ""
        createdAt = try c.decodeIfPresent(Date.self, forKey: .createdAt) ?? .distantPast
        agent = try c.decodeIfPresent(String.self, forKey: .agent)
        dangerous = try c.decodeIfPresent(Bool.self, forKey: .dangerous) ?? false
        ticket = try c.decodeIfPresent(String.self, forKey: .ticket)
        pr = try c.decodeIfPresent(String.self, forKey: .pr)
        prompt = try c.decodeIfPresent(String.self, forKey: .prompt)
        archived = try c.decodeIfPresent(Bool.self, forKey: .archived) ?? false
        lastOpened = try c.decodeIfPresent(Date.self, forKey: .lastOpened) ?? .distantPast
    }
}

// MARK: - Config

public struct Project: Decodable, Hashable, Sendable {
    public var kind: String?
    public var repo: String
    public var branchPrefix: String?
    public var baseBranch: String?
    public var agent: String?
    public var dangerous: Bool
    public var noWorktree: Bool
    public var emoji: String?

    public var isPlain: Bool { kind == "plain" }
    public var usesWorktree: Bool { !isPlain && !noWorktree }

    // Go field names, not TOML keys — see the note at the top of this file.
    enum CodingKeys: String, CodingKey {
        case kind = "Kind"
        case repo = "Repo"
        case branchPrefix = "BranchPrefix"
        case baseBranch = "BaseBranch"
        case agent = "Agent"
        case dangerous = "Dangerous"
        case noWorktree = "NoWorktree"
        case emoji = "Emoji"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = try c.decodeIfPresent(String.self, forKey: .kind)
        repo = try c.decodeIfPresent(String.self, forKey: .repo) ?? ""
        branchPrefix = try c.decodeIfPresent(String.self, forKey: .branchPrefix)
        baseBranch = try c.decodeIfPresent(String.self, forKey: .baseBranch)
        agent = try c.decodeIfPresent(String.self, forKey: .agent)
        dangerous = try c.decodeIfPresent(Bool.self, forKey: .dangerous) ?? false
        noWorktree = try c.decodeIfPresent(Bool.self, forKey: .noWorktree) ?? false
        emoji = try c.decodeIfPresent(String.self, forKey: .emoji)
    }
}

public struct Config: Decodable, Sendable {
    public var projects: [String: Project]
    /// The user's manual project order. Names missing from it sort
    /// alphabetically after the ordered ones — same rule as
    /// `config.OrderedProjectNames`.
    public var order: [String]
    public var theme: String?
    public var appearance: String?

    enum CodingKeys: String, CodingKey {
        case projects = "Projects"
        case order = "Order"
        case theme = "Theme"
        case appearance = "Appearance"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        projects = try c.decodeIfPresent([String: Project].self, forKey: .projects) ?? [:]
        order = try c.decodeIfPresent([String].self, forKey: .order) ?? []
        theme = try c.decodeIfPresent(String.self, forKey: .theme)
        appearance = try c.decodeIfPresent(String.self, forKey: .appearance)
    }

    public var orderedProjectNames: [String] {
        var seen = Set<String>()
        var out = order.filter { projects[$0] != nil && seen.insert($0).inserted }
        out.append(contentsOf: projects.keys.filter { !seen.contains($0) }.sorted())
        return out
    }
}

// MARK: - Pull request

/// `prstatus.Info`. No json tags on the Go side, so Go field names again — and
/// `CI` is spelled exactly that.
public struct PRInfo: Decodable, Equatable, Sendable {
    /// OPEN, MERGED, CLOSED
    public var state: String
    /// MERGEABLE, CONFLICTING, UNKNOWN
    public var mergeable: String
    /// PASSING, FAILING, PENDING, NONE
    public var ci: String

    enum CodingKeys: String, CodingKey {
        case state = "State"
        case mergeable = "Mergeable"
        case ci = "CI"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? ""
        mergeable = try c.decodeIfPresent(String.self, forKey: .mergeable) ?? ""
        ci = try c.decodeIfPresent(String.self, forKey: .ci) ?? ""
    }

    /// A one-line summary, lower-cased the way the rest of the UI reads.
    /// Empty when the core knew nothing, so the caller can hide the row.
    public var summary: String {
        var parts: [String] = []
        if !state.isEmpty { parts.append(state.lowercased()) }
        switch ci {
        case "PASSING": parts.append("checks passing")
        case "FAILING": parts.append("checks failing")
        case "PENDING": parts.append("checks running")
        default: break
        }
        // Only worth saying when it is a problem; MERGEABLE is the boring case.
        if mergeable == "CONFLICTING" { parts.append("conflicts") }
        return parts.joined(separator: " · ")
    }
}

// MARK: - Status stream

/// One tick of `watcher.Snapshot`: worktree path → state.
public struct StatusSnapshot: Decodable, Sendable {
    public var states: [String: AgentState]
    public var pollTime: Date
    public var err: String?

    enum CodingKeys: String, CodingKey {
        case states
        case pollTime = "poll_time"
        case err
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // Decoded as Int, not as AgentState: an int this build doesn't know
        // would throw, and one unknown state must not tear down the stream.
        let raw = try c.decodeIfPresent([String: Int].self, forKey: .states) ?? [:]
        states = raw.mapValues { AgentState(rawValue: $0) ?? .unknown }
        pollTime = try c.decodeIfPresent(Date.self, forKey: .pollTime) ?? Date()
        err = try c.decodeIfPresent(String.self, forKey: .err)
    }
}

// MARK: - Coding

public enum Wire {

    /// Anything at or before this is Go's zero `time.Time`
    /// ("0001-01-01T00:00:00Z"), meaning "never", not "in the year 1".
    public static let goZeroTimeCutoff = Date(timeIntervalSince1970: 0)

    public static let encoder = JSONEncoder()

    public static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let raw = try decoder.singleValueContainer().decode(String.self)
            // Lenient on purpose: `last_opened` on a never-opened session is
            // the year-1 zero time, and a strict strategy would fail the whole
            // Sessions call over it.
            return parseTimestamp(raw) ?? .distantPast
        }
        return d
    }()

    /// Parses Go's RFC 3339.
    ///
    /// Go emits up to nine fractional digits; `ISO8601DateFormatter` accepts at
    /// most three and returns nil on the rest. Nothing in this app displays
    /// sub-second time, so the fraction is dropped rather than carrying a
    /// hand-rolled parser. Upgrade path if that ever changes: peel the fraction
    /// off, parse the rest, add it back as a `TimeInterval`.
    public static func parseTimestamp(_ raw: String) -> Date? {
        rfc3339.date(from: stripFractionalSeconds(raw))
    }

    static func stripFractionalSeconds(_ raw: String) -> String {
        guard let dot = raw.firstIndex(of: ".") else { return raw }
        let after = raw.index(after: dot)
        guard let end = raw[after...].firstIndex(where: { !$0.isNumber }) else {
            return String(raw[..<dot])
        }
        return String(raw[..<dot]) + String(raw[end...])
    }

    private static let rfc3339: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    // MARK: Checks

    /// Every assumption above that the Go side could quietly change under us.
    public static func demo() {
        assert(stripFractionalSeconds("2026-09-02T10:11:12.123456789Z") == "2026-09-02T10:11:12Z")
        assert(stripFractionalSeconds("2026-09-02T10:11:12Z") == "2026-09-02T10:11:12Z")
        assert(stripFractionalSeconds("2026-09-02T10:11:12.5+01:00") == "2026-09-02T10:11:12+01:00")
        assert(parseTimestamp("2026-09-02T10:11:12.123456789Z") != nil)
        assert(parseTimestamp("0001-01-01T00:00:00Z").map { $0 <= goZeroTimeCutoff } ?? true)

        // A session as `moomux serve` actually sends one, zero time included.
        let sessionJSON = """
        {"id":"abc","project":"moomux","name":"macos","branch":"alan/macos",
         "worktree_path":"/tmp/wt","tmux_session":"moomux-macos",
         "created_at":"2026-09-02T10:11:12.987654321Z",
         "last_opened":"0001-01-01T00:00:00Z","dangerous":true}
        """
        let s = try! decoder.decode(Session.self, from: Data(sessionJSON.utf8))
        assert(s.id == "abc")
        assert(s.worktreePath == "/tmp/wt")
        assert(s.tmuxSession == "moomux-macos")
        assert(s.dangerous)
        assert(s.agentName == "claude", "a session with no agent field must default to claude")
        assert(!s.hasBeenOpened, "the Go zero time must read as never opened")
        assert(s.createdAt > goZeroTimeCutoff)

        // Config: Go field names, because config.Config has no json tags.
        let configJSON = """
        {"Projects":{"moomux":{"Repo":"/src/moomux","Agent":"codex","Emoji":"🐮"},
                     "site":{"Repo":"/src/site","Kind":"plain"}},
         "Order":["site","moomux"],"Theme":"gruvbox"}
        """
        let cfg = try! decoder.decode(Config.self, from: Data(configJSON.utf8))
        assert(cfg.projects["moomux"]?.repo == "/src/moomux")
        assert(cfg.projects["moomux"]?.emoji == "🐮")
        assert(cfg.projects["site"]?.isPlain == true)
        assert(cfg.projects["moomux"]?.usesWorktree == true)
        assert(cfg.orderedProjectNames == ["site", "moomux"], "Order must win over alphabetical")
        // A project missing from Order sorts alphabetically, after the ordered ones.
        let extraJSON = """
        {"Projects":{"b":{"Repo":"/b"},"a":{"Repo":"/a"},"z":{"Repo":"/z"}},"Order":["z"]}
        """
        let extra = try! decoder.decode(Config.self, from: Data(extraJSON.utf8))
        assert(extra.orderedProjectNames == ["z", "a", "b"])

        // prstatus.Info: Go field names, and CI is capitalised.
        let pr = try! decoder.decode(
            PRInfo.self,
            from: Data(#"{"State":"OPEN","Mergeable":"CONFLICTING","CI":"FAILING"}"#.utf8))
        assert(pr.state == "OPEN" && pr.ci == "FAILING" && pr.mergeable == "CONFLICTING")
        assert(pr.summary == "open · checks failing · conflicts", pr.summary)
        let merged = try! decoder.decode(
            PRInfo.self, from: Data(#"{"State":"MERGED","Mergeable":"UNKNOWN","CI":"NONE"}"#.utf8))
        assert(merged.summary == "merged", merged.summary)
        // A struct Go could not fill in at all must read as "nothing to show",
        // not as a row full of blanks.
        let empty = try! decoder.decode(PRInfo.self, from: Data("{}".utf8))
        assert(empty.summary.isEmpty)

        // Snapshot states are raw ints, and an unfamiliar one must not throw.
        let snapJSON = """
        {"states":{"/tmp/a":4,"/tmp/b":3,"/tmp/c":99},"poll_time":"2026-09-02T10:11:12Z"}
        """
        let snap = try! decoder.decode(StatusSnapshot.self, from: Data(snapJSON.utf8))
        assert(snap.states["/tmp/a"] == .needsInput)
        assert(snap.states["/tmp/b"] == .working)
        assert(snap.states["/tmp/c"] == .unknown, "an unknown state int must degrade, not throw")
        assert(snap.err == nil)
    }
}
