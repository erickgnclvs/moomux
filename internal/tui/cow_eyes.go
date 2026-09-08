package tui

import "github.com/erickgnclvs/moomux/internal/watcher"

// stateEyes returns the cow's eyes for a session's state, used by both the
// header cow and the detail panel's cowsay.
func stateEyes(st watcher.State) string {
	switch st {
	case watcher.Working:
		return "**"
	case watcher.Done:
		return "oo"
	case watcher.NeedsInput:
		return "!!"
	default:
		return "--"
	}
}
