package edgar

const (
	ParsedSchemaVersion    = 1
	ParserVersion          = "1"
	DefaultParsedDirectory = "./data/parsed"
	ParseComplete          = "complete"
	ParseNoXBRL            = "no_xbrl"
	ParseUnsupported       = "unsupported"
	ParsePartial           = "partial"
)

// ParsedFiling is a source-level result, not a harmonized financial statement.
type ParsedFiling struct {
	SchemaVersion int                  `json:"schema_version"`
	ParserVersion string               `json:"parser_version"`
	SourcePath    string               `json:"source_path"`
	SourceSHA256  string               `json:"source_sha256"`
	Metadata      FilingMetadata       `json:"metadata"`
	Documents     []SubmissionDocument `json:"documents"`
	Instances     []XBRLInstance       `json:"instances"`
	Status        string               `json:"status"`
	Diagnostics   []ParseDiagnostic    `json:"diagnostics"`
}

type FilingMetadata struct {
	Accession  string            `json:"accession"`
	CIK        string            `json:"cik"`
	FormType   string            `json:"form_type"`
	FilingDate string            `json:"filing_date"`
	ReportDate string            `json:"report_date"`
	Filers     []SubmissionFiler `json:"filers"`
	Items      []string          `json:"items"`

	// Header preserves order, indentation, repeated fields, and unknown fields.
	Header []string `json:"header"`
}

type SubmissionFiler struct {
	CIK    string   `json:"cik"`
	Name   string   `json:"name"`
	Header []string `json:"header"`
}

// SubmissionDocument offsets address bytes in the unchanged source file.
// Content excludes an optional SEC <XML> or <XBRL> wrapper.
type SubmissionDocument struct {
	Type          string `json:"type"`
	Sequence      string `json:"sequence"`
	Filename      string `json:"filename"`
	Description   string `json:"description"`
	Role          string `json:"role"`
	Offset        int64  `json:"offset"`
	Length        int64  `json:"length"`
	ContentOffset int64  `json:"content_offset"`
	ContentLength int64  `json:"content_length"`
}

type ParseDiagnostic struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Document string `json:"document,omitempty"`
}

// QName identity is independent of the prefix chosen by the filing generator.
type QName struct {
	Namespace string `json:"namespace"`
	Local     string `json:"local"`
}

// XMLNode retains structured context, typed dimensions, and link content.
// Content preserves mixed text/element order. Namespaces resolve QName values.
type XMLNode struct {
	Name       QName             `json:"name"`
	Language   string            `json:"language,omitempty"`
	Attributes []XMLAttribute    `json:"attributes,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
	Content    []XMLContent      `json:"content,omitempty"`
}

type XMLAttribute struct {
	Name  QName  `json:"name"`
	Value string `json:"value"`
}

type XMLContent struct {
	Text    string   `json:"text,omitempty"`
	Element *XMLNode `json:"element,omitempty"`
}

type XBRLInstance struct {
	Root          XMLNode       `json:"root"`
	DocumentIndex int           `json:"document_index"`
	Document      string        `json:"document"`
	Facts         []Fact        `json:"facts"`
	Contexts      []FactContext `json:"contexts"`
	Units         []FactUnit    `json:"units"`
	References    []XMLNode     `json:"references"`
	Footnotes     []XMLNode     `json:"footnotes"`
	Unsupported   []XMLNode     `json:"unsupported,omitempty"`
}

// Fact values are XML-decoded source strings, never floating-point numbers.
type Fact struct {
	Namespaces map[string]string `json:"namespaces"`
	Concept    QName             `json:"concept"`
	Value      string            `json:"value"`
	ContextRef string            `json:"context_ref"`
	UnitRef    string            `json:"unit_ref,omitempty"`
	Decimals   string            `json:"decimals,omitempty"`
	Precision  string            `json:"precision,omitempty"`
	Language   string            `json:"language,omitempty"`
	Nil        bool              `json:"nil"`
	ID         string            `json:"id,omitempty"`
	// Attributes retain source attributes, including xsi:nil="false".
	Attributes []XMLAttribute `json:"attributes,omitempty"`
	Structured *XMLNode       `json:"structured,omitempty"`
}

type FactContext struct {
	ID         string          `json:"id"`
	Entity     string          `json:"entity"`
	Scheme     string          `json:"scheme"`
	Instant    string          `json:"instant,omitempty"`
	StartDate  string          `json:"start_date,omitempty"`
	EndDate    string          `json:"end_date,omitempty"`
	Forever    bool            `json:"forever,omitempty"`
	Dimensions []FactDimension `json:"dimensions"`
	Source     XMLNode         `json:"source"`
}

type FactDimension struct {
	Axis   QName    `json:"axis"`
	Member *QName   `json:"member,omitempty"`
	Typed  *XMLNode `json:"typed,omitempty"`
}

type FactUnit struct {
	ID          string  `json:"id"`
	Measures    []QName `json:"measures,omitempty"`
	Numerator   []QName `json:"numerator,omitempty"`
	Denominator []QName `json:"denominator,omitempty"`
	Source      XMLNode `json:"source"`
}

func (f *ParsedFiling) AccessionNumber() string { return f.Metadata.Accession }
func (f *ParsedFiling) FormType() string        { return f.Metadata.FormType }
func (f *ParsedFiling) FilingDate() string      { return f.Metadata.FilingDate }
func (f *ParsedFiling) ReportDate() string      { return f.Metadata.ReportDate }

// FactsByConcept returns every occurrence. References are local to this instance.
func (x *XBRLInstance) FactsByConcept(concept QName) []Fact {
	var result []Fact

	for _, fact := range x.Facts {
		if fact.Concept == concept {
			result = append(result, fact)
		}
	}

	return result
}

func (x *XBRLInstance) Context(id string) (FactContext, bool) {
	for _, value := range x.Contexts {
		if value.ID == id {
			return value, true
		}
	}

	var missing FactContext

	return missing, false
}

func (x *XBRLInstance) Unit(id string) (FactUnit, bool) {
	for _, value := range x.Units {
		if value.ID == id {
			return value, true
		}
	}

	var missing FactUnit

	return missing, false
}
