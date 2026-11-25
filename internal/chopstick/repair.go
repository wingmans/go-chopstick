package chopstick

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar/pipeline"
)

// InfrastructureRepair describes safe creations and issues requiring manual
// action. It does not include changes to user settings.
type InfrastructureRepair struct {
	Create []string
	Manual []string
}

// PlanInfrastructureRepair inspects the resources setup would provision.
func PlanInfrastructureRepair(ctx context.Context, config Config) InfrastructureRepair {
	var plan InfrastructureRepair

	missingNews := false

	for _, database := range databases(config) {
		missing, err := needsDatabase(database.path)
		switch {
		case missing:
			plan.Create = append(plan.Create, fmt.Sprintf("%s SQLite database at %s",
				database.label, database.path))
			missingNews = missingNews || database.label == "news"
		case err != nil:
			plan.Manual = append(plan.Manual, fmt.Sprintf("inspect %s: %v", database.path, err))
		default:
			if err := database.inspect(ctx, database.path); err != nil {
				plan.Manual = append(plan.Manual, fmt.Sprintf("%s SQLite database at %s: %v",
					database.label, database.path, err))
			}
		}
	}

	for _, directory := range []string{config.FilingsDirectory, config.ParsedDirectory} {
		info, err := os.Stat(directory)
		switch {
		case errors.Is(err, os.ErrNotExist):
			plan.Create = append(plan.Create, "directory "+directory)
		case err != nil:
			plan.Manual = append(plan.Manual, fmt.Sprintf("inspect %s: %v", directory, err))
		case !info.IsDir():
			plan.Manual = append(plan.Manual, directory+" is not a directory")
		}
	}

	plan.inspectJetStream(ctx, config, missingNews)

	return plan
}

func needsDatabase(path string) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return true, nil
	case err != nil:
		return false, err
	case !info.Mode().IsRegular():
		return false, fmt.Errorf("%s is not a regular database file", path)
	default:
		return info.Size() == 0, nil
	}
}

func (plan *InfrastructureRepair) inspectJetStream(ctx context.Context, config Config, missingNews bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	bus, err := pipeline.Inspect(ctx, config.NATSURL)
	if err != nil {
		plan.Manual = append(plan.Manual, fmt.Sprintf("NATS at %s: %v",
			NATSAddress(config.NATSURL), err))
		if config.NATSURL == pipeline.DefaultURL {
			plan.Manual = append(plan.Manual, "Start local NATS with: make nats-start")
		}

		return
	}
	defer bus.CloseImmediately()

	for _, issue := range bus.ResourceIssues(ctx) {
		switch {
		case errors.Is(issue.Err, nats.ErrStreamNotFound):
			plan.Create = append(plan.Create, "JetStream stream "+pipeline.StreamName+" and its durable consumers")
		case errors.Is(issue.Err, nats.ErrConsumerNotFound):
			plan.Create = append(plan.Create, "JetStream consumer "+issue.Name)
		default:
			plan.Manual = append(plan.Manual, fmt.Sprintf("JetStream at %s: %v",
				NATSAddress(config.NATSURL), issue.Err))
		}
	}

	if missingNews {
		info, err := bus.JetStream().ConsumerInfo(pipeline.StreamName, pipeline.NewsDurable, nats.Context(ctx))
		switch {
		case err == nil && info.Delivered.Stream > 0:
			plan.Manual = append(plan.Manual, pipeline.ErrNewsReplayRequired.Error())
		case err != nil && !errors.Is(err, nats.ErrStreamNotFound) && !errors.Is(err, nats.ErrConsumerNotFound):
			plan.Manual = append(plan.Manual, fmt.Sprintf("inspect news consumer before creating its database: %v", err))
		}
	}
}
