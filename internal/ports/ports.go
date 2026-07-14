// Package ports defines the boundary interfaces between the application
// core and the outside world (trackers, peer sockets, disk, UI). Adapters
// in internal/adapters/* implement these; internal/app depends only on
// these interfaces, never on a concrete adapter.
package ports

import (
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
)

// PeerAddr is an IP:port pair returned by a tracker.
type PeerAddr struct {
	IP   string
	Port uint16
}

// TrackerAnnouncer contacts one class of tracker (UDP, HTTP/HTTPS, ...) to
// obtain a peer list for a torrent.
type TrackerAnnouncer interface {
	Supports(announceURL string) bool
	Announce(announceURL string, t *domain.Torrent, peerID [20]byte, listenPort uint16) ([]PeerAddr, error)
}

// PeerDiscoverer finds peers for a torrent without being tied to a specific
// tracker URL — DHT, PEX. Runs alongside TrackerAnnouncer results;
// DownloadService merges and dedupes both.
type PeerDiscoverer interface {
	Discover(t *domain.Torrent, nodeID [20]byte) ([]PeerAddr, error)
}

// PeerLink is one connected, handshaken peer wire-protocol session.
type PeerLink interface {
	Handshake(infoHash, peerID [20]byte) (remotePeerID [20]byte, err error)
	SendMessage(id byte, payload []byte) error
	SendKeepAlive() error
	// ReceiveMessage blocks until a message arrives, the read times out, or
	// the link errors. A zero-length keep-alive is reported as id == 0xFF.
	ReceiveMessage(timeout time.Duration) (id byte, payload []byte, err error)
	Close() error
}

const KeepAliveMessageID byte = 0xFF

// PeerDialer opens new PeerLink connections.
type PeerDialer interface {
	Dial(ip string, port uint16, timeout time.Duration) (PeerLink, error)
}

// PieceStorage persists verified piece data to its final location on disk.
// WritePiece may be asynchronous — implementations are free to return
// before the data is durably written, as long as Close blocks until
// everything queued has been flushed.
type PieceStorage interface {
	Prepare(t *domain.Torrent, downloadDir string) error
	WritePiece(t *domain.Torrent, pieceIndex int, data []byte) error
	// ReadBlock reads a block from an already-completed piece, to serve an
	// incoming peer request (reciprocity — see internal/app's choking
	// logic). Only ever called for pieces PieceManager reports as had.
	ReadBlock(t *domain.Torrent, pieceIndex int, begin, length uint32) ([]byte, error)
	Close() error
}

// Stats is a snapshot of download progress for a ProgressReporter tick.
type Stats struct {
	DownloadedBytes int64
	TotalBytes      int64
	PiecesDone      int
	TotalPieces     int
	ActivePeers     int
	TotalPeers      int
}

// ProgressReporter surfaces download lifecycle events to the outside world
// (a CLI, a JSON event stream for another process, a GUI, ...).
type ProgressReporter interface {
	OnStart(t *domain.Torrent)
	OnPeersDiscovered(count int)
	OnPeerConnected(addr PeerAddr)
	OnPeerDisconnected(addr PeerAddr)
	OnPieceVerified(index int)
	OnPieceFailed(index int)
	OnTick(stats Stats)
	OnComplete(t *domain.Torrent)
	OnError(err error)
}
