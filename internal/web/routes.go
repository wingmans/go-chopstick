package web

import (
	"net/http"
)

const (
	filingsRoute   = "/"
	ownershipRoute = "/ownership"
	newsRoute      = "/news"
	statusRoute    = "/status"
)

func publicModuleRoutes() []string {
	return []string{
		filingsRoute,
		ownershipRoute,
		newsRoute,
		statusRoute,
	}
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Public modules are the stable application-shell navigation targets.
	mux.HandleFunc(filingsRoute+"{$}", s.index)
	mux.HandleFunc(ownershipRoute, s.ownershipDirectory)
	mux.HandleFunc(newsRoute, s.serveNews)
	mux.HandleFunc(newsRoute+"/", s.serveNews)
	mux.HandleFunc(statusRoute, s.status)

	// Contextual pages are reached from their parent module.
	mux.HandleFunc("/companies/{cik}", s.company)
	mux.HandleFunc("/filings/{cik}/{accession}", s.detail)
	mux.HandleFunc("/ownership/companies/{cik}", s.ownershipCompany)
	mux.HandleFunc("/debug/raw/filings/{cik}/{accession}", s.rawInspector)

	// APIs, actions, raw documents, and assets are intentionally absent from
	// primary navigation.
	mux.HandleFunc("/api/filings", s.apiFilings)
	mux.HandleFunc("/debug/raw/api/filings/{cik}/{accession}",
		s.rawAPIFiling)
	mux.HandleFunc(
		"/debug/raw/filings/{cik}/{accession}/documents/{document}",
		s.rawDocument,
	)
	mux.HandleFunc("/assets/chopstick.svg", func(w http.ResponseWriter,
		r *http.Request,
	) {
		s.asset(w, r, "static/assets/chopstick.svg", "image/svg+xml")
	})
	mux.HandleFunc("/assets/filingweb.css", func(w http.ResponseWriter,
		r *http.Request,
	) {
		s.asset(w, r, "static/css/filingweb.css", "text/css")
	})
	mux.HandleFunc("/assets/news.css", func(w http.ResponseWriter,
		r *http.Request,
	) {
		s.asset(w, r, "static/css/news.css", "text/css")
	})
	mux.HandleFunc("/assets/htmx.min.js", func(w http.ResponseWriter,
		r *http.Request,
	) {
		s.asset(w, r, "static/script/htmx-4.0.0.min.js",
			"text/javascript")
	})
	mux.HandleFunc("/assets/dashboard.js", func(w http.ResponseWriter,
		r *http.Request,
	) {
		s.asset(w, r, "static/script/dashboard.js", "text/javascript")
	})

	return mux
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.logger != nil {
		s.logger.Debug("local dashboard request",
			"method", r.Method, "url", r.URL.String())
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) serveNews(w http.ResponseWriter, r *http.Request) {
	if s.news == nil {
		http.NotFound(w, r)

		return
	}

	s.news.ServeHTTP(w, r)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request, path, contentType string) {
	data, err := staticFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if _, err := w.Write(data); err != nil && s.logger != nil {
		s.logger.Debug("write asset response failed", "error", err)
	}
}
