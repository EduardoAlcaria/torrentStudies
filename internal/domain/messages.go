package domain

import (
	"encoding/binary"
	"fmt"
)

// Peer wire protocol message IDs (BEP 3).
const (
	MsgChoke         byte = 0
	MsgUnchoke       byte = 1
	MsgInterested    byte = 2
	MsgNotInterested byte = 3
	MsgHave          byte = 4
	MsgBitfield      byte = 5
	MsgRequest       byte = 6
	MsgPiece         byte = 7
	MsgCancel        byte = 8
)

const (
	protocolName = "BitTorrent protocol"
	HandshakeLen = 49 + len(protocolName)
	BlockSize    = 16384
)

// BuildHandshake constructs the 68-byte BitTorrent handshake.
func BuildHandshake(infoHash [20]byte, peerID [20]byte) []byte {
	buf := make([]byte, 0, HandshakeLen)
	buf = append(buf, byte(len(protocolName)))
	buf = append(buf, protocolName...)
	buf = append(buf, make([]byte, 8)...) // reserved
	buf = append(buf, infoHash[:]...)
	buf = append(buf, peerID[:]...)
	return buf
}

// ParseHandshake validates and extracts info_hash/peer_id from a peer's handshake reply.
func ParseHandshake(data []byte) (infoHash [20]byte, peerID [20]byte, err error) {
	if len(data) != HandshakeLen || string(data[1:1+len(protocolName)]) != protocolName {
		return infoHash, peerID, fmt.Errorf("invalid or truncated handshake")
	}
	copy(infoHash[:], data[28:48])
	copy(peerID[:], data[48:68])
	return infoHash, peerID, nil
}

// BuildKeepAlive returns the zero-length keep-alive message.
func BuildKeepAlive() []byte {
	return []byte{0, 0, 0, 0}
}

// BuildMessage wraps a message ID and payload in the length-prefixed wire format.
func BuildMessage(id byte, payload []byte) []byte {
	length := uint32(len(payload) + 1)
	buf := make([]byte, 4+len(payload)+1)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = id
	copy(buf[5:], payload)
	return buf
}

// RequestPayload builds the 12-byte payload for a "request" message
// (piece index, begin offset, block length) — pair with PeerLink.SendMessage.
func RequestPayload(pieceIndex, begin, length uint32) []byte {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], pieceIndex)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	binary.BigEndian.PutUint32(payload[8:12], length)
	return payload
}

func ParseHave(payload []byte) (uint32, error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("malformed have payload: %d bytes", len(payload))
	}
	return binary.BigEndian.Uint32(payload), nil
}

// HavePayload builds the 4-byte payload for a "have" message.
func HavePayload(pieceIndex uint32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, pieceIndex)
	return payload
}

// ParseRequest splits an incoming "request" message payload into piece
// index, begin offset, and requested length — the receiving side of
// RequestPayload, used to serve a peer that's requesting data from us.
func ParseRequest(payload []byte) (pieceIndex, begin, length uint32, err error) {
	if len(payload) != 12 {
		return 0, 0, 0, fmt.Errorf("malformed request payload: %d bytes", len(payload))
	}
	pieceIndex = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	length = binary.BigEndian.Uint32(payload[8:12])
	return pieceIndex, begin, length, nil
}

// PiecePayload builds the payload for an outgoing "piece" message — the
// receiving side of ParsePieceBlock, used to serve a peer's request.
func PiecePayload(pieceIndex, begin uint32, data []byte) []byte {
	payload := make([]byte, 8+len(data))
	binary.BigEndian.PutUint32(payload[0:4], pieceIndex)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	copy(payload[8:], data)
	return payload
}

// ParsePieceBlock splits a "piece" message payload into index, begin, and block data.
func ParsePieceBlock(payload []byte) (pieceIndex, begin uint32, data []byte, err error) {
	if len(payload) < 8 {
		return 0, 0, nil, fmt.Errorf("truncated piece payload: %d bytes", len(payload))
	}
	pieceIndex = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	return pieceIndex, begin, payload[8:], nil
}

// BlockRequest is one (offset, length) block within a piece.
type BlockRequest struct {
	Begin  uint32
	Length uint32
}

// SetBitIndices returns the piece indices whose bit is set in a "bitfield"
// message payload (MSB-first within each byte, per BEP 3).
func SetBitIndices(bitfield []byte, numPieces int) []int {
	var indices []int
	for i := 0; i < numPieces; i++ {
		byteIndex := i / 8
		if byteIndex >= len(bitfield) {
			break
		}
		bitMask := byte(1 << (7 - uint(i%8)))
		if bitfield[byteIndex]&bitMask != 0 {
			indices = append(indices, i)
		}
	}
	return indices
}

// IterBlockRequests splits a piece of the given length into BlockSize-capped block requests.
func IterBlockRequests(pieceLength int64) []BlockRequest {
	var blocks []BlockRequest
	var offset int64
	for offset < pieceLength {
		size := int64(BlockSize)
		if remaining := pieceLength - offset; remaining < size {
			size = remaining
		}
		blocks = append(blocks, BlockRequest{Begin: uint32(offset), Length: uint32(size)})
		offset += size
	}
	return blocks
}
