import Foundation

/// The state behind the two forms that ask more than one question, and the
/// rules that decide what they send.
///
/// Pure and here rather than in `@State` next to the controls, because these
/// rules are not the app's to invent: every one of them mirrors a specific
/// piece of `internal/tui`, and a front end that gets one wrong produces
/// sessions and projects that behave differently from the same fields typed
/// into the TUI. Being pure is what lets `demo()` pin them.

// MARK: - New session

/// Mirrors the TUI's new-session form. The picker *contents* come from the
/// core (`AppState.agentNames` / `models(for:)` / `thinking(for:)`); what lives
/// here is which of them is chosen and how the project's config seeds it.
public struct NewSessionForm: Equatable, Sendable {
    public var project = ""
    public var name = ""
    /// A branch that already exists, to resume rather than cut. Either this or
    /// `name` is enough — the core names the session after whichever it gets.
    public var existingBranch = ""
    /// Empty means the project's own base branch.
    public var baseBranch = ""
    /// Empty means "not chosen yet", which only happens for a project with
    /// `prompt_agent` set — and blocks Create until the user picks one.
    public var agent = ""
    public var dangerous = false
    /// The selected entry from the agent's model list. "default" passes no
    /// `--model` flag at all.
    public var model = defaultChoice
    /// The free-text model for an agent with no list worth offering
    /// (opencode). Kept apart from `model` so switching agents back and forth
    /// does not lose either one.
    public var modelText = ""
    public var thinking = defaultChoice
    public var ticket = ""
    public var pr = ""
    public var prompt = ""
    public var autoSubmit = false

    /// The one string that means "pass nothing" in both the model and thinking
    /// lists. It is the core's spelling, not ours — see `agentOptionsTable`.
    public static let defaultChoice = "default"

    public init() {}

    /// Seeds the agent controls from the chosen project, exactly as
    /// `newFormApplyProjectDefaults` does.
    ///
    /// The `prompt_agent` branch is the subtle one: a project that insists on
    /// an explicit agent every time gets *no* preselection **and no inherited
    /// `dangerous`** — the TUI only copies `p.Dangerous` in the else branch.
    /// Inheriting it there would arm `--dangerously-skip-permissions` for an
    /// agent the user has not chosen yet.
    public mutating func applyProjectDefaults(_ p: Project?, agentNames: [String]) {
        guard let p else {
            agent = ""
            dangerous = false
            return
        }
        if p.promptAgent {
            agent = ""
            dangerous = false
        } else {
            // An agent the core no longer offers would leave the picker with no
            // valid selection; claude is the TUI's fallback too.
            agent = agentNames.contains(p.agentName) ? p.agentName : (agentNames.first ?? "claude")
            dangerous = p.dangerous
        }
    }

    /// Keeps the model and thinking selections valid after the agent changes:
    /// the lists differ per agent, and a stale selection would be sent as a
    /// flag value the new agent has never heard of.
    public mutating func clampChoices(models: [String], thinking: [String]) {
        if !models.isEmpty, !models.contains(model) { model = models.first ?? Self.defaultChoice }
        if !thinking.isEmpty, !thinking.contains(self.thinking) {
            self.thinking = thinking.first ?? Self.defaultChoice
        }
    }

    /// What to send as the model: the free-text field for an agent with no
    /// list, the picked entry otherwise. `internal/tui/update.go` makes exactly
    /// this swap on `agent != "opencode"`, keyed off the list being empty here
    /// so a future agent without models needs no change.
    public func modelToSend(hasModelList: Bool) -> String {
        hasModelList ? model : modelText
    }

    /// A name or an existing branch is enough, but not neither — and a project
    /// with `prompt_agent` blocks until an agent is chosen. The same two checks
    /// the TUI's Enter handler makes before it will submit.
    public var canCreate: Bool {
        !project.isEmpty && !(name.isEmpty && existingBranch.isEmpty) && !agent.isEmpty
    }

    static func demo() {
        let names = ["claude", "codex", "opencode"]
        var form = NewSessionForm()
        form.project = "moomux"

        // An ordinary project preselects its agent and inherits its dangerous flag.
        form.applyProjectDefaults(
            Project(repo: "/src", agent: "codex", dangerous: true), agentNames: names)
        assert(form.agent == "codex")
        assert(form.dangerous, "a dangerous project's sessions are dangerous by default")

        // prompt_agent forces a choice — and must not carry the flag over with
        // no agent chosen to apply it to.
        form.applyProjectDefaults(
            Project(repo: "/src", agent: "codex", dangerous: true, promptAgent: true),
            agentNames: names)
        assert(form.agent.isEmpty, "prompt_agent means no preselection")
        assert(!form.dangerous, "dangerous must not be inherited without an agent")
        assert(!form.canCreate, "no agent chosen yet")
        form.agent = "claude"
        form.name = "x"
        assert(form.canCreate)

        // An unset agent is claude, and an agent the core dropped falls back
        // rather than leaving the picker on a value that cannot be selected.
        form.applyProjectDefaults(Project(repo: "/src"), agentNames: names)
        assert(form.agent == "claude")
        form.applyProjectDefaults(Project(repo: "/src", agent: "gone"), agentNames: names)
        assert(form.agent == "claude")

        // Either a name or a branch to resume, but not neither.
        var empty = NewSessionForm()
        empty.project = "moomux"
        empty.agent = "claude"
        assert(!empty.canCreate)
        empty.existingBranch = "alan/x"
        assert(empty.canCreate, "resuming a branch needs no name")

        // Switching agents must not carry a model the new one has never heard of.
        var switching = NewSessionForm()
        switching.model = "opus"
        switching.thinking = "ultrathink"
        switching.clampChoices(models: ["default", "gpt"], thinking: ["default", "high"])
        assert(switching.model == "default" && switching.thinking == "default")
        // A valid selection survives.
        switching.model = "gpt"
        switching.clampChoices(models: ["default", "gpt"], thinking: ["default", "high"])
        assert(switching.model == "gpt")
        // An agent with no model list keeps whatever the picker had — its
        // control is the text field, and that is what gets sent.
        switching.clampChoices(models: [], thinking: ["default"])
        assert(switching.model == "gpt")
        switching.modelText = "anthropic/claude-x"
        assert(switching.modelToSend(hasModelList: false) == "anthropic/claude-x")
        assert(switching.modelToSend(hasModelList: true) == "gpt")
    }
}

