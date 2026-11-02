package app

import (
	"sort"
	"strings"
)

// keyTree arranges scanned keys in a namespace tree split on the
// delimiter, as tiny-rdm's key browser: keys "a:b:1" and "a:b:2" share the
// branch "a:b". A node whose path is itself a key is both branch and key.
type keyTree struct {
	delim  string
	root   *keyNode
	byPath map[string]*keyNode
	count  int
	bytes  int
}

const (
	maxLoadedKeys = 50000
	maxTreeBytes  = 16 << 20
)

type keyNode struct {
	name     string // path segment, "b" for "a:b"
	path     string // full path from the root
	parent   *keyNode
	children []*keyNode
	isKey    bool
	keyCount int // keys in this node's subtree, this node included
}

func newKeyTree(delim string) *keyTree {
	t := &keyTree{delim: delim, byPath: map[string]*keyNode{}}
	t.root = &keyNode{}
	t.byPath[""] = t.root
	return t
}

func (t *keyTree) reset() {
	t.root = &keyNode{}
	t.byPath = map[string]*keyNode{"": t.root}
	t.count = 0
	t.bytes = 0
}

func (t *keyTree) add(key string) bool {
	if n := t.byPath[key]; n != nil && n.isKey {
		return true
	}
	if t.count >= maxLoadedKeys {
		return false
	}
	parts := []string{key}
	if t.delim != "" {
		parts = strings.SplitN(key, t.delim, 32)
	}
	cost := 0
	for i, part := range parts {
		path := strings.Join(parts[:i+1], t.delim)
		if t.byPath[path] == nil {
			cost += len(path) + len(part) + 128
		}
		if cost > maxTreeBytes-t.bytes {
			return false
		}
	}
	n := t.root
	path := ""
	for i, part := range parts {
		if i > 0 {
			path += t.delim
		}
		path += part
		child := n.find(part)
		if child == nil {
			child = &keyNode{name: part, path: path, parent: n}
			n.insert(child)
			t.byPath[path] = child
			t.bytes += len(path) + len(part) + 128
		}
		n = child
	}
	if !n.isKey {
		n.isKey = true
		t.count++
		for x := n; x != nil; x = x.parent {
			x.keyCount++
		}
	}
	return true
}

func (t *keyTree) remove(key string) {
	n := t.byPath[key]
	if n == nil || !n.isKey {
		return
	}
	n.isKey = false
	t.count--
	for x := n; x != nil; x = x.parent {
		x.keyCount--
	}
	// Drop empty branches up to the root.
	for n != t.root && n != nil && !n.isKey && len(n.children) == 0 {
		parent := n.parent
		if parent == nil {
			break
		}
		delete(t.byPath, n.path)
		t.bytes -= len(n.path) + len(n.name) + 128
		for i, ch := range parent.children {
			if ch == n {
				parent.children = append(parent.children[:i], parent.children[i+1:]...)
				break
			}
		}
		n = parent
	}
}

func (t *keyTree) node(path string) *keyNode { return t.byPath[path] }

// childPaths returns the sorted child paths of the node at path, the
// outline's children callback.
func (t *keyTree) childPaths(path string) []string {
	n := t.byPath[path]
	if n == nil || len(n.children) == 0 {
		return nil
	}
	paths := make([]string, len(n.children))
	for i, ch := range n.children {
		paths[i] = ch.path
	}
	return paths
}

// rootPaths returns the top-level items.
func (t *keyTree) rootPaths() []string { return t.childPaths("") }

func (n *keyNode) find(name string) *keyNode {
	i := sort.Search(len(n.children), func(i int) bool { return n.children[i].name >= name })
	if i < len(n.children) && n.children[i].name == name {
		return n.children[i]
	}
	return nil
}

func (n *keyNode) insert(child *keyNode) {
	i := sort.Search(len(n.children), func(i int) bool { return n.children[i].name >= child.name })
	n.children = append(n.children, nil)
	copy(n.children[i+1:], n.children[i:])
	n.children[i] = child
}
