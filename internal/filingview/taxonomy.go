package filingview

import "slices"

type metricDefinition struct {
	Key       string
	Label     string
	Statement string
	Concepts  []string
}

// taxonomy is intentionally small and editable. Concepts retain their SEC
// names in the view; these keys provide stable product-level names.
//
//nolint:gochecknoglobals // The registry is intentionally easy to edit.
var taxonomy = []metricDefinition{
	{
		Key: "revenue", Label: "Revenue", Statement: "income",
		Concepts: []string{"RevenueFromContractWithCustomerExcludingAssessedTax", "Revenues", "SalesRevenueNet"},
	},
	{
		Key: "cost_of_revenue", Label: "Cost of revenue", Statement: "income",
		Concepts: []string{"CostOfRevenue", "CostOfGoodsAndServicesSold"},
	},
	{
		Key: "gross_profit", Label: "Gross profit", Statement: "income",
		Concepts: []string{"GrossProfit"},
	},
	{
		Key: "operating_income", Label: "Operating income", Statement: "income",
		Concepts: []string{"OperatingIncomeLoss"},
	},
	{
		Key: "net_income", Label: "Net income", Statement: "income",
		Concepts: []string{"NetIncomeLoss", "ProfitLoss"},
	},
	{
		Key: "eps_diluted", Label: "Diluted EPS", Statement: "income",
		Concepts: []string{"EarningsPerShareDiluted"},
	},
	{
		Key: "assets", Label: "Assets", Statement: "balance",
		Concepts: []string{"Assets"},
	},
	{
		Key: "current_assets", Label: "Current assets", Statement: "balance",
		Concepts: []string{"AssetsCurrent"},
	},
	{
		Key: "cash", Label: "Cash and equivalents", Statement: "balance",
		Concepts: []string{"CashAndCashEquivalentsAtCarryingValue"},
	},
	{
		Key: "liabilities", Label: "Liabilities", Statement: "balance",
		Concepts: []string{"Liabilities"},
	},
	{
		Key: "current_liabilities", Label: "Current liabilities", Statement: "balance",
		Concepts: []string{"LiabilitiesCurrent"},
	},
	{
		Key: "equity", Label: "Shareholders' equity", Statement: "balance",
		Concepts: []string{"StockholdersEquity"},
	},
	{
		Key: "operating_cash_flow", Label: "Operating cash flow", Statement: "cash_flow",
		Concepts: []string{"NetCashProvidedByUsedInOperatingActivities"},
	},
	{
		Key: "investing_cash_flow", Label: "Investing cash flow", Statement: "cash_flow",
		Concepts: []string{"NetCashProvidedByUsedInInvestingActivities"},
	},
	{
		Key: "financing_cash_flow", Label: "Financing cash flow", Statement: "cash_flow",
		Concepts: []string{"NetCashProvidedByUsedInFinancingActivities"},
	},
	{
		Key: "capital_expenditures", Label: "Capital expenditures", Statement: "cash_flow",
		Concepts: []string{"PaymentsToAcquirePropertyPlantAndEquipment"},
	},
}

type ratioDefinition struct {
	Key         string
	Label       string
	Numerator   string
	Denominator string
	Format      string
	Formula     string
}

//nolint:gochecknoglobals // The registry is intentionally easy to edit.
var ratios = []ratioDefinition{
	{
		Key: "gross_margin", Label: "Gross margin", Numerator: "gross_profit",
		Denominator: "revenue", Format: "percent", Formula: "gross profit / revenue",
	},
	{
		Key: "operating_margin", Label: "Operating margin", Numerator: "operating_income",
		Denominator: "revenue", Format: "percent", Formula: "operating income / revenue",
	},
	{
		Key: "net_margin", Label: "Net margin", Numerator: "net_income",
		Denominator: "revenue", Format: "percent", Formula: "net income / revenue",
	},
	{
		Key: "current_ratio", Label: "Current ratio", Numerator: "current_assets",
		Denominator: "current_liabilities", Format: "multiple",
		Formula: "current assets / current liabilities",
	},
	{
		Key: "debt_to_equity", Label: "Liabilities to equity", Numerator: "liabilities",
		Denominator: "equity", Format: "multiple",
		Formula: "liabilities / shareholders' equity",
	},
}

func metricDefinitionForConcept(concept string) (metricDefinition, bool) {
	for _, definition := range taxonomy {
		if slices.Contains(definition.Concepts, concept) {
			return definition, true
		}
	}

	return metricDefinition{Key: "", Label: "", Statement: "", Concepts: nil}, false
}
