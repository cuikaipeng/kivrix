package app

import (
	"reflect"
	"testing"
)

func TestKeyTreeAdd(t *testing.T) {
	tr := newKeyTree(":")
	for _, k := range []string{"user:2", "user:1", "sess:a", "plain"} {
		tr.add(k)
	}
	if tr.count != 4 {
		t.Fatalf("count %d", tr.count)
	}
	// Roots sorted, branches before nothing special: alphabetical.
	if got := tr.rootPaths(); !reflect.DeepEqual(got, []string{"plain", "sess", "user"}) {
		t.Fatalf("roots %v", got)
	}
	if got := tr.childPaths("user"); !reflect.DeepEqual(got, []string{"user:1", "user:2"}) {
		t.Fatalf("user children %v", got)
	}
	if tr.childPaths("plain") != nil || tr.childPaths("user:1") != nil {
		t.Fatal("Outline requires nil children for leaves; an empty slice makes a false branch")
	}
	n := tr.node("user:1")
	if n == nil || !n.isKey || n.name != "1" {
		t.Fatalf("node %+v", n)
	}
	if branch := tr.node("user"); branch == nil || branch.isKey {
		t.Fatalf("branch %+v", branch)
	}
	if got := tr.node("user").keyCount; got != 2 {
		t.Fatalf("user keyCount %d, want 2", got)
	}
	if got := tr.node("user:1").keyCount; got != 1 {
		t.Fatalf("user:1 keyCount %d, want 1", got)
	}
	if got := tr.node("plain").keyCount; got != 1 {
		t.Fatalf("plain keyCount %d, want 1", got)
	}
}

func TestKeyTreeKeyAndBranch(t *testing.T) {
	// "a" is a key and the parent of "a:b" at once.
	tr := newKeyTree(":")
	tr.add("a")
	tr.add("a:b")
	if tr.count != 2 {
		t.Fatalf("count %d", tr.count)
	}
	n := tr.node("a")
	if !n.isKey || len(n.children) != 1 {
		t.Fatalf("node %+v", n)
	}
	if n.keyCount != 2 {
		t.Fatalf("a keyCount %d, want 2", n.keyCount)
	}
}

func TestKeyTreeRemove(t *testing.T) {
	tr := newKeyTree(":")
	for _, k := range []string{"user:1", "user:2", "plain"} {
		tr.add(k)
	}
	tr.remove("user:1")
	if tr.count != 2 {
		t.Fatalf("count %d", tr.count)
	}
	// The branch stays while it has children, then goes with the last one.
	if got := tr.childPaths("user"); !reflect.DeepEqual(got, []string{"user:2"}) {
		t.Fatalf("children %v", got)
	}
	if got := tr.node("user").keyCount; got != 1 {
		t.Fatalf("user keyCount %d, want 1", got)
	}
	tr.remove("user:2")
	if tr.node("user") != nil {
		t.Fatal("empty branch survived")
	}
	tr.remove("plain")
	if got := tr.rootPaths(); len(got) != 0 {
		t.Fatalf("roots %v", got)
	}
	tr.remove("missing") // no panic, no change
	if tr.count != 0 {
		t.Fatalf("count %d", tr.count)
	}
}

func TestKeyTreeReset(t *testing.T) {
	tr := newKeyTree(":")
	tr.add("a:b")
	tr.reset()
	if tr.count != 0 || len(tr.rootPaths()) != 0 || tr.node("a:b") != nil {
		t.Fatal("reset left state behind")
	}
}
