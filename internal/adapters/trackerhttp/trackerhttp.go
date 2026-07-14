// Package trackerhttp implements the HTTP/HTTPS tracker announce protocol
// (BEP 3). The original Python client called this and crashed — the method
// was never implemented. This is the real thing.
package trackerhttp

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

const requestTimeout = 15 * time.Second

type Announcer struct {
	client *http.Client
}

func New() *Announcer {
	return &Announcer{client: &http.Client{Timeout: requestTimeout}}
}

func (a *Announcer) Supports(announceURL string) bool {
	return strings.HasPrefix(announceURL, "http://") || strings.HasPrefix(announceURL, "https://")
}

func (a *Announcer) Announce(announceURL string, t *domain.Torrent, peerID [20]byte, listenPort uint16) ([]ports.PeerAddr, error) {
	query := strings.Join([]string{
		"info_hash=" + percentEncode(t.InfoHash[:]),
		"peer_id=" + percentEncode(peerID[:]),
		"port=" + strconv.Itoa(int(listenPort)),
		"uploaded=0",
		"downloaded=0",
		"left=" + strconv.FormatInt(t.TotalLength, 10),
		"compact=1",
		"event=started",
	}, "&")

	sep := "?"
	if strings.Contains(announceURL, "?") {
		sep = "&"
	}
	fullURL := announceURL + sep + query

	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building tracker request: %w", err)
	}
	req.Header.Set("User-Agent", "torrentmotor/1.0")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting tracker: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB response cap
	if err != nil {
		return nil, fmt.Errorf("reading tracker response: %w", err)
	}

	decoded, err := domain.BDecode(body)
	if err != nil {
		return nil, fmt.Errorf("decoding tracker response: %w", err)
	}
	dict, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tracker response is not a bencoded dict")
	}
	if reason, ok := dict["failure reason"]; ok {
		if b, ok := reason.([]byte); ok {
			return nil, fmt.Errorf("tracker failure: %s", string(b))
		}
		return nil, fmt.Errorf("tracker failure")
	}

	peersRaw, ok := dict["peers"]
	if !ok {
		return nil, nil
	}
	switch peers := peersRaw.(type) {
	case []byte:
		return parseCompactPeers(peers), nil
	case []any:
		return parseDictPeers(peers), nil
	default:
		return nil, fmt.Errorf("unexpected 'peers' field type %T", peersRaw)
	}
}

func parseCompactPeers(data []byte) []ports.PeerAddr {
	var peers []ports.PeerAddr
	for i := 0; i+6 <= len(data); i += 6 {
		ip := net.IPv4(data[i], data[i+1], data[i+2], data[i+3]).String()
		port := binary.BigEndian.Uint16(data[i+4 : i+6])
		peers = append(peers, ports.PeerAddr{IP: ip, Port: port})
	}
	return peers
}

func parseDictPeers(entries []any) []ports.PeerAddr {
	var peers []ports.PeerAddr
	for _, entryRaw := range entries {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			continue
		}
		ipRaw, hasIP := entry["ip"]
		portRaw, hasPort := entry["port"]
		if !hasIP || !hasPort {
			continue
		}
		ipBytes, ok := ipRaw.([]byte)
		if !ok {
			continue
		}
		portNum, ok := portRaw.(int64)
		if !ok {
			continue
		}
		peers = append(peers, ports.PeerAddr{IP: string(ipBytes), Port: uint16(portNum)})
	}
	return peers
}

const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"

// percentEncode escapes raw bytes for a tracker query string the way
// BitTorrent trackers expect: every byte except RFC 3986 unreserved
// characters is percent-encoded, including inside binary info_hash/peer_id.
func percentEncode(data []byte) string {
	var b strings.Builder
	b.Grow(len(data) * 3)
	for _, c := range data {
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
