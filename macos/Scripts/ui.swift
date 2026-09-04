// A minimal accessibility driver for poking at Moomux from the shell.
//
// System Events' `entire contents` returns an empty list against SwiftUI windows, so this walks
// the AXUIElement tree directly instead.
//
//   swift Scripts/ui.swift dump                 the whole tree, indented, with labels and frames
//   swift Scripts/ui.swift find <text>          matching elements only
//   swift Scripts/ui.swift click <text>         press the first actionable match
//   swift Scripts/ui.swift frame                the window frame, as screencapture -R wants it

import ApplicationServices
import AppKit
import Foundation

let appName = "Moomux"

func attr(_ e: AXUIElement, _ key: String) -> CFTypeRef? {
    var v: CFTypeRef?
    return AXUIElementCopyAttributeValue(e, key as CFString, &v) == .success ? v : nil
}

func str(_ e: AXUIElement, _ key: String) -> String? {
    guard let v = attr(e, key) else { return nil }
    if let s = v as? String { return s.isEmpty ? nil : s }
    if let n = v as? NSNumber { return n.stringValue }
    return nil
}

func children(_ e: AXUIElement) -> [AXUIElement] {
    (attr(e, kAXChildrenAttribute as String) as? [AXUIElement]) ?? []
}

func frame(_ e: AXUIElement) -> CGRect? {
    guard let p = attr(e, kAXPositionAttribute as String),
          let s = attr(e, kAXSizeAttribute as String) else { return nil }
    var origin = CGPoint.zero, size = CGSize.zero
    AXValueGetValue(p as! AXValue, .cgPoint, &origin)
    AXValueGetValue(s as! AXValue, .cgSize, &size)
    return CGRect(origin: origin, size: size)
}

/// Everything a human would call this element: SwiftUI scatters the label across title,
/// description and value depending on the control.
func labels(_ e: AXUIElement) -> [String] {
    [kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute, kAXHelpAttribute]
        .compactMap { str(e, $0 as String) }
}

func actions(of e: AXUIElement) -> [String] {
    var names: CFArray?
    AXUIElementCopyActionNames(e, &names)
    return (names as? [String]) ?? []
}

func role(_ e: AXUIElement) -> String {
    (str(e, kAXRoleAttribute as String) ?? "?").replacingOccurrences(of: "AX", with: "")
}

guard let app = NSWorkspace.shared.runningApplications
        .first(where: { $0.localizedName == appName }) else {
    FileHandle.standardError.write(Data("\(appName) is not running\n".utf8))
    exit(1)
}
let axApp = AXUIElementCreateApplication(app.processIdentifier)
guard let window = children(axApp).first(where: { role($0) == "Window" }) else {
    FileHandle.standardError.write(Data("\(appName) has no window\n".utf8))
    exit(1)
}

/// Raising the app is not optional before a click or a capture: a synthetic mouse event goes to
/// whatever is under the cursor, so clicking while another app is frontmost drives that app
/// instead — and `screencapture -R` would then photograph it. `activate()` is asynchronous, so
/// wait for it to actually take.
///
/// `NSRunningApplication.isActive` is read once and never refreshed in a process with no runloop
/// to service KVO, so it reports the state at launch forever. The AX attribute is live.
@discardableResult
func bringToFront(timeout: TimeInterval = 2) -> Bool {
    func isFront() -> Bool { (attr(axApp, kAXFrontmostAttribute as String) as? Bool) ?? false }
    if isFront() { return true }
    app.activate(options: [.activateAllWindows])
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if isFront() { usleep(150_000); return true }
        usleep(50_000)
    }
    return isFront()
}

let command = CommandLine.arguments.dropFirst().first ?? "dump"
let needle = CommandLine.arguments.dropFirst(2).joined(separator: " ").lowercased()

func walk(_ e: AXUIElement, depth: Int, visit: (AXUIElement, Int) -> Bool) {
    if !visit(e, depth) { return }
    for c in children(e) { walk(c, depth: depth + 1, visit: visit) }
}

func describe(_ e: AXUIElement, _ depth: Int) -> String {
    let pad = String(repeating: "  ", count: depth)
    let names = labels(e).joined(separator: " · ")
    let box = frame(e).map { " [\(Int($0.midX)),\(Int($0.midY))]" } ?? ""
    return "\(pad)\(role(e))\(names.isEmpty ? "" : "  \(names)")\(box)"
}

switch command {
case "frame":
    bringToFront()
    if let f = frame(window) {
        print("\(Int(f.minX)),\(Int(f.minY)),\(Int(f.width)),\(Int(f.height))")
    }

case "dump":
    walk(window, depth: 0) { e, d in
        // Containers with no label are structure, not content — keep the indent, skip the noise.
        if !labels(e).isEmpty || ["Window", "SplitGroup", "Toolbar", "Table", "Outline"].contains(role(e)) {
            print(describe(e, d))
        }
        return true
    }

case "find":
    walk(window, depth: 0) { e, d in
        if labels(e).contains(where: { $0.lowercased().contains(needle) }) { print(describe(e, d)) }
        return true
    }

case "click":
    // SwiftUI exposes AXPress on real Buttons but not on rows, list items or most labels, so
    // fall back to synthesising a click at the element's centre. Prefer a pressable ancestor
    // match when there is one — it survives layout changes that move pixels around.
    var pressable: AXUIElement?
    var anyMatch: AXUIElement?
    walk(window, depth: 0) { e, _ in
        guard labels(e).contains(where: { $0.lowercased().contains(needle) }) else { return true }
        if anyMatch == nil { anyMatch = e }
        if pressable == nil, actions(of: e).contains(kAXPressAction as String) { pressable = e }
        return true
    }

    guard bringToFront() else {
        FileHandle.standardError.write(Data("could not bring \(appName) to the front\n".utf8))
        exit(1)
    }

    if let target = pressable {
        AXUIElementPerformAction(target, kAXPressAction as CFString)
        print("pressed: \(labels(target).joined(separator: " · "))")
    } else if let target = anyMatch, let f = frame(target) {
        let point = CGPoint(x: f.midX, y: f.midY)
        for (type, phase) in [(CGEventType.leftMouseDown, "down"), (.leftMouseUp, "up")] {
            _ = phase
            CGEvent(mouseEventSource: nil, mouseType: type,
                    mouseCursorPosition: point, mouseButton: .left)?.post(tap: .cghidEventTap)
            usleep(40_000)
        }
        print("clicked at \(Int(point.x)),\(Int(point.y)): \(labels(target).joined(separator: " · "))")
    } else {
        FileHandle.standardError.write(Data("nothing matching \"\(needle)\"\n".utf8))
        exit(1)
    }

default:
    FileHandle.standardError.write(Data("usage: Scripts/ui.swift dump|find <text>|click <text>|frame\n".utf8))
    exit(2)
}
