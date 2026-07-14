# Vow — Raw BitTorrent Engine

Vow is a from-scratch BitTorrent protocol engine, written in Go with zero third-party dependencies. It targets anyone who wants BitTorrent download capability inside their own tool — a CLI, a daemon, a TUI — without vendoring libtorrent, its build toolchain, or its platform quirks (notably flaky on some Linux distros, Arch included).

---

## Objectives

- **Speak the raw protocol, not a wrapper.** Bencode codec, peer wire protocol, tracker announces (UDP + HTTP), and DHT lookups are all hand-implemented against the BEPs — no `libtorrent`, no bencode/wire-protocol libraries, no C bindings.
- **Run as a subprocess, not a library lock-in.** The engine is a single static binary that takes a `.torrent` path and streams newline-delimited JSON progress events on stdout. Any controller — a Python/Textual UI, a daemon, a test harness — drives it by launching the process and reading a pipe.
- **Actually be fast, not just correct.** Request pipelining, rarest-first + endgame piece selection, async disk I/O, DHT peer discovery, and a reciprocity-based choking algorithm — the mechanics that drive real BitTorrent throughput, not a naive one-piece-at-a-time downloader.
- **Stay auditable.** Hexagonal architecture (ports & adapters) — the protocol/domain logic has no I/O in it at all, every socket and disk call sits behind a swappable interface.
- **Remain zero-dependency.** `go build` works offline on a bare Go install. No `go.sum`, no vendored modules, no supply-chain surface.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Controller (Python/Textual CLI, daemon, test harness, ...) │
│  reads newline-delimited JSON from stdout                   │
└──────────────────────────┬──────────────────────────────────┘
                           │ subprocess stdout (JSON events)
┌──────────────────────────▼──────────────────────────────────┐
│  torrentmotor  (Go, stdlib only)                             │
│                                                               │
│  cmd/torrentmotor   ──► composition root, flag parsing       │
│  internal/app       ──► DownloadService (orchestrator)       │
│      │                                                        │
│      ├─ discoverAllPeers()                                   │
│      │     → TrackerAnnouncer (UDP/HTTP) + PeerDiscoverer(DHT)│
│      │                                                        │
│      ├─ one goroutine per peer                                │
│      │     → PeerLink (TCP wire protocol, pipelined requests) │
│      │     → PieceManager.ClaimBlocks() (rarest-first,        │
│      │        endgame duplicate requests)                     │
│      │     → peerRegistry (reciprocity choking, have-broadcast)│
│      │                                                        │
│      └─ PieceStorage.WritePiece()/ReadBlock()                 │
│            → async worker pool, persistent file handles       │
└──────┬─────────────────────────────────────────────────────────┘
       │
       ├── Peer swarm    (TCP, BEP 3 wire protocol)
       ├── Trackers      (UDP/HTTP, BEP 15 / BEP 3)
       ├── Mainline DHT  (UDP, BEP 5)
       └── Local disk    (downloads/<torrent name>/...)
