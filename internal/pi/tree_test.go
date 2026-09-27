package pi

import "testing"

func TestActivePath(t *testing.T) {
	t.Run("branch", func(t *testing.T) {
		// root -> a -> b -> c (old leaf), then a branch off b: root -> a -> b -> d (new leaf).
		// c is off the active path; a, b, d and the root are on it.
		nodes := []sessNode{
			{ID: "a", ParentID: "root"},
			{ID: "b", ParentID: "a"},
			{ID: "c", ParentID: "b"},
			{ID: "d", ParentID: "b"},
		}
		active := sessActivePath(nodes)
		for _, id := range []string{"a", "b", "d"} {
			if !sessIsActive(active, id) {
				t.Errorf("expected %q active", id)
			}
		}
		if sessIsActive(active, "c") {
			t.Errorf("expected %q off the active path", "c")
		}
	})

	t.Run("cycle safe", func(t *testing.T) {
		// x's parent is y, y's parent is x: a cycle. The leaf is x. A
		// non-terminating implementation would hang this test rather than
		// fail an assertion.
		nodes := []sessNode{
			{ID: "y", ParentID: "x"},
			{ID: "x", ParentID: "y"},
		}
		active := sessActivePath(nodes)
		if !active["x"] || !active["y"] {
			t.Errorf("expected both cycle members active, got %v", active)
		}
	})

	t.Run("no ids means every event active", func(t *testing.T) {
		nodes := []sessNode{{}, {}, {}}
		if active := sessActivePath(nodes); active != nil {
			t.Errorf("expected nil active map for an all-empty-id file, got %v", active)
		}
		if !sessIsActive(nil, "anything") {
			t.Errorf("expected sessIsActive(nil, ...) to be true")
		}
	})

	t.Run("missing parent stops the walk", func(t *testing.T) {
		// "ghost" is not any node's id, so the walk marks it and stops
		// rather than looping or panicking. It's harmless: no real entry
		// ever has that id, so sessIsActive is never asked about it.
		nodes := []sessNode{
			{ID: "a", ParentID: "ghost"},
			{ID: "b", ParentID: "a"},
		}
		active := sessActivePath(nodes)
		if !sessIsActive(active, "b") || !sessIsActive(active, "a") {
			t.Errorf("expected the leaf and its parent active, got %v", active)
		}
	})
}
