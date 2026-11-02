package main

import "github.com/alecthomas/kong"

type cli struct {
	loggingCLI `embed:""`

	Version kong.VersionFlag `group:"diag" help:"Print version."                              short:"v"`
	Verbose bool             `group:"diag" help:"Print verbose output and use debug logging."`

	Backfill  backfillCLI  `cmd:"" group:"data" help:"Run a finite historical pipeline."`
	Filings   filingsCLI   `cmd:"" group:"data" help:"Download and process filings."`
	Parse     parseCLI     `cmd:"" group:"data" help:"Parse local submissions."`
	Coverage  coverageCLI  `cmd:"" group:"data" help:"Report local filing coverage."`
	Index     indexCLI     `cmd:"" group:"data" help:"Manage SEC indexes."`
	Ownership ownershipCLI `cmd:"" group:"data" help:"Import and inspect ownership filings."`
	Artifacts artifactsCLI `cmd:"" group:"data" help:"Inspect filing artifacts."`
	Taxonomy  taxonomyCLI  `cmd:"" group:"data" help:"Inspect taxonomy coverage."`

	Doctor   doctorCLI   `cmd:"" group:"operations" help:"Validate local dependencies."`
	Status   statusCLI   `cmd:"" group:"operations" help:"Show live application status."`
	Rebuild  rebuildCLI  `cmd:"" group:"operations" help:"Rebuild disposable state."`
	Failures failuresCLI `cmd:"" group:"operations" help:"Inspect and retry failures."`
}

type loggingCLI struct {
	Log *string `enum:"critical,error,warn,info,debug,trace,off" group:"diag" help:"Log level (default: info)." placeholder:"level"`
}

type backfillCLI struct {
	Set      string `default:"${set}"       help:"Constituent set to discover."  placeholder:"name"`
	FromYear int    `default:"${from_year}" help:"First filing year to include." placeholder:"year"`
	NATSURL  string `default:"${nats_url}"  help:"NATS server URL."              name:"nats-url"    placeholder:"url"`
}

type rebuildCLI struct {
	Edgar rebuildEdgarCLI `cmd:"" help:"Rebuild EDGAR state."`
}

type rebuildEdgarCLI struct {
	Set      string `default:"${set}"                                   help:"Constituent set to rebuild."   placeholder:"name"`
	FromYear int    `default:"${from_year}"                             help:"First filing year to restore." placeholder:"year"`
	NATSURL  string `default:"${nats_url}"                              help:"NATS server URL."              name:"nats-url"    placeholder:"url"`
	Confirm  bool   `help:"Confirm removal of rebuildable EDGAR state."`
}

type indexCLI struct {
	Download indexDownloadCLI `cmd:"" default:"withargs"                               help:"Download SEC indexes."`
	Import   indexImportCLI   `cmd:"" help:"Import local indexes."`
	Status   indexStatusCLI   `cmd:"" help:"Show index inventory and import coverage."`
}

type indexDownloadCLI struct {
	FromYear      int  `default:"${from_year}"                                   help:"First year to download."                          short:"y"`
	RefreshLatest bool `help:"Refresh the latest quarter and reuse older files." short:"r"`
	Stitch        bool `default:"true"                                           help:"Concatenate quarterly TSV files into master.tsv." short:"s"`
	Noop          bool `help:"Show actions without writing files."               short:"n"`
}

type indexImportCLI struct {
	CommandAddress string `default:"${command_address}"                  help:"chopd command address." placeholder:"host:port"`
	File           string `help:"Import one quarterly TSV file."         placeholder:"path"`
	Latest         bool   `help:"Import the newest local quarterly TSV."`
	Year           int    `help:"Import all quarters for one year."      placeholder:"year"`
	FromYear       int    `help:"First year to import."                  placeholder:"year"`
	ToYear         int    `help:"Last year to import."                   placeholder:"year"`
	DryRun         bool   `help:"Read and report without writing."`
}

type indexStatusCLI struct {
	CommandAddress string `default:"${command_address}" help:"chopd command address." placeholder:"host:port"`
}

