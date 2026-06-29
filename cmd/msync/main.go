// Command msync is the Merkle Sync daemon. It watches a folder and keeps it
// mirrored with peers discovered on the local network: no central server, raw TCP
// (over TLS 1.3) for transfer and UDP multicast for discovery, with a Merkle tree as
// the source of truth for what differs.
//
// Wiring (docs/audit/decisions/ws4/cmd-msync-wiring.md): one signal.NotifyContext
// root ctx (GR-2) threaded into the transport, discovery, and reconcile engine; the
// engine is the single consumer of both event streams (GR-4) and the single writer of
// tree state (GR-5). Cancelling the ctx (SIGINT/SIGTERM) tears every subsystem down
// through its own teardown.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/haider-toha/merkle-sync/internal/discovery"
	"github.com/haider-toha/merkle-sync/internal/protocol"
	"github.com/haider-toha/merkle-sync/internal/reconcile"
	"github.com/haider-toha/merkle-sync/internal/transport"
)

// stringList is a repeatable string flag (-peer A -peer B).
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	dir := flag.String("dir", ".", "folder to keep in sync")
	port := flag.Int("port", 22000, "TCP port for peer-to-peer sync")
	folder := flag.String("folder", "default", "folder id shared by both peers")
	configDir := flag.String("config", "", "config dir for identity + snapshot (default <dir>/.msync)")
	var peers stringList
	flag.Var(&peers, "peer", "hex DeviceID of a paired peer to trust (repeatable; out-of-band TOFU pairing)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	if err := run(*dir, *port, *folder, *configDir, peers); err != nil {
		fmt.Fprintf(os.Stderr, "msync: %v\n", err)
		os.Exit(1)
	}
}

func run(dir string, port int, folderID, configDir string, peerIDs []string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve dir %q: %w", dir, err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("cannot watch %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", absDir)
	}
	if configDir == "" {
		configDir = filepath.Join(absDir, ".msync")
	}

	// One root context, cancelled on SIGINT/SIGTERM (GR-2).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	id, err := transport.LoadOrCreateIdentity(configDir)
	if err != nil {
		return err
	}
	log.Printf("device id: %s", id.DeviceID)
	log.Printf("watching %q, syncing folder %q on TCP :%d", absDir, folderID, port)

	// Out-of-band paired allow-list (PR-7 TOFU). Trusting our own id is harmless and
	// lets a same-host two-instance demo connect.
	allow := transport.NewAllowlist(id.DeviceID)
	for _, p := range peerIDs {
		did, perr := protocol.ParseDeviceID(strings.TrimSpace(p))
		if perr != nil {
			return fmt.Errorf("invalid -peer %q: %w", p, perr)
		}
		allow.Add(did)
		log.Printf("paired peer: %s", did)
	}

	// The engine's Hello (root hash for the SR-5 short-circuit) is captured by
	// reference; it is only ever called once a peer handshakes, which is after the
	// engine is constructed and Listen is called below (cmd-msync-wiring decision).
	var eng *reconcile.Engine
	tp := transport.New(ctx, id, allow, transport.WithHello(func() protocol.Hello {
		if eng == nil {
			return protocol.Hello{ProtoVersion: transport.ProtoVersion, FolderID: folderID}
		}
		return eng.Hello()
	}))
	defer tp.Close()

	disco, err := discovery.New(ctx, id.DeviceID, uint16(port))
	if err != nil {
		return err
	}
	defer disco.Close()

	eng, err = reconcile.New(reconcile.Config{
		FolderID:      folderID,
		AbsRoot:       absDir,
		Self:          id.DeviceID,
		SnapshotPath:  filepath.Join(configDir, "snapshot.gob"),
		Transport:     tp,
		Discovery:     disco,
		EnableWatcher: true,
		Logf:          log.Printf,
	})
	if err != nil {
		return err
	}

	if _, err := tp.Listen("tcp", fmt.Sprintf(":%d", port)); err != nil {
		return err
	}

	log.Printf("msync up; press Ctrl-C to stop")
	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	log.Printf("msync stopped")
	return nil
}
