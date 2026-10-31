package ownership

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"wingman.com/chopstick/internal/edgar"
)

func parseSection16(ctx context.Context, path string,
	documents []edgar.SubmissionDocument, filing *Filing,
) error {
	document, err := primaryDocument(documents, filing.FormType)
	if err != nil {
		return err
	}

	root, err := parseDocument(ctx, path, document)
	if err != nil {
		return err
	}

	if root.name != "ownershipDocument" {
		return fmt.Errorf("unexpected Section 16 root %q", root.name)
	}

	documentType := NormalizeFormType(root.value("documentType"))
	if documentType != filing.FormType {
		return fmt.Errorf("document type %q does not match submission %q",
			documentType, filing.FormType)
	}

	if value := normalizeDate(root.value("periodOfReport")); value != "" {
		filing.ReportDate = value
	}

	filing.OriginalSubmissionDate = normalizeDate(
		root.value("dateOfOriginalSubmission"))
	if filing.Amendment {
		filing.AmendmentType = "CORRECTION"
	}

	issuer := root.child("issuer")
	if issuer == nil {
		return errors.New("section 16 filing has no issuer")
	}

	filing.Parties = append(filing.Parties, Party{
		Key:  "issuer",
		CIK:  issuer.value("issuerCik"),
		Name: issuer.value("issuerName"),
		Role: "issuer",
	})
	issuerTicker := issuer.value("issuerTradingSymbol")

	owners := root.childrenNamed("reportingOwner")
	if len(owners) == 0 {
		return errors.New("section 16 filing has no reporting owner")
	}

	ownerKeys := make([]string, 0, len(owners))
	for index, owner := range owners {
		key := fmt.Sprintf("owner:%d", index+1)
		identity := owner.child("reportingOwnerId")
		relationship := owner.child("reportingOwnerRelationship")
		filing.Parties = append(filing.Parties, Party{
			Key:          key,
			CIK:          identity.value("rptOwnerCik"),
			Name:         identity.value("rptOwnerName"),
			Role:         "reporting_owner",
			IsDirector:   parseBoolean(relationship.value("isDirector")),
			IsOfficer:    parseBoolean(relationship.value("isOfficer")),
			IsTenPercent: parseBoolean(relationship.value("isTenPercentOwner")),
			IsOther:      parseBoolean(relationship.value("isOther")),
			OfficerTitle: relationship.value("officerTitle"),
		})
		ownerKeys = append(ownerKeys, key)
	}

	for _, note := range root.path("footnotes").childrenNamed("footnote") {
		filing.Footnotes = append(filing.Footnotes, Footnote{
			ID: note.attrs["id"], Text: note.text(),
		})
	}

	for _, signature := range root.childrenNamed("ownerSignature") {
		filing.Signatures = append(filing.Signatures, Signature{
			Name: signature.value("signatureName"),
			Date: normalizeDate(signature.value("signatureDate")),
		})
	}

	row := 0

	for _, table := range []struct {
		name       string
		derivative bool
	}{
		{name: "nonDerivativeTable", derivative: false},
		{name: "derivativeTable", derivative: true},
	} {
		tableElement := root.child(table.name)
		if tableElement == nil {
			continue
		}

		holdingName, transactionName := "nonDerivativeHolding",
			"nonDerivativeTransaction"
		if table.derivative {
			holdingName, transactionName = "derivativeHolding",
				"derivativeTransaction"
		}

		for _, element := range tableElement.childrenNamed(holdingName) {
			row++

			securityKey := section16Security(filing, element, table.derivative,
				issuerTicker)
			for _, ownerKey := range ownerKeys {
				filing.InsiderHoldings = append(filing.InsiderHoldings,
					readInsiderHolding(element, row, ownerKey, securityKey,
						filing.ReportDate, table.derivative))
			}
		}

		for _, element := range tableElement.childrenNamed(transactionName) {
			row++

			securityKey := section16Security(filing, element, table.derivative,
				issuerTicker)
			for _, ownerKey := range ownerKeys {
				filing.InsiderTransactions = append(
					filing.InsiderTransactions,
					readInsiderTransaction(element, row, ownerKey, securityKey,
						filing.ReportDate, table.derivative))
			}
		}
	}

	return nil
}

