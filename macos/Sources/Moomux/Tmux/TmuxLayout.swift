import Foundation

/// A window's panes, in character cells, as tmux describes them.
///
/// tmux's layout string is a tree — `{}` for panes side by side, `[]` for
/// stacked — but every cell in it already carries absolute coordinates, so
/// this flattens to a list. Absolute rectangles are also exactly what the view
/// wants: one proportional frame per pane, no nested split containers.
///
///     367d,120x40,0,0{30x40,0,0,956,29x40,31,0,959,59x40,61,0[59x20,61,0,957,59x19,61,21,958]}
///
/// Note the gaps — a pane at x=0 of width 30 is followed by one at x=31. That
/// single column is tmux's divider, and leaving it undrawn is what makes the
/// panes look separated.
public struct TmuxWindowLayout: Equatable {

    public struct Pane: Equatable, Identifiable {
        public var id: String // "%956"
        public var x: Int, y: Int, width: Int, height: Int
    }

    public var width: Int
    public var height: Int
    public var panes: [Pane]

    /// Parses a `window_layout` / `%layout-change` string. Returns nil on
    /// anything unexpected rather than a half-built window: a wrong layout
    /// renders as garbage, where a missing one can just be ignored.
    public init?(_ layout: String) {
        // Drop the leading checksum.
        guard let comma = layout.firstIndex(of: ",") else { return nil }
        var scanner = Scanner(Array(layout[layout.index(after: comma)...].utf8))
        guard let root = scanner.cell(), scanner.atEnd else { return nil }
        width = root.width
        height = root.height
        panes = scanner.panes
    }

    private struct Scanner {
        let bytes: [UInt8]
        var i = 0
        var panes: [Pane] = []

        init(_ bytes: [UInt8]) { self.bytes = bytes }

        var atEnd: Bool { i >= bytes.count }
        private var peek: UInt8? { i < bytes.count ? bytes[i] : nil }

        private mutating func take(_ c: UInt8) -> Bool {
            guard peek == c else { return false }
            i += 1
            return true
        }

        private mutating func number() -> Int? {
            var value = 0, digits = 0
            while let c = peek, c >= UInt8(ascii: "0"), c <= UInt8(ascii: "9") {
                value = value * 10 + Int(c - UInt8(ascii: "0"))
                digits += 1
                i += 1
            }
            return digits > 0 ? value : nil
        }

        /// `WxH,X,Y` then a pane id, a `{}` group, or a `[]` group.
        mutating func cell() -> (width: Int, height: Int)? {
            guard let w = number(), take(UInt8(ascii: "x")), let h = number(),
                  take(UInt8(ascii: ",")), let x = number(),
                  take(UInt8(ascii: ",")), let y = number() else { return nil }

            switch peek {
            case UInt8(ascii: "{"), UInt8(ascii: "["):
                let close = peek == UInt8(ascii: "{") ? UInt8(ascii: "}") : UInt8(ascii: "]")
                i += 1
                repeat {
                    guard cell() != nil else { return nil }
                } while take(UInt8(ascii: ","))
                guard take(close) else { return nil }
            case UInt8(ascii: ","):
                // A leaf: the pane's number, without the '%' the rest of tmux
                // uses. Only consume it if a digit really follows, since ','
                // also separates siblings.
                let save = i
                i += 1
                guard let id = number() else {
                    i = save
                    return nil
                }
                panes.append(Pane(id: "%\(id)", x: x, y: y, width: w, height: h))
            default:
                // A childless, paneless cell is not something tmux emits.
                return nil
            }
            return (w, h)
        }
    }

    // MARK: Checks

    public static func demo() {
        // One pane, as a fresh session reports it.
        let single = TmuxWindowLayout("2a71,100x30,0,0,955")
        assert(single?.width == 100 && single?.height == 30)
        assert(single?.panes == [.init(id: "%955", x: 0, y: 0, width: 100, height: 30)])

        // Captured from a real four-pane window: three columns, the last split
        // into two rows. Exercises both group kinds and the nesting.
        let real = TmuxWindowLayout(
            "367d,120x40,0,0{30x40,0,0,956,29x40,31,0,959,59x40,61,0[59x20,61,0,957,59x19,61,21,958]}")
        guard let real else { assertionFailure("real layout failed to parse"); return }
        assert(real.width == 120 && real.height == 40)
        assert(real.panes == [
            .init(id: "%956", x: 0, y: 0, width: 30, height: 40),
            .init(id: "%959", x: 31, y: 0, width: 29, height: 40),
            .init(id: "%957", x: 61, y: 0, width: 59, height: 20),
            .init(id: "%958", x: 61, y: 21, width: 59, height: 19),
        ], "\(real.panes)")
        // These were checked against `list-panes -F '#{pane_left},#{pane_top}'`
        // for the same window: the layout string and tmux agree, gaps included.
        assert(real.panes[1].x == real.panes[0].width + 1, "the gap is tmux's divider column")

        // Junk must be nil, never a partial window.
        assert(TmuxWindowLayout("") == nil)
        assert(TmuxWindowLayout("nocomma") == nil)
        assert(TmuxWindowLayout("abcd,") == nil)
        assert(TmuxWindowLayout("abcd,120x40,0,0{30x40,0,0,1") == nil, "unclosed group")
        assert(TmuxWindowLayout("abcd,120x40,0,0") == nil, "a cell with neither pane nor children")
        assert(TmuxWindowLayout("abcd,120x40,0,0,5trailing") == nil, "trailing junk")
    }
}
