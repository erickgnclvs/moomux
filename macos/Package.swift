// swift-tools-version: 6.0
import PackageDescription

// Swift 5 language mode on purpose: strict concurrency would want the socket
// client to be an actor, and it is a blocking file descriptor wrapped in
// Task.detached. Revisit when there is a reason to.
let package = Package(
    name: "Moomux",
    platforms: [.macOS(.v14)],
    dependencies: [
        // The one dependency, and the one the plan doc picks: there is no VT
        // emulator in the stdlib and no writing one. Reached only through
        // UI/TerminalPane.swift, so swapping in libghostty later is one file.
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", from: "1.20.0")
    ],
    targets: [
        .executableTarget(
            name: "Moomux",
            dependencies: [.product(name: "SwiftTerm", package: "SwiftTerm")],
            path: "Sources/Moomux",
            swiftSettings: [.swiftLanguageMode(.v5)]
        )
    ]
)
