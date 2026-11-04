package operator

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/ownership"
	ownershipimporter "wingman.com/chopstick/internal/ownership/importer"
)

type ownershipCommandConfig struct {
	operation    string
	masterPath   string
	databasePath string
	download     edgar.FilingDownloadConfig
	formTypes    []string
	fromYear     int
	toYear       int
	reprocess    bool
	noop         bool
}

func runOwnership(ctx context.Context, client *http.Client, cfg ownershipCommandConfig) error {
	if cfg.operation == "status" {
		return printOwnershipStatus(ctx, cfg.databasePath)
	}

	summary, err := ImportOwnership(ctx, client,
		ownershipimporter.Config{
			MasterPath: cfg.masterPath, DatabasePath: cfg.databasePath,
			Download: cfg.download, FromYear: cfg.fromYear,
			ToYear: cfg.toYear, FormTypes: cfg.formTypes,
			Reprocess: cfg.reprocess, Noop: cfg.noop,
		})
	ctxlog.FromContext(ctx).Info("ownership import summary",
		"selected", summary.Selected, "planned", summary.Planned,
		"processed", summary.Processed, "reused", summary.Reused,
		"unsupported", summary.Unsupported, "failed", summary.Failed)

	return err
}

func printOwnershipStatus(ctx context.Context, databasePath string) error {
	coverage, counts, err := OwnershipStatus(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("inspect ownership database: %w", err)
	}

	if len(coverage) == 0 {
		fmt.Fprintln(os.Stdout, "No ownership completion runs recorded.")
	} else {
		for _, row := range coverage {
			fmt.Fprintf(os.Stdout,
				"%-13s %-8s %s through %s: %d processed, "+
					"%d failed, %d unsupported\n",
				ownershipFamilyLabel(row.Family), row.Status,
				row.FromDate, row.ThroughDate, row.Processed,
				row.Failed, row.Unsupported)
		}
	}

	if len(counts) > 0 {
		fmt.Fprintln(os.Stdout, "\nDurable filing outcomes:")

		for _, count := range counts {
			fmt.Fprintf(os.Stdout, "%-13s %-11s %d\n",
				ownershipFamilyLabel(count.Family), count.Status, count.Count)
		}
	}

	return nil
}

func ownershipFamilyLabel(family string) string {
	switch family {
	case ownership.FamilyInstitutional:
		return "13F"
	case ownership.FamilyInsider:
		return "Forms 3/4/5"
	case ownership.FamilyBeneficial:
		return "13D/G"
	default:
		return family
	}
}
