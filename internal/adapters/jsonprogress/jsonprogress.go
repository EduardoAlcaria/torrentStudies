// Package jsonprogress reports download progress as newline-delimited JSON
// on an io.Writer (stdout by default). This is the engine's contract with
// any outside controller — a Python/Textual CLI, a daemon, a test harness —
// launched as a subprocess and reading one JSON object per line.
package jsonprogress

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

type Reporter struct {
	mu sync.Mutex
	w  io.Writer
}

func New() *Reporter {
	return &Reporter{w: os.Stdout}
}

// NewWithWriter is for tests/embedding — routes events to an arbitrary writer.
func NewWithWriter(w io.Writer) *Reporter {
	return &Reporter{w: w}
}

func (r *Reporter) emit(event map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	r.w.Write(line)
	r.w.Write([]byte("\n"))
}

func (r *Reporter) OnStart(t *domain.Torrent) {
	r.emit(map[string]any{
		"type":         "start",
		"name":         t.Name,
		"total_bytes":  t.TotalLength,
		"total_pieces": t.NumPieces(),
		"info_hash":    hex.EncodeToString(t.InfoHash[:]),
	})
}

func (r *Reporter) OnPeersDiscovered(count int) {
	r.emit(map[string]any{"type": "peers_discovered", "count": count})
}

func (r *Reporter) OnPeerConnected(addr ports.PeerAddr) {
	r.emit(map[string]any{"type": "peer_connected", "ip": addr.IP, "port": addr.Port})
}

func (r *Reporter) OnPeerDisconnected(addr ports.PeerAddr) {
	r.emit(map[string]any{"type": "peer_disconnected", "ip": addr.IP, "port": addr.Port})
}

func (r *Reporter) OnPieceVerified(index int) {
	r.emit(map[string]any{"type": "piece_verified", "index": index})
}

func (r *Reporter) OnPieceFailed(index int) {
	r.emit(map[string]any{"type": "piece_failed", "index": index})
}

func (r *Reporter) OnTick(stats ports.Stats) {
	r.emit(map[string]any{
		"type":             "progress",
		"downloaded_bytes": stats.DownloadedBytes,
		"total_bytes":      stats.TotalBytes,
		"pieces_done":      stats.PiecesDone,
		"total_pieces":     stats.TotalPieces,
		"active_peers":     stats.ActivePeers,
		"total_peers":      stats.TotalPeers,
	})
}

func (r *Reporter) OnComplete(t *domain.Torrent) {
	r.emit(map[string]any{"type": "complete", "name": t.Name})
}

func (r *Reporter) OnError(err error) {
	r.emit(map[string]any{"type": "error", "message": err.Error()})
}
