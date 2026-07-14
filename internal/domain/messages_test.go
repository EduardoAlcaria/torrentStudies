package domain

import (
	"bytes"
	"testing"
)

func TestBitfieldRoundTripsThroughSetBitIndices(t *testing.T) {
	pm := NewPieceManager(&Torrent{
		PieceLength: 8,
		Pieces:      make([]byte, 20*10), // 10 fake pieces
		TotalLength: 80,
	})

	have := []int{0, 3, 7, 9}
	for _, i := range have {
		pm.have[i] = true
	}

	bf := pm.Bitfield()
	got := SetBitIndices(bf, 10)
	if len(got) != len(have) {
		t.Fatalf("expected %d set bits, got %d: %v", len(have), len(got), got)
	}
	for i, want := range have {
		if got[i] != want {
			t.Fatalf("bit %d: got %d, want %d (full: %v)", i, got[i], want, got)
		}
	}
}

func TestRequestAndPiecePayloadRoundTrip(t *testing.T) {
	reqPayload := RequestPayload(42, 16384, 4096)
	pieceIndex, begin, length, err := ParseRequest(reqPayload)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if pieceIndex != 42 || begin != 16384 || length != 4096 {
		t.Fatalf("got (%d,%d,%d), want (42,16384,4096)", pieceIndex, begin, length)
	}

	data := []byte("some block bytes")
	piecePayload := PiecePayload(pieceIndex, begin, data)
	gotIndex, gotBegin, gotData, err := ParsePieceBlock(piecePayload)
	if err != nil {
		t.Fatalf("ParsePieceBlock: %v", err)
	}
	if gotIndex != 42 || gotBegin != 16384 || !bytes.Equal(gotData, data) {
		t.Fatalf("got (%d,%d,%q), want (42,16384,%q)", gotIndex, gotBegin, gotData, data)
	}
}

func TestParseRequestRejectsWrongLength(t *testing.T) {
	if _, _, _, err := ParseRequest([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error for truncated request payload")
	}
}
