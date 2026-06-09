package tui

var quipsWorking = []string{
	"on it...",
	"crunching...",
	"in the zone",
	"don't interrupt me",
	"almost there...",
	"trust the process",
	"heads down",
	"grinding...",
	"locked in",
	"making it happen",
	"cooking something up",
	"deep focus",
}

var quipsWaiting = []string{
	"moo...",
	"waiting for orders",
	"chewing cud",
	"your move",
	"ready when you are",
	"standing by",
	"at your service",
	"just here vibing",
	"anytime now...",
	"still here",
	"no rush... or is there?",
	"patiently waiting",
}

var quipsParked = []string{
	"zzz...",
	"taking a nap",
	"out to pasture",
	"resting",
	"offline",
	"on break",
	"do not disturb",
	"gone fishin'",
	"lights out",
	"hibernating",
	"clocked out",
	"pasture mode",
}

func pickQuip(sessionID string, pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	var h uint32
	for _, c := range sessionID {
		h = h*31 + uint32(c)
	}
	return pool[h%uint32(len(pool))]
}
