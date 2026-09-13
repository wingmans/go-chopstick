package filingweb

import (
	"regexp"
	"strings"
)

func conceptLabel(local string) string {
	if label, ok := summaryConceptLabel(local); ok {
		return label
	}

	label := regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(local, "$1 $2")
	label = strings.ReplaceAll(label, "And", "and")
	label = strings.ReplaceAll(label, "At", "at")
	label = strings.ReplaceAll(label, "Of", "of")
	label = strings.TrimSpace(label)

	if label == "" {
		return "Unknown concept"
	}

	return label
}

func summaryConceptLabel(local string) (string, bool) {
	labels := map[string]string{
		"Assets":                                "Assets",
		"CashAndCashEquivalentsAtCarryingValue": "Cash and equivalents",
		"GrossProfit":                           "Gross profit",
		"Liabilities":                           "Liabilities",
		"LiabilitiesAndStockholdersEquity":      "Liabilities and equity",
		"NetIncomeLoss":                         "Net income",
		"OperatingIncomeLoss":                   "Operating income",
		"ProfitLoss":                            "Profit or loss",
		"Revenues":                              "Revenue",
		"RevenueFromContractWithCustomerExcludingAssessedTax": "Revenue",
		"SalesRevenueNet":    "Revenue",
		"StockholdersEquity": "Shareholders' equity",
	}

	label, ok := labels[local]

	return label, ok
}
