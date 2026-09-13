package sessionview

import (
	"cmp"
	"maps"
	"math"
	"slices"

	"github.com/erickgnclvs/moomux/internal/config"
	"github.com/erickgnclvs/moomux/internal/session"
)

// The three FolderRow kinds. Kind is always set, so a client switches on it
// rather than inferring the row's shape from which fields happen to be
// empty — the trick Row gets away with because it only has two kinds.
const (
	KindFolder  = "folder"
	KindProject = "project"
	KindSession = "session"
)

// FolderRow is one line of the folder-first layout: folders at the top
// level, a project subheader under each, and that project's member sessions
// under that.
//
// A separate type from Row rather than a Kind discriminator bolted onto it:
// the two layouts genuinely differ — this one has three levels and inverts
// the outer two — and keeping them apart means the project-first clients
// that already ship keep rendering the rows they always did. Both are built
// here for the same reason (see Row): a front end that grouped for itself
// is a second implementation free to disagree with the first.
type FolderRow struct {
	// Kind is KindFolder, KindProject or KindSession.
	Kind string `json:"kind"`
	// Folder is the folder this row sits under — the one it is the header
	// for, or the one owning the project subheader or session. Empty on the
	// loose-session block that follows every folder.
	Folder string `json:"folder,omitempty"`
	// Project is the project a subheader names, and the project a session
	// belongs to. Empty on a folder header, which spans projects.
	Project string `json:"project,omitempty"`
	// ID is the session this row draws, empty on either header kind.
	ID string `json:"id,omitempty"`
	// Collapsed is a folder header's state — its subheaders and sessions
	// are still present, marked Hidden.
	Collapsed bool `json:"collapsed,omitempty"`
	// Hidden marks a row inside a collapsed folder. Same contract as
	// Row.Hidden: the row keeps its slot rather than being dropped, so a
	// client that sends an order back sends a complete one.
	Hidden bool `json:"hidden,omitempty"`
	// Count and ArchivedCount are a header's member counts, one per view a
	// client can be filtered to. A folder header counts its members
	// everywhere; a project subheader counts only its own.
	Count         int `json:"count,omitempty"`
	ArchivedCount int `json:"archived_count,omitempty"`
}

// FolderOrder is the one total order folders are listed in: Order
// ascending, Order 0 ("never positioned", i.e. written by a binary older
// than the global namespace) last, ties broken by name.
//
// The tiebreak is not cosmetic. Ranging the map instead would make two
// consecutive snapshots of unchanged state differ, and a streaming front
// end redraws on that. internal/app calls this rather than keeping its own
// copy: App.ReorderFolders has to number folders in the same order this
// lays them out, and two comparators that must agree forever is a drift
// this codebase has already paid for once.
func FolderOrder(folders map[string]config.FolderMeta) []string {
	rank := func(name string) int64 {
		if o := folders[name].Order; o != 0 {
			return o
		}
		return math.MaxInt64
	}
	names := slices.Collect(maps.Keys(folders))
	slices.SortFunc(names, func(x, y string) int {
		return cmp.Or(cmp.Compare(rank(x), rank(y)), cmp.Compare(x, y))
	})
	return names
}

// BuildFolderRows lays every session out folder-first: each folder in
// FolderOrder, then a project subheader for each project with members in
// it, then that project's members in the order they arrive in sessions.
// After the last folder come the sessions filed nowhere, under a project
// subheader each (with Folder empty).
//
// Project subheaders follow the user's project order, which is what
// projects carries; a project not in that list is appended by name rather
// than left to map iteration.
//
// The top level is the union of the folder map's keys and the distinct
// non-empty Session.Folder values, not just the map. A session can name a
// folder the map has never heard of — App.SetSessionFolder writes the two
// in separate steps and the config write can fail after the session one —
// and that session has to stay visible somewhere, which is exactly what
// BuildRows already does by emitting headers off the session loop. An
// orphan folder has no stored Order, so it sorts last with the unpositioned
// ones.
//
// sessions must already be in display order (Snapshot.Sessions is); this
// only groups.
func BuildFolderRows(sessions []session.Session, folders map[string]config.FolderMeta, projects []string) []FolderRow {
	// The orphan case: a folder named only by a session still gets a
	// header, with a zero-value meta (uncollapsed, unpositioned).
	all := maps.Clone(folders)
	if all == nil {
		all = map[string]config.FolderMeta{}
	}
	for _, s := range sessions {
		if s.Folder == "" {
			continue
		}
		if _, ok := all[s.Folder]; !ok {
			all[s.Folder] = config.FolderMeta{}
		}
	}

	rows := make([]FolderRow, 0, len(sessions)+2*len(all))
	for _, name := range FolderOrder(all) {
		collapsed := all[name].Collapsed
		block := blockOf(sessions, name, projects)
		active, arch := countOf(sessions, func(s session.Session) bool { return s.Folder == name })
		rows = append(rows, FolderRow{
			Kind: KindFolder, Folder: name, Collapsed: collapsed,
			Count: active, ArchivedCount: arch,
		})
		for _, project := range block {
			active, arch := countOf(sessions, func(s session.Session) bool {
				return s.Folder == name && s.Project == project
			})
			rows = append(rows, FolderRow{
				Kind: KindProject, Folder: name, Project: project, Hidden: collapsed,
				Count: active, ArchivedCount: arch,
			})
			for _, s := range sessions {
				if s.Folder == name && s.Project == project {
					rows = append(rows, FolderRow{
						Kind: KindSession, Folder: name, Project: s.Project, ID: s.ID, Hidden: collapsed,
					})
				}
			}
		}
	}

	// Everything filed nowhere, last: the folder-first view's answer to a
	// loose session, which otherwise has no folder to sit under at all.
	for _, project := range blockOf(sessions, "", projects) {
		active, arch := countOf(sessions, func(s session.Session) bool {
			return s.Folder == "" && s.Project == project
		})
		rows = append(rows, FolderRow{
			Kind: KindProject, Project: project, Count: active, ArchivedCount: arch,
		})
		for _, s := range sessions {
			if s.Folder == "" && s.Project == project {
				rows = append(rows, FolderRow{Kind: KindSession, Project: project, ID: s.ID})
			}
		}
	}
	return rows
}

// blockOf is the projects with at least one session filed under folder, in
// the user's order, with any project that order doesn't mention appended by
// name so the result never rides map iteration.
func blockOf(sessions []session.Session, folder string, projects []string) []string {
	have := map[string]bool{}
	for _, s := range sessions {
		if s.Folder == folder {
			have[s.Project] = true
		}
	}
	out := make([]string, 0, len(have))
	for _, p := range projects {
		if have[p] {
			out = append(out, p)
			delete(have, p)
		}
	}
	rest := slices.Collect(maps.Keys(have))
	slices.Sort(rest)
	return append(out, rest...)
}

// countOf splits the matching sessions the way both header kinds report
// them: one count per view a client can be filtered to.
func countOf(sessions []session.Session, match func(session.Session) bool) (active, archived int) {
	for _, s := range sessions {
		if !match(s) {
			continue
		}
		if s.Archived {
			archived++
		} else {
			active++
		}
	}
	return active, archived
}

// Reordering the folder-first view needs no helper here. At the top level
// a client already knows the folder order it is displaying and hands the
// whole list to App.ReorderFolders, which is total on its own. Inside a
// folder, a (folder, project) bucket is exactly one folder block in that
// project's project-first rows, so Reorder already does the job: its "a
// member can't leave its folder" guard is also "can't leave its project",
// since a folder block only ever holds one project's sessions. A second
// implementation would be a second chance to disagree about what a move
// means.
