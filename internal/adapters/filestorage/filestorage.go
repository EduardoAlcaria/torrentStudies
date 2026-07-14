// Package filestorage writes verified pieces to disk, splitting each piece
// across the (possibly multiple) files it spans.
//
// Writes are asynchronous: WritePiece hands data off to a small worker
// pool over a bounded channel and returns immediately, so a peer's
// network-reading goroutine never blocks on a disk syscall. The channel's
// bound (writeQueueDepth) is the backpressure — if workers can't keep up,
// WritePiece's channel send blocks the caller, which is exactly the signal
// "stop reading this peer's socket until disk catches up." File handles
// are opened once in Prepare and kept open for the life of the download —
// no open+write+close syscall per block. No custom read/write cache is
// built on top; the OS page cache already does that job (this mirrors
// libtorrent 2.x, which deliberately removed its own disk cache in favor
// of the kernel's).
package filestorage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EduardoAlcaria/Vow/internal/domain"
)

const (
	writeQueueDepth = 128 // pieces buffered before WritePiece starts blocking (backpressure)
	writerCount     = 4
)

type writeJob struct {
	torrent    *domain.Torrent
	pieceIndex int
	data       []byte
}

type Storage struct {
	downloadDir string
	onError     func(error)

	// files is populated once, synchronously, in Prepare, and only ever
	// read afterward by worker goroutines — no lock needed for that
	// read-only phase.
	files map[string]*os.File

	jobs chan writeJob
	wg   sync.WaitGroup
}

// New creates a Storage. onError (may be nil) is called from a worker
// goroutine whenever an async write fails — there's no other way to learn
// about it, since WritePiece itself always returns immediately.
func New(onError func(error)) *Storage {
	s := &Storage{
		onError: onError,
		files:   make(map[string]*os.File),
		jobs:    make(chan writeJob, writeQueueDepth),
	}
	for i := 0; i < writerCount; i++ {
		s.wg.Add(1)
		go s.writeWorker()
	}
	return s
}

func (s *Storage) writeWorker() {
	defer s.wg.Done()
	for job := range s.jobs {
		if err := s.writePieceSync(job.torrent, job.pieceIndex, job.data); err != nil && s.onError != nil {
			s.onError(err)
		}
	}
}

func (s *Storage) Prepare(t *domain.Torrent, downloadDir string) error {
	s.downloadDir = downloadDir
	basePath := filepath.Join(downloadDir, t.Name)

	for _, file := range t.Files {
		fullPath, err := resolveContained(basePath, file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", fullPath, err)
		}
		f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return fmt.Errorf("creating file %s: %w", fullPath, err)
		}
		if err := f.Truncate(file.Length); err != nil {
			f.Close()
			return fmt.Errorf("sizing file %s: %w", fullPath, err)
		}
		s.files[fullPath] = f
	}
	return nil
}

// WritePiece enqueues data for async writing and returns immediately. Any
// error surfaces later via the onError callback, not the return value.
func (s *Storage) WritePiece(t *domain.Torrent, pieceIndex int, data []byte) error {
	s.jobs <- writeJob{torrent: t, pieceIndex: pieceIndex, data: data}
	return nil
}

func (s *Storage) writePieceSync(t *domain.Torrent, pieceIndex int, data []byte) error {
	pieceStart := int64(pieceIndex) * t.PieceLength
	pieceEnd := pieceStart + int64(len(data))
	basePath := filepath.Join(s.downloadDir, t.Name)

	for _, file := range t.Files {
		fileStart := file.Offset
		fileEnd := fileStart + file.Length
		if pieceStart >= fileEnd || pieceEnd <= fileStart {
			continue // this piece doesn't touch this file
		}

		overlapStart := max(pieceStart, fileStart)
		overlapEnd := min(pieceEnd, fileEnd)
		dataOffset := overlapStart - pieceStart
		fileOffset := overlapStart - fileStart
		overlapLength := overlapEnd - overlapStart

		fullPath, err := resolveContained(basePath, file.Path)
		if err != nil {
			return err
		}
		f, ok := s.files[fullPath]
		if !ok {
			return fmt.Errorf("no open handle for %s (Prepare not called?)", fullPath)
		}
		if _, err := f.WriteAt(data[dataOffset:dataOffset+overlapLength], fileOffset); err != nil {
			return fmt.Errorf("writing %s: %w", fullPath, err)
		}
	}
	return nil
}

// ReadBlock reads a block back out of already-written files, to serve an
// incoming peer request. os.File.ReadAt is safe for concurrent use (it's
// pread under the hood, no shared offset), including concurrently with the
// write workers' WriteAt calls on the same handle — no extra locking needed.
func (s *Storage) ReadBlock(t *domain.Torrent, pieceIndex int, begin, length uint32) ([]byte, error) {
	blockStart := int64(pieceIndex)*t.PieceLength + int64(begin)
	blockEnd := blockStart + int64(length)
	basePath := filepath.Join(s.downloadDir, t.Name)

	out := make([]byte, length)
	for _, file := range t.Files {
		fileStart := file.Offset
		fileEnd := fileStart + file.Length
		if blockStart >= fileEnd || blockEnd <= fileStart {
			continue
		}

		overlapStart := max(blockStart, fileStart)
		overlapEnd := min(blockEnd, fileEnd)
		outOffset := overlapStart - blockStart
		fileOffset := overlapStart - fileStart
		overlapLength := overlapEnd - overlapStart

		fullPath, err := resolveContained(basePath, file.Path)
		if err != nil {
			return nil, err
		}
		f, ok := s.files[fullPath]
		if !ok {
			return nil, fmt.Errorf("no open handle for %s (Prepare not called?)", fullPath)
		}
		if _, err := f.ReadAt(out[outOffset:outOffset+overlapLength], fileOffset); err != nil {
			return nil, fmt.Errorf("reading %s: %w", fullPath, err)
		}
	}
	return out, nil
}

// Close drains any queued writes, stops the worker pool, and closes every
// open file handle. Safe to call even if Prepare failed partway through.
func (s *Storage) Close() error {
	close(s.jobs)
	s.wg.Wait()

	var firstErr error
	for _, f := range s.files {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// resolveContained joins base with segments and verifies the result stays
// inside base. Defense in depth: internal/domain.ParseTorrent already
// rejects "..", separators, and empty segments in file paths, but a torrent
// wasn't necessarily built by that parser, and disk writes are exactly
// where a path-traversal bug turns into arbitrary file overwrite.
func resolveContained(base string, segments []string) (string, error) {
	full := filepath.Join(append([]string{base}, segments...)...)

	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolving base path: %w", err)
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("resolving path %s: %w", full, err)
	}
	rel, err := filepath.Rel(absBase, absFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write outside download directory: %s", full)
	}
	return full, nil
}
