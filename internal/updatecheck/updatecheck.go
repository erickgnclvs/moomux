// Package updatecheck checks GitHub Releases for a moomux version newer
// than the one currently running.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const releaseURL = "https://api.github.com/repos/erickgnclvs/moomux/releases/latest"

// Latest fetches the latest published release tag (e.g. "v0.5.4") from
// GitHub. Errors (offline, GitHub down, rate-limited) are the caller's cue
// to silently skip the update notice, not surface anything to the user.
func Latest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases: %s", resp.Status)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// Newer reports whether latest (a "vX.Y.Z" release tag) is a newer version
// than current (the running binary's version). current values that aren't a
// parseable X.Y.Z version — "dev", a local build — never count as needing
// an update, since there's no real baseline to compare against.
func Newer(current, latest string) bool {
	c, ok := parts(current)
	if !ok {
		return false
	}
	l, ok := parts(latest)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parts(v string) ([3]int, bool) {
	var out [3]int
	fields := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3)
	if len(fields) != 3 {
		return out, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
