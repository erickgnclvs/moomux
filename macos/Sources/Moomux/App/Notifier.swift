import AppKit
import UserNotifications

/// Banners for sessions that start waiting on you while you are elsewhere.
///
/// The whole UserNotifications surface lives here, the way SwiftTerm lives in
/// `UI/TerminalPane.swift`: `UNUserNotificationCenter.current()` **traps** in a
/// binary with no bundle identifier — `swift run`, and `--selftest`, which
/// `Scripts/selfcheck.sh` execs from `.build`. `center` is nil there and every
/// path is guarded on it, so nothing outside this file may reach the center.
@MainActor
public final class Notifier: NSObject, UNUserNotificationCenterDelegate {

    private weak var app: AppState?
    private let center: UNUserNotificationCenter?

    public init(app: AppState) {
        self.app = app
        center = Bundle.main.bundleIdentifier == nil ? nil : .current()
        super.init()
        center?.delegate = self
        // Asked once at startup rather than at the first banner. `.badge` is the
        // reason it cannot wait: macOS suppresses `NSDockTile.badgeLabel`
        // entirely for an app whose badge permission is off, and a session that
        // is *already* waiting when the app opens sets the badge without ever
        // being a transition, so no banner would ever have asked. After the
        // first answer the system returns it without prompting again.
        if let center {
            Task { _ = try? await center.requestAuthorization(options: [.alert, .sound, .badge]) }
        }
    }

    /// Post for every session that just *became* blocked; clear the banner for
    /// every one that stopped being.
    public func report(previous: [String: AgentState], current: [String: AgentState]) {
        guard let center, let app else { return }
        let change = Notifier.transitions(from: previous, to: current)
        center.removeDeliveredNotifications(
            withIdentifiers: change.ended.compactMap { app.session(atPath: $0)?.id })
        for path in change.started {
            guard let session = app.session(atPath: path), !session.archived else { continue }
            // A banner for the window you are already looking at is noise.
            if NSApp.isActive, app.selectedSessionID == session.id { continue }
            post(session, to: center)
        }
    }

    private func post(_ session: Session, to center: UNUserNotificationCenter) {
        let content = UNMutableNotificationContent()
        content.title = "\(session.project) · \(session.name)"
        content.body = "needs input"
        content.sound = .default
        // Identifier = session id: a re-post replaces rather than stacks, and
        // it is how a tap finds its way back to a session.
        let request = UNNotificationRequest(identifier: session.id, content: content, trigger: nil)
        // No authorization check here: `add` returns no error while denied, so
        // there is nothing to learn from asking. `init` did the asking.
        Task { try? await center.add(request) }
    }

    // MARK: Tapping a banner

    /// Come forward with that session selected. `NSApp.activate()` rather than
    /// `activate(ignoringOtherApps:)`, which is deprecated at the 14.0
    /// deployment target and would cost a warning.
    public nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler done: @escaping () -> Void
    ) {
        let id = response.notification.request.identifier
        Task { @MainActor in
            app?.selectedSessionID = id
            NSApp.activate()
            NSApp.windows.first { $0.canBecomeKey }?.makeKeyAndOrderFront(nil)
            done()
        }
    }

    // MARK: The pure half

    /// The transitions worth acting on: paths that just entered needs-input,
    /// and paths that just left it.
    ///
    /// A path must already be in `old` for a start to count. That is both the
    /// dedup (sitting in needs-input is not a transition) and the launch guard:
    /// the first snapshot seeds rather than firing a banner for every session
    /// that was already waiting when the app opened — the menu-bar count is
    /// what says that.
    nonisolated static func transitions(
        from old: [String: AgentState], to new: [String: AgentState]
    ) -> (started: [String], ended: [String]) {
        var started: [String] = [], ended: [String] = []
        for (path, state) in new where old[path] != nil && old[path] != state {
            if state == .needsInput { started.append(path) }
            if old[path] == .needsInput { ended.append(path) }
        }
        return (started.sorted(), ended.sorted())  // sorted so demo() can assert
    }

    nonisolated static func demo() {
        let a = "/wt/a", b = "/wt/b"
        // First sight of a session seeds; it never banners.
        assert(transitions(from: [:], to: [a: .needsInput]).started.isEmpty)
        // A real transition fires once, and sitting there does not re-fire.
        assert(transitions(from: [a: .working], to: [a: .needsInput]).started == [a])
        assert(transitions(from: [a: .needsInput], to: [a: .needsInput]).started.isEmpty)
        // Leaving needs-input clears the banner and is not itself one.
        let t = transitions(from: [a: .needsInput], to: [a: .working])
        assert(t.started.isEmpty && t.ended == [a], "\(t)")
        // Another session's churn is ignored.
        assert(transitions(from: [a: .working, b: .working],
                           to: [a: .working, b: .needsInput]).started == [b])
        // The watcher's partial snapshots: an absent path is not a change.
        let u = transitions(from: [a: .needsInput, b: .working], to: [b: .working])
        assert(u.started.isEmpty && u.ended.isEmpty, "\(u)")
    }
}
