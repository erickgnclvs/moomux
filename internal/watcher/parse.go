package watcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// rawSession captures the subset of fields curral cares about from a
// ~/.claude/sessions/*.json file. Schema is best-effort: missing fields are tolerated.
type rawSession struct {
	CWD    string `json:"cwd"`
	Status string `json:"status"`
	Busy   *bool  `json:"busy,omitempty"`
	State  string `json:"state,omitempty"`
}

func parseFile(path string) (rawSession, error) {
	var rs rawSession
	data, err := os.ReadFile(path)
	if err != nil {
		return rs, err
	}
	_ = json.Unmarshal(data, &rs)
	if rs.CWD != "" {
		rs.CWD = filepath.Clean(rs.CWD)
	}
	rs.Status = strings.ToLower(rs.Status)
	rs.State = strings.ToLower(rs.State)
	return rs, nil
}

// classify maps a rawSession to a State.
func classify(rs rawSession) State {
	if rs.Busy != nil {
		if *rs.Busy {
			return Working
		}
		return Waiting
	}
	switch {
	case rs.Status == "busy", rs.Status == "working", rs.State == "busy":
		return Working
	case rs.Status == "idle", rs.Status == "waiting", rs.State == "idle":
		return Waiting
	}
	return Waiting
}
