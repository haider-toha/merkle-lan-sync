// Command msync is the Merkle Sync daemon. It watches a folder and keeps it
// mirrored with peers discovered on the local network: no central server, raw
// TCP for transfer and UDP multicast for discovery, with a Merkle tree as the
// source of truth for what differs.
//
// This file is a pre-flight stub. Real wiring (discovery, transport, scan/diff,
// reconciliation) is added across workstreams WS-1..WS-4 during implementation.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

func main() {
	dir := flag.String("dir", ".", "folder to keep in sync")
	port := flag.Int("port", 22000, "TCP port for peer-to-peer sync")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	info, err := os.Stat(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "msync: cannot watch %q: %v\n", *dir, err)
		os.Exit(1)
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "msync: %q is not a directory\n", *dir)
		os.Exit(1)
	}

	log.Printf("msync (pre-flight stub): would watch %q and sync with LAN peers on :%d", *dir, *port)
	log.Printf("not yet implemented — see docs/audit/plan/implementation-plan.md")
}
