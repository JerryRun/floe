// Re-reading a file is deliberately separate from re-rendering an HTML draft.
function cancelPreviewLoad(item) {
  item.loadSequence = (item.loadSequence || 0) + 1;
  item.loadController?.abort();
  item.loadController = null;
  item.loading = false;
}

function syncPreviewReloadButtons() {
  const item = activePreview();
  $$('[data-preview-reload]').forEach((button) => {
    button.disabled = !item || Boolean(item.loading || item.editor?.saving);
    button.title = item?.loading ? "正在重新载入…" : "从文件重新载入";
  });
  if (item?.kind === "text") {
    $("#editorContent").readOnly = Boolean(item.loading);
    if (item.loading) $("#saveEditor").disabled = true;
  }
}

async function loadTextPreview(item, side, reload = false) {
  cancelPreviewLoad(item);
  const sequence = item.loadSequence;
  const controller = new AbortController();
  item.loadController = controller;
  item.loading = true;
  item.loadError = "";
  const previous = item.editor;
  const previousText = item.text;
  if (activePreview()?.id === item.id) {
    $("#editorState").textContent = reload ? "正在重新载入…" : "正在读取…";
    syncPreviewReloadButtons();
  }
  try {
    const result = await api(`/api/v1/files/content?provider=${encodeURIComponent(item.provider)}&path=${encodeURIComponent(item.path)}`, { cache: "no-store", signal: controller.signal });
    if (findPreview(item.id) !== item || sequence !== item.loadSequence) return false;
    const previewKind = isMarkdownPath(item.path) ? "markdown" : (isHTMLPath(item.path) ? "html" : "");
    item.editor = {
      provider: item.provider, path: item.path, etag: result.etag, side, language: syntaxForPath(item.path),
      originalContent: result.content, dirty: false, saving: false, encoding: result.encoding || "utf-8",
      bom: Boolean(result.bom), newline: result.newline || "lf", mixedNewlines: Boolean(result.mixed_newlines),
      size: result.size ?? new TextEncoder().encode(result.content).length, lineCount: 0, matchCase: previous?.matchCase || false,
      previewKind, previewOpen: previous?.previewOpen || false, previewFocus: false, htmlPreviewToken: "", htmlPreviewSequence: 0,
      htmlViewportMode: previous?.htmlViewportMode || "responsive", htmlViewportWidth: previous?.htmlViewportWidth || 1280,
      htmlViewportHeight: previous?.htmlViewportHeight || 720, htmlViewportScale: 1,
    };
    item.text = {
      content: result.content, syntax: previousText?.syntax || "auto", scrollTop: previousText?.scrollTop || 0,
      selectionStart: Math.min(previousText?.selectionStart || 0, result.content.length),
      selectionEnd: Math.min(previousText?.selectionEnd || 0, result.content.length),
    };
    releaseHTMLPreview(previous);
    if (activePreview()?.id === item.id) {
      applyPreview(item);
      if (!reload) $("#editorContent").focus();
    }
    renderPreviewTabs();
    return true;
  } catch (error) {
    if (sequence !== item.loadSequence || findPreview(item.id) !== item || error.name === "AbortError") return false;
    item.loadError = `${reload ? "重新载入" : "读取"}失败：${error.message}`;
    if (activePreview()?.id === item.id) $("#editorState").textContent = item.loadError;
    toast(item.loadError, "error");
    return false;
  } finally {
    if (sequence === item.loadSequence) {
      item.loading = false;
      item.loadController = null;
      if (activePreview()?.id === item.id) {
        syncPreviewReloadButtons();
        if (state.editor) $("#saveEditor").disabled = !state.editor.dirty || state.editor.saving;
      }
    }
  }
}

async function reloadPreview() {
  const item = activePreview();
  if (!item || item.loading) return;
  if (item.kind === "text") {
    const editor = state.editor;
    if (editor?.saving) { toast("文件正在保存，请稍候"); return; }
    if (editor?.dirty && !confirm(`${item.name} 有未保存的修改，放弃修改并重新载入？`)) return;
    capturePreview(item);
    await loadTextPreview(item, editor?.side || state.activePane, true);
  } else if (item.kind === "image" && item.image) {
    await reloadImagePreview(item);
  } else if (item.kind === "media" && item.media) {
    capturePreview(item);
    item.media.source = `${mediaURL(item.provider, item.path)}&reload=${Date.now()}`;
    item.media.playing = false;
    applyMediaPreview(item);
  } else if (item.kind === "pdf") {
    await FloePDF.reload(item);
  }
}

async function reloadImagePreview(item) {
  cancelPreviewLoad(item);
  const sequence = item.loadSequence;
  const controller = new AbortController();
  item.loadController = controller;
  item.loading = true;
  syncPreviewReloadButtons();
  const revision = Date.now();
  const url = `/api/v1/files/raw?provider=${encodeURIComponent(item.provider)}&path=${encodeURIComponent(item.path)}&reload=${revision}`;
  let objectURL;
  try {
    const response = await fetch(url, { cache: "no-store", signal: controller.signal });
    if (!response.ok) throw new Error((await response.json()).message || "无法读取图片");
    objectURL = URL.createObjectURL(await response.blob());
    const image = new Image();
    image.src = objectURL;
    await image.decode();
    if (sequence !== item.loadSequence || findPreview(item.id) !== item) return;
    item.image.revision = revision;
    // Keep the decoded replacement until the display has consumed it. The URL
    // is also retained per tab, so switching back does not issue a stale request.
    const previousURL = item.image.objectURL;
    item.image.objectURL = objectURL;
    objectURL = null;
    if (activePreview()?.id === item.id) {
      const zoom = item.image.zoom;
      $("#imagePreview").src = item.image.objectURL;
      $("#imagePreview").addEventListener("load", () => {
        if (activePreview()?.id === item.id) setImageZoom(zoom);
      }, { once: true });
    }
    if (previousURL) URL.revokeObjectURL(previousURL);
  } catch (error) {
    if (sequence === item.loadSequence && findPreview(item.id) === item && error.name !== "AbortError") toast(`重新载入失败：${error.message}`, "error");
  } finally {
    if (objectURL) URL.revokeObjectURL(objectURL);
    if (sequence === item.loadSequence) {
      item.loading = false;
      item.loadController = null;
      syncPreviewReloadButtons();
    }
  }
}

function releaseImagePreview(item) {
  if (item.image?.objectURL) URL.revokeObjectURL(item.image.objectURL);
}
