package sessionview

import (
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// layout renders rows compactly so a test can assert on the whole shape at
// once: "[folder]" for a header, "id" for a visible session, "(id)" for one
// hidden inside a collapsed folder.
func layout(rows []Row) string {
	var parts []string
	for _, r := range rows {
		switch {
		case r.IsFolder():
			parts = append(parts, "["+r.Folder+"]")
		case r.Hidden:
			parts = append(parts, "("+r.ID+")")
		default:
			parts = append(parts, r.ID)
		}
	}
	return strings.Join(parts, " ")
}

func rowSess(id, folder string) session.Session {
	return session.Session{ID: id, Project: "demo", Name: id, Folder: folder}
}

func folderMetas(names ...string) map[string]config.FolderMeta {
	out := map[string]config.FolderMeta{}
	for _, n := range names {
		collapsed := strings.HasSuffix(n, "!")
		out[strings.TrimSuffix(n, "!")] = config.FolderMeta{Collapsed: collapsed}
	}
	return out
}

func TestBuildRowsGroupsMembersUnderTheirHeader(t *testing.T) {
	// Members are deliberately not adjacent in the input: folder membership
	// doesn't constrain Order, so grouping has to gather them.
	rows := BuildRows([]session.Session{
		rowSess("a", "auth"),
		rowSess("b", ""),
		rowSess("c", "auth"),
	}, folderMetas("auth"), "demo")
	if got, want := layout(rows), "[auth] a c b"; got != want {
		t.Errorf("layout = %q, want %q", got, want)
	}
}

// A collapsed folder must keep its position rather than sinking to the
// bottom. This is the regression that mattered most in practice: Order is 0
// for every session until someone manually reorders, and the old
// splice-by-stored-order put the header at the far end of a list whose
// entries all tied at 0.
func TestBuildRowsCollapsedFolderKeepsItsPosition(t *testing.T) {
	sessions := []session.Session{
		rowSess("a", ""),
		rowSess("b", "auth"),
		rowSess("c", "auth"),
		rowSess("d", ""),
	}
	if got, want := layout(BuildRows(sessions, folderMetas("auth"), "demo")), "a [auth] b c d"; got != want {
		t.Fatalf("expanded layout = %q, want %q", got, want)
	}
	if got, want := layout(BuildRows(sessions, folderMetas("auth!"), "demo")), "a [auth] (b) (c) d"; got != want {
		t.Errorf("collapsed layout = %q, want %q", got, want)
	}
}

func TestBuildRowsCountsMembersPerView(t *testing.T) {
	sessions := []session.Session{
		rowSess("a", "auth"),
		{ID: "b", Project: "demo", Folder: "auth", Archived: true},
		rowSess("c", ""),
	}
	rows := BuildRows(sessions, folderMetas("auth"), "demo")
	if rows[0].Count != 1 || rows[0].ArchivedCount != 1 {
		t.Errorf("header counts = %d active / %d archived, want 1/1", rows[0].Count, rows[0].ArchivedCount)
	}
}

// The header rule, now that folders is one global table shared by every
// project: a header belongs to the project that actually has members in
// the folder, and nowhere else. Before this, a folder map was the
// project's own, so "every folder in the map gets a header" was the same
// sentence — applied globally it would print a dead header per folder per
// project.
func TestBuildRowsHeaderOnlyWhereTheFolderHasMembers(t *testing.T) {
	sessions := []session.Session{
		rowSess("a", ""),
		{ID: "other:x", Project: "other", Folder: "auth"},
	}
	folders := folderMetas("auth", "zed")
	if got, want := layout(BuildRows(sessions, folders, "demo")), "a"; got != want {
		t.Errorf("demo layout = %q, want %q — auth has no member here", got, want)
	}
	if got, want := layout(BuildRows(sessions, folders, "other")), "[auth] other:x"; got != want {
		t.Errorf("other layout = %q, want %q", got, want)
	}
	// zed has no members anywhere. It is reachable through the Folders
	// overlay and through FolderRows; it must not squat a row in a project
	// list that would never draw it.
	for _, project := range []string{"demo", "other"} {
		for _, r := range BuildRows(sessions, folders, project) {
			if r.Folder == "zed" {
				t.Errorf("memberless folder emitted a row in %q", project)
			}
		}
	}
}

// Moving a loose session past a folder has to clear the whole folder. The
// old per-session swap put it between two members, where the next render
// pulled it straight back out — a keypress that did nothing but write to
// disk.
func TestReorderHopsAWholeFolder(t *testing.T) {
	rows := BuildRows([]session.Session{
		rowSess("a", "auth"),
		rowSess("c", "auth"),
		rowSess("b", ""),
	}, folderMetas("auth"), "demo")
	ids, ok := Reorder(rows, "b", -1, nil)
	if !ok {
		t.Fatal("expected the move to be possible")
	}
	if got, want := strings.Join(ids, " "), "b a c"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestReorderMovesWithinAFolder(t *testing.T) {
	rows := BuildRows([]session.Session{
		rowSess("a", "auth"),
		rowSess("c", "auth"),
		rowSess("b", ""),
	}, folderMetas("auth"), "demo")
	ids, ok := Reorder(rows, "c", -1, nil)
	if !ok {
		t.Fatal("expected the move to be possible")
	}
	if got, want := strings.Join(ids, " "), "c a b"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// A member at the edge of its folder has nowhere to go: it must not escape
// the folder as a side effect of a reorder keypress.
func TestReorderMemberCantLeaveItsFolder(t *testing.T) {
	rows := BuildRows([]session.Session{
		rowSess("x", ""),
		rowSess("a", "auth"),
	}, folderMetas("auth"), "demo")
	if _, ok := Reorder(rows, "a", -1, nil); ok {
		t.Error("expected the first member of a folder to have nowhere up to go")
	}
}

// A collapsed folder's hidden members ride along with the header, and the
// returned order covers every session — that totality is what keeps
// Store.Reorder's 1..N numbering from colliding with sessions left out.
func TestReorderCarriesHiddenMembersAndReturnsTheFullOrder(t *testing.T) {
	rows := BuildRows([]session.Session{
		rowSess("x", ""),
		rowSess("a", "auth"),
		rowSess("b", "auth"),
		rowSess("y", ""),
	}, folderMetas("auth!"), "demo")
	ids, ok := Reorder(rows, "y", -1, nil)
	if !ok {
		t.Fatal("expected the move to be possible")
	}
	if got, want := strings.Join(ids, " "), "x y a b"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

// A filtered-out neighbour (the archived view hides it) is never the thing a
// move swaps with — otherwise the keypress is spent on a row the user can't
// see and the list appears frozen.
func TestReorderSkipsFilteredNeighbours(t *testing.T) {
	rows := BuildRows([]session.Session{
		rowSess("x", ""),
		rowSess("hidden", ""),
		rowSess("y", ""),
	}, nil, "demo")
	skip := func(id string) bool { return id == "hidden" }
	ids, ok := Reorder(rows, "y", -1, skip)
	if !ok {
		t.Fatal("expected the move to be possible")
	}
	if got, want := strings.Join(ids, " "), "y hidden x"; got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestReorderAtTheEdgeIsNotPossible(t *testing.T) {
	rows := BuildRows([]session.Session{rowSess("a", ""), rowSess("b", "")}, nil, "demo")
	if _, ok := Reorder(rows, "a", -1, nil); ok {
		t.Error("expected no move above the first row")
	}
	if _, ok := Reorder(rows, "b", 1, nil); ok {
		t.Error("expected no move below the last row")
	}
	if _, ok := Reorder(rows, "nope", -1, nil); ok {
		t.Error("expected an unknown session to be refused")
	}
}

// The Watcher must actually serve the layout, not just be able to compute
// it: Rows is what a non-Go client renders from.
func TestBuildServesRowsPerProject(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{
			{ID: "demo:a", Project: "demo"},
			{ID: "demo:b", Project: "demo", Folder: "auth"},
			{ID: "other:z", Project: "other"},
		},
		alive:    map[string]bool{},
		projects: []string{"demo", "other"},
		folders:  map[string]config.FolderMeta{"auth": {Collapsed: true}},
	}
	w := newWatcher(core)
	snap, _ := w.build()

	if got, want := layout(snap.Rows["demo"]), "demo:a [auth] (demo:b)"; got != want {
		t.Errorf("demo rows = %q, want %q", got, want)
	}
	if got, want := layout(snap.Rows["other"]), "other:z"; got != want {
		t.Errorf("other rows = %q, want %q", got, want)
	}
}

// A project whose sessions have all been deleted still exists, and a client
// showing it still needs a (empty) row list for it. That used to fall out
// of "projects that have folders"; with folders global, Core.Projects is
// the only thing that knows, which is why it is on the interface.
func TestBuildServesRowsForAProjectWithNoSessions(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{{ID: "demo:a", Project: "demo"}},
		alive:    map[string]bool{},
		projects: []string{"demo", "emptied"},
	}
	snap, _ := newWatcher(core).build()
	if _, ok := snap.Rows["emptied"]; !ok {
		t.Errorf("Rows = %v, want an entry for the sessionless project", snap.Rows)
	}
}
