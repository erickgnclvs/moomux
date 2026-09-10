package sessionview

import (
	"sort"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// Row is one line of a project's session list, as a client should draw it:
// either a folder header (Folder set) or a session (ID set).
//
// This is the whole point of the type — grouping sessions under their
// folder, deciding where a folder's header sits, and knowing which members
// a collapsed folder is hiding are all things a front end would otherwise
// have to compute for itself, from config and the session list, in its own
// language. Two front ends doing that is two chances to disagree about what
// order a project is in (see Snapshot.Sessions for the last time that
// happened). Clients walk Rows and render; they don't group.
type Row struct {
	// ID is the session this row draws, empty on a folder header.
	ID string `json:"id,omitempty"`
	// Folder is the folder this row belongs to: the one it is the header
	// for (ID empty), or the one the session is filed under. Empty on a
	// loose session. A client indents a session row with a Folder set; it
	// never has to join back to the session to find that out, and neither
	// does Blocks.
	Folder string `json:"folder,omitempty"`
	// Collapsed is the header's state — its members are present in Rows but
	// marked Hidden.
	Collapsed bool `json:"collapsed,omitempty"`
	// Hidden marks a session row inside a collapsed folder. It stays in
	// Rows rather than being dropped because it still holds a position: a
	// client persisting a manual reorder has to send the whole order back
	// (see App.ReorderSessions), hidden rows included, or the sessions it
	// left out keep stale Order values that interleave with the ones it
	// renumbered.
	Hidden bool `json:"hidden,omitempty"`
	// Count and ArchivedCount are a header's member counts, one per view a
	// client can be filtered to, so neither has to be recounted client-side.
	Count         int `json:"count,omitempty"`
	ArchivedCount int `json:"archived_count,omitempty"`
}

// IsFolder reports whether r is a folder header rather than a session.
func (r Row) IsFolder() bool { return r.ID == "" }

// BuildRows lays out one project's sessions as display rows: loose sessions
// keep the order they arrive in, and the first session belonging to a
// folder is preceded by that folder's header and followed immediately by
// every other member, so a folder always renders as one contiguous block.
//
// A folder's position is therefore its first member's position — it is not
// stored anywhere. That is deliberate: an earlier version kept a per-folder
// order value alongside session.Session.Order, and because Store.Reorder
// renumbers only the sessions it is handed, the two number spaces drifted
// apart the moment anything was reordered while a folder was collapsed. A
// derived anchor cannot drift.
//
// Folders with no members at all (just created, or emptied) have nothing to
// anchor to and go last, by name — they move into place as soon as
// something is filed into them.
//
// sessions must already be in display order (Snapshot.Sessions is); this
// only groups, it never sorts. Sessions from other projects are ignored,
// and so is the archived split: both views share one layout, and the
// per-view member counts on each header are what a filtered client renders.
func BuildRows(sessions []session.Session, folders map[string]config.FolderMeta, project string) []Row {
	counts := map[string]int{}
	archived := map[string]int{}
	for _, s := range sessions {
		if s.Project != project || s.Folder == "" {
			continue
		}
		if s.Archived {
			archived[s.Folder]++
		} else {
			counts[s.Folder]++
		}
	}
	header := func(name string) Row {
		return Row{
			Folder:        name,
			Collapsed:     folders[name].Collapsed,
			Count:         counts[name],
			ArchivedCount: archived[name],
		}
	}

	seen := map[string]bool{}
	rows := make([]Row, 0, len(sessions)+len(folders))
	for _, s := range sessions {
		if s.Project != project {
			continue
		}
		if s.Folder == "" {
			rows = append(rows, Row{ID: s.ID})
			continue
		}
		if seen[s.Folder] {
			continue
		}
		seen[s.Folder] = true
		collapsed := folders[s.Folder].Collapsed
		rows = append(rows, header(s.Folder))
		for _, member := range sessions {
			if member.Project == project && member.Folder == s.Folder {
				rows = append(rows, Row{ID: member.ID, Folder: s.Folder, Hidden: collapsed})
			}
		}
	}

	// Memberless folders: nothing anchors them, so they sit at the end in a
	// stable order rather than wherever a map iteration put them.
	var empty []string
	for name := range folders {
		if !seen[name] {
			empty = append(empty, name)
		}
	}
	sort.Strings(empty)
	for _, name := range empty {
		rows = append(rows, header(name))
	}
	return rows
}

// Block is one movable unit of a project's list: a loose session on its
// own, or a folder header together with every member it owns. Manual
// reorder works in these units — dragging a session past a folder means
// past the whole folder, not into the middle of it, and a collapsed folder
// carries its hidden members with it instead of leaving them behind at
// their old positions.
type Block struct {
	// Folder is the folder this block is, empty for a loose session.
	Folder string
	// IDs are the session ids in the block, in order: exactly one for a
	// loose session, zero or more for a folder.
	IDs []string
}

// Blocks groups rows into movable units. Rows must come from BuildRows,
// where a folder's members already follow its header contiguously.
func Blocks(rows []Row) []Block {
	var blocks []Block
	for _, r := range rows {
		switch {
		case r.IsFolder():
			blocks = append(blocks, Block{Folder: r.Folder})
		case r.Folder != "" && len(blocks) > 0 && blocks[len(blocks)-1].Folder == r.Folder:
			last := &blocks[len(blocks)-1]
			last.IDs = append(last.IDs, r.ID)
		default:
			blocks = append(blocks, Block{IDs: []string{r.ID}})
		}
	}
	return blocks
}

// Flatten returns every session id in blocks, in order — the full,
// gap-free order to hand App.ReorderSessions. Passing the *whole* project
// this way, rather than only the rows a client happens to be showing,
// is what keeps Store.Reorder's 1..N numbering total: a session left out
// keeps a stale Order that then interleaves with the renumbered ones.
func Flatten(blocks []Block) []string {
	var ids []string
	for _, b := range blocks {
		ids = append(ids, b.IDs...)
	}
	return ids
}

// Reorder moves the session id one step in the direction of delta (-1 up,
// +1 down) and returns the project's resulting full session order.
//
// A session inside a folder moves within that folder — it can be
// rearranged among its siblings but never silently escapes the block, since
// leaving a folder is what SetSessionFolder is for. Anything else moves as
// a whole block, hopping the entire folder next to it rather than landing
// in its middle (where BuildRows would only pull it back out again on the
// next render, which is how "shift+up does nothing" used to happen).
//
// skip reports session ids the caller isn't showing (an archived filter);
// those keep their slots but are never chosen as the thing to swap with, so
// a move is never spent on a row the user can't see. nil skips nothing.
// ok is false when the move has nowhere to go.
func Reorder(rows []Row, id string, delta int, skip func(string) bool) ([]string, bool) {
	if delta == 0 {
		return nil, false
	}
	if skip == nil {
		skip = func(string) bool { return false }
	}
	blocks := Blocks(rows)
	bi, si := -1, -1
	for i, b := range blocks {
		for j, bid := range b.IDs {
			if bid == id {
				bi, si = i, j
			}
		}
	}
	if bi < 0 {
		return nil, false
	}

	// Inside a folder: rearrange among visible siblings only.
	if blocks[bi].Folder != "" {
		ids := blocks[bi].IDs
		for j := si + delta; j >= 0 && j < len(ids); j += delta {
			if skip(ids[j]) {
				continue
			}
			ids[si], ids[j] = ids[j], ids[si]
			return Flatten(blocks), true
		}
		return nil, false
	}

	// Loose session: swap with the neighbouring block the user can see.
	for j := bi + delta; j >= 0 && j < len(blocks); j += delta {
		if visibleBlock(blocks[j], skip) {
			blocks[bi], blocks[j] = blocks[j], blocks[bi]
			return Flatten(blocks), true
		}
	}
	return nil, false
}

// visibleBlock reports whether a block has anything the caller is showing —
// a folder always does (its header renders even with every member filtered
// out), a loose session only if it survives the filter itself.
func visibleBlock(b Block, skip func(string) bool) bool {
	if b.Folder != "" {
		return true
	}
	for _, id := range b.IDs {
		if !skip(id) {
			return true
		}
	}
	return false
}
