(() => {
  const recentKey = "edgar.recent-filings.v1";
  const searchTrailKey = "edgar.search-trail.v1";
  const recentLimit = 8;
  const searchTrailLimit = 8;
  const esc = value => String(value ?? "").replace(/[&<>"']/g, character => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;",
    "'": "&#39;"
  })[character]);

  function searchTrailEntry(query, form, year) {
    query = String(query || "").trim();
    form = String(form || "").trim();
    year = String(year || "").trim();
    const parameters = new URLSearchParams();
    if (query) parameters.set("q", query);
    if (form) parameters.set("form_type", form);
    if (year) parameters.set("year", year);
    const suffix = parameters.toString();
    return {
      key: suffix,
      label: [query.toUpperCase(), form, year].filter(Boolean).join(" / ") ||
        "All filings",
      href: suffix ? "/?" + suffix : "/",
      query, form, year
    };
  }

  function read(key) {
    try {
      const entries = JSON.parse(localStorage.getItem(key) || "[]");
      return Array.isArray(entries) ? entries : [];
    } catch (_) {
      return [];
    }
  }

  function write(key, entries) {
    try { localStorage.setItem(key, JSON.stringify(entries)); } catch (_) {}
  }

  function rememberSearch(query, form, year) {
    if (!query && !form && !year) return;
    const next = searchTrailEntry(query, form, year);
    const entries = read(searchTrailKey)
      .map(entry => entry.key ? entry : searchTrailEntry(
        entry.query, entry.form, entry.year))
      .filter(entry => entry.key !== next.key);
    entries.unshift(next);
    write(searchTrailKey, entries.slice(0, searchTrailLimit));
  }

  function renderSearchBreadcrumb() {
    const container = document.getElementById("search-breadcrumb-items");
    if (!container) return;
    const entries = read(searchTrailKey);
    container.innerHTML = entries.length ? entries.map(entry =>
      `<a href="${esc(entry.href)}">${esc(entry.label)}</a>`).join("") :
      "<span data-chip>All filings</span>";
  }

  function renderRecent() {
    const container = document.getElementById("recent-links");
    const navigation = document.getElementById("recent-filings");
    if (!container || !navigation) return;
    const entries = read(recentKey).filter(entry => entry && entry.ticker &&
      typeof entry.href === "string" && entry.href.startsWith("/filings/"));
    container.replaceChildren(...entries.map(entry => {
      const link = document.createElement("a");
      link.href = entry.href;
      link.textContent = entry.ticker;
      return link;
    }));
    navigation.classList.toggle("visible", entries.length > 0);
  }

  function rememberFiling(ticker, href) {
    if (!ticker || !href) return;
    const entries = read(recentKey).filter(entry => entry.href !== href);
    entries.unshift({ ticker, href });
    write(recentKey, entries.slice(0, recentLimit));
    renderRecent();
  }

  document.getElementById("search")?.addEventListener("submit", () => {
    rememberSearch(
      document.getElementById("query").value.trim(),
      document.getElementById("form-type").value,
      document.getElementById("year").value.trim()
    );
    renderSearchBreadcrumb();
  });
  document.addEventListener("click", event => {
    const link = event.target.closest("a[data-ticker]");
    if (link) rememberFiling(link.dataset.ticker, link.getAttribute("href"));
    if (event.target.id === "clear-recent") {
      write(recentKey, []);
      renderRecent();
    }
  });
  document.body.addEventListener("htmx:after:swap", () => {
    renderSearchBreadcrumb();
    renderRecent();
  });
  renderSearchBreadcrumb();
  renderRecent();
})();
