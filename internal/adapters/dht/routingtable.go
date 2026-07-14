package dht

import (
	"encoding/binary"
	"net"

	"github.com/EduardoAlcaria/Vow/internal/ports"
)

// NodeInfo is one known DHT node. ID is the zero value for bootstrap nodes
// until a response tells us their real ID — fine, since IDs are only used
// to sort candidates by distance, never to address a query (we dial by
// IP:port).
type NodeInfo struct {
	ID   [20]byte
	IP   string
	Port uint16
}

// xorDistance is BEP 5's node-distance metric.
func xorDistance(a, b [20]byte) [20]byte {
	var d [20]byte
	for i := range a {
		d[i] = a[i] ^ b[i]
	}
	return d
}

// closer reports whether a is closer to target than b (lexicographic
// compare of the XOR distance == numeric compare of the 160-bit distance).
func closer(target, a, b [20]byte) bool {
	da, db := xorDistance(target, a), xorDistance(target, b)
	for i := range da {
		if da[i] != db[i] {
			return da[i] < db[i]
		}
	}
	return false
}

// parseCompactPeer decodes one BEP 5 compact peer info (4 bytes IP + 2 bytes port).
func parseCompactPeer(b []byte) ports.PeerAddr {
	ip := net.IPv4(b[0], b[1], b[2], b[3]).String()
	port := binary.BigEndian.Uint16(b[4:6])
	return ports.PeerAddr{IP: ip, Port: port}
}

// parseCompactNodes decodes BEP 5 compact node info: 26 bytes each
// (20-byte node ID + 4-byte IP + 2-byte port).
func parseCompactNodes(data []byte) []NodeInfo {
	var nodes []NodeInfo
	for i := 0; i+26 <= len(data); i += 26 {
		var id [20]byte
		copy(id[:], data[i:i+20])
		ip := net.IPv4(data[i+20], data[i+21], data[i+22], data[i+23]).String()
		port := binary.BigEndian.Uint16(data[i+24 : i+26])
		nodes = append(nodes, NodeInfo{ID: id, IP: ip, Port: port})
	}
	return nodes
}
