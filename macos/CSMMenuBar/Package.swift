// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "CSMMenuBar",
    platforms: [
        .macOS(.v13)
    ],
    targets: [
        .executableTarget(
            name: "CSMMenuBar",
            path: "Sources",
            exclude: ["Info.plist"]
        )
    ]
)
