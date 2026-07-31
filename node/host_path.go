package main

import "strings"

// Translating a container path to its host path is what lets the node bind-mount
// a server's directory into the MC container it spawns: the Docker API wants the
// path as the HOST sees it, not as this container sees it.
//
// Getting it wrong does not fail loudly. The container starts with a bind mount
// pointing at a directory that is empty or belongs to something else, which
// looks like a server that lost its world.

// hostMount is one entry of a container's mount table, narrowed to what the
// translation needs so it can be tested without a Docker daemon.
type hostMount struct {
	Destination string // path inside this container
	Source      string // path on the host
}

// resolveHostPath translates a container path to its host equivalent using the
// most specific mount that contains it.
//
// Two rules, both of which the previous plain strings.HasPrefix got wrong:
//
//   - The match is on a PATH COMPONENT boundary. A mount at /stor is not a
//     parent of /storage, but a prefix test says it is, and the resulting
//     substitution produces a path that exists nowhere.
//   - The MOST SPECIFIC mount wins, not the first one listed. Docker returns
//     mounts in no meaningful order, so with both / and /storage mounted, a
//     first-match loop resolves /storage/x through / half the time.
func resolveHostPath(path string, mounts []hostMount) (string, bool) {
	best := -1
	for i, m := range mounts {
		if m.Destination == "" || m.Source == "" || !mountContains(m.Destination, path) {
			continue
		}
		if best < 0 || len(m.Destination) > len(mounts[best].Destination) {
			best = i
		}
	}
	if best < 0 {
		return "", false
	}
	m := mounts[best]
	// Trim the destination the same way mountContains matches it, or the
	// remainder loses its leading slash and the two halves get glued together:
	// a mount at "/" would turn /storage into /hostrootstorage.
	rest := strings.TrimPrefix(path, strings.TrimSuffix(m.Destination, "/"))
	return strings.TrimSuffix(m.Source, "/") + rest, true
}

// mountContains reports whether dest is path itself or one of its ancestors.
func mountContains(dest, path string) bool {
	if dest == path {
		return true
	}
	if dest == "/" {
		return strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dest, "/")+"/")
}