type selectionCLI struct {
	CIK       string   `help:"Select filings for this CIK."       placeholder:"cik"  short:"c"`
	Set       string   `help:"Select a constituent set."          placeholder:"name"`
	FormTypes []string `help:"Select this form type; repeatable." name:"form-type"   placeholder:"type" short:"f"`
	Year      string   `help:"Select filings filed in this year." placeholder:"year" short:"y"`
	FromYear  int      `help:"First filing year to include."      placeholder:"year"`
	ToYear    int      `help:"Last filing year to include."       placeholder:"year"`
}

type filingsCLI struct {
	selectionCLI `embed:""`

	AnalysisDB string `default:"${analysis_db}"                                      help:"SQLite analysis database." placeholder:"path"`
	Reprocess  bool   `help:"Rebuild parsed results without forcing a new download." short:"r"`
	Noop       bool   `help:"Show planned work without changing files."              short:"n"`
}

type parseCLI struct {
	selectionCLI `embed:""`

	AnalysisDB string   `default:"${analysis_db}"                                 help:"SQLite analysis database." placeholder:"path"`
	Files      []string `help:"Process this local submission; repeatable."        name:"file"                      placeholder:"path"`
	Reprocess  bool     `help:"Rebuild parsed results even when current."         short:"r"`
	Noop       bool     `help:"Show processing decisions without changing files." short:"n"`
}

type coverageCLI struct {
	Set        string   `help:"Select a constituent set."          placeholder:"name"                         short:"s"`
	CIK        string   `help:"Select filings for this CIK."       placeholder:"cik"                          short:"c"`
	FormTypes  []string `help:"Select this form type; repeatable." name:"form-type"                           placeholder:"type" short:"f"`
	FromYear   int      `help:"First filing year to include."      placeholder:"year"`
	ToYear     int      `help:"Last filing year to include."       placeholder:"year"`
	Master     string   `default:"${master}"                       help:"Master TSV path."                    placeholder:"path" short:"m"`
	IndexesDir string   `default:"${indexes_dir}"                  help:"Quarterly indexes directory."        placeholder:"path"`
	FilingsDir string   `default:"${filings_dir}"                  help:"Downloaded filings directory."       placeholder:"path"`
	ParsedDir  string   `default:"${parsed_dir}"                   help:"Optional parser artifact directory." placeholder:"path" short:"d"`
	AnalysisDB string   `default:"${analysis_db}"                  help:"Analysis database path."             placeholder:"path"`
	Output     string   `default:"${coverage_output}"              help:"JSON report path."                   name:"out"         short:"o"`
}

type ownershipCLI struct {
	Import ownershipImportCLI `cmd:"" default:"withargs"            help:"Import ownership filings."`
	Status ownershipStatusCLI `cmd:"" help:"Show ownership status."`
}

type ownershipImportCLI struct {
	Master    string   `default:"${master}"                               help:"Local master.tsv path."      placeholder:"path"`
	Database  string   `default:"${ownership_db}"                         help:"Ownership SQLite database."  placeholder:"path"`
	CacheDir  string   `default:"${filings_dir}"                          help:"Raw filing cache directory." placeholder:"path"`
	FormTypes []string `help:"Select a form type; repeatable."            name:"form-type"                   placeholder:"type" short:"f"`
	FromYear  int      `default:"2022"                                    help:"First filing year."          placeholder:"year"`
	ToYear    int      `help:"Last filing year; default is present."      placeholder:"year"`
	Reprocess bool     `help:"Reparse already terminal accessions."       short:"r"`
	Noop      bool     `help:"Count selected accessions without writing." short:"n"`
}

type ownershipStatusCLI struct {
	Database string `default:"${ownership_db}" help:"Ownership SQLite database." placeholder:"path"`
}

type artifactOptions struct {
	CIK          string `help:"Filing CIK."              placeholder:"cik"                  required:""`
	Accession    string `help:"Filing accession number." placeholder:"number"               required:""`
	ArtifactsDir string `default:"${artifacts_dir}"      help:"Disposable artifact root."   placeholder:"path"`
	ParsedDir    string `default:"${parsed_dir}"         help:"Parsed filing directory."    placeholder:"path"`
	FilingsDir   string `default:"${filings_dir}"        help:"Raw filing cache directory." placeholder:"path"`
}

