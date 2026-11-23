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
