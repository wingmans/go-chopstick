package web

import "net/http"

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	page := pageChrome{
		Title:         "EDGAR status",
		ActiveSection: "status", UseHTMX: true,
	}

	ctx := r.Context()

	component := statusPage(ctx, page, s.news != nil)
	if isWorkspaceRequest(r) {
		component = statusPageBody(ctx, s.news != nil)
	}

	if err := component.Render(ctx, w); err != nil {
		s.writeError(w, err)
	}
}
