package artifacts

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed viewer/index.html viewer/viewer.css viewer/viewer.js
var viewerFiles embed.FS

type viewerData struct {
	CIK       string
	Accession string
	Documents []viewerDocument
	Images    int
	Types     []string
}

type viewerDocument struct {
	Type        string
	Sequence    string
	Filename    string
	Description string
	Size        string
	URL         string
	Image       bool
}

// Viewer returns a local HTTP handler for one generated artifact bundle.
func Viewer(directory, cik, accession string) (http.Handler, error) {
	viewerTemplate, err := template.ParseFS(viewerFiles, "viewer/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse embedded viewer template: %w", err)
	}

	manifest, err := readManifest(directory)
	if err != nil {
		return nil, err
	}

	if manifest.GeneratedBy != "chopstick-edgar-artifacts" ||
		manifest.CIK != cik || manifest.Accession != accession {
		return nil, errors.New("artifact manifest does not match viewer target")
	}

	data := viewerData{
		CIK: cik, Accession: accession,
		Documents: make([]viewerDocument, 0, len(manifest.Documents)),
	}
	types := make(map[string]struct{})

	for _, document := range manifest.Documents {
		item := viewerDocument{
			Type: document.Type, Sequence: document.Sequence,
			Filename: document.Filename, Description: document.Description,
			Size: formatBytes(document.Bytes),
			URL:  "/files/" + url.PathEscape(document.Filename),
			Image: strings.HasPrefix(mime.TypeByExtension(
				filepath.Ext(document.Filename)), "image/"),
		}

		data.Documents = append(data.Documents, item)
		if item.Image {
			data.Images++
		}

		if item.Type != "" {
			types[item.Type] = struct{}{}
		}
	}

	for documentType := range types {
		data.Types = append(data.Types, documentType)
	}

	sort.Strings(data.Types)

	assets, err := fs.Sub(viewerFiles, "viewer")
	if err != nil {
		return nil, fmt.Errorf("open embedded viewer assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/files/", http.StripPrefix("/files/",
		http.FileServer(http.Dir(directory))))
	mux.Handle("/viewer-assets/", http.StripPrefix("/viewer-assets/",
		http.FileServer(http.FS(assets))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)

			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if err := viewerTemplate.ExecuteTemplate(w, "index.html", data); err != nil {
			http.Error(w, "render artifact viewer", http.StatusInternalServerError)
		}
	})

	return mux, nil
}

func readManifest(directory string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(directory, manifestName))
	if err != nil {
		return Manifest{}, fmt.Errorf("read artifact manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode artifact manifest: %w", err)
	}

	return manifest, nil
}

func formatBytes(size int64) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1024*1024))
	}

	if size >= 1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}

	return fmt.Sprintf("%d B", size)
}
