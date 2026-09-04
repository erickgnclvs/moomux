import SwiftUI

/// Settings and project management: the two write surfaces that are about the
/// configuration rather than about one session.
///
/// A sheet and not a `Settings` scene, so it is reachable the same way every
/// other form here is (`app.sheet`), and so the Session menu and the toolbar
/// can open it with a row selected or not. ⌘, still opens it — the shortcut is
/// what people reach for, the scene is not.
struct SettingsSheet: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var tab = Tab.projects
    /// The project form, presented *from here* rather than through
    /// `app.sheet`: this view is already what that modifier is showing, and one
    /// `.sheet(item:)` presents one thing. Its own state, because it is the
    /// only surface that opens it.
    @State private var editingProject: ProjectTarget?

    enum Tab: Hashable { case projects, preferences }

    /// nil `name` is "add"; a name is "edit that one". A wrapper because
    /// `.sheet(item:)` wants something `Identifiable` and `String?` isn't.
    struct ProjectTarget: Identifiable, Hashable {
        let name: String?
        var id: String { name ?? "" }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Picker("", selection: $tab) {
                Text("Projects").tag(Tab.projects)
                Text("Preferences").tag(Tab.preferences)
            }
            .pickerStyle(.segmented)
            .labelsHidden()

            if app.config == nil {
                // Every control on both tabs writes to the core. Rendering them
                // against a config we never got would show defaults that are
                // not the user's and write them back on the first click.
                ContentUnavailableView {
                    Label("Not connected", systemImage: "bolt.horizontal.circle")
                } description: {
                    Text("Settings and projects come from the core — start `moomux serve`.")
                }
            } else {
                switch tab {
                case .projects: ProjectsPane(editing: $editingProject)
                case .preferences: PreferencesPane()
                }
            }

            // Inline, not an alert. `RootView`'s "Couldn't do that" alert is
            // bound on a view this sheet covers, so SwiftUI defers it until the
            // sheet closes — which is how "Remove" on a project that still has
            // sessions came to look like a button that does nothing. Measured.
            // A second `.alert` on the same state would race the first for the
            // presentation; a row cannot.
            if let error = app.actionError {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: "exclamationmark.triangle").foregroundStyle(.orange)
                    Text(error)
                        .font(.caption)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer()
                    Button {
                        app.actionError = nil
                    } label: {
                        Image(systemName: "xmark")
                    }
                    .buttonStyle(.borderless)
                    .help("Dismiss")
                }
            }

            HStack {
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 520, height: 460)
        // The row above already showed it. Left set, `RootView`'s deferred
        // alert fires the moment this sheet closes and says the same thing a
        // second time — measured, and it reads as the action having failed
        // twice.
        .onDisappear { app.actionError = nil }
        .sheet(item: $editingProject) { target in
            ProjectSheet(editing: target.name)
        }
        // Hosted here and nowhere else, for the same reason the form is: the
        // add that raises them can only have come from this sheet, and an alert
        // bound on a view that a sheet is covering never appears.
        .alert("Path is not a git repository", isPresented: Binding(
            get: { app.pendingProjectInit != nil },
            set: { if !$0 { app.pendingProjectInit = nil } }
        ), presenting: app.pendingProjectInit) { pending in
            Button("Init a git repo") { app.initProject(name: pending.name, pending.project) }
            Button("Add as plain folder") {
                app.addPlainProject(name: pending.name, pending.project)
            }
            Button("Cancel", role: .cancel) {}
        } message: { pending in
            Text("\(pending.project.repo)\n\nmoomux can run `git init` there (plus an empty "
                 + "first commit), or manage it as a plain folder — no worktrees, no branches, "
                 + "every session in the folder itself.")
        }
        .alert("Remove project?", isPresented: Binding(
            get: { app.pendingProjectDelete != nil },
            set: { if !$0 { app.pendingProjectDelete = nil } }
        ), presenting: app.pendingProjectDelete) { name in
            Button("Remove", role: .destructive) { app.removeProject(name: name) }
            Button("Cancel", role: .cancel) {}
        } message: { name in
            Text("Drops “\(name)” from moomux's config. The repository and everything in it "
                 + "stays on disk. Sessions have to be deleted first.")
        }
    }
}

// MARK: - Projects

/// Every project moomux knows about, in the user's own order, with the four
/// writes the core offers. No drag-to-reorder: `MoveProject` takes a ±1 delta
/// and `onMove` wants index sets over a bound array, so two buttons are the
/// whole feature for a day less work — same trade the session list makes.
private struct ProjectsPane: View {
    @Environment(AppState.self) private var app
    @Binding var editing: SettingsSheet.ProjectTarget?
    @State private var selection: String?