```

`internal/domain` has no imports beyond the Go standard library and never touches a socket or a file — bencode, torrent metadata, wire-protocol messages, and piece selection are pure functions and data structures, independently testable. Disk writes are asynchronous: a peer's network-reading goroutine hands a completed piece to a bounded channel and moves on, never blocking on a syscall; the channel's bound is the backpressure. Peer choking is re-evaluated every 10 seconds against real reciprocity data (bytes received per peer since the last round) rather than left static. Every lifecycle event — peer connected, piece verified, tracker error — is emitted as one JSON object per line on stdout, so a controller process never has to parse human-readable log output.

---

## Tech Stack

### Core Engine

| Technology | Version | Why |
|---|---|---|
| **Go** | 1.25 | Goroutine-per-peer concurrency maps directly onto BitTorrent's one-connection-per-peer model; no async framework needed. |
| **Hand-rolled bencode codec** | — | BEP 3 encode/decode in ~150 lines (`internal/domain/bencode.go`); removes the last non-stdlib dependency the project used to carry. |
| **`net` (raw TCP/UDP sockets)** | — | Peer wire protocol and UDP tracker protocol talk directly to sockets — no HTTP framework, no protocol library. |
| **`sync` + channels** | — | Piece manager state is mutex-guarded and shared across peer goroutines; disk writes go through a buffered channel to a small worker pool for backpressure. |
| **`encoding/json`** | — | Progress events serialize to newline-delimited JSON — the entire contract with any controller process. |

### Protocol Adapters

| Component | Why |
|---|---|
| **`adapters/trackerudp`** | BEP 15 UDP tracker announce (connect/announce handshake, compact peer parsing). |
| **`adapters/trackerhttp`** | BEP 3 HTTP/HTTPS tracker announce — the original client's biggest bug (`_get_peers_http` was never implemented) fixed here for real, with binary-safe query encoding. |
| **`adapters/dht`** | Lookup-only mainline DHT client (BEP 5) — bootstraps from well-known nodes, runs an iterative `get_peers` lookup, reuses the bencode codec as its KRPC wire format. |
| **`adapters/peertcp`** | TCP peer wire protocol transport — handshake, pipelined block requests, write-serialized (a mutex guards concurrent sends now that choking messages and request messages can originate from different goroutines on the same connection). |
| **`adapters/filestorage`** | Multi-file-aware piece writer/reader — persistent file handles (no per-block open/close), async write-behind, path-traversal-checked against the torrent's own file list. |
| **`adapters/jsonprogress`** | Emits every lifecycle event as one JSON object per line on stdout. |

---

## Project Structure

```
torrent/
├── cmd/
│   └── torrentmotor/           # composition root — wires adapters, parses flags, runs one download
├── internal/
│   ├── domain/                 # pure protocol logic: bencode, torrent metadata,
│   │                           # wire messages, piece selection. No I/O, no deps.
│   ├── ports/                  # interfaces: TrackerAnnouncer, PeerDiscoverer,
│   │                           # PeerDialer/PeerLink, PieceStorage, ProgressReporter
│   ├── adapters/
│   │   ├── trackerudp/         # BEP 15 UDP tracker protocol
│   │   ├── trackerhttp/        # BEP 3 HTTP/HTTPS tracker protocol
│   │   ├── dht/                # BEP 5 lookup-only DHT client
│   │   ├── peertcp/            # TCP peer wire protocol transport
│   │   ├── filestorage/        # async, path-checked disk I/O
│   │   └── jsonprogress/       # stdout JSON event stream
│   └── app/                    # DownloadService — orchestrator, choking algorithm
└── go.mod                      # zero external dependencies
```

---

## Running Locally

**Prerequisites:** a Go toolchain, nothing else.

```bash
git clone https://github.com/EduardoAlcaria/Vow.git
cd Vow
go build -o torrentmotor ./cmd/torrentmotor
./torrentmotor -torrent path/to/file.torrent -dir downloads -port 6881 -max-peers 150 -dht=true
```

No `go.sum`, no external modules — `go build` works offline on a bare Go install.

---

## Download Pipeline — How It Works

1. `cmd/torrentmotor` parses the `.torrent` file into a `domain.Torrent` and validates every file path against traversal (`../` segments rejected before any disk I/O happens).
2. `DownloadService.discoverAllPeers` queries every configured tracker tier (UDP and HTTP) and, if enabled, runs a DHT `get_peers` lookup — results are merged and deduped by address.
3. One goroutine is spawned per discovered peer (capped at `-max-peers`): it dials, handshakes, sends `interested`, and sends its current bitfield so a peer connecting mid-download still learns what's already available.
4. Each peer goroutine keeps a sliding window of in-flight block requests (`PieceManager.ClaimBlocks`), topped up on every delivered block — pieces are chosen rarest-first, with an endgame fallback so one slow peer can't stall the last piece.
5. On each delivered block, `PieceManager.BlockReceived` assembles it into its piece; once complete, the piece is SHA1-verified and handed to `PieceStorage.WritePiece`, which enqueues it for an async disk-writer worker pool and returns immediately.
6. On successful verification, the piece is broadcast (`have`) to every other currently connected peer, and `ProgressReporter.OnPieceVerified` emits a JSON event.
7. Every 10 seconds, the choking algorithm re-ranks interested peers by how much data they've sent us recently and unchokes the top slots (plus one optimistic slot for newcomers) — this is what keeps other leechers willing to send us data back.
8. When every piece is verified, `PieceStorage.Close` drains the write queue, all file handles are closed, and a `complete` event is emitted.

---

## CLI Flags & Event Overview

| Flag | Default | Description |
|---|---|---|
| `-torrent` | *(required)* | Path to the `.torrent` file |
| `-dir` | `downloads` | Directory to download into |
| `-port` | `6881` | Port advertised to trackers/peers |
| `-max-peers` | `150` | Maximum simultaneous peer connections |
| `-dht` | `true` | Also discover peers via the mainline DHT |

| Event `type` | Description |
|---|---|
| `start` | Torrent parsed, download beginning — name, size, piece count, info hash |
| `peers_discovered` | Total unique peers found across trackers + DHT |
| `peer_connected` / `peer_disconnected` | One peer connection opened/closed |
| `piece_verified` / `piece_failed` | A piece passed or failed SHA1 verification |
| `progress` | Periodic snapshot — bytes/pieces done, active/total peers |
| `complete` | Every piece verified and flushed to disk |
| `error` | Non-fatal error (tracker unreachable, disk write failure, ...) |

---

## Security

`filestorage` validates every path segment from the `.torrent` at parse time and re-checks containment before every disk read/write (`filepath.Rel` against the download dir) — closes a path-traversal hole where a malicious `.torrent` could set a file path like `../../etc/passwd`.

---

## Performance

Measured against a real, live swarm (Sintel test torrent, 129MB, 987 pieces): a naive baseline (sequential piece-at-a-time, tracker-only, synchronous disk I/O) did 73% in 60s. With request pipelining, rarest-first + endgame selection, async disk I/O, DHT, and reciprocity choking: **100% in ~30s**, byte-identical output, 0 data races under `-race`.

Not a claim to universally beat libtorrent — a decade of tuning (adaptive bandwidth-delay-product request windows, uTP/LEDBAT, a full k-bucket DHT routing table, PEX) isn't fully replicated. But the core throughput mechanics are real, not aspirational.

---

## Tests

```bash
go test ./...          # unit tests
go build -race ./...   # then run a real download — verified 0 races
```

Verified end-to-end against a real, live swarm: tracker + DHT peer discovery, peer handshake, pipelined block requests, rarest-first + endgame piece selection, SHA1 verification, reciprocal upload serving, and multi-file async disk I/O — race-clean, byte-identical output.

---

## Disclaimer

Educational purposes only. Do not use to download copyrighted or unauthorized content.

---

## Contact

[Eduardo Alcaria](https://github.com/EduardoAlcaria) · [LinkedIn](https://www.linkedin.com/in/eduardo-alcaria-lopes-7072b02b4/) · eduardoalcarialopes@gmail.com
