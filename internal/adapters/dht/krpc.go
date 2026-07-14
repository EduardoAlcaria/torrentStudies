// Package dht implements a lookup-only mainline DHT client (BEP 5): enough
// to bootstrap into the DHT and run an iterative get_peers lookup for one
// info_hash. It does not listen for or answer incoming queries from other
// nodes — this process exits once its download finishes, so there's no
// long-lived node identity worth being a routing hop for.
// # ponytail: lookup-only, no announce_peer, no persistent routing table.
// Add announce_peer if you also want to help other peers find you; add a
// real k-bucket table with refresh if this ever runs as a long-lived node.
package dht

import (
	"crypto/rand"
	"fmt"

	"github.com/EduardoAlcaria/Vow/internal/domain"
)

// KRPC messages are just bencoded dicts (BEP 5) — internal/domain's codec
// is generic enough to reuse as-is, no DHT-specific wire format needed.

func randomTransactionID() string {
	b := make([]byte, 2)
	rand.Read(b)
	return string(b)
}

func buildQuery(transactionID, queryName string, args map[string]any) []byte {
	msg := map[string]any{
		"t": transactionID,
		"y": "q",
		"q": queryName,
		"a": args,
	}
	raw, _ := domain.BEncode(msg) // args are all []byte/int64/string — never fails
	return raw
}

type krpcResponse struct {
	transactionID string
	reply         map[string]any
	isError       bool
}

func parseResponse(raw []byte) (*krpcResponse, error) {
	decoded, err := domain.BDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("dht: decoding response: %w", err)
	}
	dict, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dht: response is not a dict")
	}
	tBytes, _ := dict["t"].([]byte)
	y, _ := dict["y"].([]byte)
	if string(y) == "e" {
		return &krpcResponse{transactionID: string(tBytes), isError: true}, nil
	}
	r, ok := dict["r"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("dht: response missing 'r' dict")
	}
	return &krpcResponse{transactionID: string(tBytes), reply: r}, nil
}
