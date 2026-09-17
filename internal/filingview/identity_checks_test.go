package filingview

import "testing"

func TestCheckAccountingIdentitiesReportsFailure(t *testing.T) {
	view := balanceIdentityTestView("100", "40", "50", "0")

	checks := CheckAccountingIdentities(view)

	failure := identityCheckByName(checks, "assets = liabilities + equity")
	if failure == nil {
		t.Fatalf("missing identity check: %+v", checks)
	}

	if failure.Status != identityFail || failure.Expected != "90.00" || failure.Actual != "100.00" {
		t.Fatalf("unexpected identity failure: %+v", failure)
	}

	if len(failure.Evidence) != 3 ||
		failure.Evidence[0].Metric != "assets" ||
		failure.Evidence[0].Role != "actual" ||
		failure.Evidence[1].Metric != "liabilities" ||
		failure.Evidence[1].Sign != 1 ||
		failure.Evidence[2].Metric != "equity" ||
		failure.Evidence[2].Sign != 1 {
		t.Fatalf("unexpected identity evidence: %+v", failure.Evidence)
	}
}

func TestCheckAccountingIdentitiesAllowsRoundingTolerance(t *testing.T) {
	view := balanceIdentityTestView("1000", "400", "601", "-3")

	checks := CheckAccountingIdentities(view)

	check := identityCheckByName(checks, "assets = liabilities + equity")
	if check == nil {
		t.Fatalf("missing identity check: %+v", checks)
	}

	if check.Status != identityPass {
		t.Fatalf("rounded values should pass within tolerance: %+v", check)
	}
}

func TestCheckAccountingIdentitiesSkipsDifferentContexts(t *testing.T) {
	view := View{Statements: Statements{
		Balance: StatementView{Groups: []SummaryGroup{{
			Title:   "Instant",
			Periods: []string{"2026-06-30"},
			Rows: []FactSeries{
				identityRowWithContext("assets", "USD", "2026-06-30", "150", "0", "assets-context"),
				identityRowWithContext("liabilities", "USD", "2026-06-30", "75", "0", "liabilities-context"),
				identityRowWithContext("equity", "USD", "2026-06-30", "75", "0", "equity-context"),
			},
		}}},
	}}

	check := identityCheckByName(CheckAccountingIdentities(view), "assets = liabilities + equity")
	if check == nil {
		t.Fatalf("missing identity check")
	}

	if check.Status != identitySkipped || check.Message != "formula facts use different contexts" {
		t.Fatalf("context mismatch should skip identity check: %+v", check)
	}
}

func TestCheckAccountingIdentitiesSupportsAddition(t *testing.T) {
	view := View{Statements: Statements{
		Balance: StatementView{Groups: []SummaryGroup{{
			Title:   "Instant",
			Periods: []string{"2026-06-30"},
			Rows: []FactSeries{
				identityRow("assets", "USD", "2026-06-30", "150", "0"),
				identityRow("liabilities", "USD", "2026-06-30", "75", "0"),
				identityRow("equity", "USD", "2026-06-30", "75", "0"),
			},
		}}},
	}}

	check := identityCheckByName(CheckAccountingIdentities(view), "assets = liabilities + equity")
	if check == nil || check.Status != identityPass {
		t.Fatalf("unexpected balance identity check: %+v", check)
	}
}

func TestCheckAccountingIdentitiesUsesDirectBalanceTotalWhenPresent(t *testing.T) {
	view := View{Statements: Statements{
		Balance: StatementView{Groups: []SummaryGroup{{
			Title:   "Instant",
			Periods: []string{"2026-06-30"},
			Rows: []FactSeries{
				identityRow("assets", "USD", "2026-06-30", "150", "0"),
				identityRow("liabilities", "USD", "2026-06-30", "90", "0"),
				identityRow("equity", "USD", "2026-06-30", "50", "0"),
				identityRow("equity_including_noncontrolling_interest", "USD", "2026-06-30", "55", "0"),
				identityRow("liabilities_and_equity", "USD", "2026-06-30", "150", "0"),
			},
		}}},
	}}

	checks := CheckAccountingIdentities(view)

	direct := identityCheckByName(checks, "assets = liabilities_and_equity")
	if direct == nil || direct.Status != identityPass {
		t.Fatalf("unexpected direct balance identity check: %+v", checks)
	}

	fallback := identityCheckByName(checks, "assets = liabilities + equity")
	if fallback != nil {
		t.Fatalf("fallback balance check should be skipped when direct total exists: %+v", fallback)
	}

	nciFallback := identityCheckByName(checks, "assets = liabilities + equity_including_noncontrolling_interest")
	if nciFallback != nil {
		t.Fatalf("NCI-inclusive fallback should be skipped when direct total exists: %+v", nciFallback)
	}
}

