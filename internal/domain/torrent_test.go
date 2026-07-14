package domain

import "testing"

func buildTorrentBytes(t *testing.T, filePath []any) []byte {
	t.Helper()
	info := map[string]any{
		"name":         []byte("root"),
		"piece length": int64(4),
		"pieces":       make([]byte, 20), // one fake piece hash
		"files": []any{
			map[string]any{
				"length": int64(4),
				"path":   filePath,
			},
		},
	}
	root := map[string]any{
		"announce": []byte("http://tracker.example.com/announce"),
		"info":     info,
	}
	raw, err := BEncode(root)
	if err != nil {
		t.Fatalf("BEncode fixture: %v", err)
	}
	return raw
}

func TestParseTorrentRejectsPathTraversal(t *testing.T) {
	cases := [][]any{
		{[]byte(".."), []byte("..") , []byte("etc"), []byte("passwd")},
		{[]byte("..")},
		{[]byte("a"), []byte("..")},
		{[]byte("a/b")},
		{[]byte("")},
	}
	for _, filePath := range cases {
		raw := buildTorrentBytes(t, filePath)
		if _, err := ParseTorrent(raw); err == nil {
			t.Errorf("expected rejection for path %v, got no error", filePath)
		}
	}
}

func TestParseTorrentAcceptsNormalPath(t *testing.T) {
	raw := buildTorrentBytes(t, []any{[]byte("subdir"), []byte("movie.mp4")})
	tor, err := ParseTorrent(raw)
	if err != nil {
		t.Fatalf("expected valid torrent to parse, got: %v", err)
	}
	if len(tor.Files) != 1 || tor.Files[0].Path[0] != "subdir" || tor.Files[0].Path[1] != "movie.mp4" {
		t.Fatalf("unexpected files: %+v", tor.Files)
	}
}
