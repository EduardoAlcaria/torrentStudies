// Package domain holds the pure BitTorrent protocol logic: bencode codec,
// torrent metadata, peer wire messages, piece assembly. No sockets, no disk,
// no third-party imports — stdlib only.
package domain

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// BDecode parses a bencoded byte slice (BEP 3) into:
//   - int64            for integers
//   - []byte           for byte strings
//   - []any            for lists
//   - map[string]any    for dictionaries (keys decoded as UTF-8 strings)
func BDecode(data []byte) (any, error) {
	value, index, err := bdecodeAt(data, 0)
	if err != nil {
		return nil, err
	}
	if index != len(data) {
		return nil, fmt.Errorf("bencode: %d trailing bytes after top-level value", len(data)-index)
	}
	return value, nil
}

func bdecodeAt(data []byte, index int) (any, int, error) {
	if index >= len(data) {
		return nil, index, fmt.Errorf("bencode: unexpected end of input at %d", index)
	}

	switch {
	case data[index] == 'i':
		end := bytes.IndexByte(data[index:], 'e')
		if end == -1 {
			return nil, index, fmt.Errorf("bencode: unterminated integer at %d", index)
		}
		end += index
		n, err := strconv.ParseInt(string(data[index+1:end]), 10, 64)
		if err != nil {
			return nil, index, fmt.Errorf("bencode: invalid integer at %d: %w", index, err)
		}
		return n, end + 1, nil

	case data[index] == 'l':
		index++
		items := make([]any, 0, 8)
		for index < len(data) && data[index] != 'e' {
			item, next, err := bdecodeAt(data, index)
			if err != nil {
				return nil, index, err
			}
			items = append(items, item)
			index = next
		}
		if index >= len(data) {
			return nil, index, fmt.Errorf("bencode: unterminated list")
		}
		return items, index + 1, nil

	case data[index] == 'd':
		index++
		result := make(map[string]any, 8)
		for index < len(data) && data[index] != 'e' {
			key, next, err := bdecodeAt(data, index)
			if err != nil {
				return nil, index, err
			}
			keyBytes, ok := key.([]byte)
			if !ok {
				return nil, index, fmt.Errorf("bencode: dict key at %d is not a byte string", index)
			}
			index = next
			val, next2, err := bdecodeAt(data, index)
			if err != nil {
				return nil, index, err
			}
			result[string(keyBytes)] = val
			index = next2
		}
		if index >= len(data) {
			return nil, index, fmt.Errorf("bencode: unterminated dict")
		}
		return result, index + 1, nil

	case data[index] >= '0' && data[index] <= '9':
		colon := bytes.IndexByte(data[index:], ':')
		if colon == -1 {
			return nil, index, fmt.Errorf("bencode: malformed byte string length at %d", index)
		}
		colon += index
		length, err := strconv.Atoi(string(data[index:colon]))
		if err != nil || length < 0 {
			return nil, index, fmt.Errorf("bencode: invalid byte string length at %d", index)
		}
		start := colon + 1
		if start+length > len(data) {
			return nil, index, fmt.Errorf("bencode: byte string length overruns input at %d", index)
		}
		return data[start : start+length], start + length, nil

	default:
		return nil, index, fmt.Errorf("bencode: unexpected byte %q at %d", data[index], index)
	}
}

// BEncode serializes a value built from int64/int, []byte, string, []any,
// and map[string]any back into canonical bencode (sorted dict keys, BEP 3).
func BEncode(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := bencodeInto(&buf, value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func bencodeInto(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case int:
		buf.WriteByte('i')
		buf.WriteString(strconv.Itoa(v))
		buf.WriteByte('e')
	case int64:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(v, 10))
		buf.WriteByte('e')
	case []byte:
		buf.WriteString(strconv.Itoa(len(v)))
		buf.WriteByte(':')
		buf.Write(v)
	case string:
		buf.WriteString(strconv.Itoa(len(v)))
		buf.WriteByte(':')
		buf.WriteString(v)
	case []any:
		buf.WriteByte('l')
		for _, item := range v {
			if err := bencodeInto(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case map[string]any:
		buf.WriteByte('d')
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := bencodeInto(buf, k); err != nil {
				return err
			}
			if err := bencodeInto(buf, v[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("bencode: cannot encode type %T", value)
	}
	return nil
}