func TestCheckAccountingIdentitiesUsesNCIInclusiveEquityWhenPresent(t *testing.T) {
	view := View{Statements: Statements{
		Balance: StatementView{Groups: []SummaryGroup{{
			Title:   "Instant",
			Periods: []string{"2026-06-30"},
			Rows: []FactSeries{
				identityRow("assets", "USD", "2026-06-30", "150", "0"),
				identityRow("liabilities", "USD", "2026-06-30", "90", "0"),
				identityRow("equity", "USD", "2026-06-30", "50", "0"),
				identityRow("equity_including_noncontrolling_interest", "USD", "2026-06-30", "60", "0"),
			},
		}}},
	}}

	checks := CheckAccountingIdentities(view)

	nci := identityCheckByName(checks, "assets = liabilities + equity_including_noncontrolling_interest")
	if nci == nil || nci.Status != identityPass {
		t.Fatalf("unexpected NCI-inclusive balance identity check: %+v", checks)
	}

	fallback := identityCheckByName(checks, "assets = liabilities + equity")
	if fallback != nil {
		t.Fatalf("plain equity fallback should be skipped when NCI-inclusive equity exists: %+v", fallback)
	}
}

func TestLintViewReportsIdentityFailures(t *testing.T) {
	report := LintView(balanceIdentityTestView("100", "40", "50", "0"), Taxonomy{
		SchemaVersion: 1, TaxonomyVersion: "test",
		Metrics: []MetricDefinition{
			{Key: "assets", Label: "Assets", Statement: "balance", Concepts: []ConceptReference{{Name: "Assets", NamespaceFamily: "any"}}},
			{Key: "liabilities", Label: "Liabilities", Statement: "balance", Concepts: []ConceptReference{{Name: "Liabilities", NamespaceFamily: "any"}}},
			{Key: "equity", Label: "Equity", Statement: "balance", Concepts: []ConceptReference{{Name: "Equity", NamespaceFamily: "any"}}},
		},
	})

	found := false

	for _, issue := range report.QualityIssues {
		if issue == "identity check failed: assets = liabilities + equity period=2026-06-30 unit=USD" {
			found = true
		}
	}

	if !found {
		t.Fatalf("missing identity quality issue: %+v", report.QualityIssues)
	}

	if len(report.QualityChecks) != 1 || report.QualityChecks[0].Actual != "100.00" ||
		report.QualityChecks[0].Expected != "90.00" {
		t.Fatalf("missing structured identity quality check: %+v", report.QualityChecks)
	}

	if len(report.QualityChecks[0].Evidence) != 3 {
		t.Fatalf("missing structured identity evidence: %+v", report.QualityChecks[0])
	}
}

func balanceIdentityTestView(assets, liabilities, equity, decimals string) View {
	return View{Statements: Statements{
		Balance: StatementView{Groups: []SummaryGroup{{
			Title:   "Instant",
			Periods: []string{"2026-06-30"},
			Rows: []FactSeries{
				identityRow("assets", "USD", "2026-06-30", assets, decimals),
				identityRow("liabilities", "USD", "2026-06-30", liabilities, decimals),
				identityRow("equity", "USD", "2026-06-30", equity, decimals),
			},
		}}},
	}}
}

func identityRow(key, unit, period, value, decimals string) FactSeries {
	return identityRowWithContext(key, unit, period, value, decimals, "")
}

func identityRowWithContext(key, unit, period, value, decimals, context string) FactSeries {
	return FactSeries{
		Key: key, Label: key, Namespace: "test", Concept: key, Unit: unit,
		Values: map[string]FactValue{
			period: {Value: value, Decimals: decimals, ContextRef: context},
		},
	}
}

func identityCheckByName(checks []IdentityCheck, name string) *IdentityCheck {
	for index := range checks {
		if checks[index].Name == name {
			return &checks[index]
		}
	}

	return nil
}
