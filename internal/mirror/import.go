package mirror

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

func walkRawSubmissions(ctx context.Context, root string,
	visit func(path, key string) error,
) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			return nil
		}

		key, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		return visit(path, filepath.ToSlash(key))
	})
}

func openRaw(path string) (*os.File, error) { return os.Open(path) }
