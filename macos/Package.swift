// swift-tools-version: 6.0
import PackageDescription

// Swift 5 language mode on purpose: strict concurrency would want the socket
// client to be an actor, and it is a blocking file descriptor wrapped in
// Task.detached. Revisit when there is a reason to.
let package = Package(
    name: "Moomux",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "Moomux",
            path: "Sources/Moomux",
            swiftSettings: [.swiftLanguageMode(.v5)]
        )
    ]
)
