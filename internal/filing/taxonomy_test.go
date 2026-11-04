package filing

import "testing"

func TestLoadTaxonomyDefault(t *testing.T) {
	taxonomy, err := LoadTaxonomy("")
	if err != nil {
		t.Fatalf("LoadTaxonomy returned error: %v", err)
	}

	if taxonomy.TaxonomyVersion == "" || len(taxonomy.Metrics) == 0 {
		t.Fatalf("default taxonomy is incomplete: %+v", taxonomy)
	}

	if _, ok := taxonomy.Metric(
		"http://fasb.org/us-gaap/2013-01-31", "Revenues"); !ok {
		t.Fatal("expected versioned US-GAAP revenue concept to resolve")
	}

	if _, ok := taxonomy.Metric(
		"http://example.test/company/2026", "Revenues"); ok {
		t.Fatal("company extension must not resolve as US-GAAP")
	}

	metric, ok := taxonomy.Metric(
		"http://fasb.org/us-gaap/2024",
		"LiabilitiesAndStockholdersEquity",
	)
	if !ok || metric.Key != "liabilities_and_equity" {
		t.Fatalf(
			"expected liabilities and equity concept to resolve, got %+v",
			metric,
		)
	}

	metric, ok = taxonomy.Metric(
		"http://fasb.org/us-gaap/2024",
		"StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest",
	)
	if !ok || metric.Key != "equity_including_noncontrolling_interest" {
		t.Fatalf(
			"expected NCI-inclusive equity concept to resolve separately, got %+v",
			metric,
		)
	}
}
