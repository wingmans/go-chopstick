package artifacts

import (
	"encoding/json"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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

var viewerTemplate = template.Must(template.New("artifact-viewer").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>SEC artifact viewer</title>
  <style>
    :root { font: 14px/1.4 system-ui, sans-serif; color: #171717;
      background: #f1f1ec; }
    body { height: 100vh; margin: 0; overflow: hidden; }
    header { height: 48px; padding: 7px 14px; background: #e75b20;
      border-bottom: 1px solid #a63f14; box-sizing: border-box; }
    header strong { display: block; }
    header span { font-size: 11px; }
    main { display: grid; grid-template-columns: 270px minmax(0, 1fr);
      height: calc(100vh - 48px); }
    aside { overflow-y: auto; padding: 8px; background: #f7f7f1;
      border-right: 1px solid #d9d9d1; }
    .rail-controls { position: sticky; top: -8px; z-index: 1;
      margin: -8px -8px 8px; padding: 8px; color: #555;
      background: #f7f7f1; border-bottom: 1px solid #d9d9d1;
      font-size: 11px; }
    .rail-summary { margin-bottom: 6px; }
    .rail-controls input, .rail-controls select { box-sizing: border-box;
      width: 100%; min-height: 30px; padding: 4px 6px; background: white;
      border: 1px solid #aaa; border-radius: 4px; font: inherit; }
    .rail-filter-row { display: grid; grid-template-columns: 1fr auto;
      gap: 5px; margin-top: 5px; }
    #graphics-only { padding: 4px 7px; color: #555; background: white;
      border: 1px solid #aaa; border-radius: 4px; cursor: pointer;
      font: inherit; }
    #graphics-only.active { color: #111; background: #f5c6b2;
      border-color: #e75b20; }
    .document { display: block; width: 100%; margin: 0 0 8px; padding: 6px;
      color: #171717; background: transparent; border: 1px solid transparent;
      border-radius: 4px; cursor: pointer; font: inherit; text-align: left; }
    .document:hover { background: #ecece5; }
    .document.selected { background: #fff; border-color: #e75b20; }
    .thumb { display: grid; place-items: center; width: 100%; height: 140px;
      overflow: hidden; color: #777; background: white;
      border: 1px solid #d9d9d1; font-size: 10px; font-weight: 700; }
    .thumb img { width: 100%; height: 100%; object-fit: cover; }
    .document-details { display: block; min-width: 0; margin-top: 5px; }
    .filename { display: block; overflow: hidden; font-size: 11px;
      font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
    .meta { display: block; margin-top: 3px; color: #777; font-size: 10px; }
    .preview { display: grid; grid-template-rows: 42px minmax(0, 1fr);
      min-width: 0; min-height: 0; }
    .toolbar { display: flex; align-items: center; gap: 8px; padding: 6px 10px;
      background: #f7f7f1; border-bottom: 1px solid #d9d9d1; }
    .toolbar button { min-width: 32px; padding: 4px 8px; color: #171717;
      background: white; border: 1px solid #aaa; border-radius: 4px;
      cursor: pointer; }
    .toolbar button:disabled { color: #aaa; cursor: default; }
    #progress { min-width: 72px; color: #555; font-variant-numeric: tabular-nums;
      font-size: 12px; }
    #current-name { flex: 1 1 auto; min-width: 0; overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap; }
    .image-controls { display: flex; align-items: center; gap: 4px;
      margin-left: auto; }
    #zoom-status { min-width: 42px; color: #555; font-size: 11px;
      text-align: center; }
    #open-document { flex: 0 0 auto; color: #a63f14;
      font-size: 12px; font-weight: 650; text-decoration: none; }
    .stage { display: grid; min-width: 0; min-height: 0; padding: 10px;
      background: #deded7; }
    .viewer-frame, .image-canvas { width: 100%; height: 100%;
      min-width: 0; min-height: 0; background: white; border: 0; }
    .image-canvas { display: grid; overflow: auto; place-items: center; }
    .image-canvas.actual { place-items: start; }
    .viewer-image.fit { width: 100%; height: 100%; object-fit: contain; }
    .viewer-image.actual { width: auto; height: auto; max-width: none;
      max-height: none; transform-origin: top left; }
    [hidden] { display: none !important; }
    @media (max-width: 700px) {
      main { grid-template-columns: 120px minmax(0, 1fr); }
      .thumb { width: 100%; height: 90px; }
      #current-name { display: none; }
      #zoom-out, #zoom-in, #zoom-status { display: none; }
      #open-document { font-size: 0; }
      #open-document::after { content: "Open"; font-size: 12px; }
    }
  </style>
</head>
<body>
  <header><strong>SEC artifact viewer</strong>
    <span>CIK {{.CIK}} · accession {{.Accession}}</span></header>
  <main>
    <aside id="document-rail">
      <div class="rail-controls">
        <div class="rail-summary"><span id="visible-count">{{len .Documents}}</span>
          of {{len .Documents}} documents · {{.Images}} graphics</div>
        <input id="document-search" type="search"
          placeholder="Search filename or description" aria-label="Search documents">
        <div class="rail-filter-row">
          <select id="type-filter" aria-label="Filter by document type">
            <option value="">All document types</option>
            {{range .Types}}<option value="{{.}}">{{.}}</option>{{end}}
          </select>
          <button id="graphics-only" type="button" aria-pressed="false"
            title="Show graphics only (G)">Graphics</button>
        </div>
      </div>
      {{range $index, $document := .Documents}}
      <button class="document" type="button" data-index="{{$index}}"
        data-url="{{$document.URL}}" data-name="{{$document.Filename}}"
        data-type="{{$document.Type}}" data-image="{{$document.Image}}"
        data-description="{{$document.Description}}"
        title="{{$document.Description}}">
        <span class="thumb">{{if $document.Image}}
          <img src="{{$document.URL}}" alt="" loading="lazy">
          {{else}}{{$document.Type}}{{end}}</span>
        <span class="document-details">
          <span class="filename">{{$document.Filename}}</span>
          <span class="meta">#{{$document.Sequence}} · {{$document.Type}} ·
            {{$document.Size}}</span>
        </span>
      </button>
      {{end}}
    </aside>
    <section class="preview">
      <div class="toolbar">
        <button id="previous" type="button" title="Previous document (←)">←</button>
        <button id="next" type="button" title="Next document (→)">→</button>
        <span id="progress" aria-live="polite"></span>
        <span id="current-name"></span>
        <span class="image-controls">
          <button id="fit-image" type="button" title="Fit image (F)">Fit</button>
          <button id="zoom-out" type="button" title="Zoom out (-)">−</button>
          <span id="zoom-status">Fit</span>
          <button id="zoom-in" type="button" title="Zoom in (+)">+</button>
          <button id="rotate-image" type="button" title="Rotate image (R)">↻</button>
        </span>
        <a id="open-document" target="_blank" rel="noopener">Open separately</a>
      </div>
      <div class="stage">
        <div id="image-canvas" class="image-canvas" hidden>
          <img id="viewer-image" class="viewer-image fit" alt="">
        </div>
        <iframe id="viewer-frame" class="viewer-frame"
          title="Selected filing document" sandbox="allow-same-origin"></iframe>
      </div>
    </section>
  </main>
  <script>
    (() => {
      const documents = [...document.querySelectorAll(".document")];
      const frame = document.querySelector("#viewer-frame");
      const canvas = document.querySelector("#image-canvas");
      const image = document.querySelector("#viewer-image");
      const progress = document.querySelector("#progress");
      const name = document.querySelector("#current-name");
      const open = document.querySelector("#open-document");
      const previous = document.querySelector("#previous");
      const next = document.querySelector("#next");
      const search = document.querySelector("#document-search");
      const typeFilter = document.querySelector("#type-filter");
      const graphicsOnly = document.querySelector("#graphics-only");
      const visibleCount = document.querySelector("#visible-count");
      const fitImage = document.querySelector("#fit-image");
      const zoomOut = document.querySelector("#zoom-out");
      const zoomIn = document.querySelector("#zoom-in");
      const rotateImage = document.querySelector("#rotate-image");
      const zoomStatus = document.querySelector("#zoom-status");
      let current = 0;
      let graphicsOnlyEnabled = false;
      let fitted = true;
      let zoom = 1;
      let rotation = 0;
      let noMatches = false;

      function visibleDocuments() {
        return documents.filter((item) => !item.hidden);
      }

      function updateImageView() {
        const isImage = documents[current] &&
          documents[current].dataset.image === "true";
        [fitImage, zoomOut, zoomIn, rotateImage].forEach((control) => {
          control.disabled = !isImage;
        });
        zoomStatus.textContent = fitted ? "Fit" :
          Math.round(zoom * 100) + "%";
        canvas.classList.toggle("actual", !fitted);
        image.classList.toggle("fit", fitted);
        image.classList.toggle("actual", !fitted);
        image.style.transform = "scale(" + (fitted ? 1 : zoom) +
          ") rotate(" + rotation + "deg)";
      }

      function updateProgress() {
        const visible = visibleDocuments();
        const selected = documents[current];
        const position = visible.indexOf(selected);
        progress.textContent = position < 0 ? "0 / " + visible.length :
          (position + 1) + " / " + visible.length;
        previous.disabled = position <= 0;
        next.disabled = position < 0 || position === visible.length - 1;
      }

      function select(index, updateHash = true) {
        if (!documents.length || index < 0 || index >= documents.length) return;
        current = index;
        const selected = documents[current];
        documents.forEach((item, itemIndex) => {
          item.classList.toggle("selected", itemIndex === current);
        });
        selected.scrollIntoView({block: "nearest"});
        const isImage = selected.dataset.image === "true";
        canvas.hidden = !isImage;
        frame.hidden = isImage;
        if (isImage) {
          frame.removeAttribute("src");
          image.src = selected.dataset.url;
          image.alt = selected.dataset.name;
        } else {
          image.removeAttribute("src");
          frame.src = selected.dataset.url;
        }
        name.textContent = selected.dataset.name;
        name.title = selected.dataset.name;
        open.href = selected.dataset.url;
        updateProgress();
        updateImageView();
        if (updateHash) history.replaceState(null, "", "#" + (current + 1));
      }

      function move(offset) {
        const visible = visibleDocuments();
        const position = visible.indexOf(documents[current]);
        const target = Math.max(0, Math.min(visible.length - 1,
          position + offset));
        if (visible[target]) select(Number(visible[target].dataset.index));
      }

      function applyFilters() {
        const query = search.value.trim().toLowerCase();
        const wantedType = typeFilter.value;
        documents.forEach((item) => {
          const searchable = (item.dataset.name + " " + item.dataset.type +
            " " + item.dataset.description).toLowerCase();
          item.hidden = (query && !searchable.includes(query)) ||
            (wantedType && item.dataset.type !== wantedType) ||
            (graphicsOnlyEnabled && item.dataset.image !== "true");
        });
        const visible = visibleDocuments();
        visibleCount.textContent = visible.length;
        if (!visible.length) {
          noMatches = true;
          canvas.hidden = true;
          frame.hidden = true;
          name.textContent = "No matching documents";
          open.removeAttribute("href");
          progress.textContent = "0 / 0";
          previous.disabled = true;
          next.disabled = true;
          [fitImage, zoomOut, zoomIn, rotateImage].forEach((control) => {
            control.disabled = true;
          });

          return;
        }
        const selected = visible.includes(documents[current]) ?
          documents[current] : visible[0];
        if (noMatches || selected !== documents[current]) {
          noMatches = false;
          select(Number(selected.dataset.index));
        } else {
          updateProgress();
        }
      }

      function setGraphicsOnly(enabled) {
        graphicsOnlyEnabled = enabled;
        graphicsOnly.classList.toggle("active", enabled);
        graphicsOnly.setAttribute("aria-pressed", String(enabled));
        applyFilters();
      }

      function setActualZoom(nextZoom) {
        fitted = false;
        zoom = Math.max(0.25, Math.min(4, nextZoom));
        updateImageView();
      }

      function handleKeys(event) {
        if (event.target === search || event.target === typeFilter) return;
        const currentDocument = documents[current];
        if (event.key === "ArrowLeft") {
          event.preventDefault();
          move(-1);
        } else if (event.key === "ArrowRight") {
          event.preventDefault();
          move(1);
        } else if (event.key === "Home") {
          event.preventDefault();
          const visible = visibleDocuments();
          if (visible[0]) select(Number(visible[0].dataset.index));
        } else if (event.key === "End") {
          event.preventDefault();
          const visible = visibleDocuments();
          if (visible.length) {
            select(Number(visible[visible.length - 1].dataset.index));
          }
        } else if (event.key.toLowerCase() === "g") {
          setGraphicsOnly(!graphicsOnlyEnabled);
        } else if (event.key.toLowerCase() === "f" && currentDocument &&
          currentDocument.dataset.image === "true") {
          fitted = true;
          updateImageView();
        } else if (event.key.toLowerCase() === "r" && currentDocument &&
          currentDocument.dataset.image === "true") {
          rotation = (rotation + 90) % 360;
          updateImageView();
        } else if ((event.key === "+" || event.key === "=") &&
          currentDocument && currentDocument.dataset.image === "true") {
          setActualZoom(fitted ? 1.25 : zoom + 0.25);
        } else if (event.key === "-" && currentDocument &&
          currentDocument.dataset.image === "true") {
          setActualZoom(fitted ? 0.75 : zoom - 0.25);
        } else if (event.key === "/") {
          event.preventDefault();
          search.focus();
        }
      }

      documents.forEach((item, index) => {
        item.addEventListener("click", () => select(index));
      });
      previous.addEventListener("click", () => move(-1));
      next.addEventListener("click", () => move(1));
      search.addEventListener("input", applyFilters);
      search.addEventListener("keydown", (event) => {
        if (event.key === "Escape") {
          search.value = "";
          applyFilters();
          search.blur();
        }
      });
      typeFilter.addEventListener("change", applyFilters);
      graphicsOnly.addEventListener("click", () => {
        setGraphicsOnly(!graphicsOnlyEnabled);
      });
      fitImage.addEventListener("click", () => {
        fitted = true;
        zoom = 1;
        updateImageView();
      });
      zoomOut.addEventListener("click", () => {
        setActualZoom(fitted ? 0.75 : zoom - 0.25);
      });
      zoomIn.addEventListener("click", () => {
        setActualZoom(fitted ? 1.25 : zoom + 0.25);
      });
      rotateImage.addEventListener("click", () => {
        rotation = (rotation + 90) % 360;
        updateImageView();
      });
      image.addEventListener("dblclick", () => {
        fitted = !fitted;
        zoom = 1;
        updateImageView();
      });
      document.addEventListener("keydown", handleKeys);
      frame.addEventListener("load", () => {
        try { frame.contentDocument.addEventListener("keydown", handleKeys); }
        catch (_) { /* Some document types do not expose a DOM. */ }
      });
      const requested = Number.parseInt(location.hash.slice(1), 10);
      select(Number.isFinite(requested) ? requested - 1 : 0, false);
    })();
  </script>
</body>
</html>`))

// Viewer returns a local HTTP handler for one generated artifact bundle.
func Viewer(directory, cik, accession string) (http.Handler, error) {
	manifest, err := readManifest(directory)
	if err != nil {
		return nil, err
	}
	if manifest.GeneratedBy != "chopstick-edgar-artifacts" ||
		manifest.CIK != cik || manifest.Accession != accession {
		return nil, fmt.Errorf("artifact manifest does not match viewer target")
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

	mux := http.NewServeMux()
	mux.Handle("/files/", http.StripPrefix("/files/",
		http.FileServer(http.Dir(directory))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)

			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if err := viewerTemplate.Execute(w, data); err != nil {
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
