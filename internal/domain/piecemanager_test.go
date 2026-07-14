package domain

import (
	"bytes"
	"crypto/sha1"
	"testing"
)

func makeTestTorrent(pieceData [][]byte) *Torrent {
	var pieces []byte
	var total int64
	for _, p := range pieceData {
		sum := sha1.Sum(p)
		pieces = append(pieces, sum[:]...)
		total += int64(len(p))
	}
	return &Torrent{
		Name:        "test",
		PieceLength: int64(len(pieceData[0])),
		Pieces:      pieces,
		TotalLength: total,
		Files:       []TorrentFile{{Path: []string{"test"}, Length: total}},
	}
}

func TestPieceManagerAssemblesAndVerifies(t *testing.T) {
	// Bigger than BlockSize so the piece splits into two real blocks,
	// exercising the same multi-block assembly path production code uses.
	piece0 := bytes.Repeat([]byte{0xAB}, BlockSize+100)
	torrent := makeTestTorrent([][]byte{piece0})
	pm := NewPieceManager(torrent)

	assignments := pm.ClaimBlocks("peer-1", 10)
	if len(assignments) != 2 {
		t.Fatalf("expected 2 block assignments for a %d-byte piece, got %d: %+v", len(piece0), len(assignments), assignments)
	}
	if got := pm.OutstandingCount("peer-1"); got != 2 {
		t.Fatalf("expected 2 outstanding blocks for peer-1, got %d", got)
	}

	var complete []byte
	for _, a := range assignments {
		complete = pm.BlockReceived("peer-1", a.PieceIndex, a.Begin, piece0[a.Begin:a.Begin+a.Length])
	}
	if complete == nil {
		t.Fatal("expected assembled piece after both blocks delivered, got nil")
	}
	if !bytes.Equal(complete, piece0) {
		t.Fatal("assembled piece does not match original bytes")
	}
	if pm.OutstandingCount("peer-1") != 0 {
		t.Fatal("expected outstanding count to drop to 0 once blocks are delivered")
	}

	if !pm.StorePiece(0, complete) {
		t.Fatal("StorePiece rejected a correctly-hashed piece")
	}
	if !pm.IsComplete() {
		t.Fatal("expected torrent to be complete after its only piece stored")
	}
}

func TestPieceManagerRejectsBadHashAndAllowsRetry(t *testing.T) {
	piece0 := []byte("AAAABBBB")
	torrent := makeTestTorrent([][]byte{piece0})
	pm := NewPieceManager(torrent)

	if pm.StorePiece(0, []byte("WRONGDATA")) {
		t.Fatal("StorePiece accepted data with a mismatched hash")
	}
	if pm.IsComplete() {
		t.Fatal("torrent reported complete despite a failed verification")
	}

	assignments := pm.ClaimBlocks("peer-2", 10)
	if len(assignments) != 1 || assignments[0].PieceIndex != 0 {
		t.Fatalf("expected piece 0 to be retryable after failed verify, got %+v", assignments)
	}
}

func TestPieceManagerRarestFirst(t *testing.T) {
	pieceData := [][]byte{[]byte("piece000"), []byte("piece111"), []byte("piece222")}
	torrent := makeTestTorrent(pieceData)
	pm := NewPieceManager(torrent)

	pm.IncrementAvailability(0)
	pm.IncrementAvailability(0)
	pm.IncrementAvailability(0)
	pm.IncrementAvailability(1) // piece 1 is rarest — only one peer has it
	pm.IncrementAvailability(2)
	pm.IncrementAvailability(2)

	assignments := pm.ClaimBlocks("peer-1", 1)
	if len(assignments) != 1 || assignments[0].PieceIndex != 1 {
		t.Fatalf("expected rarest piece (1) to be picked first, got %+v", assignments)
	}
}

func TestPieceManagerEndgameDuplicatesOnlyForIdlePeer(t *testing.T) {
	piece0 := []byte("AAAABBBB") // single block, < BlockSize
	torrent := makeTestTorrent([][]byte{piece0})
	pm := NewPieceManager(torrent)

	first := pm.ClaimBlocks("peer-1", 1)
	if len(first) != 1 {
		t.Fatalf("expected peer-1 to claim the only block, got %+v", first)
	}

	// peer-2 is completely idle and nothing free is left: should get an
	// endgame duplicate of the same block peer-1 already holds.
	second := pm.ClaimBlocks("peer-2", 1)
	if len(second) != 1 || second[0].PieceIndex != first[0].PieceIndex || second[0].Begin != first[0].Begin {
		t.Fatalf("expected peer-2 to get an endgame duplicate of %+v, got %+v", first, second)
	}
	if pm.OutstandingCount("peer-2") != 1 {
		t.Fatal("expected peer-2's endgame claim to count toward its outstanding requests")
	}

	// Whichever arrives first wins; the second delivery for an already-
	// completed piece must not resurrect or corrupt state.
	complete := pm.BlockReceived("peer-1", 0, 0, piece0)
	if complete == nil {
		t.Fatal("expected the piece to complete from peer-1's delivery")
	}
	if !pm.StorePiece(0, complete) {
		t.Fatal("StorePiece rejected a correctly-hashed piece")
	}
	if late := pm.BlockReceived("peer-2", 0, 0, piece0); late != nil {
		t.Fatalf("expected the late duplicate delivery to be ignored, got %v", late)
	}
}

func TestPieceManagerReleasePeerFreesUnclaimedBlocks(t *testing.T) {
	piece0 := bytes.Repeat([]byte{0xCD}, BlockSize+100) // two blocks
	torrent := makeTestTorrent([][]byte{piece0})
	pm := NewPieceManager(torrent)

	claimed := pm.ClaimBlocks("peer-1", 10)
	if len(claimed) != 2 {
		t.Fatalf("expected 2 blocks claimed, got %d", len(claimed))
	}

	pm.ReleasePeer("peer-1")
	if got := pm.OutstandingCount("peer-1"); got != 0 {
		t.Fatalf("expected 0 outstanding after release, got %d", got)
	}

	// Both blocks should be claimable again by someone else.
	reclaimed := pm.ClaimBlocks("peer-2", 10)
	if len(reclaimed) != 2 {
		t.Fatalf("expected both released blocks to be reclaimable, got %d: %+v", len(reclaimed), reclaimed)
	}
}