    private var names: [String] { app.config?.orderedProjectNames ?? app.projects }

    var body: some View {
        VStack(spacing: 8) {
            List(selection: $selection) {
                ForEach(names, id: \.self) { name in
                    // No double-click-to-edit: a tap gesture on the row
                    // content competes with the `List`'s own selection, and a
                    // row that will not select leaves Edit and Remove
                    // permanently disabled. The buttons are the whole feature.
                    ProjectRow(name: name, project: app.config?.projects[name])
                        .tag(name)
                }
            }
            .frame(maxHeight: .infinity)
            .overlay {
                // A fresh install lands here with nothing in the list, and an
                // empty `List` is a blank box that says nothing about what to do.
                if names.isEmpty {
                    ContentUnavailableView {
                        Label("No projects", systemImage: "folder.badge.plus")
                    } description: {
                        Text("Add the repo you want moomux to cut session worktrees from.")
                    }
                }
            }
            HStack {
                // Never disabled: with nothing configured and nothing selected,
                // this is the only way out of the empty state.
                Button {
                    editing = .init(name: nil)
                } label: {
                    Label("Add", systemImage: "plus")
                }
                Button("Edit") { if let selection { editing = .init(name: selection) } }
                    .disabled(selection == nil)
                Button("Remove") { app.pendingProjectDelete = selection }
                    .disabled(selection == nil)
                Spacer()
                Button {
                    if let selection { app.moveProject(name: selection, by: -1) }
                } label: {
                    Label("Move up", systemImage: "arrow.up")
                }
                .labelStyle(.iconOnly)
                .disabled(selection == nil)
                Button {
                    if let selection { app.moveProject(name: selection, by: 1) }
                } label: {
                    Label("Move down", systemImage: "arrow.down")
                }
                .labelStyle(.iconOnly)
                .disabled(selection == nil)
            }
            Text("Removing a project only drops it from moomux's config — nothing on disk is "
                 + "touched, and its sessions have to be deleted first.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

private struct ProjectRow: View {
    let name: String
    let project: Project?

    var body: some View {
        HStack(spacing: 8) {
            Text(project?.emoji ?? "").frame(width: 20)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(name)
                    if project?.isPlain == true {
                        Text("plain").font(.caption).foregroundStyle(.secondary)
                    } else if project?.noWorktree == true {
                        Text("no worktree").font(.caption).foregroundStyle(.secondary)
                    }
                    if project?.dangerous == true {
                        Text("⚠︎ dangerous").font(.caption).foregroundStyle(.orange)
                    }
                }
                Text(project?.repo ?? "").font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            // Not just the agent name: a `prompt_agent` project has one stored
            // and deliberately ignores it, so showing it would be a lie.
            Text(project?.promptAgent == true ? "asks each time" : (project?.agentName ?? ""))
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

/// Add or edit one project. The kind (git vs plain) is not a field: adding
/// decides it — a real repo path goes in as git, and a path that isn't one
/// raises the init-or-plain choice in `RootView` — and editing cannot change
/// it at all, because existing sessions were made under the old one.
struct ProjectSheet: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss
    /// nil to add, a name to edit.
    let editing: String?
    @State private var form = ProjectForm()

    private var isPlain: Bool { app.config?.projects[editing ?? ""]?.isPlain == true }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(editing == nil ? "Add project" : "Edit “\(editing!)”").font(.headline)
            Form {
                TextField("Name", text: $form.name)
                    // The core keys projects by name and has no rename, so
                    // editing one is a remove plus an add — not this form.
                    .disabled(editing != nil)
                TextField("Repo path", text: $form.repo, prompt: Text("~/src/thing"))
                Picker("Agent", selection: $form.agent) {
                    ForEach(app.agentNames, id: \.self) { Text($0).tag($0) }
                }
                .disabled(form.askAgent)
                Toggle("Ask which agent every time", isOn: $form.askAgent)
                    .help("New sessions in this project start with no agent chosen")
                Toggle("Skip permission prompts", isOn: $form.dangerous)
                    .disabled(form.askAgent)
                    .help("The default for new sessions in this project")
                if !isPlain {
                    TextField("Base branch", text: $form.baseBranch, prompt: Text("main"))
                    TextField("Branch prefix", text: $form.branchPrefix,
                              prompt: Text("optional, e.g. alan/"))
                    Toggle("One folder, no worktrees", isOn: $form.noWorktree)
                        .help("Sessions run in the repo itself, sharing its checkout. "
                              + "Can't be changed while the project has sessions.")
                }
                TextField("Emoji", text: $form.emoji, prompt: Text("none"))
                    // No palette picker: `config.ProjectEmojiPalette` is a Go
                    // table nothing serves over IPC, and this app must not keep
                    // a copy to drift. ⌃⌘Space is macOS's own picker.
                    .help("Shown in place of the name in the TUI's compact views. "
                          + "Left empty, the TUI picks one and this app shows none.")
            }
            .formStyle(.grouped)
            .textFieldStyle(.roundedBorder)
            if let problem = form.problem {
                Text(problem).font(.caption).foregroundStyle(.secondary)
            } else if isPlain {
                Text("A plain project has no branches or worktrees, so it has no base branch.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(editing == nil ? "Add" : "Save") {
                    if let editing {
                        app.updateProject(name: editing, form.project)
                    } else {
                        app.addProject(name: form.name.trimmed, form.project)
                    }
                    dismiss()
                }
                .keyboardShortcut(.defaultAction)
                .disabled(form.problem != nil)
            }
        }
        .padding(20)
        .frame(width: 460)
        .onAppear {
            if let editing, let p = app.config?.projects[editing] {
                form = ProjectForm(editing: editing, p)
            }
            // The picker needs a valid selection even for a project whose
            // agent is unset — "" is not one of `agentNames`.
            if form.agent.isEmpty { form.agent = app.agentNames.first ?? "claude" }
        }
    }
}

// MARK: - Preferences

/// The config flags the core persists. Two of them change what this app does;
/// the other three are the TUI's, and are here because it is one config file
/// and a front end that can edit projects but not these would be an odd place
/// to stop. Each toggle is one socket write, and the poll loop is what puts
/// the new value back on screen.
private struct PreferencesPane: View {
    @Environment(AppState.self) private var app

    private var cfg: Config? { app.config }

    /// The known themes, plus whatever is stored if it isn't one of them. A
    /// `Picker` whose selection matches no tag renders blank *and* writes
    /// nothing — so a theme this app has not heard of would look like a bug and
    /// then be silently replaced by the first click on any other row here.
    private var themeChoices: [String] {
        let stored = cfg?.theme?.nilIfEmpty
        guard let stored, !SettingsSheet.themes.contains(stored) else { return SettingsSheet.themes }
        return SettingsSheet.themes + [stored]
    }

    var body: some View {
        Form {
            Toggle("Sort sessions by last opened", isOn: Binding(
                get: { cfg?.sortRecentFirst ?? false },
                set: { app.setSortRecentFirst($0) }))
                .help("Turns manual reordering off — the next open would undo it")
            Toggle("Send the first prompt by default", isOn: Binding(
                get: { cfg?.autoSubmitDefault ?? false },
                set: { app.setAutoSubmitDefault($0) }))
                .help("The starting state of the new-session form's send toggle, here and in the TUI")
            Toggle("Relaunch the TUI inside tmux", isOn: Binding(
                get: { cfg?.autoTmux ?? false },
                set: { app.setAutoTmux($0) }))
                .help("`moomux` in a terminal puts itself in a dedicated tmux session on startup")
            Picker("TUI theme", selection: Binding(
                get: { cfg?.theme?.nilIfEmpty ?? SettingsSheet.themes[0] },
                set: { app.setTheme($0, appearance: cfg?.appearance ?? "") })) {
                ForEach(themeChoices, id: \.self) { Text($0).tag($0) }
            }
            Picker("TUI appearance", selection: Binding(
                get: { cfg?.appearance?.nilIfEmpty ?? "auto" },
                set: { app.setTheme(cfg?.theme ?? "", appearance: $0 == "auto" ? "" : $0) })) {
                Text("auto").tag("auto")
                Text("light").tag("light")
                Text("dark").tag("dark")
            }
        }
        .formStyle(.grouped)
        Text("The last two are the terminal UI's own palette — this app follows the system "
             + "appearance and is unaffected by either.")
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
    }
}

extension SettingsSheet {
    /// The TUI's palettes. A hardcoded list, unlike the agent table, because
    /// there is no IPC method serving it and these are strings `applyTheme`
    /// falls back to "default" on — a stale entry costs a wrong preview in
    /// another program, not a broken session. Serve it from the core if it
    /// ever grows a fifth.
    static let themes = ["default", "terminal", "gruvbox", "catppuccin"]
}
