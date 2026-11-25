package operator

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"wingman.com/chopstick/internal/chopstick"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/xerr"
)

// RepairDoctor previews or creates missing infrastructure after reporting the
// resolved settings.
func RepairDoctor(ctx context.Context, output io.Writer, config chopstick.Config, setOverride, dryRun bool, settingsErr error) error {
	plan := chopstick.PlanInfrastructureRepair(ctx, config)

	fmt.Fprintln(output, "Infrastructure")

	action := "Create"
	if dryRun {
		action = "Would create"
	}

	for _, resource := range plan.Create {
		fmt.Fprintf(output, "  %s %s\n", action, resource)
	}

	for _, issue := range plan.Manual {
		fmt.Fprintf(output, "  Manual action required: %s\n", issue)
	}

	if len(plan.Create) == 0 && len(plan.Manual) == 0 {
		fmt.Fprintln(output, "  No repairs needed.")
	}

	writeRepairSettings(output, config, setOverride, settingsErr)

	if dryRun {
		fmt.Fprintln(output, "\nDry run. No repairs applied.")
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if len(plan.Manual) > 0 || settingsErr != nil {
		return &xerr.PreflightError{
			Title: "Doctor repair is blocked",
			Hints: []string{
				"Address the manual actions or invalid settings above before running repair.",
				"See the dependency report with: ./bin/chop doctor",
			},
		}
	}

	if dryRun || len(plan.Create) == 0 {
		return nil
	}

	if err := chopstick.RepairInfrastructure(ctx, config); err != nil {
		return err
	}

	fmt.Fprintln(output, "\nInfrastructure repair complete.")

	return nil
}

func writeRepairSettings(output io.Writer, config chopstick.Config, setOverride bool, settingsErr error) {
	fmt.Fprintln(output, "\nSettings")

	source := "default"

	switch {
	case setOverride:
		source = "--set"
	case config.SetName != "":
		source = "EDGAR_SET"
	}

	setName := config.SetName
	if setName == "" {
		setName = "not configured"
	}

	fmt.Fprintf(output, "  Constituent set: %s (from %s)\n", setName, source)

	if config.SetName != "" && filepath.Base(config.SetName) == config.SetName && filepath.Ext(config.SetName) == "" {
		fmt.Fprintf(output, "  Manifest: %s\n", appdata.Path("sets", config.SetName+".json"))
	}

	fmt.Fprintf(output, "  From year: %d\n", config.FromYear)
	fmt.Fprintf(output, "  Form types: %s\n", strings.Join(config.FormTypes, ", "))

	if settingsErr != nil {
		for problem := range strings.SplitSeq(settingsErr.Error(), "\n") {
			fmt.Fprintf(output, "  Invalid: %s\n", problem)
		}
	} else {
		fmt.Fprintln(output, "  Status: valid")
	}
}
