package pollingworker

import (
	"log"
)

type IngestionPipeline struct {
	dedupe  *DedupeService
	outChan chan<- FilingEvent // Output channel (e.g., towards NATS Publisher)
}

func NewIngestionPipeline(dedupe *DedupeService, outChan chan<- FilingEvent) *IngestionPipeline {
	return &IngestionPipeline{
		dedupe:  dedupe,
		outChan: outChan,
	}
}

// ProcessSingle handles real-time streams (like RSS feeds).
func (p *IngestionPipeline) ProcessSingle(event FilingEvent) {
	isDup, err := p.dedupe.IsDuplicate(event.AccessionNumber)
	if err != nil {
		log.Printf("[Error] Dedupe check failed for %s: %v", event.AccessionNumber, err)

		return
	}

	if isDup {
		return // Ignore known item
	}

	if err := p.dedupe.SaveAccession(event); err != nil {
		log.Printf("[Error] Failed to persist accession %s: %v", event.AccessionNumber, err)

		return
	}

	// Emit clean event downstream
	p.outChan <- event
}

// ProcessBatch handles high-volume inputs efficiently (chunks from master.zip).
func (p *IngestionPipeline) ProcessBatch(events []FilingEvent) {
	if len(events) == 0 {
		return
	}

	// Filter and persist in a single atomic SQLite transaction
	newEvents, err := p.dedupe.SaveBatch(events)
	if err != nil {
		log.Printf("[Error] Batch dedupe operation failed: %v", err)

		return
	}

	// Emit all newly discovered items from the batch downstream
	for _, event := range newEvents {
		p.outChan <- event
	}

	log.Printf("[Ingest] Processed batch of %d items: %d new, %d duplicates skipped",
		len(events), len(newEvents), len(events)-len(newEvents))
}