func section16Security(filing *Filing, element *xmlElement,
	derivative bool, ticker string,
) string {
	title := element.value("securityTitle")

	kind := "non_derivative"
	if derivative {
		kind = "derivative"
	}

	for _, security := range filing.Securities {
		if security.IssuerKey == "issuer" && security.Title == title &&
			security.Kind == kind {
			return security.Key
		}
	}

	key := fmt.Sprintf("security:%d", len(filing.Securities)+1)
	filing.Securities = append(filing.Securities, Security{
		Key: key, IssuerKey: "issuer", Title: title, Class: title,
		Kind: kind, Ticker: ticker,
	})

	return key
}

func readInsiderHolding(element *xmlElement, row int, ownerKey,
	securityKey, reportDate string, derivative bool,
) InsiderHolding {
	holding := InsiderHolding{
		Row: row, IssuerKey: "issuer", OwnerKey: ownerKey,
		SecurityKey: securityKey, ReportDate: reportDate,
		Derivative: derivative,
		Amount: element.value("postTransactionAmounts",
			"sharesOwnedFollowingTransaction"),
		DirectOrIndirect: element.value("ownershipNature",
			"directOrIndirectOwnership"),
		NatureOfOwnership: element.value("ownershipNature",
			"natureOfOwnership"),
		TransactionFormType: element.value("transactionCoding",
			"transactionFormType"),
		FootnoteRefs: footnoteReferences(element),
	}
	if derivative {
		holding.ExercisePrice = element.value("conversionOrExercisePrice")
		holding.ExerciseDate = normalizeDate(element.value("exerciseDate"))
		holding.ExpirationDate = normalizeDate(element.value("expirationDate"))
		holding.UnderlyingTitle = element.value("underlyingSecurity",
			"underlyingSecurityTitle")
		holding.UnderlyingShares = element.value("underlyingSecurity",
			"underlyingSecurityShares")
	}

	return holding
}

func readInsiderTransaction(element *xmlElement, row int, ownerKey,
	securityKey, reportDate string, derivative bool,
) InsiderTransaction {
	transaction := InsiderTransaction{
		Row: row, IssuerKey: "issuer", OwnerKey: ownerKey,
		SecurityKey: securityKey, ReportDate: reportDate,
		Derivative:      derivative,
		TransactionDate: normalizeDate(element.value("transactionDate")),
		ExecutionDate:   normalizeDate(element.value("deemedExecutionDate")),
		TransactionFormType: element.value("transactionCoding",
			"transactionFormType"),
		TransactionCode: element.value("transactionCoding",
			"transactionCode"),
		EquitySwapInvolved: element.value("transactionCoding",
			"equitySwapInvolved"),
		AcquiredDisposed: element.value("transactionAmounts",
			"transactionAcquiredDisposedCode"),
		Amount: element.value("transactionAmounts", "transactionShares"),
		Price:  element.value("transactionAmounts", "transactionPricePerShare"),
		SharesAfter: element.value("postTransactionAmounts",
			"sharesOwnedFollowingTransaction"),
		DirectOrIndirect: element.value("ownershipNature",
			"directOrIndirectOwnership"),
		NatureOfOwnership: element.value("ownershipNature",
			"natureOfOwnership"),
		FootnoteRefs: footnoteReferences(element),
	}
	if derivative {
		transaction.ExercisePrice = element.value("conversionOrExercisePrice")
		transaction.ExerciseDate = normalizeDate(
			element.value("exerciseDate"))
		transaction.ExpirationDate = normalizeDate(
			element.value("expirationDate"))
		transaction.UnderlyingTitle = element.value("underlyingSecurity",
			"underlyingSecurityTitle")
		transaction.UnderlyingShares = element.value("underlyingSecurity",
			"underlyingSecurityShares")
	}

	return transaction
}

func footnoteReferences(element *xmlElement) string {
	seen := make(map[string]struct{})

	for _, footnote := range element.descendants("footnoteId") {
		if id := strings.TrimSpace(footnote.attrs["id"]); id != "" {
			seen[id] = struct{}{}
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	return strings.Join(ids, ",")
}
