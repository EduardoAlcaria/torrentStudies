// Package trackerudp implements the UDP tracker protocol (BEP 15).
package trackerudp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

const (
	protocolMagic = 0x41727101980
	actionConnect = 0
	actionAnnounce = 1
	requestTimeout = 10 * time.Second
)

type Announcer struct{}

func New() *Announcer { return &Announcer{} }

func (a *Announcer) Supports(announceURL string) bool {
	return strings.HasPrefix(announceURL, "udp://")
}

func (a *Announcer) Announce(announceURL string, t *domain.Torrent, peerID [20]byte, listenPort uint16) ([]ports.PeerAddr, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return nil, fmt.Errorf("parsing tracker url: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "80"
	}

	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("resolving tracker address: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, fmt.Errorf("dialing tracker: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(requestTimeout))

	connectionID, err := connect(conn)
	if err != nil {
		return nil, err
	}
	return announce(conn, connectionID, t, peerID, listenPort)
}

func connect(conn *net.UDPConn) (uint64, error) {
	transactionID := randomUint32()
	req := make([]byte, 16)
	binary.BigEndian.PutUint64(req[0:8], protocolMagic)
	binary.BigEndian.PutUint32(req[8:12], actionConnect)
	binary.BigEndian.PutUint32(req[12:16], transactionID)

	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("sending connect request: %w", err)
	}

	resp := make([]byte, 16)
	n, err := conn.Read(resp)
	if err != nil {
		return 0, fmt.Errorf("reading connect response: %w", err)
	}
	if n < 16 {
		return 0, fmt.Errorf("connect response too short: %d bytes", n)
	}
	respAction := binary.BigEndian.Uint32(resp[0:4])
	respTransactionID := binary.BigEndian.Uint32(resp[4:8])
	if respAction != actionConnect || respTransactionID != transactionID {
		return 0, fmt.Errorf("connect response mismatch")
	}
	return binary.BigEndian.Uint64(resp[8:16]), nil
}

func announce(conn *net.UDPConn, connectionID uint64, t *domain.Torrent, peerID [20]byte, listenPort uint16) ([]ports.PeerAddr, error) {
	transactionID := randomUint32()
	req := make([]byte, 98)
	binary.BigEndian.PutUint64(req[0:8], connectionID)
	binary.BigEndian.PutUint32(req[8:12], actionAnnounce)
	binary.BigEndian.PutUint32(req[12:16], transactionID)
	copy(req[16:36], t.InfoHash[:])
	copy(req[36:56], peerID[:])
	binary.BigEndian.PutUint64(req[56:64], 0)                    // downloaded
	binary.BigEndian.PutUint64(req[64:72], uint64(t.TotalLength)) // left
	binary.BigEndian.PutUint64(req[72:80], 0)                    // uploaded
	binary.BigEndian.PutUint32(req[80:84], 0)                    // event: none
	binary.BigEndian.PutUint32(req[84:88], 0)                    // ip: default
	binary.BigEndian.PutUint32(req[88:92], randomUint32())       // key
	binary.BigEndian.PutUint32(req[92:96], math.MaxUint32)       // num_want: -1
	binary.BigEndian.PutUint16(req[96:98], listenPort)

	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("sending announce request: %w", err)
	}

	resp := make([]byte, 8192)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("reading announce response: %w", err)
	}
	if n < 20 {
		return nil, fmt.Errorf("announce response too short: %d bytes", n)
	}
	respAction := binary.BigEndian.Uint32(resp[0:4])
	respTransactionID := binary.BigEndian.Uint32(resp[4:8])
	if respAction != actionAnnounce || respTransactionID != transactionID {
		return nil, fmt.Errorf("announce response mismatch")
	}

	return parseCompactPeers(resp[20:n]), nil
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

func randomUint32() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively unrecoverable entropy starvation;
		// fall back to a timestamp-derived value rather than a hard panic.
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}
