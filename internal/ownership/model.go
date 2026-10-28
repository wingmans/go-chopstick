package ownership

const ParserVersion = "1"

// Filing is one normalized ownership submission. Values remain source strings
// so parsing never introduces floating-point rounding.
type Filing struct {
	Accession              string
	FormType               string
	Family                 string
	FilingDate             string
	ReportDate             string
	SourcePath             string
	SourceSHA256           string
	ParserVersion          string
	Amendment              bool
	AmendmentType          string
	AmendmentNumber        string
	OriginalSubmissionDate string
	PreviousAccession      string
	ReportKind             string
	Parties                []Party
	Securities             []Security
	InstitutionalSummary   *InstitutionalSummary
	InstitutionalPositions []InstitutionalPosition
	InstitutionalNotices   []InstitutionalNotice
	BeneficialReports      []BeneficialReport
	BeneficialNarratives   []BeneficialNarrative
	InsiderHoldings        []InsiderHolding
	InsiderTransactions    []InsiderTransaction
	Footnotes              []Footnote
	Signatures             []Signature
}

// Party is an issuer, manager, reporting owner, or related manager.
type Party struct {
	Key           string
	CIK           string
	Name          string
	Role          string
	Type          string
	IsDirector    bool
	IsOfficer     bool
	IsTenPercent  bool
	IsOther       bool
	OfficerTitle  string
	MemberOfGroup string
}

// InstitutionalSummary is the filer-provided Form 13F summary page.
type InstitutionalSummary struct {
	EntryCount           string
	TotalValue           string
	IncludedManagerCount string
}

// Security is a reported security. Key is local to the parsed filing.
type Security struct {
	Key             string
	IssuerKey       string
	Title           string
	Class           string
	Kind            string
	CUSIP           string
	AlternateCUSIPs []string
	Ticker          string
}

// InstitutionalPosition is one row from a Form 13F information table.
type InstitutionalPosition struct {
	Row                  int
	ManagerKey           string
	ReportPeriod         string
	IssuerName           string
	Class                string
	CUSIP                string
	FIGI                 string
	Value                string
	Amount               string
	AmountType           string
	PutCall              string
	InvestmentDiscretion string
	OtherManagers        string
	VotingSole           string
	VotingShared         string
	VotingNone           string
}

// InstitutionalNotice identifies a manager that reports another manager's
// positions in a Form 13F notice or combination report.
type InstitutionalNotice struct {
	Row                  int
	ManagerKey           string
	ReportPeriod         string
	Kind                 string
	ReportingManagerName string
	ReportingManagerFile string
}

// BeneficialReport is one reporting person's state in Schedule 13D or 13G.
type BeneficialReport struct {
	Row                    int
	IssuerKey              string
	OwnerKey               string
	SecurityKey            string
	CUSIP                  string
	EventDate              string
	Shares                 string
	Percent                string
	SoleVotingPower        string
	SharedVotingPower      string
	SoleDispositivePower   string
	SharedDispositivePower string
	ReportingPersonType    string
	FilerCategory          string
}

// BeneficialNarrative preserves one structured Schedule 13D/G item field.
type BeneficialNarrative struct {
	Row   int
	Item  string
	Field string
	Text  string
}

// InsiderHolding is one derivative or non-derivative holding row.
type InsiderHolding struct {
	Row                 int
	IssuerKey           string
	OwnerKey            string
	SecurityKey         string
	ReportDate          string
	Derivative          bool
	Amount              string
	DirectOrIndirect    string
	NatureOfOwnership   string
	TransactionFormType string
	ExercisePrice       string
	ExerciseDate        string
	ExpirationDate      string
	UnderlyingTitle     string
	UnderlyingShares    string
	FootnoteRefs        string
}

// InsiderTransaction is one derivative or non-derivative transaction row.
type InsiderTransaction struct {
	Row                 int
	IssuerKey           string
	OwnerKey            string
	SecurityKey         string
	ReportDate          string
	Derivative          bool
	TransactionDate     string
	ExecutionDate       string
	TransactionFormType string
	TransactionCode     string
	EquitySwapInvolved  string
	AcquiredDisposed    string
	Amount              string
	Price               string
	SharesAfter         string
	DirectOrIndirect    string
	NatureOfOwnership   string
	ExercisePrice       string
	ExerciseDate        string
	ExpirationDate      string
	UnderlyingTitle     string
	UnderlyingShares    string
	FootnoteRefs        string
}

// Footnote retains filing-level explanatory text.
type Footnote struct {
	ID   string
	Text string
}

// Signature preserves a reported signer and signature date.
type Signature struct {
	Name string
	Date string
}
