// Package peertcp implements the peer wire protocol transport over a raw
// TCP socket. No BitTorrent framework, no libtorrent — just net/socket
// plumbing around internal/domain's pure message encode/decode.
package peertcp

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

type Dialer struct{}

func New() *Dialer { return &Dialer{} }

func (d *Dialer) Dial(ip string, port uint16, timeout time.Duration) (ports.PeerLink, error) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, fmt.Sprintf("%d", port)), timeout)
	if err != nil {
		return nil, err
	}
	return &link{conn: conn}, nil
}

type link struct {
	conn net.Conn

	// writeMu serializes writes. Once a peer is registered for "have"
	// broadcasts, its own request-sending goroutine and the choking
	// coordinator (a different goroutine) can both call SendMessage on the
	// same connection concurrently — without this, two concurrent Write
	// calls could interleave mid-message and corrupt the wire stream.
	writeMu sync.Mutex
}

func (l *link) Handshake(infoHash, peerID [20]byte) ([20]byte, error) {
	var remotePeerID [20]byte

	l.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := l.conn.Write(domain.BuildHandshake(infoHash, peerID)); err != nil {
		return remotePeerID, fmt.Errorf("sending handshake: %w", err)
	}

	resp := make([]byte, domain.HandshakeLen)
	if _, err := io.ReadFull(l.conn, resp); err != nil {
		return remotePeerID, fmt.Errorf("reading handshake reply: %w", err)
	}

	remoteInfoHash, remotePeerID, err := domain.ParseHandshake(resp)
	if err != nil {
		return remotePeerID, err
	}
	if remoteInfoHash != infoHash {
		return remotePeerID, fmt.Errorf("peer handshake info_hash mismatch")
	}
	return remotePeerID, nil
}

func (l *link) SendMessage(id byte, payload []byte) error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := l.conn.Write(domain.BuildMessage(id, payload))
	return err
}

func (l *link) SendKeepAlive() error {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := l.conn.Write(domain.BuildKeepAlive())
	return err
}

func (l *link) ReceiveMessage(timeout time.Duration) (byte, []byte, error) {
	l.conn.SetReadDeadline(time.Now().Add(timeout))

	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(l.conn, lengthBuf); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lengthBuf)
	if length == 0 {
		return ports.KeepAliveMessageID, nil, nil
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(l.conn, body); err != nil {
		return 0, nil, err
	}
	return body[0], body[1:], nil
}

func (l *link) Close() error {
	return l.conn.Close()
}
