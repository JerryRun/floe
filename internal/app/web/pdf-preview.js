const FloePDF = (() => {
  const assetRoot = "/assets/vendor/pdfjs/";
  let modulesPromise, viewer, linkService, attachedItem, restoreState;
  let resizeFrame = 0;

  async function modules() {
    if (!modulesPromise) {
      modulesPromise = (async () => {
        const core = await import(`${assetRoot}build/pdf.min.mjs`);
        core.GlobalWorkerOptions.workerSrc = `${assetRoot}build/pdf.worker.min.mjs`;
        const components = await import(`${assetRoot}web/pdf_viewer.mjs`);
        const eventBus = new components.EventBus();
        linkService = new components.PDFLinkService({ eventBus, externalLinkTarget: 2, externalLinkRel: "noopener noreferrer" });
        viewer = new components.PDFViewer({
          container: $("#pdfContainer"), viewer: $("#pdfViewer"), eventBus, linkService,
          annotationMode: core.AnnotationMode.DISABLE,
          annotationEditorMode: core.AnnotationEditorType.DISABLE,
          enableAutoLinking: false, maxCanvasPixels: 8 * 1024 * 1024,
          imageResourcesPath: `${assetRoot}web/images/`,
        });
        linkService.setViewer(viewer);
        eventBus.on("pagesinit", () => {
          const item = attachedItem;
          if (!item || activePreview()?.id !== item.id) return;
          const saved = restoreState || item.pdf;
          const page = Math.min(Math.max(1, saved.page || 1), item.pdf.document.numPages);
          viewer.currentScaleValue = saved.scale || "page-width";
          viewer.currentPageNumber = page;
          if (saved.location) {
            viewer.scrollPageIntoView({ pageNumber: page, destArray: [null, { name: "XYZ" }, saved.location.left, saved.location.top, null] });
          }
          restoreState = null;
          controls();
        });
        eventBus.on("pagechanging", () => { if (!restoreState) remember(); controls(); });
        eventBus.on("scalechanging", () => { if (!restoreState) remember(); controls(); });
        eventBus.on("updateviewarea", ({ location }) => {
          if (attachedItem && !restoreState) attachedItem.pdf.location = { left: location.left, top: location.top };
          if (!restoreState) remember();
        });
        eventBus.on("pagerendered", ({ error }) => {
          if (error && attachedItem && activePreview()?.id === attachedItem.id) {
            $("#pdfState").textContent = `页面渲染失败：${error.message || error}`;
          }
        });
        return core;
      })().catch((error) => { modulesPromise = null; throw error; });
    }
    return modulesPromise;
  }

  function remember() {
    if (!attachedItem?.pdf || !viewer?.pdfDocument) return;
    attachedItem.pdf.page = viewer.currentPageNumber;
    attachedItem.pdf.scale = viewer.currentScaleValue || "page-width";
  }

  function capture(item) {
    if (attachedItem !== item) return;
    remember();
    viewer.setDocument(null);
    linkService.setDocument(null);
    attachedItem = null;
    restoreState = null;
    $("#pdfPasswordForm").hidden = true;
    $("#pdfPassword").value = "";
  }

  function message(text = "") {
    $("#pdfMessage").textContent = text;
    $("#pdfMessage").hidden = !text;
  }

  function controls() {
    const item = activePreview();
    const pdf = item?.kind === "pdf" ? item.pdf : null;
    const ready = Boolean(pdf?.document && viewer?.pdfDocument === pdf.document);
    const page = ready ? viewer.currentPageNumber : pdf?.page || 1;
    const count = pdf?.document?.numPages || 0;
    $("#pdfPage").value = page;
    $("#pdfPage").max = Math.max(1, count);
    $("#pdfPageCount").textContent = `/ ${count}`;
    $("#pdfZoom").textContent = ready ? `${Math.round(viewer.currentScale * 100)}%` : "—";
    $("#pdfPrevious").disabled = !ready || page <= 1;
    $("#pdfNext").disabled = !ready || page >= count;
    for (const id of ["pdfPage", "pdfZoomIn", "pdfZoomOut", "pdfFitWidth"]) $(`#${id}`).disabled = !ready;
  }

  function showPassword(item) {
    const waiting = Boolean(item.pdf?.passwordCallback);
    $("#pdfPasswordForm").hidden = !waiting;
    if (!waiting) return;
    message();
    $("#pdfPasswordLabel").textContent = item.pdf.passwordReason === 2 ? "密码错误，请重新输入" : "此 PDF 需要密码";
    $("#pdfPassword").value = "";
    $("#pdfPassword").focus();
  }

  function attach(item) {
    if (!viewer || !item.pdf.document || activePreview()?.id !== item.id) return;
    if (attachedItem !== item || viewer.pdfDocument !== item.pdf.document) {
      attachedItem = item;
      restoreState = { page: item.pdf.page, scale: item.pdf.scale, location: item.pdf.location };
      linkService.setDocument(item.pdf.document);
      viewer.setDocument(item.pdf.document);
    }
    controls();
    resize();
  }

  function apply(item) {
    item.pdf ||= { page: 1, scale: "page-width", sequence: 0 };
    $("#pdfTitle").textContent = item.path;
    $("#pdfTitle").title = item.path;
    $("#pdfState").textContent = item.pdf.error || (item.loading ? "正在读取…" : item.pdf.document ? `${item.pdf.document.numPages} 页` : "正在读取…");
    message(item.pdf.document ? "" : item.pdf.error || "正在读取 PDF…");
    showPassword(item);
    if (item.pdf.document) attach(item);
    else if (!item.loading && !item.pdf.error) load(item);
    controls();
  }

  async function load(item, reload = false) {
    const pdf = item.pdf;
    const sequence = ++pdf.sequence;
    item.loading = true;
    pdf.error = "";
    syncPreviewReloadButtons();
    if (activePreview()?.id === item.id) {
      $("#pdfState").textContent = reload ? "正在重新载入…" : "正在读取…";
      if (!pdf.document) message("正在读取 PDF…");
    }
    let task;
    try {
      const core = await modules();
      if (pdf.sequence !== sequence || findPreview(item.id) !== item) return;
      task = core.getDocument({
        url: `/api/v1/files/pdf?provider=${encodeURIComponent(item.provider)}&path=${encodeURIComponent(item.path)}&reload=${Date.now()}`,
        cMapUrl: `${assetRoot}cmaps/`, cMapPacked: true,
        standardFontDataUrl: `${assetRoot}standard_fonts/`, wasmUrl: `${assetRoot}wasm/`,
        // Prefer bounded range requests to reading a large remote PDF eagerly.
        disableAutoFetch: true, disableStream: true, rangeChunkSize: 256 * 1024,
        useWasm: false,
      });
      pdf.pendingTask = task;
      task.onPassword = (callback, reason) => {
        if (pdf.sequence !== sequence || findPreview(item.id) !== item) return;
        pdf.passwordCallback = callback;
        pdf.passwordReason = reason;
        if (activePreview()?.id === item.id) showPassword(item);
      };
      const document = await task.promise;
      if (pdf.sequence !== sequence || findPreview(item.id) !== item) { await task.destroy(); return; }
      const oldTask = pdf.task;
      pdf.document = document;
      pdf.task = task;
      pdf.pendingTask = null;
      pdf.passwordCallback = null;
      pdf.page = Math.min(pdf.page || 1, document.numPages);
      if (activePreview()?.id === item.id) {
        $("#pdfPasswordForm").hidden = true;
        $("#pdfPassword").value = "";
        message();
        $("#pdfState").textContent = `${document.numPages} 页`;
        attach(item);
      }
      if (oldTask) oldTask.destroy().catch(() => {});
    } catch (error) {
      if (pdf.sequence !== sequence || findPreview(item.id) !== item) return;
      pdf.passwordCallback = null;
      pdf.pendingTask = null;
      if (task) task.destroy().catch(() => {});
      const detail = error.name === "InvalidPDFException" ? "文件损坏或不是有效的 PDF" : error.message || "无法读取文件";
      pdf.error = `PDF ${reload ? "重新载入" : "读取"}失败：${detail}`;
      if (activePreview()?.id === item.id) {
        $("#pdfPasswordForm").hidden = true;
        $("#pdfState").textContent = pdf.error;
        if (!pdf.document) message(`${pdf.error}。可点击重新载入重试。`);
      }
    } finally {
      if (pdf.sequence === sequence) { item.loading = false; syncPreviewReloadButtons(); controls(); }
    }
  }

  function open(side, entry) {
    const item = openPreview("pdf", currentProvider(side), entry.path);
    apply(item);
  }

  function reload(item) {
    remember();
    return load(item, true);
  }

  function release(item) {
    const pdf = item.pdf;
    if (!pdf) return;
    if (attachedItem === item) capture(item);
    pdf.sequence++;
    for (const task of new Set([pdf.task, pdf.pendingTask])) task?.destroy().catch(() => {});
    pdf.task = pdf.pendingTask = pdf.document = pdf.passwordCallback = null;
    item.loading = false;
  }

  function resize() {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => {
      if (!attachedItem || activePreview()?.id !== attachedItem.id || !viewer?.pdfDocument || !$("#pdfContainer").clientWidth) return;
      const scale = viewer.currentScaleValue;
      if (["page-width", "page-fit", "auto"].includes(scale)) viewer.currentScaleValue = scale;
      viewer.update();
      controls();
    });
  }

  function goToPage(number) {
    if (!viewer?.pdfDocument) return;
    const count = viewer.pdfDocument.numPages;
    viewer.currentPageNumber = Math.min(count, Math.max(1, Math.trunc(Number(number)) || 1));
    controls();
  }

  function zoom(factor) {
    if (!viewer?.pdfDocument) return;
    viewer.currentScaleValue = Math.min(4, Math.max(0.1, viewer.currentScale * factor));
  }

  $("#pdfPrevious").addEventListener("click", () => goToPage(viewer.currentPageNumber - 1));
  $("#pdfNext").addEventListener("click", () => goToPage(viewer.currentPageNumber + 1));
  $("#pdfPage").addEventListener("change", (event) => goToPage(event.target.value));
  $("#pdfPage").addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); goToPage(event.target.value); } });
  $("#pdfZoomOut").addEventListener("click", () => zoom(1 / 1.2));
  $("#pdfZoomIn").addEventListener("click", () => zoom(1.2));
  $("#pdfFitWidth").addEventListener("click", () => { if (viewer?.pdfDocument) viewer.currentScaleValue = "page-width"; });
  $("#collapsePDF").addEventListener("click", () => setPreviewHost("panel"));
  $("#maximizePDF").addEventListener("click", () => toggleDialogMaximized($("#pdfDialog"), $("#maximizePDF")));
  $("#closePDF").addEventListener("click", () => closePreviewWindow("pdf"));
  $("#pdfDialog").addEventListener("cancel", (event) => { event.preventDefault(); closePreviewWindow("pdf"); });
  $("#pdfPasswordForm").addEventListener("submit", (event) => {
    event.preventDefault();
    const pdf = activePreview()?.pdf;
    if (!pdf?.passwordCallback) return;
    const callback = pdf.passwordCallback;
    pdf.passwordCallback = null;
    const password = $("#pdfPassword").value;
    $("#pdfPassword").value = "";
    $("#pdfPasswordForm").hidden = true;
    message(pdf.document ? "" : "正在打开 PDF…");
    callback(password);
  });
  $("#pdfPasswordCancel").addEventListener("click", () => {
    const item = activePreview();
    if (item?.kind !== "pdf" || !item.pdf.passwordCallback) return;
    const pdf = item.pdf;
    pdf.sequence++;
    pdf.pendingTask?.destroy().catch(() => {});
    pdf.pendingTask = pdf.passwordCallback = null;
    pdf.error = "已取消密码输入，可点击重新载入重试";
    item.loading = false;
    $("#pdfPasswordForm").hidden = true;
    $("#pdfPassword").value = "";
    apply(item);
    syncPreviewReloadButtons();
  });
  $$('[data-preview-reload]').forEach((button) => button.addEventListener("click", reloadPreview));
  new ResizeObserver(resize).observe($("#pdfContainer"));
  return { open, apply, capture, reload, release, resize };
})();
