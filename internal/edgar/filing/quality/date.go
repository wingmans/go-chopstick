package quality

import (
	"fmt"
	"time"
)

func parseDate(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date %q", value)
}
