package dht

import (
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

// bootstrapNodes are well-known, long-standing public DHT entry points
// (BEP 5) used to join the mainline DHT with no prior state.
var bootstrapNodes = []string{
	"router.bittorrent.com:6881",
	"router.utorrent.com:6881",
	"dht.transmissionbt.com:6881",
}

const (
	alpha         = 3 // parallel lookups per iteration, mirrors libtorrent's dht_search_branching
	queryTimeout  = 3 * time.Second
	maxIterations = 8
)

// Discoverer implements ports.PeerDiscoverer via an iterative mainline DHT
// get_peers lookup.
type Discoverer struct{}

func New() *Discoverer { return &Discoverer{} }

func (d *Discoverer) Discover(t *domain.Torrent, nodeID [20]byte) ([]ports.PeerAddr, error) {
	var frontier []NodeInfo
	for _, addr := range bootstrapNodes {
		udpAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		frontier = append(frontier, NodeInfo{IP: udpAddr.IP.String(), Port: uint16(udpAddr.Port)})
	}
	if len(frontier) == 0 {
		return nil, fmt.Errorf("no resolvable DHT bootstrap nodes")
	}

	seen := make(map[[20]byte]bool)
	queried := make(map[string]bool)
	closestKnown := frontier
	var peers []ports.PeerAddr

	for iter := 0; iter < maxIterations; iter++ {
		sort.Slice(closestKnown, func(i, j int) bool {
			return closer(t.InfoHash, closestKnown[i].ID, closestKnown[j].ID)
		})

		var toQuery []NodeInfo
		for _, n := range closestKnown {
			key := fmt.Sprintf("%s:%d", n.IP, n.Port)
			if queried[key] {
				continue
			}
			toQuery = append(toQuery, n)
			if len(toQuery) >= alpha {
				break
			}
		}
		if len(toQuery) == 0 {
			break // nothing new to query — converged
		}

		type lookupResult struct {
			peers []ports.PeerAddr
			nodes []NodeInfo
		}
		results := make(chan lookupResult, len(toQuery))
		for _, n := range toQuery {
			queried[fmt.Sprintf("%s:%d", n.IP, n.Port)] = true
			go func(n NodeInfo) {
				p, newNodes, _ := getPeers(n, nodeID, t.InfoHash)
				results <- lookupResult{p, newNodes}
			}(n)
		}
		for i := 0; i < len(toQuery); i++ {
			r := <-results
			peers = append(peers, r.peers...)
			for _, n := range r.nodes {
				if !seen[n.ID] {
					seen[n.ID] = true
					closestKnown = append(closestKnown, n)
				}
			}
		}

		if len(peers) > 0 && iter >= 2 {
			break // got real results and gave the swarm a couple more rounds to add to them
		}
	}
	return peers, nil
}

// getPeers sends one get_peers query to node and parses whichever of
// "values" (peers) or "nodes" (closer nodes to try next) comes back.
func getPeers(node NodeInfo, myID, infoHash [20]byte) ([]ports.PeerAddr, []NodeInfo, error) {
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(node.IP, fmt.Sprintf("%d", node.Port)))
	if err != nil {
		return nil, nil, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(queryTimeout))

	transactionID := randomTransactionID()
	query := buildQuery(transactionID, "get_peers", map[string]any{
		"id":        myID[:],
		"info_hash": infoHash[:],
	})
	if _, err := conn.Write(query); err != nil {
		return nil, nil, err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, nil, err
	}

	resp, err := parseResponse(buf[:n])
	if err != nil {
		return nil, nil, err
	}
	if resp.isError || resp.transactionID != transactionID {
		return nil, nil, fmt.Errorf("dht: bad get_peers response from %s", node.IP)
	}

	var peers []ports.PeerAddr
	if valuesRaw, ok := resp.reply["values"].([]any); ok {
		for _, v := range valuesRaw {
			if b, ok := v.([]byte); ok && len(b) == 6 {
				peers = append(peers, parseCompactPeer(b))
			}
		}
	}
	var nodes []NodeInfo
	if nodesRaw, ok := resp.reply["nodes"].([]byte); ok {
		nodes = parseCompactNodes(nodesRaw)
	}
	return peers, nodes, nil
}
