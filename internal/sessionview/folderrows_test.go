package sessionview

import (
	"strings"
	"testing"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// folderLayout renders folder-first rows compactly so a test can assert on
// the whole tree at once: "[folder]", "{project}", "id", each wrapped in
// parentheses when the row is hidden by a collapsed folder.
func folderLayout(rows []FolderRow) string {
	var parts []string
	for _, r := range rows {
		var part string
		switch r.Kind {
		case KindFolder:
			part = "[" + r.Folder + "]"
		case KindProject:
			part = "{" + r.Project + "}"
		default:
			part = r.ID
		}
		if r.Hidden {
			part = "(" + part + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func fsess(id, project, folder string) session.Session {
	return session.Session{ID: id, Project: project, Name: id, Folder: folder}
}

// The layout this whole change is for: one folder spanning two projects,
// folders in their stored order, and every session filed nowhere gathered
// under the last folder rather than scattered.
func TestBuildFolderRowsLaysOutFolderThenProjectThenSession(t *testing.T) {
	rows := BuildFolderRows([]session.Session{
		fsess("a", "demo", "auth"),
		fsess("b", "other", "auth"),
		fsess("c", "demo", ""),
		fsess("d", "other", "ops"),
		fsess("e", "demo", "auth"),
		fsess("f", "other", ""),
	}, map[string]config.FolderMeta{
		"auth":  {Order: 1},
		"ops":   {Order: 2},
		"empty": {Order: 3},
	}, []string{"demo", "other"})

	want := "[auth] {demo} a e {other} b [ops] {other} d [empty] {demo} c {other} f"
	if got := folderLayout(rows); got != want {
		t.Errorf("layout =\n  %q\nwant\n  %q", got, want)
	}
}

// A collapsed folder keeps its subtree in the list — subheaders included —
// and marks it Hidden, the same contract Row.Hidden has: a client that
// sends an order back has to send a complete one.
func TestBuildFolderRowsCollapsedMarksItsWholeSubtree(t *testing.T) {
	rows := BuildFolderRows([]session.Session{
		fsess("a", "demo", "auth"),
		fsess("b", "demo", ""),
	}, map[string]config.FolderMeta{"auth": {Order: 1, Collapsed: true}}, []string{"demo"})

	if got, want := folderLayout(rows), "[auth] ({demo}) (a) {demo} b"; got != want {
		t.Errorf("layout = %q, want %q", got, want)
	}
	if !rows[0].Collapsed {
		t.Error("folder header should report Collapsed")
	}
	if rows[0].Hidden {
		t.Error("a collapsed folder's own header must stay visible")
	}
}

// Two count levels, because a client filtered to one view must not have to
// recount: the folder header counts its members everywhere, each project
// subheader only its own.
func TestBuildFolderRowsCountsPerLevelAndPerView(t *testing.T) {
	rows := BuildFolderRows([]session.Session{
		fsess("a", "demo", "auth"),
		{ID: "b", Project: "demo", Folder: "auth", Archived: true},
		fsess("c", "other", "auth"),
	}, map[string]config.FolderMeta{"auth": {Order: 1}}, []string{"demo", "other"})

	if rows[0].Count != 2 || rows[0].ArchivedCount != 1 {
		t.Errorf("folder counts = %d/%d, want 2 active / 1 archived across both projects", rows[0].Count, rows[0].ArchivedCount)
	}
	if rows[1].Project != "demo" || rows[1].Count != 1 || rows[1].ArchivedCount != 1 {
		t.Errorf("demo subheader = %+v, want 1 active / 1 archived", rows[1])
	}
}

// A session can name a folder the config has never heard of —
// SetSessionFolder writes the session and the config in two steps, and the
// second can fail. That session has to stay visible; app.go's comment says
// the orphan case degrades gracefully, and this is where it has to.
func TestBuildFolderRowsRendersAnOrphanFolder(t *testing.T) {
	rows := BuildFolderRows(
		[]session.Session{fsess("a", "demo", "ghost")},
		map[string]config.FolderMeta{"auth": {Order: 1}},
		[]string{"demo"},
	)
	if got, want := folderLayout(rows), "[auth] [ghost] {demo} a"; got != want {
		t.Errorf("layout = %q, want %q — the orphan folder must still render", got, want)
	}
}

// A project the config order doesn't mention is appended by name rather
// than left wherever map iteration put it: two consecutive builds of
// unchanged state have to be byte-identical or a streaming client redraws
// for nothing.
func TestBuildFolderRowsOrdersUnlistedProjectsByName(t *testing.T) {
	sessions := []session.Session{
		fsess("a", "zulu", "auth"),
		fsess("b", "alpha", "auth"),
		fsess("c", "demo", "auth"),
	}
	folders := map[string]config.FolderMeta{"auth": {Order: 1}}
	want := "[auth] {demo} c {alpha} b {zulu} a"
	for i := range 2 {
		if got := folderLayout(BuildFolderRows(sessions, folders, []string{"demo"})); got != want {
			t.Fatalf("build %d layout = %q, want %q", i, got, want)
		}
	}
}

// Order 0 means "never positioned" (written by a binary older than the
// global namespace) and sorts last, after every numbered folder; ties break
// by name so the result never rides map iteration.
func TestFolderOrderRanksZeroLastThenByName(t *testing.T) {
	folders := map[string]config.FolderMeta{
		"zed":   {Order: 1},
		"auth":  {Order: 2},
		"never": {},
		"also":  {},
	}
	want := []string{"zed", "auth", "also", "never"}
	for i := range 2 {
		got := FolderOrder(folders)
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("build %d order = %v, want %v", i, got, want)
		}
	}
}

func TestBuildServesFolderRows(t *testing.T) {
	core := &fakeCore{
		sessions: []session.Session{
			{ID: "demo:a", Project: "demo", Folder: "auth"},
			{ID: "other:z", Project: "other"},
		},
		alive:    map[string]bool{},
		projects: []string{"demo", "other"},
		folders:  map[string]config.FolderMeta{"auth": {Order: 1}},
	}
	snap, _ := newWatcher(core).build()
	if got, want := folderLayout(snap.FolderRows), "[auth] {demo} demo:a {other} other:z"; got != want {
		t.Errorf("folder rows = %q, want %q", got, want)
	}
}
