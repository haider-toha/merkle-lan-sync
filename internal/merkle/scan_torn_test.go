package merkle

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestScan_LeafSizeMatchesHashedContent is the REV-FLAKE-1 regression guard. Scan must
// never return a leaf whose Size disagrees with the content that produced its
// ContentHash. The original bug read Size from os.Stat/DirEntry.Info() and ContentHash
// from a SEPARATE HashFile pass, so a file rewritten between the two reads yielded a
// torn leaf (e.g. Size=0 with the hash of a 9-byte file). Such a leaf is internally
// impossible to transfer: the receiver computes numBlocks(0)=0, reconstructs an empty
// file, and fails the content-hash verify forever (the integration convergence timeout
// the finding misattributed to CPU starvation).
//
// The writer flips the file between two DISTINCT contents of DIFFERENT sizes while Scan
// runs in a tight loop. Any returned leaf whose ContentHash equals one known content's
// hash MUST carry that content's exact size; a torn (hashB,sizeA) leaf fails here.
func TestScan_LeafSizeMatchesHashedContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.bin")

	small := []byte("v2-from-B") // 9 bytes — the exact shape that bit the integration suite
	big := bytes.Repeat([]byte("A"), 64<<10)
	hSmall, sSmall := HashBytes(small), uint64(len(small))
	hBig, sBig := HashBytes(big), uint64(len(big))

	if err := os.WriteFile(target, small, 0o644); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		cur := big
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(target, cur, 0o644)
			if &cur[0] == &big[0] {
				cur = small
			} else {
				cur = big
			}
		}
	}()

	const iterations = 4000
	for i := 0; i < iterations; i++ {
		set, err := Scan(dir)
		if err != nil {
			// A concurrent rewrite can momentarily race open/stat; that is fine — only a
			// successfully-returned leaf must be self-consistent.
			continue
		}
		for _, fi := range set {
			if fi.Path != "f.bin" || fi.Type != TypeFile {
				continue
			}
			switch fi.ContentHash {
			case hSmall:
				if fi.Size != sSmall {
					close(stop)
					wg.Wait()
					t.Fatalf("torn leaf: ContentHash=hash(small) but Size=%d, want %d (iter %d)", fi.Size, sSmall, i)
				}
			case hBig:
				if fi.Size != sBig {
					close(stop)
					wg.Wait()
					t.Fatalf("torn leaf: ContentHash=hash(big) but Size=%d, want %d (iter %d)", fi.Size, sBig, i)
				}
			default:
				// ContentHash matches neither known content. With the fix (Size = bytes
				// streamed through the hasher) such a torn-CONTENT read is still internally
				// consistent; we cannot cheaply re-derive its size here, so skip it.
			}
		}
	}
	close(stop)
	wg.Wait()
}
