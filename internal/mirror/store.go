package mirror

import (
	"context"
	"io"
)

// SubmissionStore stores immutable compressed submissions by logical SEC key.
type SubmissionStore interface {
	Stat(ctx context.Context, key string) (ObjectInfo, error)
	Put(ctx context.Context, key string, source io.Reader) (ObjectInfo, error)
	Replace(ctx context.Context, key string, source io.Reader) (ObjectInfo, error)
	Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	Walk(ctx context.Context, visit func(ObjectInfo) error) error
}
