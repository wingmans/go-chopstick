package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// RebuildResult reports indexed objects and isolated object failures.
type RebuildResult struct {
	Indexed  int64
	Problems []error
}

// RebuildCatalog derives a new SQLite catalog from authoritative raw objects.
func RebuildCatalog(ctx context.Context, path, temporaryDirectory string,
	store SubmissionStore,
) (RebuildResult, error) {
	result := RebuildResult{Problems: make([]error, 0)}

	temporaryPath := path + ".rebuilding"
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(temporaryPath + suffix); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("remove stale mirror catalog: %w", err)
		}
	}

	catalog, _, err := OpenCatalog(ctx, temporaryPath)
	if err != nil {
		return result, err
	}

	batch := make([]Submission, 0, catalogBatchSize)
	flush := func() error {
		if err := catalog.UpsertBatch(ctx, batch); err != nil {
			return err
		}

		result.Indexed += int64(len(batch))
		batch = batch[:0]

		return nil
	}

	walkErr := store.Walk(ctx, func(object ObjectInfo) error {
		value, inspectErr := inspectObject(ctx, store, object,
			temporaryDirectory)
		if inspectErr != nil {
			result.Problems = append(result.Problems,
				fmt.Errorf("inspect %s: %w", object.Key, inspectErr))

			return nil
		}

		batch = append(batch, value)
		if len(batch) == catalogBatchSize {
			return flush()
		}

		return nil
	})
	if walkErr == nil {
		walkErr = flush()
	}

	if walkErr == nil {
		walkErr = catalog.IntegrityCheck(ctx)
	}

	if walkErr == nil {
		walkErr = catalog.Checkpoint(ctx)
	}

	if closeErr := catalog.Close(); walkErr == nil && closeErr != nil {
		walkErr = closeErr
	}

	if walkErr != nil {
		return result, walkErr
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(temporaryPath + suffix)
		_ = os.Remove(path + suffix)
	}

	if err := os.Rename(temporaryPath, path); err != nil {
		return result, fmt.Errorf("publish rebuilt mirror catalog: %w", err)
	}

	return result, nil
}

// Verify walks storage independently and returns valid objects and failures.
func Verify(ctx context.Context, store SubmissionStore,
	temporaryDirectory string,
) (int64, []error, error) {
	var valid int64

	problems := make([]error, 0)
	err := store.Walk(ctx, func(object ObjectInfo) error {
		if _, inspectErr := inspectObject(ctx, store, object,
			temporaryDirectory); inspectErr != nil {
			problems = append(problems,
				fmt.Errorf("%s: %w", object.Key, inspectErr))

			return nil
		}

		valid++

		return nil
	})

	return valid, problems, err
}

// Repair reindexes valid objects and replaces corrupt ones from SEC.
func Repair(ctx context.Context, store SubmissionStore, catalog *Catalog,
	origin Origin, temporaryDirectory string,
) (int64, []error, error) {
	var repaired int64

	problems := make([]error, 0)
	err := store.Walk(ctx, func(object ObjectInfo) error {
		value, inspectErr := inspectObject(ctx, store, object,
			temporaryDirectory)
		if inspectErr == nil {
			if err := catalog.Upsert(ctx, value); err != nil {
				return err
			}

			return nil
		}

		if origin == nil {
			problems = append(problems,
				fmt.Errorf("%s: %w", object.Key, inspectErr))

			return nil
		}

		ref, refErr := refFromKey(object.Key)
		if refErr != nil {
			problems = append(problems, refErr)

			return nil
		}

		reader, fetchErr := origin.Fetch(ctx, ref)
		if fetchErr != nil {
			problems = append(problems, fetchErr)

			return nil
		}

		replacement, replaceErr := store.Replace(ctx, object.Key, reader)
		_ = reader.Close()

		if replaceErr != nil {
			problems = append(problems, replaceErr)

			return nil
		}

		value, inspectErr = inspectObject(ctx, store, replacement,
			temporaryDirectory)
		if inspectErr != nil {
			problems = append(problems, inspectErr)

			return nil
		}

		if err := catalog.Upsert(ctx, value); err != nil {
			return err
		}

		repaired++

		return nil
	})

	return repaired, problems, err
}
