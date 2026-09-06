package sessionview

import "github.com/erickgnclvs/moomux/internal/watcher"

var quipsWorking = []string{
	"udderly focused",
	"milking this for progress",
	"no time to graze",
	"plowing through it",
	"cud-crunching the details",
	"herding tokens into place",
	"in the zone, don't spook me",
}

var quipsDone = []string{
	"that's a wrap, moove along",
	"udder success",
	"cow-culated risk paid off",
	"steaks were high, nailed it",
	"mission moo-complished",
	"pasture perfect finish",
	"that'll do, cow, that'll do",
}

var quipsNeedsInput = []string{
	"udderly stuck without you",
	"moo-ve this along, please",
	"cow-nfused, help me out",
	"can't budge without a moo-tion",
	"stalled in the stall",
}

var quipsParked = []string{
	"chewing the cud, nothing to see",
	"herd nothing, seen nothing",
	"mootering off for now",
	"out to pasture",
	"on a moo-ratorium",
}

// quipPool returns the quip pool matching a session's state.
func quipPool(st watcher.State) []string {
	switch st {
	case watcher.Working:
		return quipsWorking
	case watcher.Done:
		return quipsDone
	case watcher.NeedsInput:
		return quipsNeedsInput
	default:
		return quipsParked
	}
}

// Quip deterministically picks a quip for sessionID's state, so the same
// session always shows the same flavor text for a given state. Exported
// because it's the single source of the wording for every front end: the
// core stamps it onto each View, and a client with no View yet (the frame
// before the first snapshot lands) calls this rather than keeping a copy of
// the word lists.
func Quip(sessionID string, st watcher.State) string {
	pool := quipPool(st)
	if len(pool) == 0 {
		return ""
	}
	var h uint32
	for _, c := range sessionID {
		h = h*31 + uint32(c)
	}
	return pool[h%uint32(len(pool))]
}

// Label is the human-readable name for a session's state — the cow-flavored
// wording the TUI shows in its detail panel. Lives here, next to the quips,
// so it's served from the core with the rest of a View instead of being
// re-hardcoded per front end (the state *colors* are already served this
// way, via config.Themes).
func Label(st watcher.State) string {
	switch st {
	case watcher.Working:
		return "grazing"
	case watcher.Done:
		return "chewing cud"
	case watcher.NeedsInput:
		return "mooing for you"
	default:
		return "in the barn"
	}
}
