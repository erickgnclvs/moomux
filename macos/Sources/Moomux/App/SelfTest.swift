import Foundation

/// `Moomux --selftest` — runs every `demo()` check in the binary that ships.
///
/// There is no test target and there cannot be one: neither XCTest nor
/// swift-testing exists in a command-line-tools toolchain, both ship inside
/// Xcode. So the checks are `assert`-based functions compiled alongside the
/// code they verify, and this is how they run. Running them through the real
/// binary rather than a separately-compiled subset means there is no file list
/// to keep in sync.
enum SelfTest {

    static func runIfRequested() {
        guard CommandLine.arguments.contains("--selftest") else { return }
        requireAssertsAreLive()

        Wire.demo()
        MoomuxClient.demo()
        ToolPath.demo()
        TmuxProtocolChecks.demo()
        TmuxWindowLayout.demo()
        AppState.demo()
        NewSessionForm.demo()
        ProjectForm.demo()
        Notifier.demo()
        TmuxControlClient.demo()
        TmuxSnapshot.demo()

        print("selftest: ok")
        exit(0)
    }

    /// `assert` is compiled out entirely at `-O`, so a release build would
    /// print "selftest: ok" having executed nothing at all. The harness has to
    /// prove it can fail before it is allowed to report success.
    private static func requireAssertsAreLive() {
        var live = false
        assert({ live = true; return true }())
        guard live else {
            FileHandle.standardError.write(Data("""
                selftest: FAILED — this binary was built with optimisation, so every assert has \
                been compiled out and nothing would be verified.
                Run `make selfcheck`, which builds without optimisation.

                """.utf8))
            exit(1)
        }
    }
}
