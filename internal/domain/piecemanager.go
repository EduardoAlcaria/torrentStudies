package domain

import (
	"crypto/sha1"
	"math/rand"
	"sort"
	"sync"
)

// blockKey identifies one block within the torrent.
type blockKey struct {
	piece int
	begin uint32
}

// blockMeta tracks one block's request/delivery state within an active piece.
type blockMeta struct {
	owner  string // "" = unclaimed; otherwise the peer id holding the primary request
	length uint32
	data   []byte // nil until the block has arrived
}

// activePiece is the in-flight bookkeeping for one piece that isn't
// complete yet: which of its blocks are claimed, by whom, and what's
// arrived so far.
type activePiece struct {
	blocks map[uint32]*blockMeta
}

// BlockAssignment is one block a peer should request.
type BlockAssignment struct {
	PieceIndex int
	Begin      uint32
	Length     uint32
}

// PieceManager tracks piece availability, in-flight block requests, and
// assembles/verifies completed pieces. Safe for concurrent use by one
// goroutine per peer.
//
// Piece selection is rarest-first among pieces not yet in progress, with
// ties broken randomly (mirrors libtorrent: avoids every peer racing the
// single rarest piece). Pieces already in progress are always topped up
// before a new one is opened, which keeps the number of simultaneously
// partial pieces bounded without a separate cap.
//
// Endgame: once ClaimBlocks finds nothing free to hand an otherwise-idle
// peer, it allows that peer one duplicate ("busy") request on a block
// someone else already holds. Whichever copy arrives first wins; the loser
// is silently discarded in BlockReceived. This is the same trade libtorrent
// makes — pay a little redundant bandwidth at the very end instead of
// letting one slow peer stall the last piece.
type PieceManager struct {
	torrent *Torrent

	mu           sync.Mutex
	have         []bool
	availability []int
	active       map[int]*activePiece

	// peerOutstanding[peerID] is the set of blocks currently requested by
	// that peer (primary claims and endgame duplicates alike) — used for
	// per-peer request-queue depth and to release claims on disconnect.
	peerOutstanding map[string]map[blockKey]struct{}
}

func NewPieceManager(t *Torrent) *PieceManager {
	return &PieceManager{
		torrent:         t,
		have:            make([]bool, t.NumPieces()),
		availability:    make([]int, t.NumPieces()),
		active:          make(map[int]*activePiece),
		peerOutstanding: make(map[string]map[blockKey]struct{}),
	}
}

// IncrementAvailability records that some peer has the given piece (from a
// "have" message).
func (pm *PieceManager) IncrementAvailability(pieceIndex int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pieceIndex >= 0 && pieceIndex < len(pm.availability) {
		pm.availability[pieceIndex]++
	}
}

// IncrementAvailabilityMany records availability for every piece index in
// indices (from a "bitfield" message — see SetBitIndices).
func (pm *PieceManager) IncrementAvailabilityMany(indices []int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, i := range indices {
		if i >= 0 && i < len(pm.availability) {
			pm.availability[i]++
		}
	}
}

// OutstandingCount returns how many blocks peerID currently has requested
// (in flight), for sizing its next batch of requests.
func (pm *PieceManager) OutstandingCount(peerID string) int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return len(pm.peerOutstanding[peerID])
}

