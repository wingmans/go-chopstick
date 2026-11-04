// Package filing defines the persisted, normalized filing read model.
package filing

import "wingman.com/chopstick/internal/parser"

const SchemaVersion = 1

// View is the useful filing read model produced from namespace-heavy parsed
// XBRL data. It is generated data, but it is not a web cache.
type View struct {
	SchemaVersion   int                         `json:"schema_version"`
	ParserVersion   string                      `json:"parser_version"`
	TaxonomyVersion string                      `json:"taxonomy_version"`
	SourcePath      string                      `json:"source_path"`
	SourceSHA256    string                      `json:"source_sha256"`
	Metadata        Metadata                    `json:"metadata"`
	Documents       []parser.SubmissionDocument `json:"documents"`
	Summary         []SummaryGroup              `json:"summary"`
	Statements      Statements                  `json:"statements"`
	Ratios          []RatioSeries               `json:"ratios"`
	Quality         Quality                     `json:"quality"`
	Counts          Counts                      `json:"counts"`
	Diagnostics     []parser.ParseDiagnostic    `json:"diagnostics"`
}

type Metadata struct {
	Accession  string `json:"accession"`
	CIK        string `json:"cik"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	ReportDate string `json:"report_date"`
	Company    string `json:"company"`
}

type Counts struct {
	Documents                int    `json:"documents"`
	Instances                int    `json:"instances"`
	Facts                    int    `json:"facts"`
	Contexts                 int    `json:"contexts"`
	DimensionalFactsExcluded int    `json:"dimensional_facts_excluded"`
	Status                   string `json:"status"`
}

type Quality struct {
	IdentityChecks []IdentityCheck `json:"identity_checks"`
}

type IdentityCheck struct {
	Name      string             `json:"name"`
	Period    string             `json:"period,omitempty"`
	Unit      string             `json:"unit,omitempty"`
	Status    string             `json:"status"`
	Expected  string             `json:"expected,omitempty"`
	Actual    string             `json:"actual,omitempty"`
	Tolerance string             `json:"tolerance,omitempty"`
	Metrics   []string           `json:"metrics,omitempty"`
	Evidence  []IdentityEvidence `json:"evidence,omitempty"`
	Message   string             `json:"message,omitempty"`
}

type IdentityEvidence struct {
	Metric     string `json:"metric"`
	Role       string `json:"role"`
	Sign       int64  `json:"sign,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Concept    string `json:"concept,omitempty"`
	Value      string `json:"value,omitempty"`
	ContextRef string `json:"context_ref,omitempty"`
	Decimals   string `json:"decimals,omitempty"`
	UnitRef    string `json:"unit_ref,omitempty"`
}

type SummaryGroup struct {
	Title   string       `json:"title"`
	Periods []string     `json:"periods"`
	Rows    []FactSeries `json:"rows"`
}

type FactSeries struct {
	Key       string               `json:"key,omitempty"`
	Label     string               `json:"label"`
	Namespace string               `json:"namespace,omitempty"`
	Concept   string               `json:"concept"`
	Unit      string               `json:"unit"`
	Values    map[string]FactValue `json:"values"`
}

type FactValue struct {
	Value      string `json:"value"`
	Nil        bool   `json:"nil"`
	ContextRef string `json:"context_ref,omitempty"`
	Decimals   string `json:"decimals,omitempty"`
	UnitRef    string `json:"unit_ref,omitempty"`
}

type Statements struct {
	Income   StatementView `json:"income"`
	Balance  StatementView `json:"balance_sheet"`
	CashFlow StatementView `json:"cash_flow"`
}

type StatementView struct {
	Title  string         `json:"title"`
	Groups []SummaryGroup `json:"groups"`
}

type RatioSeries struct {
	Key     string            `json:"key"`
	Label   string            `json:"label"`
	Derived bool              `json:"derived"`
	Formula string            `json:"formula"`
	Values  map[string]string `json:"values"`
}
