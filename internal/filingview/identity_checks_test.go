package filingview

import "testing"

func TestCheckAccountingIdentitiesReportsFailure(t *testing.T) {
	view := identityTestView("100", "40", "50", "0")

	checks := CheckAccountingIdentities(view)

	failure := identityCheckByName(checks, "gross_profit = revenue - cost_of_revenue")
	if failure == nil {
		t.Fatalf("missing identity check: %+v", checks)
	}

	if failure.Status != identityFail || failure.Expected != "60.00" || failure.Actual != "50.00" {
		t.Fatalf("unexpected identity failure: %+v", failure)
	}

	if len(failure.Evidence) != 3 ||
		failure.Evidence[0].Metric != "gross_profit" ||
		failure.Evidence[0].Role != "actual" ||
		failure.Evidence[1].Metric != "revenue" ||
		failure.Evidence[1].Sign != 1 ||
		failure.Evidence[2].Metric != "cost_of_revenue" ||
		failure.Evidence[2].Sign != -1 {
		t.Fatalf("unexpected identity evidence: %+v", failure.Evidence)
	}
}

func TestCheckAccountingIdentitiesAllowsRoundingTolerance(t *testing.T) {
	view := identityTestView("1000", "400", "601", "-3")

	checks := CheckAccountingIdentities(view)

	check := identityCheckByName(checks, "gross_profit = revenue - cost_of_revenue")
	if check == nil {
		t.Fatalf("missing identity check: %+v", checks)
	}

	if check.Status != identityPass {
		t.Fatalf("rounded values should pass within tolerance: %+v", check)
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
	report := LintView(identityTestView("100", "40", "50", "0"), Taxonomy{
		SchemaVersion: 1, TaxonomyVersion: "test",
		Metrics: []MetricDefinition{
			{Key: "revenue", Label: "Revenue", Statement: "income", Concepts: []ConceptReference{{Name: "Revenue", NamespaceFamily: "any"}}},
			{Key: "cost_of_revenue", Label: "Cost", Statement: "income", Concepts: []ConceptReference{{Name: "Cost", NamespaceFamily: "any"}}},
			{Key: "gross_profit", Label: "Gross profit", Statement: "income", Concepts: []ConceptReference{{Name: "GrossProfit", NamespaceFamily: "any"}}},
		},
	})

	found := false

	for _, issue := range report.QualityIssues {
		if issue == "identity check failed: gross_profit = revenue - cost_of_revenue period=FY2026 unit=USD" {
			found = true
		}
	}

	if !found {
		t.Fatalf("missing identity quality issue: %+v", report.QualityIssues)
	}

	if len(report.QualityChecks) != 1 || report.QualityChecks[0].Actual != "50.00" ||
		report.QualityChecks[0].Expected != "60.00" {
		t.Fatalf("missing structured identity quality check: %+v", report.QualityChecks)
	}

	if len(report.QualityChecks[0].Evidence) != 3 {
		t.Fatalf("missing structured identity evidence: %+v", report.QualityChecks[0])
	}
}

func identityTestView(revenue, cost, grossProfit, decimals string) View {
	return View{Statements: Statements{
		Income: StatementView{Groups: []SummaryGroup{{
			Title:   "Fiscal year",
			Periods: []string{"FY2026"},
			Rows: []FactSeries{
				identityRow("revenue", "USD", "FY2026", revenue, decimals),
				identityRow("cost_of_revenue", "USD", "FY2026", cost, decimals),
				identityRow("gross_profit", "USD", "FY2026", grossProfit, decimals),
			},
		}}},
	}}
}

func identityRow(key, unit, period, value, decimals string) FactSeries {
	return FactSeries{
		Key: key, Label: key, Namespace: "test", Concept: key, Unit: unit,
		Values: map[string]FactValue{
			period: {Value: value, Decimals: decimals},
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
