package operator

import (
	"fmt"
	"strings"
	"time"

	"wingman.com/chopstick/internal/transport"
)

func renderIndexStatus(status transport.IndexStatusResponse) {
	fmt.Println("Chop index status")
	fmt.Println()
	fmt.Printf("  quarterly cache   %s\n", status.Directory)
	fmt.Printf("  available periods %s\n",
		periodSummary(status.AvailablePeriods))
	fmt.Printf("  missing periods   %s\n", listOrNone(status.MissingPeriods))
	fmt.Printf("  master index      %s (%s)\n",
		status.MasterPath, status.MasterState)

	imported := periodSummary(status.ImportedPeriods)
	if len(status.ImportedPeriods) == 0 {
		imported = "none recorded"
	}

	fmt.Printf("  imported periods  %s\n", imported)

	if status.LastImport != nil {
		last := status.LastImport

		selection := last.Periods
		if len(selection) == 0 {
			selection = last.Files
		}

		fmt.Printf("  last committed    %s\n",
			last.CommittedAt.Format(time.RFC3339))
		fmt.Printf("  last selection    %s\n", listOrNone(selection))
		fmt.Printf("  last result       files=%d examined=%d queued=%d "+
			"duplicates=%d\n", len(last.Files), last.Examined,
			last.Queued, last.Duplicates)
	}

	if status.ActiveImport != nil {
		active := status.ActiveImport
		fmt.Printf("  active import     %s (%s)\n",
			active.State, active.Phase)
		fmt.Printf("  active progress   files=%d examined=%d queued=%d "+
			"duplicates=%d\n", active.Files, active.Examined,
			active.Queued, active.Duplicates)
	}
}

func periodSummary(periods []string) string {
	if len(periods) == 0 {
		return "none"
	}

	if periodsAreContiguous(periods) {
		if len(periods) == 1 {
			return periods[0]
		}

		return fmt.Sprintf("%s through %s (%d quarters)",
			periods[0], periods[len(periods)-1], len(periods))
	}

	return strings.Join(periods, ", ")
}

func periodsAreContiguous(periods []string) bool {
	for index := 1; index < len(periods); index++ {
		if nextPeriod(periods[index-1]) != periods[index] {
			return false
		}
	}

	return true
}

func nextPeriod(period string) string {
	var year, quarter int
	if _, err := fmt.Sscanf(period, "%d-QTR%d", &year, &quarter); err != nil {
		return ""
	}

	if quarter == 4 {
		return fmt.Sprintf("%04d-QTR1", year+1)
	}

	return fmt.Sprintf("%04d-QTR%d", year, quarter+1)
}

func listOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}

	return strings.Join(values, ", ")
}
