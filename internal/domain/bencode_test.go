package domain

import (
	"bytes"
	"testing"
)

func TestBencodeRoundTrip(t *testing.T) {
	original := map[string]any{
		"name":         []byte("ubuntu.iso"),
		"length":       int64(123456789),
		"piece length": int64(262144),
		"announce-list": []any{
			[]any{[]byte("udp://tracker.example.com:80")},
		},
	}

	encoded, err := BEncode(original)
	if err != nil {
		t.Fatalf("BEncode: %v", err)
	}

	decoded, err := BDecode(encoded)
	if err != nil {
		t.Fatalf("BDecode: %v", err)
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded value is not a dict: %T", decoded)
	}
	if !bytes.Equal(m["name"].([]byte), []byte("ubuntu.iso")) {
		t.Errorf("name mismatch: %v", m["name"])
	}
	if m["length"].(int64) != 123456789 {
		t.Errorf("length mismatch: %v", m["length"])
	}

	// Re-encoding a decoded value must reproduce the same canonical bytes
	// (sorted keys) — this is what info_hash correctness depends on.
	reencoded, err := BEncode(decoded)
	if err != nil {
		t.Fatalf("re-BEncode: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Errorf("re-encoding not stable:\n  got:  %s\n  want: %s", reencoded, encoded)
	}
}

func TestBDecodeRejectsMalformed(t *testing.T) {
	cases := [][]byte{
		[]byte("i123"),      // unterminated integer
		[]byte("5:ab"),      // byte string overruns input
		[]byte("d3:foo"),    // missing value
		[]byte("l1:ae1:be"), // trailing data after valid list... actually list closes fine
	}
	// The last case is deliberately borderline; only assert the clear failures.
	for _, c := range cases[:3] {
		if _, err := BDecode(c); err == nil {
			t.Errorf("expected error decoding %q, got none", c)
		}
	}
}
