package domain

import (
	"crypto/sha1"
	"fmt"
	"os"
	"strings"
)

const pieceHashLen = 20

// TorrentFile is one file within a (possibly multi-file) torrent.
type TorrentFile struct {
	Path   []string // path segments, joined relative to the torrent's root name
	Length int64
	Offset int64 // byte offset of this file within the concatenated piece stream
}

// Torrent is parsed, immutable .torrent metadata.
type Torrent struct {
	Name         string
	Announce     string
	AnnounceList [][]string
	PieceLength  int64
	Pieces       []byte // concatenated 20-byte SHA1 hashes
	Files        []TorrentFile
	TotalLength  int64
	InfoHash     [20]byte
}

func (t *Torrent) NumPieces() int {
	return len(t.Pieces) / pieceHashLen
}

func (t *Torrent) PieceHash(index int) []byte {
	start := index * pieceHashLen
	return t.Pieces[start : start+pieceHashLen]
}

func (t *Torrent) PieceLengthFor(index int) int64 {
	if index == t.NumPieces()-1 {
		return t.TotalLength - int64(index)*t.PieceLength
	}
	return t.PieceLength
}

// ParseTorrentFile reads and parses a .torrent file from disk.
func ParseTorrentFile(path string) (*Torrent, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading torrent file: %w", err)
	}
	return ParseTorrent(raw)
}

// ParseTorrent decodes raw bencoded .torrent bytes into a Torrent.
func ParseTorrent(raw []byte) (*Torrent, error) {
	decoded, err := BDecode(raw)
	if err != nil {
		return nil, fmt.Errorf("malformed .torrent: %w", err)
	}
	root, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("malformed .torrent: top-level value is not a dict")
	}

	infoRaw, ok := root["info"]
	if !ok {
		return nil, fmt.Errorf("malformed .torrent: missing 'info' dict")
	}
	info, ok := infoRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("malformed .torrent: 'info' is not a dict")
	}

	infoBencoded, err := BEncode(info)
	if err != nil {
		return nil, fmt.Errorf("re-encoding info dict for hash: %w", err)
	}
	infoHash := sha1.Sum(infoBencoded)

	name, err := stringField(info, "name")
	if err != nil {
		return nil, err
	}
	if err := validatePathSegment(name); err != nil {
		return nil, fmt.Errorf("malformed .torrent: 'name' %w", err)
	}
	pieceLength, err := intField(info, "piece length")
	if err != nil {
		return nil, err
	}
	pieces, err := bytesField(info, "pieces")
	if err != nil {
		return nil, err
	}
	if len(pieces)%pieceHashLen != 0 {
		return nil, fmt.Errorf("malformed .torrent: 'pieces' length %d not a multiple of %d", len(pieces), pieceHashLen)
	}

	var files []TorrentFile
	var totalLength int64

	if filesRaw, multiFile := info["files"]; multiFile {
		fileList, ok := filesRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("malformed .torrent: 'files' is not a list")
		}
		var offset int64
		for i, entryRaw := range fileList {
			entry, ok := entryRaw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("malformed .torrent: files[%d] is not a dict", i)
			}
			length, err := intField(entry, "length")
			if err != nil {
				return nil, err
			}
			pathRaw, ok := entry["path"]
			if !ok {
				return nil, fmt.Errorf("malformed .torrent: files[%d] missing 'path'", i)
			}
			pathList, ok := pathRaw.([]any)
			if !ok {
				return nil, fmt.Errorf("malformed .torrent: files[%d].path is not a list", i)
			}
			segments := make([]string, 0, len(pathList))
			for _, seg := range pathList {
				b, ok := seg.([]byte)
				if !ok {
					return nil, fmt.Errorf("malformed .torrent: files[%d].path segment is not a byte string", i)
				}
				segment := string(b)
				if err := validatePathSegment(segment); err != nil {
					return nil, fmt.Errorf("malformed .torrent: files[%d].path %w", i, err)
				}
				segments = append(segments, segment)
			}
			files = append(files, TorrentFile{Path: segments, Length: length, Offset: offset})
			offset += length
		}
		totalLength = offset
	} else {
		length, err := intField(info, "length")
		if err != nil {
			return nil, err
		}
		files = []TorrentFile{{Path: []string{name}, Length: length, Offset: 0}}
		totalLength = length
	}

	announce, err := stringField(root, "announce")
	if err != nil {
		return nil, err
	}

	var announceList [][]string
	if tiersRaw, ok := root["announce-list"]; ok {
		tierList, ok := tiersRaw.([]any)
		if !ok {
			return nil, fmt.Errorf("malformed .torrent: 'announce-list' is not a list")
		}
		for _, tierRaw := range tierList {
			urls, ok := tierRaw.([]any)
			if !ok {
				return nil, fmt.Errorf("malformed .torrent: announce-list tier is not a list")
			}
			var tier []string
			for _, u := range urls {
				b, ok := u.([]byte)
				if !ok {
					return nil, fmt.Errorf("malformed .torrent: announce-list url is not a byte string")
				}
				tier = append(tier, string(b))
			}
			announceList = append(announceList, tier)
		}
	} else {
		announceList = [][]string{{announce}}
	}

	return &Torrent{
		Name:         name,
		Announce:     announce,
		AnnounceList: announceList,
		PieceLength:  pieceLength,
		Pieces:       pieces,
		Files:        files,
		TotalLength:  totalLength,
		InfoHash:     infoHash,
	}, nil
}

// validatePathSegment rejects path segments a malicious .torrent could use
// to escape the download directory: empty, ".", "..", path separators, or
// absolute-path markers. Applied to 'name' and every 'files[].path' entry.
func validatePathSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." {
		return fmt.Errorf("has an invalid path segment %q", segment)
	}
	if strings.ContainsAny(segment, "/\\") {
		return fmt.Errorf("path segment %q contains a path separator", segment)
	}
	if segment[0] == 0 {
		return fmt.Errorf("path segment %q contains a NUL byte", segment)
	}
	return nil
}

func stringField(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("malformed .torrent: missing field %q", key)
	}
	b, ok := v.([]byte)
	if !ok {
		return "", fmt.Errorf("malformed .torrent: field %q is not a byte string", key)
	}
	return string(b), nil
}

func bytesField(m map[string]any, key string) ([]byte, error) {
	v, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("malformed .torrent: missing field %q", key)
	}
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("malformed .torrent: field %q is not a byte string", key)
	}
	return b, nil
}

func intField(m map[string]any, key string) (int64, error) {
	v, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("malformed .torrent: missing field %q", key)
	}
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("malformed .torrent: field %q is not an integer", key)
	}
	return n, nil
}
