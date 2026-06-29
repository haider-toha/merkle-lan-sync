package merkle

import "sort"

// DiffEntry is one path where two trees differ. Local or Remote is nil when the
// path exists on only one side — a single-sided node is emitted as a CANDIDATE, not
// pre-classified as a create or a delete: absence is ambiguous (deleted here, not
// yet created here, or deleted there), and only the version-vector + tombstone
// resolver (WS-4) may decide direction (MK-2).
//
// A nil Local/Remote means TRUE absence on that side EXCEPT on a file-vs-directory
// type clash, where the corresponding LocalDir/RemoteDir flag is set: at this exact
// path one side is a file leaf and the other is a DIRECTORY (e.g. a file deleted +
// recreated as a dir of the same name, or a Mac↔Windows structural divergence). The
// directory has no leaf, so its *FileInfo is nil — but the path is NOT absent there;
// the flag says so. Emitting that nil without the flag would be a FALSE "absent"
// signal, breaking the "absence is ambiguous" invariant (MK-2 refutation). The
// resolver MUST consult IsTypeClash before treating a nil side as absence.
type DiffEntry struct {
	Path      string
	Local     *FileInfo // nil if the path is absent locally, OR LocalDir (a directory, no leaf)
	Remote    *FileInfo // nil if the path is absent remotely, OR RemoteDir (a directory, no leaf)
	LocalDir  bool      // local side is a DIRECTORY at this path while remote is a file leaf (type clash)
	RemoteDir bool      // remote side is a DIRECTORY at this path while local is a file leaf (type clash)
}

// IsTypeClash reports whether this entry is a file-vs-directory type clash (one side a
// file leaf, the other a directory at the same path). On a clash the nil side is NOT
// absent — it is a directory — so the resolver must refuse to read the nil as a
// create/delete (MK-2).
func (e DiffEntry) IsTypeClash() bool { return e.LocalDir || e.RemoteDir }

// Diff returns the set of differing paths between local and remote. It prunes any
// subtree whose two node hashes are equal at the top of the call (never recursing
// it) and recurses only where children differ, so the cost is proportional to the
// differences and a one-byte change touches exactly one leaf's branch (SR-5, MK-2).
// The walk is read-only and does zero I/O (GR-5).
func Diff(local, remote *Tree) []DiffEntry {
	entries, _ := diffCounted(local, remote)
	return entries
}

// diffCounted is Diff plus the number of node-pairs compared, for the prune-equal
// property test (white-box). A pruned (equal) subtree contributes exactly one
// comparison — the top-of-call hash check — and zero recursion.
func diffCounted(local, remote *Tree) ([]DiffEntry, int) {
	var (
		out         []DiffEntry
		comparisons int
	)
	var lroot, rroot *Node
	if local != nil {
		lroot = local.root
	}
	if remote != nil {
		rroot = remote.root
	}
	diffNodes("", lroot, rroot, &out, &comparisons)
	return out, comparisons
}

func emit(out *[]DiffEntry, path string, l, r *Node) {
	e := DiffEntry{Path: path}
	if l != nil && l.leaf != nil {
		e.Local = l.leaf
	}
	if r != nil && r.leaf != nil {
		e.Remote = r.leaf
	}
	*out = append(*out, e)
}

func diffNodes(path string, l, r *Node, out *[]DiffEntry, comparisons *int) {
	*comparisons++
	if l == nil && r == nil {
		return
	}
	// PRUNE: both present with equal structural hash — the whole subtree is
	// identical on both sides, so it is skipped entirely (the efficiency win).
	if l != nil && r != nil && l.hash == r.hash {
		return
	}

	lLeaf := l != nil && l.leaf != nil
	rLeaf := r != nil && r.leaf != nil
	lDir := l != nil && l.isDir
	rDir := r != nil && r.isDir

	// Both sides are leaves or absent (at least one present) and not equal: emit
	// the differing leaf pair (either side may be nil for a single-sided leaf).
	if (l == nil || lLeaf) && (r == nil || rLeaf) {
		emit(out, path, l, r)
		return
	}

	// FILE-vs-DIRECTORY TYPE CLASH: exactly one side is a file leaf and the other is
	// a directory at this same path. Emit ONE truthful clash entry — the file leaf on
	// its side, the *Dir flag on the directory side (whose *FileInfo stays nil because
	// a directory has no leaf) — and PRUNE the directory subtree. Two reasons not to
	// recurse it: (1) emitting the file side with the other side nil and no flag would
	// be a FALSE "absent" signal, the exact MK-2 refutation; (2) the directory's
	// children cannot be materialised on the file side while the path is a file, so
	// recursing them only manufactures impossible single-sided installs (the WS-4
	// livelock). The resolver refuses + flags the clash; a later reconcile syncs the
	// subtree once the clash is gone.
	if (lLeaf && rDir) || (rLeaf && lDir) {
		e := DiffEntry{Path: path}
		if lLeaf {
			e.Local = l.leaf
			e.RemoteDir = true
		} else {
			e.Remote = r.leaf
			e.LocalDir = true
		}
		*out = append(*out, e)
		return
	}

	// Recurse the union of child names (sorted for deterministic output). The
	// top-of-call hash check on each child prunes equal child subtrees.
	for _, name := range unionChildren(lDir, rDir, l, r) {
		var lc, rc *Node
		if lDir {
			lc = l.children[name]
		}
		if rDir {
			rc = r.children[name]
		}
		child := name
		if path != "" {
			child = path + "/" + name
		}
		diffNodes(child, lc, rc, out, comparisons)
	}
}

func unionChildren(lDir, rDir bool, l, r *Node) []string {
	set := make(map[string]struct{})
	if lDir {
		for name := range l.children {
			set[name] = struct{}{}
		}
	}
	if rDir {
		for name := range r.children {
			set[name] = struct{}{}
		}
	}
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}