// ClaimBlocks hands peerID up to `want` blocks to request next: first
// topping up pieces already in progress, then opening new pieces
// rarest-first, then — only if peerID would otherwise be completely idle —
// one endgame duplicate request.
func (pm *PieceManager) ClaimBlocks(peerID string, want int) []BlockAssignment {
	if want <= 0 {
		return nil
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var assignments []BlockAssignment

	// 1. Resume pieces already in progress before starting new ones.
	for pieceIndex, ap := range pm.active {
		if len(assignments) >= want {
			break
		}
		for begin, meta := range ap.blocks {
			if len(assignments) >= want {
				break
			}
			if meta.owner == "" && meta.data == nil {
				meta.owner = peerID
				assignments = append(assignments, BlockAssignment{pieceIndex, begin, meta.length})
			}
		}
	}

	// 2. Open new pieces, rarest-first, while more are still wanted.
	for len(assignments) < want {
		pieceIndex, ok := pm.pickNewPieceLocked()
		if !ok {
			break
		}
		ap := pm.openPieceLocked(pieceIndex)
		for begin, meta := range ap.blocks {
			if len(assignments) >= want {
				break
			}
			meta.owner = peerID
			assignments = append(assignments, BlockAssignment{pieceIndex, begin, meta.length})
		}
	}

	// 3. Endgame: peerID is otherwise completely idle and nothing free is
	// left anywhere — allow one duplicate request on a block someone else
	// already holds, so a single slow peer can't stall the last piece.
	if len(assignments) == 0 && len(pm.peerOutstanding[peerID]) == 0 {
	endgameSearch:
		for pieceIndex, ap := range pm.active {
			for begin, meta := range ap.blocks {
				if meta.data != nil || meta.owner == "" {
					continue // already delivered, or actually free (step 1/2 would've taken it)
				}
				if _, dup := pm.peerOutstanding[peerID][blockKey{pieceIndex, begin}]; dup {
					continue
				}
				assignments = append(assignments, BlockAssignment{pieceIndex, begin, meta.length})
				break endgameSearch // one busy block is enough to keep this peer busy
			}
		}
	}

	if len(assignments) > 0 {
		outstanding := pm.peerOutstanding[peerID]
		if outstanding == nil {
			outstanding = make(map[blockKey]struct{})
			pm.peerOutstanding[peerID] = outstanding
		}
		for _, a := range assignments {
			outstanding[blockKey{a.PieceIndex, a.Begin}] = struct{}{}
		}
	}
	return assignments
}

// pickNewPieceLocked returns the rarest not-yet-active, not-yet-had piece,
// ties broken randomly. Caller must hold pm.mu.
func (pm *PieceManager) pickNewPieceLocked() (int, bool) {
	best := -1
	var candidates []int
	for i, done := range pm.have {
		if done {
			continue
		}
		if _, active := pm.active[i]; active {
			continue
		}
		switch {
		case best == -1 || pm.availability[i] < pm.availability[best]:
			best = i
			candidates = candidates[:0]
			candidates = append(candidates, i)
		case pm.availability[i] == pm.availability[best]:
			candidates = append(candidates, i)
		}
	}
	if best == -1 {
		return 0, false
	}
	return candidates[rand.Intn(len(candidates))], true
}

// openPieceLocked initializes an active piece's block bookkeeping. Caller
// must hold pm.mu.
func (pm *PieceManager) openPieceLocked(pieceIndex int) *activePiece {
	reqs := IterBlockRequests(pm.torrent.PieceLengthFor(pieceIndex))
	blocks := make(map[uint32]*blockMeta, len(reqs))
	for _, r := range reqs {
		blocks[r.Begin] = &blockMeta{length: r.Length}
	}
	ap := &activePiece{blocks: blocks}
	pm.active[pieceIndex] = ap
	return ap
}

// BlockReceived records a delivered block for peerID and returns the fully
// assembled piece once every block has arrived, or nil if more are still
// pending (or the piece was already completed/discarded — a redundant
// endgame delivery is simply ignored).
func (pm *PieceManager) BlockReceived(peerID string, pieceIndex int, begin uint32, data []byte) []byte {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.peerOutstanding[peerID], blockKey{pieceIndex, begin})

	ap, ok := pm.active[pieceIndex]
	if !ok {
		return nil
	}
	meta, ok := ap.blocks[begin]
	if !ok {
		return nil
	}
	if meta.data == nil {
		meta.data = data // first arrival wins; later duplicates are ignored
	}

	begins := make([]uint32, 0, len(ap.blocks))
	for b, m := range ap.blocks {
		if m.data == nil {
			return nil // still incomplete
		}
		begins = append(begins, b)
	}
	sort.Slice(begins, func(i, j int) bool { return begins[i] < begins[j] })

	assembled := make([]byte, 0, pm.torrent.PieceLengthFor(pieceIndex))
	for _, b := range begins {
		assembled = append(assembled, ap.blocks[b].data...)
	}
	return assembled
}

func (pm *PieceManager) VerifyPiece(pieceIndex int, data []byte) bool {
	sum := sha1.Sum(data)
	return string(sum[:]) == string(pm.torrent.PieceHash(pieceIndex))
}

// StorePiece verifies a completed piece and marks it done, or discards it
// (for a full retry from scratch) if the hash doesn't match.
func (pm *PieceManager) StorePiece(pieceIndex int, data []byte) bool {
	ok := pm.VerifyPiece(pieceIndex, data)

	pm.mu.Lock()
	if ok {
		pm.have[pieceIndex] = true
	}
	delete(pm.active, pieceIndex)
	for _, set := range pm.peerOutstanding {
		for key := range set {
			if key.piece == pieceIndex {
				delete(set, key)
			}
		}
	}
	pm.mu.Unlock()

	return ok
}

// ReleasePeer frees every block peerID had a primary (non-endgame) claim on
// but hadn't delivered, so other peers can pick them up, and forgets its
// outstanding-request bookkeeping. Call this when a peer disconnects.
func (pm *PieceManager) ReleasePeer(peerID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for key := range pm.peerOutstanding[peerID] {
		if ap, ok := pm.active[key.piece]; ok {
			if meta, ok := ap.blocks[key.begin]; ok && meta.owner == peerID && meta.data == nil {
				meta.owner = ""
			}
		}
	}
	delete(pm.peerOutstanding, peerID)
}

// Bitfield encodes our current have-state as a BEP 3 bitfield payload
// (MSB-first within each byte), to tell a newly connected peer what we can
// already serve — including pieces we finished before it connected, which
// a "have" broadcast alone would never tell it about.
func (pm *PieceManager) Bitfield() []byte {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	bf := make([]byte, (len(pm.have)+7)/8)
	for i, has := range pm.have {
		if has {
			bf[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	return bf
}

// HavePiece reports whether we've already verified and stored pieceIndex —
// used to decide whether an incoming peer request can be served.
func (pm *PieceManager) HavePiece(pieceIndex int) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pieceIndex >= 0 && pieceIndex < len(pm.have) && pm.have[pieceIndex]
}

func (pm *PieceManager) IsComplete() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, done := range pm.have {
		if !done {
			return false
		}
	}
	return true
}

func (pm *PieceManager) PiecesDone() int {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	count := 0
	for _, done := range pm.have {
		if done {
			count++
		}
	}
	return count
}

func (pm *PieceManager) Progress() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if len(pm.have) == 0 {
		return 0
	}
	count := 0
	for _, done := range pm.have {
		if done {
			count++
		}
	}
	return float64(count) / float64(len(pm.have)) * 100
}

func (pm *PieceManager) BytesDownloaded() int64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if len(pm.have) == 0 {
		return 0
	}
	count := 0
	for _, done := range pm.have {
		if done {
			count++
		}
	}
	return int64(float64(count) / float64(len(pm.have)) * float64(pm.torrent.TotalLength))
}
