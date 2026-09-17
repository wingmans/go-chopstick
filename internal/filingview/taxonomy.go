package filingview

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

//go:embed taxonomy.json
var defaultTaxonomyJSON []byte

type Taxonomy struct {
	SchemaVersion   int                `json:"schema_version"`
	TaxonomyVersion string             `json:"taxonomy_version"`
	Metrics         []MetricDefinition `json:"metrics"`
	Ratios          []RatioDefinition  `json:"ratios"`
}

type MetricDefinition struct {
	Key          string             `json:"key"`
	Label        string             `json:"label"`
	Statement    string             `json:"statement"`
	CoverageTier string             `json:"coverage_tier,omitempty"`
	Concepts     []ConceptReference `json:"concepts"`
}

type ConceptReference struct {
	NamespaceFamily string `json:"namespace_family"`
	Name            string `json:"name"`
	Priority        int    `json:"priority"`
}

type RatioDefinition struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
	Format      string `json:"format"`
	Formula     string `json:"formula"`
}

// LoadTaxonomy reads a taxonomy file. An empty path loads the checked-in
// default, which keeps command behavior independent of the working directory.
func LoadTaxonomy(path string) (Taxonomy, error) {
	data := defaultTaxonomyJSON

	if strings.TrimSpace(path) != "" {
		var err error

		data, err = os.ReadFile(path)
		if err != nil {
			return Taxonomy{}, fmt.Errorf("read taxonomy %s: %w", path, err)
		}
	}

	var taxonomy Taxonomy
	if err := json.Unmarshal(data, &taxonomy); err != nil {
		return Taxonomy{}, fmt.Errorf("decode taxonomy: %w", err)
	}

	if err := taxonomy.Validate(); err != nil {
		return Taxonomy{}, fmt.Errorf("validate taxonomy: %w", err)
	}

	return taxonomy, nil
}

func (t Taxonomy) Validate() error {
	if t.SchemaVersion != 1 {
		return fmt.Errorf("unsupported taxonomy schema %d", t.SchemaVersion)
	}

	if strings.TrimSpace(t.TaxonomyVersion) == "" {
		return errors.New("taxonomy version is empty")
	}

	seen := make(map[string]struct{}, len(t.Metrics))
	for _, metric := range t.Metrics {
		if metric.Key == "" || metric.Label == "" || metric.Statement == "" {
			return errors.New("metric has an empty key, label, or statement")
		}

		if _, ok := seen[metric.Key]; ok {
			return fmt.Errorf("duplicate metric key %q", metric.Key)
		}

		seen[metric.Key] = struct{}{}
		if len(metric.Concepts) == 0 {
			return fmt.Errorf("metric %q has no concepts", metric.Key)
		}

		for _, concept := range metric.Concepts {
			if concept.Name == "" {
				return fmt.Errorf("metric %q has an empty concept name", metric.Key)
			}
		}
	}

	return nil
}

func defaultTaxonomy() Taxonomy {
	taxonomy, err := LoadTaxonomy("")
	if err != nil {
		panic(err)
	}

	return taxonomy
}

func (t Taxonomy) metricForConcept(namespace, concept string) (MetricDefinition, bool) {
	definition, _, ok := t.metricReferenceForConcept(namespace, concept)

	return definition, ok
}

func (t Taxonomy) metricReferenceForConcept(namespace, concept string) (MetricDefinition, ConceptReference, bool) {
	for _, definition := range t.Metrics {
		for _, reference := range definition.Concepts {
			if reference.Name == concept && namespaceMatches(reference.NamespaceFamily, namespace) {
				return definition, reference, true
			}
		}
	}

	return MetricDefinition{
		Key: "", Label: "", Statement: "", CoverageTier: "", Concepts: nil,
	}, ConceptReference{
		NamespaceFamily: "", Name: "", Priority: 0,
	}, false
}

func namespaceMatches(family, namespace string) bool {
	if family == "" || family == "any" {
		return true
	}

	if family == "us-gaap" {
		return strings.Contains(namespace, "/us-gaap/") ||
			strings.Contains(namespace, "/us-gaap:")
	}

	return strings.Contains(namespace, family)
}

func (t Taxonomy) ratioDefinitions() []RatioDefinition {
	return t.Ratios
}
