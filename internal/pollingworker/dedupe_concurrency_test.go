package pollingworker

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentDedupeServiceInitialization(t *testing.T) {
	const workers = 12

	dbPath := filepath.Join(t.TempDir(), "polling.db")
	errorsCh := make(chan error, workers)
	var group sync.WaitGroup

	for range workers {
		group.Add(1)

		go func() {
			defer group.Done()

			store, err := NewDedupeServiceContext(t.Context(), dbPath)
			if err == nil {
				err = store.Close()
			}
			errorsCh <- err
		}()
	}

	group.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent initialization failed: %v", err)
		}
	}
}
