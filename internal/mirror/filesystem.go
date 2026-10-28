package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const compressedSuffix = ".zst"

const maxSubmissionBytes int64 = 1 << 30

// FileStore stores compressed submissions beneath one filesystem root.
type FileStore struct {
	root string
}

// NewFileStore creates a filesystem submission store.
func NewFileStore(root string) (*FileStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("mirror object directory is required")
	}

	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create mirror object directory: %w", err)
	}

	return &FileStore{root: root}, nil
}

// Stat returns physical information without decompressing the object.
func (s *FileStore) Stat(_ context.Context, key string) (ObjectInfo, error) {
	path, err := s.objectPath(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ObjectInfo{}, ErrNotFound
	}

	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat mirror object: %w", err)
	}

	if info.IsDir() {
		return ObjectInfo{}, fmt.Errorf("%w: object path is a directory",
			ErrCorruptObject)
	}

	return ObjectInfo{Key: key, StoredBytes: info.Size()}, nil
}

// Put streams one raw submission into an atomically published zstd object.
func (s *FileStore) Put(ctx context.Context, key string,
	source io.Reader,
) (ObjectInfo, error) {
	return s.put(ctx, key, source, false)
}

// Replace atomically replaces a confirmed corrupt cache object.
func (s *FileStore) Replace(ctx context.Context, key string,
	source io.Reader,
) (ObjectInfo, error) {
	return s.put(ctx, key, source, true)
}

// Open returns the original uncompressed SEC byte stream.
func (s *FileStore) Open(ctx context.Context, key string) (
	io.ReadCloser, ObjectInfo, error,
) {
	path, err := s.objectPath(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ObjectInfo{}, ErrNotFound
	}

	if err != nil {
		return nil, ObjectInfo{}, fmt.Errorf("open mirror object: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return nil, ObjectInfo{}, fmt.Errorf("stat mirror object: %w", err)
	}

	decoder, err := zstd.NewReader(file)
	if err != nil {
		_ = file.Close()

		return nil, ObjectInfo{}, fmt.Errorf("%w: open zstd stream: %w",
			ErrCorruptObject, err)
	}

	reader := &decodedReader{
		done: ctx.Done(), contextErr: ctx.Err, decoder: decoder, file: file,
	}

	return reader, ObjectInfo{Key: key, StoredBytes: info.Size()}, nil
}

// Walk visits every completed compressed object without loading all keys.
func (s *FileStore) Walk(ctx context.Context,
	visit func(ObjectInfo) error,
) error {
	return filepath.WalkDir(s.root, func(path string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt"+compressedSuffix) {
			return nil
		}

		relative, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		key := filepath.ToSlash(strings.TrimSuffix(relative, compressedSuffix))

		return visit(ObjectInfo{Key: key, StoredBytes: info.Size()})
	})
}

func (s *FileStore) objectPath(key string) (string, error) {
	key = strings.TrimSpace(strings.TrimPrefix(key, "/"))
	if key == "" {
		return "", fmt.Errorf("%w: object key is empty", ErrInvalidReference)
	}

	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: invalid object key %q",
			ErrInvalidReference, key)
	}

	if filepath.Ext(clean) != ".txt" {
		return "", fmt.Errorf("%w: object key must end in .txt",
			ErrInvalidReference)
	}

	return filepath.Join(s.root, clean) + compressedSuffix, nil
}

func (s *FileStore) put(ctx context.Context, key string, source io.Reader,
	replace bool,
) (ObjectInfo, error) {
	path, err := s.objectPath(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	if info, statErr := os.Stat(path); !replace && statErr == nil &&
		!info.IsDir() {
		return ObjectInfo{Key: key, StoredBytes: info.Size()}, nil
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return ObjectInfo{}, fmt.Errorf("inspect mirror destination: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return ObjectInfo{}, fmt.Errorf("create mirror object parent: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".mirror-*.part")
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("create mirror temporary object: %w", err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	encoder, err := zstd.NewWriter(temporary,
		zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderCRC(true))
	if err != nil {
		_ = temporary.Close()

		return ObjectInfo{}, fmt.Errorf("create zstd encoder: %w", err)
	}

	hash := sha256.New()
	counter := &countingReader{reader: io.TeeReader(
		io.LimitReader(source, maxSubmissionBytes+1), hash)}

	contextual := &contextReader{
		done: ctx.Done(), contextErr: ctx.Err, reader: counter,
	}
	if _, err := io.Copy(encoder, contextual); err != nil {
		_ = encoder.Close()
		_ = temporary.Close()

		return ObjectInfo{}, fmt.Errorf("compress mirror object: %w", err)
	}

	if counter.count > maxSubmissionBytes {
		_ = encoder.Close()
		_ = temporary.Close()

		return ObjectInfo{}, fmt.Errorf("mirror submission exceeds %d bytes",
			maxSubmissionBytes)
	}

	if err := encoder.Close(); err != nil {
		_ = temporary.Close()

		return ObjectInfo{}, fmt.Errorf("finish mirror compression: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()

		return ObjectInfo{}, fmt.Errorf("sync mirror object: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return ObjectInfo{}, fmt.Errorf("close mirror object: %w", err)
	}

	if err := os.Rename(temporaryName, path); err != nil {
		return ObjectInfo{}, fmt.Errorf("publish mirror object: %w", err)
	}

	stored, err := os.Stat(path)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("stat published mirror object: %w", err)
	}

	return ObjectInfo{
		Key: key, LogicalBytes: counter.count, StoredBytes: stored.Size(),
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

type countingReader struct {
	reader io.Reader
	count  int64
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.count += int64(n)

	return n, err
}

type contextReader struct {
	done       <-chan struct{}
	contextErr func() error
	reader     io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.done:
		return 0, r.contextErr()
	default:
	}

	return r.reader.Read(buffer)
}

type decodedReader struct {
	done       <-chan struct{}
	contextErr func() error
	decoder    *zstd.Decoder
	file       *os.File
}

func (r *decodedReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.done:
		return 0, r.contextErr()
	default:
	}

	n, err := r.decoder.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("%w: decompress object: %w", ErrCorruptObject, err)
	}

	return n, err
}

func (r *decodedReader) Close() error {
	r.decoder.Close()

	return r.file.Close()
}