// MARK: - Project

/// Mirrors the TUI's new-project / edit-project forms.
///
/// Validation is deliberately only the half a client can check without the
/// core's config in hand: name shape, and that the two required fields are
/// filled. Everything else — the duplicate name, whether the path is a git
/// repo, whether a worktree-mode flip is safe — is the core's, and its refusal
/// is the message the user sees. Two validators would be two rules to drift.
public struct ProjectForm: Equatable, Sendable {
    public var name = ""
    public var repo = ""
    public var baseBranch = ""
    public var branchPrefix = ""
    /// Empty means claude (the core's own default at rest).
    public var agent = ""
    /// The TUI's "ask each time" agent entry: `prompt_agent`, which makes the
    /// agent row of every new-session form start unset.
    public var askAgent = false
    public var dangerous = false
    public var noWorktree = false
    /// Empty means "auto" — the core picks a deterministic glyph.
    public var emoji = ""
    /// nil for a new project; the name being edited otherwise. Editing cannot
    /// change the name (the core keys projects by it), so the field is
    /// read-only there, and it cannot change the kind either.
    public var editing: String?

    public init() {}

    /// The edit form, seeded from what the core has.
    public init(editing name: String, _ p: Project) {
        self.editing = name
        self.name = name
        repo = p.repo
        baseBranch = p.baseBranch ?? ""
        branchPrefix = p.branchPrefix ?? ""
        agent = p.agent ?? ""
        askAgent = p.promptAgent
        dangerous = p.dangerous
        noWorktree = p.noWorktree
        emoji = p.emoji ?? ""
    }

    /// The `config.Project` to send. `kind` is never set from here: the core
    /// decides it (`AddProject` → "git", `AddPlainProject` → "plain") and
    /// `UpdateProject` keeps whatever the project already had.
    public var project: Project {
        Project(repo: repo.trimmed,
                branchPrefix: branchPrefix.trimmed.nilIfEmpty,
                baseBranch: baseBranch.trimmed.nilIfEmpty,
                agent: agent.nilIfEmpty,
                dangerous: dangerous,
                promptAgent: askAgent,
                noWorktree: noWorktree,
                emoji: emoji.trimmed.nilIfEmpty)
    }

    /// nil when the form is submittable, otherwise why it isn't. The strings
    /// match `validateProjectLocked`'s wording so the same mistake reads the
    /// same in both front ends.
    public var problem: String? {
        let name = self.name.trimmed
        if name.isEmpty { return "project name required" }
        if name.contains(where: { " \t/\\".contains($0) }) {
            return "project name cannot contain spaces or slashes"
        }
        if repo.trimmed.isEmpty { return "repo path required" }
        return nil
    }

    static func demo() {
        var form = ProjectForm()
        assert(form.problem == "project name required")
        form.name = "my project"
        assert(form.problem == "project name cannot contain spaces or slashes")
        form.name = "a/b"
        assert(form.problem != nil, "a slash would make a bogus branch and path component")
        form.name = "moomux"
        assert(form.problem == "repo path required")
        form.repo = "  ~/src/moomux  "
        assert(form.problem == nil)
        // Whitespace is trimmed, not sent — a path with a trailing space is a
        // different path, and `~` is the core's to expand.
        assert(form.project.repo == "~/src/moomux")
        // An empty optional field must go over as absent, so the core's own
        // default applies rather than an empty string overwriting it.
        assert(form.project.baseBranch == nil)
        assert(form.project.branchPrefix == nil)
        assert(form.project.emoji == nil, #"empty emoji means "auto", not an empty glyph"#)
        assert(form.project.agent == nil)
        assert(form.project.kind == nil, "the kind is the core's to decide")

        // The edit form round-trips what the core has, including the two flags
        // that are easy to lose.
        let stored = Project(kind: "git", repo: "/src/x", branchPrefix: "alan/",
                             baseBranch: "develop", agent: "codex", dangerous: true,
                             promptAgent: true, noWorktree: true, emoji: "🐮")
        let edit = ProjectForm(editing: "x", stored)
        assert(edit.editing == "x")
        assert(edit.askAgent && edit.dangerous && edit.noWorktree)
        assert(edit.emoji == "🐮" && edit.agent == "codex")
        var sent = edit.project
        sent.kind = stored.kind  // the only field the form drops
        assert(sent == stored, "editing and saving unchanged must send back what it was given")
    }
}

extension String {
    var trimmed: String { trimmingCharacters(in: .whitespacesAndNewlines) }
    var nilIfEmpty: String? { isEmpty ? nil : self }
}
