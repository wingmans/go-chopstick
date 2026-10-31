package web

import (
	"net/http"
	"sort"
	"strings"

	"wingman.com/chopstick/internal/ownership"
)

func (s *Server) ownershipDirectory(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	data := ownershipDirectoryData{
		Page: pageChrome{
			Title:         "Company ownership",
			BodyClass:     "ownership-page",
			ActiveSection: "ownership",
			UseHTMX:       true,
		},
		SetName:   s.setName,
		SetAsOf:   s.setAsOf,
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		Available: s.ownership != nil,
	}

	for _, member := range s.members {
		if query != "" && !strings.Contains(strings.ToLower(member.CIK), query) &&
			!strings.Contains(strings.ToLower(member.Ticker), query) &&
			!strings.Contains(strings.ToLower(member.Name), query) {
			continue
		}

		data.Members = append(data.Members, ownershipDirectoryMember{
			CIK: member.CIK, Ticker: member.Ticker, Name: member.Name,
		})
	}

	sort.Slice(data.Members, func(i, j int) bool {
		return data.Members[i].Ticker < data.Members[j].Ticker
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	component := ownershipDirectoryPage(data)
	if isWorkspaceRequest(r) {
		component = ownershipDirectoryBody(data)
	}

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}

func (s *Server) ownershipCompany(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/ownership/companies/")
	if path == "" || strings.Contains(path, "/") {
		http.NotFound(w, r)

		return
	}

	cik := canonicalCIK(path)

	member, allowed := s.memberByCIK[cik]
	if !allowed {
		http.NotFound(w, r)

		return
	}

	data := ownershipCompanyData{
		Page: pageChrome{
			Title:         member.Name + " ownership",
			BodyClass:     "ownership-page",
			ActiveSection: "ownership",
			UseHTMX:       true,
		},
		Member: ownershipDirectoryMember{
			CIK: member.CIK, Ticker: member.Ticker, Name: member.Name,
		},
		Available: s.ownership != nil,
	}

	if s.ownership != nil {
		var err error

		data.Data, err = s.ownership.LoadCompanyOwnership(r.Context(), cik)
		if err != nil {
			s.writeError(w, err)

			return
		}

		data.Coverage, err = s.ownership.LatestCoverage(
			r.Context(), ownership.ParserVersion)
		if err != nil {
			s.writeError(w, err)

			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	component := ownershipCompanyPage(data)
	if isWorkspaceRequest(r) {
		component = ownershipCompanyBody(data)
	}

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}