type artifactsCLI struct {
	List    artifactListCLI    `cmd:"" help:"List source documents."`
	Extract artifactExtractCLI `cmd:"" help:"Extract source documents."`
	Serve   artifactServeCLI   `cmd:"" help:"Serve extracted documents."`
	Clean   artifactCleanCLI   `cmd:"" help:"Clean extracted documents."`
}

type artifactListCLI struct {
	artifactOptions `embed:""`
}
type artifactCleanCLI struct {
	artifactOptions `embed:""`
}
type artifactExtractCLI struct {
	artifactOptions `embed:""`

	Types     []string `help:"Extract a document type; repeatable." name:"type"     placeholder:"type"`
	Documents []string `help:"Extract one filename; repeatable."    name:"document" placeholder:"name"`
	All       bool     `help:"Extract every document."`
}
type artifactServeCLI struct {
	artifactOptions `embed:""`

	Address string `default:"127.0.0.1:8090" help:"Viewer listen address; loopback only." placeholder:"host:port"`
}

type taxonomyOptions struct {
	File      string   `help:"Inspect one filing-view.json or filing.json." placeholder:"path"              short:"i"`
	CIK       string   `help:"Select locally parsed filings for this CIK."  placeholder:"cik"               short:"c"`
	Set       string   `help:"Select a constituent set."                    placeholder:"name"              short:"s"`
	FormTypes []string `help:"Select this form type; repeatable."           name:"form-type"                placeholder:"type"    short:"f"`
	Year      string   `help:"Select filings filed in this year."           placeholder:"year"              short:"y"`
	FromYear  int      `help:"First filing year to include."                placeholder:"year"`
	ToYear    int      `help:"Last filing year to include."                 placeholder:"year"`
	ParsedDir string   `default:"${parsed_dir}"                             help:"Parsed filing directory." placeholder:"path"    short:"d"`
	Taxonomy  string   `help:"Use a taxonomy JSON file."                    placeholder:"path"              short:"t"`
	Format    string   `default:"text"                                      enum:"text,json"                help:"Output format."`
	Output    string   `help:"Write output to this file."                   name:"out"                      placeholder:"path"    short:"o"`
}

type taxonomyCLI struct {
	Lint     taxonomyLintCLI     `cmd:"" help:"Lint compact filing views."`
	Coverage taxonomyCoverageCLI `cmd:"" help:"Report taxonomy coverage."`
}
type taxonomyLintCLI struct {
	taxonomyOptions `embed:""`
}
type taxonomyCoverageCLI struct {
	taxonomyOptions `embed:""`
}

type doctorCLI struct {
	NATSURL   string `default:"${nats_url}"   help:"NATS server URL."          name:"nats-url"    placeholder:"url"`
	PollingDB string `default:"${polling_db}" help:"Pipeline state database."  placeholder:"path"`
	NewsDB    string `default:"${news_db}"    help:"Disposable news database." placeholder:"path"`
	Master    string `default:"${master}"     help:"Stitched master index."    placeholder:"path"`
}

type statusCLI struct {
	CommandAddress string `default:"${command_address}" help:"chopd command address." placeholder:"host:port"`
}

type failuresCLI struct {
	List  failuresListCLI  `cmd:"" default:"withargs"                help:"List unresolved failures."`
	Retry failuresRetryCLI `cmd:"" help:"Retry unresolved failures."`
}
type failuresListCLI struct {
	CommandAddress string `default:"${command_address}"        help:"chopd command address."    placeholder:"host:port"`
	Stage          string `help:"Limit failures to one stage." placeholder:"stage"`
	Limit          int    `default:"50"                        help:"Maximum failures to show." placeholder:"count"`
}
type failuresRetryCLI struct {
	CommandAddress string `default:"${command_address}" help:"chopd command address."  placeholder:"host:port"`
	Stage          string `enum:"download,process"      help:"Failure stage to retry." placeholder:"stage"     required:""`
}
