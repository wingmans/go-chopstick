package marketstore

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentMarketStoreInitialization(t *testing.T) {
	const workers = 12

	databasePath := filepath.Join(t.TempDir(), "market.db")
	errorsCh := make(chan error, workers)

	var group sync.WaitGroup

	for range workers {
		group.Go(func() {
			store, err := Open(t.Context(), databasePath)
			if err == nil {
				err = store.Close()
			}

			errorsCh <- err
		})
	}

	group.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent initialization failed: %v", err)
		}
	}
}
