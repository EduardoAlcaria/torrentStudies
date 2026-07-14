// Command torrentmotor is the engine's composition root: it wires the
// concrete adapters (UDP/HTTP trackers, TCP peer links, file storage, JSON
// progress) into the application core and runs one download.
//
// It's meant to be launched as a subprocess by another controller (a
// Python/Textual CLI, a daemon, whatever) which reads newline-delimited
// JSON progress events from stdout. See internal/adapters/jsonprogress.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/EduardoAlcaria/Vow/internal/adapters/dht"
	"github.com/EduardoAlcaria/Vow/internal/adapters/filestorage"
	"github.com/EduardoAlcaria/Vow/internal/adapters/jsonprogress"
	"github.com/EduardoAlcaria/Vow/internal/adapters/peertcp"
	"github.com/EduardoAlcaria/Vow/internal/adapters/trackerhttp"
	"github.com/EduardoAlcaria/Vow/internal/adapters/trackerudp"
	"github.com/EduardoAlcaria/Vow/internal/app"
	"github.com/EduardoAlcaria/Vow/internal/domain"
	"github.com/EduardoAlcaria/Vow/internal/ports"
)

func main() {
	torrentPath := flag.String("torrent", "", "path to the .torrent file (required)")
	downloadDir := flag.String("dir", "downloads", "directory to download into")
	listenPort := flag.Uint("port", 6881, "port advertised to trackers/peers")
	// No multi-tenant fairness constraint like a general-purpose desktop
	// client has (libtorrent caps connections_limit=200 *across all
	// torrents* to stay polite to a user running many at once) — this
	// process only ever runs one torrent, so the ceiling can sit much
	// higher purely to maximize this download's own speed.
	maxPeers := flag.Int("max-peers", 150, "maximum simultaneous peer connections")
	useDHT := flag.Bool("dht", true, "also discover peers via the mainline DHT, not just trackers")
	flag.Parse()

	if *torrentPath == "" {
		fmt.Fprintln(os.Stderr, "usage: torrentmotor -torrent <file.torrent> [-dir downloads] [-port 6881] [-max-peers 150] [-dht=true]")
		os.Exit(2)
	}

	t, err := domain.ParseTorrentFile(*torrentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "torrentmotor: %v\n", err)
		os.Exit(1)
	}

	var discoverers []ports.PeerDiscoverer
	if *useDHT {
		discoverers = append(discoverers, dht.New())
	}

	progress := jsonprogress.New()
	service := &app.DownloadService{
		Announcers:  []ports.TrackerAnnouncer{trackerudp.New(), trackerhttp.New()},
		Discoverers: discoverers,
		Dialer:      peertcp.New(),
		Storage:     filestorage.New(progress.OnError),
		Progress:    progress,
		MaxPeers:    *maxPeers,
		ListenPort:  uint16(*listenPort),
	}

	if err := service.Run(t, *downloadDir); err != nil {
		os.Exit(1)
	}
}
