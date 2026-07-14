// Package app is the application core: it orchestrates the domain (piece
// selection, protocol messages) through the ports (trackers, peer links,
// storage, progress) without knowing which concrete adapter it's talking
// to. This is the hexagon's center.
package app

import (
	"crypto/rand"
	"fmt"
	mrand "math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

const (
	peerReadTimeout        = 5 * time.Second
	peerDialTimeout        = 5 * time.Second
	maxConsecutiveTimeouts = 15
	tickInterval           = 1 * time.Second

	// Per-peer request-pipeline depth (blocks kept in flight). Real
	// libtorrent derives this from a bandwidth-delay-product estimate
	// (queue_time * measured_rate / block_size, default ~3 seconds of
	// data); this is a fixed-cap approximation of the same idea — start
	// small, grow additively as blocks actually arrive, stop at a ceiling.
	// # ponytail: fixed ramp instead of adaptive BDP; revisit if peers with
	// very different bandwidth/RTT need per-peer tuning beyond this cap.
	initialQueueSize = 4
	maxQueueSize     = 64

	// Choking algorithm: unlike libtorrent (a multi-torrent desktop client
	// that has to be a fair swarm citizen across dozens of torrents at
	// once), this engine only ever runs one torrent per process and its
	// only goal is *our own* download speed. So the upload side is
	// deliberately minimal: a handful of slots given to whichever
	// interested peers have sent us the most data recently (tit-for-tat,
	// same signal libtorrent uses), re-ranked periodically, plus one
	// optimistic slot so a newcomer that hasn't had a chance to reciprocate
	// yet isn't permanently starved. This exists only to keep other
	// leechers willing to unchoke us in return — it is not trying to be a
	// good long-term swarm citizen, and unchokeSlots is intentionally well
	// below libtorrent's default of 8 so upload never meaningfully
	// competes with our own download for bandwidth.
	unchokeSlots    = 4
	unchokeInterval = 10 * time.Second
)

type DownloadService struct {
	Announcers  []ports.TrackerAnnouncer
	Discoverers []ports.PeerDiscoverer // e.g. DHT — optional, torrent-scoped peer sources
	Dialer      ports.PeerDialer
	Storage     ports.PieceStorage
	Progress    ports.ProgressReporter
	MaxPeers    int
	ListenPort  uint16
}

func NewPeerID() [20]byte {
	var id [20]byte
	copy(id[:], "-GT0001-")
	rand.Read(id[8:])
	return id
}

// Run downloads t into downloadDir, blocking until complete or until every
// peer connection has closed with the download incomplete.
func (s *DownloadService) Run(t *domain.Torrent, downloadDir string) error {
	peerID := NewPeerID()

	if err := s.Storage.Prepare(t, downloadDir); err != nil {
		s.Progress.OnError(err)
		s.Storage.Close()
		return fmt.Errorf("preparing storage: %w", err)
	}
	defer s.Storage.Close()

	peerAddrs := s.discoverAllPeers(t, peerID)
	s.Progress.OnPeersDiscovered(len(peerAddrs))
	if len(peerAddrs) == 0 {
		err := fmt.Errorf("no peers available from any tracker or discoverer")
		s.Progress.OnError(err)
		return err
	}
	if s.MaxPeers > 0 && len(peerAddrs) > s.MaxPeers {
		peerAddrs = peerAddrs[:s.MaxPeers]
	}

	s.Progress.OnStart(t)
	pieceManager := domain.NewPieceManager(t)
	registry := newPeerRegistry()

	var activePeers int64
	var wg sync.WaitGroup
	for _, addr := range peerAddrs {
		wg.Add(1)
		go func(addr ports.PeerAddr) {
			defer wg.Done()
			s.handlePeer(addr, t, peerID, pieceManager, registry, &activePeers)
		}(addr)
	}

	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	unchokeTicker := time.NewTicker(unchokeInterval)
	defer unchokeTicker.Stop()

	for {
		select {
		case <-allDone:
			return s.finish(t, pieceManager)
		case <-unchokeTicker.C:
			registry.recalculateChoking(unchokeSlots)
		case <-ticker.C:
			s.Progress.OnTick(ports.Stats{
				DownloadedBytes: pieceManager.BytesDownloaded(),
				TotalBytes:      t.TotalLength,
				PiecesDone:      pieceManager.PiecesDone(),
				TotalPieces:     t.NumPieces(),
				ActivePeers:     int(atomic.LoadInt64(&activePeers)),
				TotalPeers:      len(peerAddrs),
			})
			if pieceManager.IsComplete() {
				return s.finish(t, pieceManager)
			}
		}
	}
}

func (s *DownloadService) finish(t *domain.Torrent, pm *domain.PieceManager) error {
	if pm.IsComplete() {
		s.Progress.OnComplete(t)
		return nil
	}
	err := fmt.Errorf("download stopped at %.1f%% (%d/%d pieces)", pm.Progress(), pm.PiecesDone(), t.NumPieces())
	s.Progress.OnError(err)
	return err
}

// discoverAllPeers merges tracker-tier results with every configured
// PeerDiscoverer (e.g. DHT), deduped by address.
func (s *DownloadService) discoverAllPeers(t *domain.Torrent, peerID [20]byte) []ports.PeerAddr {
	peers := s.discoverPeers(t, peerID)
	for _, d := range s.Discoverers {
		extra, err := d.Discover(t, peerID)
		if err != nil {
			s.Progress.OnError(fmt.Errorf("peer discoverer: %w", err))
			continue
		}
		peers = append(peers, extra...)
	}
	return dedupePeers(peers)
}

func dedupePeers(peers []ports.PeerAddr) []ports.PeerAddr {
	seen := make(map[string]bool, len(peers))
	out := make([]ports.PeerAddr, 0, len(peers))
	for _, p := range peers {
		key := fmt.Sprintf("%s:%d", p.IP, p.Port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// discoverPeers tries tracker tiers in order; within a tier, the first
// announcer to return a non-empty peer list wins (BEP 3 tier semantics).
func (s *DownloadService) discoverPeers(t *domain.Torrent, peerID [20]byte) []ports.PeerAddr {
	for _, tier := range t.AnnounceList {
		for _, announceURL := range tier {
			for _, announcer := range s.Announcers {
				if !announcer.Supports(announceURL) {
					continue
				}
				peers, err := announcer.Announce(announceURL, t, peerID, s.ListenPort)
				if err != nil {
					s.Progress.OnError(fmt.Errorf("tracker %s: %w", announceURL, err))
					continue
				}
				if len(peers) > 0 {
					return peers
				}
			}
		}
	}
	return nil
}

// peerState is one connected peer's upload-side bookkeeping: whether
// they're interested in us, whether we're choking them, and how much
// they've sent us recently (the reciprocity signal the choking algorithm
// ranks on).
type peerState struct {
	link            ports.PeerLink
	interested      bool
	weChoking       bool
	bytesDownloaded int64 // cumulative bytes received from this peer
	lastSnapshot    int64 // bytesDownloaded as of the last recalculation, for a delta
}

// peerRegistry tracks every currently connected peer: it's what makes a
// completed piece broadcastable ("have") to the whole swarm we're talking
// to, and it's the shared state the choking algorithm ranks and acts on.
type peerRegistry struct {
	mu    sync.Mutex
	peers map[string]*peerState
}

func newPeerRegistry() *peerRegistry {
	return &peerRegistry{peers: make(map[string]*peerState)}
}

func (r *peerRegistry) add(peerID string, link ports.PeerLink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[peerID] = &peerState{link: link, weChoking: true}
}

func (r *peerRegistry) remove(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.peers, peerID)
}

func (r *peerRegistry) isChoking(peerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.peers[peerID]
	return !ok || ps.weChoking
}

// recordDownload feeds the reciprocity signal: n bytes of real piece data
// just arrived from peerID.
func (r *peerRegistry) recordDownload(peerID string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ps, ok := r.peers[peerID]; ok {
		ps.bytesDownloaded += int64(n)
	}
}

// onPeerInterested records interest and, if an upload slot is currently
// free, unchokes immediately rather than waiting for the next periodic
// recalculation — keeps a small/early swarm responsive.
func (r *peerRegistry) onPeerInterested(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ps, ok := r.peers[peerID]
	if !ok {
		return
	}
	ps.interested = true
	if !ps.weChoking {
		return
	}
	unchoked := 0
	for _, other := range r.peers {
		if !other.weChoking {
			unchoked++
		}
	}
	if unchoked < unchokeSlots {
		if err := ps.link.SendMessage(domain.MsgUnchoke, nil); err == nil {
			ps.weChoking = false
		}
	}
}

func (r *peerRegistry) onPeerNotInterested(peerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ps, ok := r.peers[peerID]; ok {
		ps.interested = false
	}
}

// recalculateChoking re-ranks interested peers by how much they've sent us
// since the last round (tit-for-tat) and unchokes the top unchokeSlots,
// plus one optimistic pick outside that ranking so a peer that hasn't had
// a chance to reciprocate yet still eventually gets one. Mirrors
// libtorrent's periodic unchoke/optimistic-unchoke cycle, just with far
// fewer slots since we're not trying to be generous — only responsive.
func (r *peerRegistry) recalculateChoking(slots int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	type candidate struct {
		id    string
		state *peerState
		delta int64
	}
	var interested []candidate
	for id, ps := range r.peers {
		if ps.interested {
			interested = append(interested, candidate{id, ps, ps.bytesDownloaded - ps.lastSnapshot})
		}
	}
	sort.Slice(interested, func(i, j int) bool { return interested[i].delta > interested[j].delta })

	unchoke := make(map[string]bool, slots+1)
	for i := 0; i < len(interested) && i < slots; i++ {
		unchoke[interested[i].id] = true
	}
	if len(interested) > slots {
		pick := slots + mrand.Intn(len(interested)-slots)
		unchoke[interested[pick].id] = true
	}

	for id, ps := range r.peers {
		switch {
		case unchoke[id] && ps.weChoking:
			if err := ps.link.SendMessage(domain.MsgUnchoke, nil); err == nil {
				ps.weChoking = false
			}
		case !unchoke[id] && !ps.weChoking:
			if err := ps.link.SendMessage(domain.MsgChoke, nil); err == nil {
				ps.weChoking = true
			}
		}
		ps.lastSnapshot = ps.bytesDownloaded
	}
}

func (r *peerRegistry) broadcastHave(pieceIndex int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	payload := domain.HavePayload(uint32(pieceIndex))
	for _, ps := range r.peers {
		ps.link.SendMessage(domain.MsgHave, payload) // best-effort; a dead link errors out and gets reaped on its own goroutine
	}
}

func (s *DownloadService) handlePeer(addr ports.PeerAddr, t *domain.Torrent, peerID [20]byte, pm *domain.PieceManager, registry *peerRegistry, activePeers *int64) {
	link, err := s.Dialer.Dial(addr.IP, addr.Port, peerDialTimeout)
	if err != nil {
		return
	}
	defer link.Close()

	if _, err := link.Handshake(t.InfoHash, peerID); err != nil {
		return
	}

	atomic.AddInt64(activePeers, 1)
	defer atomic.AddInt64(activePeers, -1)
	s.Progress.OnPeerConnected(addr)
	defer s.Progress.OnPeerDisconnected(addr)

	if err := link.SendMessage(domain.MsgInterested, nil); err != nil {
		return
	}
	// Tell this peer what we can already serve — including pieces
	// finished before it connected, which future "have" broadcasts alone
	// would never inform it about.
	link.SendMessage(domain.MsgBitfield, pm.Bitfield())

	peerIDStr := fmt.Sprintf("%s:%d", addr.IP, addr.Port)
	registry.add(peerIDStr, link)
	defer registry.remove(peerIDStr)
	defer pm.ReleasePeer(peerIDStr)

	peerChoking := true
	desiredQueueSize := initialQueueSize
	timeouts := 0

	for timeouts < maxConsecutiveTimeouts && !pm.IsComplete() {
		id, payload, err := link.ReceiveMessage(peerReadTimeout)
		if err != nil {
			timeouts++
			if sendErr := link.SendKeepAlive(); sendErr != nil {
				break
			}
			continue
		}
		timeouts = 0

		switch id {
		case ports.KeepAliveMessageID:
			// no-op
		case domain.MsgChoke:
			peerChoking = true
		case domain.MsgUnchoke:
			peerChoking = false
		case domain.MsgInterested:
			registry.onPeerInterested(peerIDStr)
		case domain.MsgNotInterested:
			registry.onPeerNotInterested(peerIDStr)
		case domain.MsgHave:
			if pieceIndex, err := domain.ParseHave(payload); err == nil {
				pm.IncrementAvailability(int(pieceIndex))
			}
		case domain.MsgBitfield:
			pm.IncrementAvailabilityMany(domain.SetBitIndices(payload, t.NumPieces()))
		case domain.MsgRequest:
			pieceIndex, begin, length, err := domain.ParseRequest(payload)
			if err != nil || registry.isChoking(peerIDStr) || !pm.HavePiece(int(pieceIndex)) {
				continue
			}
			data, err := s.Storage.ReadBlock(t, int(pieceIndex), begin, length)
			if err != nil {
				s.Progress.OnError(fmt.Errorf("serving block to %s: %w", peerIDStr, err))
				continue
			}
			link.SendMessage(domain.MsgPiece, domain.PiecePayload(pieceIndex, begin, data))
		case domain.MsgPiece:
			pieceIndex, begin, data, err := domain.ParsePieceBlock(payload)
			if err != nil {
				continue
			}
			registry.recordDownload(peerIDStr, len(data))
			// Additive slow-start-style ramp: grow the pipeline depth on
			// every delivered block, capped, so a fast peer fills more of
			// the pipe over time instead of staying stuck at the floor.
			if desiredQueueSize < maxQueueSize {
				desiredQueueSize++
			}
			if complete := pm.BlockReceived(peerIDStr, int(pieceIndex), begin, data); complete != nil {
				if pm.StorePiece(int(pieceIndex), complete) {
					if err := s.Storage.WritePiece(t, int(pieceIndex), complete); err != nil {
						s.Progress.OnError(err)
					}
					s.Progress.OnPieceVerified(int(pieceIndex))
					registry.broadcastHave(int(pieceIndex))
				} else {
					s.Progress.OnPieceFailed(int(pieceIndex))
				}
			}
		}

		if !peerChoking {
			if want := desiredQueueSize - pm.OutstandingCount(peerIDStr); want > 0 {
				for _, block := range pm.ClaimBlocks(peerIDStr, want) {
					reqPayload := domain.RequestPayload(uint32(block.PieceIndex), block.Begin, block.Length)
					if err := link.SendMessage(domain.MsgRequest, reqPayload); err != nil {
						break
					}
				}
			}
		}
	}
}
