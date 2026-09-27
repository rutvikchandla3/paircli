package pi

// sessNode is the minimal shape tree-walking needs from a Pi session entry.
type sessNode struct {
	ID       string
	ParentID string
}

// sessActivePath returns the set of entry ids on the active branch: the
// path from the leaf (the last entry in file order with a non-empty id)
// walking parentId up to the root. It stops at a missing parent and is
// cycle-safe: an id already marked active ends the walk instead of looping.
//
// Version 1 files carry no ids at all, so no node has a non-empty id; in
// that case (and whenever no entry has an id) sessActivePath returns nil,
// which sessIsActive treats as "every event is active".
func sessActivePath(nodes []sessNode) map[string]bool {
	var leaf string
	for _, n := range nodes {
		if n.ID != "" {
			leaf = n.ID
		}
	}
	if leaf == "" {
		return nil
	}

	parentOf := make(map[string]string, len(nodes))
	for _, n := range nodes {
		if n.ID != "" {
			parentOf[n.ID] = n.ParentID
		}
	}

	active := make(map[string]bool)
	cur := leaf
	for cur != "" {
		if active[cur] {
			break // cycle: already on the path
		}
		active[cur] = true
		parent, ok := parentOf[cur]
		if !ok {
			break // root or dangling reference: stop here
		}
		cur = parent
	}
	return active
}

// sessIsActive reports whether id is on the active path. A nil active map
// (version 1, or any file where no entry carries an id) means every event
// is active.
func sessIsActive(active map[string]bool, id string) bool {
	if active == nil {
		return true
	}
	return active[id]
}
