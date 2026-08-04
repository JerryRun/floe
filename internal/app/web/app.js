const state = {
  csrf: "",
  providers: [],
  activePane: "left",
  sessionSelected: "",
  sessionGroups: [],
  sessionGroupIndex: 0,
  groupState: {},
  bookmarks: {},
  taskStatus: new Map(),
  transferMetrics: new Map(),
  tasks: [],
  logs: [],
  taskFilter: "queue",
  localTreeSide: "",
  bookmarkSide: "",
  terminalProvider: "",
  panels: {
    left: { tabs: [], active: "", entries: [], selected: null, view: "list", sort: { field: "name", direction: "asc" }, renderQueued: false },
    right: { tabs: [], active: "", entries: [], selected: null, view: "list", sort: { field: "name", direction: "asc" }, renderQueued: false },
  },
  editor: null,
  image: null,
  hls: null,
};

const DND_FILE = "application/x-floe-file";
const DND_SESSION = "application/x-floe-session";
const DND_TAB = "application/x-floe-tab";
const BOOKMARKS_STORAGE = "floe.bookmarks.v1";
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];

async function api(url, options = {}, retrySession = true) {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (options.method && !["GET", "HEAD"].includes(options.method)) headers.set("X-Floe-CSRF", state.csrf);
  const response = await fetch(url, { ...options, headers });
  if (response.status === 401 && retrySession && url !== "/api/v1/session") {
    const session = await api("/api/v1/session", {}, false);
    state.csrf = session.csrf;
    return api(url, options, false);
  }
  const type = response.headers.get("content-type") || "";
  const payload = type.includes("application/json") ? await response.json() : { message: await response.text() };
  if (!response.ok) {
    const error = new Error(payload.message || `请求失败 (${response.status})`);
    error.payload = payload;
    error.status = response.status;
    throw error;
  }
  return payload;
}

function toast(message, kind = "info") {
  const node = document.createElement("div");
  node.className = `toast ${kind}`;
  node.textContent = message;
  $("#toastStack").append(node);
  setTimeout(() => node.remove(), 3500);
}

function providerByID(id) { return state.providers.find((item) => item.id === id); }
function currentTab(side) {
  const panel = state.panels[side];
  return panel.tabs.find((tab) => tab.provider === panel.active) || panel.tabs[0];
}
function currentProvider(side) { return currentTab(side)?.provider || ""; }
function currentPath(side) { return currentTab(side)?.path || "/"; }

function panelElements(side) {
  const root = $(`#${side}Pane`);
  return {
    root, tabs: $(`#${side}Tabs`), path: $(".path-input", root), viewport: $(".file-viewport", root),
    canvas: $(".file-canvas", root), count: $(".entry-count", root), selection: $(".selection-label", root),
    viewButton: $(".view-button", root), mask: $(".drop-mask", root),
  };
}

function initializeWorkspace() {
  const ids = new Set(state.providers.map((provider) => provider.id));
  let saved;
  try { saved = JSON.parse(localStorage.getItem("floe.workspace.v2")); } catch (_) { saved = null; }
  for (const side of ["left", "right"]) {
    const source = saved?.panels?.[side];
    if (source) {
      state.panels[side].tabs = (source.tabs || []).filter((tab) => ids.has(tab.provider)).map((tab) => ({ provider: tab.provider, path: tab.path || "/" }));
      state.panels[side].active = ids.has(source.active) ? source.active : "";
      state.panels[side].view = source.view === "grid" ? "grid" : "list";
      if (["name", "size", "modified"].includes(source.sort?.field)) {
        state.panels[side].sort = { field: source.sort.field, direction: source.sort.direction === "desc" ? "desc" : "asc" };
      }
    }
  }
  const local = providerByID("local") || state.providers.find((provider) => provider.kind === "local");
  const left = state.panels.left;
  const right = state.panels.right;
  if (local && !left.tabs.some((tab) => tab.provider === local.id)) left.tabs.unshift({ provider: local.id, path: "/" });
  if (!left.tabs.length && state.providers[0]) left.tabs.push({ provider: state.providers[0].id, path: "/" });
  if (!left.active || !left.tabs.some((tab) => tab.provider === left.active)) left.active = left.tabs[0]?.provider || "";
  if (!right.tabs.length) {
    const provider = local || state.providers[0];
    if (provider) right.tabs.push({ provider: provider.id, path: "/" });
  }
  if (!right.active || !right.tabs.some((tab) => tab.provider === right.active)) right.active = right.tabs[0]?.provider || "";
  saveWorkspace();
}

function saveWorkspace() {
  const compact = { panels: {} };
  for (const side of ["left", "right"]) {
    const panel = state.panels[side];
    compact.panels[side] = { tabs: panel.tabs.map(({ provider, path }) => ({ provider, path })), active: panel.active, view: panel.view, sort: panel.sort };
  }
  localStorage.setItem("floe.workspace.v2", JSON.stringify(compact));
}

async function loadProviders(refreshTree = true) {
  state.providers = (await api("/api/v1/providers")).sort((a, b) => {
    const order = { local: 0, sftp: 1, ftp: 2 };
    return (order[a.kind] ?? 3) - (order[b.kind] ?? 3) || a.name.localeCompare(b.name);
  });
  refreshSessionGroups();
  if (refreshTree) renderSessionTree();
}

function refreshSessionGroups() {
  state.sessionGroups = [...new Set(state.providers
    .filter((provider) => provider.kind !== "local" && provider.group)
    .map((provider) => provider.group.trim())
    .filter(Boolean))].sort((a, b) => a.localeCompare(b, "zh-CN"));
  if ($("#sessionGroupControl").classList.contains("open")) renderSessionGroupMenu();
}

function renderSessionGroupMenu() {
  const menu = $("#sessionGroupMenu");
  menu.replaceChildren(...state.sessionGroups.map((group, index) => {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("role", "option");
    button.textContent = group;
    button.classList.toggle("active", index === state.sessionGroupIndex);
    button.setAttribute("aria-selected", String(index === state.sessionGroupIndex));
    button.addEventListener("click", () => chooseSessionGroup(index));
    return button;
  }));
}

function showSessionGroupMenu() {
  if (!state.sessionGroups.length) return;
  const control = $("#sessionGroupControl");
  const input = $("input[name=group]", control);
  const match = state.sessionGroups.findIndex((group) => group === input.value.trim());
  state.sessionGroupIndex = match >= 0 ? match : 0;
  renderSessionGroupMenu();
  control.classList.add("open");
  input.setAttribute("aria-expanded", "true");
}

function hideSessionGroupMenu() {
  $("#sessionGroupControl").classList.remove("open");
  $("#sessionGroupControl input[name=group]").setAttribute("aria-expanded", "false");
}

function toggleSessionGroupMenu() {
  if ($("#sessionGroupControl").classList.contains("open")) hideSessionGroupMenu();
  else showSessionGroupMenu();
}

function chooseSessionGroup(index) {
  const group = state.sessionGroups[index];
  if (group === undefined) return;
  const input = $("#sessionGroupControl input[name=group]");
  input.value = group;
  hideSessionGroupMenu();
  input.focus();
}

function handleSessionGroupKey(event) {
  const open = $("#sessionGroupControl").classList.contains("open");
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    if (!open) showSessionGroupMenu();
    else {
      const step = event.key === "ArrowDown" ? 1 : -1;
      state.sessionGroupIndex = (state.sessionGroupIndex + step + state.sessionGroups.length) % state.sessionGroups.length;
      renderSessionGroupMenu();
    }
  } else if (event.key === "Enter" && open) {
    event.preventDefault();
    chooseSessionGroup(state.sessionGroupIndex);
  } else if (event.key === "Escape" && open) {
    event.preventDefault();
    hideSessionGroupMenu();
  }
}

function renderSessionTree() {
  const tree = $("#sessionTree");
  const filter = $("#sessionSearch").value.trim().toLowerCase();
  const groups = new Map();
  for (const provider of state.providers) {
    if (filter && !`${provider.name} ${provider.group}`.toLowerCase().includes(filter)) continue;
    const group = provider.group || (provider.kind === "sftp" ? "我的会话" : "其他");
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push(provider);
  }
  tree.replaceChildren();
  for (const [groupName, providers] of groups) {
    const group = document.createElement("section");
    group.className = `session-group${state.groupState[groupName] ? " collapsed" : ""}`;
    const head = document.createElement("button");
    head.className = "group-head";
    head.innerHTML = `<i class="material-symbols-rounded">${state.groupState[groupName] ? "chevron_right" : "expand_more"}</i><span>${escapeHTML(groupName)}</span><small>${providers.length}</small>`;
    head.addEventListener("click", () => { state.groupState[groupName] = !state.groupState[groupName]; renderSessionTree(); });
    const items = document.createElement("div");
    items.className = "session-items";
    for (const provider of providers) {
      const item = document.createElement("div");
      item.className = `session-item${currentProvider(state.activePane) === provider.id ? " active" : ""}`;
      item.draggable = true;
      item.title = provider.kind === "local" ? "双击打开，或拖到任一标签栏" : "双击连接；右键查看、修改或删除配置";
      item.innerHTML = `<span class="session-icon">${sessionIcon(provider)}</span><span class="session-name">${escapeHTML(provider.name)}</span>`;
      item.addEventListener("click", () => { state.sessionSelected = provider.id; });
      item.addEventListener("dblclick", () => openSession("right", provider.id));
      if (provider.kind !== "local") {
        item.addEventListener("contextmenu", (event) => {
          event.preventDefault(); event.stopPropagation();
          state.sessionSelected = provider.id;
          showSessionMenu(event, provider);
        });
      }
      item.addEventListener("dragstart", (event) => {
        item.classList.add("dragging");
        event.dataTransfer.effectAllowed = "copy";
        event.dataTransfer.setData(DND_SESSION, provider.id);
      });
      item.addEventListener("dragend", () => item.classList.remove("dragging"));
      items.append(item);
    }
    group.append(head, items);
    tree.append(group);
  }
}

function sessionIcon(provider) {
  const icon = provider.kind === "local" ? "computer" : provider.kind === "ftp" ? "public" : "cloud";
  const connection = provider.connected || provider.kind === "local" ? "connected" : "disconnected";
  return `<span class="material-symbols-rounded ${connection}" aria-hidden="true">${icon}</span>`;
}

function showSessionMenu(event, provider) {
  const items = [
    { label: provider.connected ? "在右侧打开" : "连接并打开", action: () => openSession("right", provider.id) },
    { label: "查看 / 修改配置", action: () => editSession(provider.id) },
    { label: "复制会话", action: () => copySession(provider) },
  ];
  if (provider.kind === "sftp") {
    items.push(
      { separator: true },
      { heading: "用 Terminal 打开" },
      { label: "新标签中打开", action: () => openSessionTerminal(provider.id, "new-tab") },
      { label: "当前标签右侧窗格", action: () => openSessionTerminal(provider.id, "split-right") },
      { label: "当前标签下方窗格", action: () => openSessionTerminal(provider.id, "split-down") },
    );
  }
  items.push(
    { separator: true },
    { label: "删除会话", danger: true, action: () => deleteSession(provider) },
  );
  showContextMenu(event.clientX, event.clientY, items);
}

async function openSession(side, providerID, path = "") {
  let provider = providerByID(providerID);
  let justConnected = false;
  if (provider && provider.kind !== "local" && !provider.connected) {
    if (!await connectSavedSession(providerID)) return;
    justConnected = true;
    await loadProviders(false);
    provider = providerByID(providerID);
  }
  const initialPath = path || (provider?.kind === "sftp" ? provider.location || "/" : "/");
  const panel = state.panels[side];
  let tab = panel.tabs.find((item) => item.provider === providerID);
  if (!tab) {
    tab = { provider: providerID, path: initialPath };
    panel.tabs.push(tab);
  } else if (justConnected && !path && provider?.kind === "sftp") {
    tab.path = initialPath;
  }
  panel.active = providerID;
  panel.selected = null;
  setActivePane(side);
  renderTabs(side);
  saveWorkspace();
  await loadPanel(side);
}

async function connectSavedSession(providerID, fingerprint = "") {
  try {
    await api(`/api/v1/connections/${encodeURIComponent(providerID)}`, {
      method: "POST", body: JSON.stringify({ fingerprint }),
    });
    return true;
  } catch (error) {
    if (error.payload?.code === "HOST_KEY_UNKNOWN") {
      const received = error.payload.fingerprint;
      if (confirm(`核对服务器主机指纹：\n\n${received}\n\n确认一致并连接？`)) return connectSavedSession(providerID, received);
      return false;
    }
    if (error.payload?.code === "HOST_KEY_CHANGED") {
      const expected = error.payload.expected;
      const received = error.payload.received;
      const confirmed = confirm(`警告：服务器主机密钥已变化！\n\n原指纹：\n${expected}\n\n新指纹：\n${received}\n\n这可能是服务器重装、密钥轮换，也可能是中间人攻击。请先通过服务器控制台或管理员核对新指纹。\n\n确认新指纹可信并替换保存值？`);
      if (confirmed) return connectSavedSession(providerID, received);
      return false;
    }
    toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
    return false;
  }
}

function renderTabs(side) {
  const panel = state.panels[side];
  const container = panelElements(side).tabs;
  container.replaceChildren();
  for (const tab of panel.tabs) {
    const provider = providerByID(tab.provider);
    if (!provider) continue;
    const node = document.createElement("div");
    node.className = `tab${panel.active === tab.provider ? " active" : ""}`;
    node.draggable = true;
    node.dataset.provider = tab.provider;
    const pinned = side === "left" && provider.id === "local";
    node.innerHTML = `<span class="tab-icon">${sessionIcon(provider)}</span><span class="tab-name">${escapeHTML(provider.name)}</span>${pinned ? "" : '<span class="tab-close" aria-label="关闭"><span class="material-symbols-rounded" aria-hidden="true">close</span></span>'}`;
    node.addEventListener("click", (event) => {
      if (event.target.closest(".tab-close")) return;
      panel.active = tab.provider; setActivePane(side); renderTabs(side); saveWorkspace(); loadPanel(side);
    });
    $(".tab-close", node)?.addEventListener("click", (event) => { event.stopPropagation(); closeTab(side, tab.provider); });
    node.addEventListener("dragstart", (event) => {
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData(DND_TAB, JSON.stringify({ side, provider: tab.provider }));
    });
    container.append(node);
  }
  const addLocal = document.createElement("button");
  addLocal.className = "new-local-tab";
  addLocal.title = "新建本地标签";
  addLocal.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">add</span>';
  addLocal.addEventListener("click", () => showLocalTree(side, addLocal));
  container.append(addLocal);
}

function closeTab(side, providerID) {
  const panel = state.panels[side];
  const provider = providerByID(providerID);
  if ((side === "left" && provider?.id === "local") || panel.tabs.length <= 1) return;
  const index = panel.tabs.findIndex((tab) => tab.provider === providerID);
  panel.tabs.splice(index, 1);
  if (panel.active === providerID) panel.active = panel.tabs[Math.max(0, index - 1)].provider;
  renderTabs(side); saveWorkspace(); loadPanel(side); renderSessionTree();
}

function moveTab(fromSide, toSide, providerID) {
  if (fromSide === toSide) return openSession(toSide, providerID);
  const source = state.panels[fromSide];
  const tab = source.tabs.find((item) => item.provider === providerID);
  if (!tab) return;
  openSession(toSide, providerID, tab.path);
  const provider = providerByID(providerID);
  const pinned = fromSide === "left" && provider?.id === "local";
  if (!pinned && source.tabs.length > 1) {
    source.tabs = source.tabs.filter((item) => item.provider !== providerID);
    if (source.active === providerID) source.active = source.tabs[0].provider;
    renderTabs(fromSide); loadPanel(fromSide);
  }
  saveWorkspace();
}

async function loadPanel(side, allowReconnect = true) {
  const panel = state.panels[side];
  const tab = currentTab(side);
  const el = panelElements(side);
  if (!tab) return;
  const provider = providerByID(tab.provider);
  if (provider && provider.kind !== "local" && !provider.connected) {
	if (allowReconnect) {
	  el.canvas.innerHTML = '<div class="empty-files">正在连接…</div>';
	  const connected = await connectSavedSession(tab.provider);
	  await loadProviders();
	  renderTabs("left"); renderTabs("right");
	  if (connected) return loadPanel(side, false);
	}
    panel.entries = [];
    panel.selected = null;
    el.path.value = tab.path;
    updateBookmarkControl(side);
    el.canvas.innerHTML = '<div class="empty-files">双击左侧会话以连接</div>';
    el.count.textContent = "未连接";
    el.selection.textContent = "";
    el.root.classList.remove("local-provider");
    return;
  }
  el.path.value = tab.path;
  updateBookmarkControl(side);
  el.canvas.innerHTML = '<div class="empty-files">正在读取…</div>';
  try {
    const result = await api(`/api/v1/files?provider=${encodeURIComponent(tab.provider)}&path=${encodeURIComponent(tab.path)}`);
    tab.path = result.path;
    tab.displayPath = result.display_path || result.path;
    panel.entries = sortEntries(result.entries || [], panel.sort);
    panel.selected = null;
    el.path.value = tab.displayPath;
    updateBookmarkControl(side);
    const provider = providerByID(tab.provider);
    el.root.classList.toggle("local-provider", provider?.kind === "local");
    $(".terminal-button", el.root).disabled = provider?.kind !== "sftp";
    el.count.textContent = `${panel.entries.length.toLocaleString()} 项`;
    el.selection.textContent = "";
    el.viewport.scrollTop = 0;
    el.root.classList.toggle("grid-view", panel.view === "grid");
    el.viewButton.innerHTML = `<span class="material-symbols-rounded" aria-hidden="true">${panel.view === "grid" ? "view_list" : "grid_view"}</span>`;
    updateSortHeaders(side);
    renderPanel(side);
    saveWorkspace();
  } catch (error) {
	if (allowReconnect && error.payload?.code === "CONNECTION_LOST" && provider?.kind !== "local") {
	  el.canvas.innerHTML = '<div class="empty-files">连接已断开，正在重新连接…</div>';
	  const reconnected = await connectSavedSession(tab.provider);
	  await loadProviders();
	  renderTabs("left"); renderTabs("right");
	  if (reconnected) {
		return loadPanel(side, false);
	  }
	}
    el.canvas.innerHTML = `<div class="empty-files">${escapeHTML(error.message)}</div>`;
    toast(error.message, "error");
  }
}

function sortEntries(entries, sort) {
  const multiplier = sort.direction === "desc" ? -1 : 1;
  return [...entries].sort((a, b) => {
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
    let comparison = 0;
    if (sort.field === "size") comparison = Number(a.size || 0) - Number(b.size || 0);
    else if (sort.field === "modified") comparison = new Date(a.modified).getTime() - new Date(b.modified).getTime();
    else comparison = a.name.localeCompare(b.name, "zh-CN", { numeric: true, sensitivity: "base" });
    if (!comparison && sort.field !== "name") comparison = a.name.localeCompare(b.name, "zh-CN", { numeric: true, sensitivity: "base" });
    return comparison * multiplier;
  });
}

function updateSortHeaders(side) {
  const panel = state.panels[side];
  $$(".column-head button[data-sort]", panelElements(side).root).forEach((button) => {
    const active = button.dataset.sort === panel.sort.field;
    button.classList.toggle("active", active);
    button.setAttribute("aria-sort", active ? (panel.sort.direction === "asc" ? "ascending" : "descending") : "none");
    const indicator = $(".sort-indicator", button);
    indicator.textContent = active ? "arrow_upward" : "";
    indicator.classList.toggle("descending", active && panel.sort.direction === "desc");
  });
}

function changeSort(side, field) {
  const panel = state.panels[side];
  if (panel.sort.field === field) panel.sort.direction = panel.sort.direction === "asc" ? "desc" : "asc";
  else panel.sort = { field, direction: field === "name" ? "asc" : "desc" };
  panel.entries = sortEntries(panel.entries, panel.sort);
  panel.selected = null;
  panelElements(side).selection.textContent = "";
  panelElements(side).viewport.scrollTop = 0;
  updateSortHeaders(side);
  renderPanel(side);
  saveWorkspace();
}

function renderPanel(side) {
  const panel = state.panels[side];
  const { viewport, canvas } = panelElements(side);
  const entries = panel.entries;
  if (!entries.length) {
    canvas.style.height = "100%";
    canvas.innerHTML = '<div class="empty-files">空目录</div>';
    return;
  }
  const list = panel.view === "list";
  const cellWidth = list ? Math.max(viewport.clientWidth, 380) : 152;
  const cellHeight = list ? 29 : 138;
  const columns = list ? 1 : Math.max(1, Math.floor(viewport.clientWidth / cellWidth));
  const rows = Math.ceil(entries.length / columns);
  const startRow = Math.max(0, Math.floor(viewport.scrollTop / cellHeight) - 3);
  const endRow = Math.min(rows, Math.ceil((viewport.scrollTop + viewport.clientHeight) / cellHeight) + 3);
  const start = startRow * columns;
  const end = Math.min(entries.length, endRow * columns);
  const width = list ? viewport.clientWidth : cellWidth;
  canvas.style.height = `${Math.max(rows * cellHeight, viewport.clientHeight)}px`;
  canvas.replaceChildren();
  for (let index = start; index < end; index++) {
    const entry = entries[index];
    const row = Math.floor(index / columns);
    const column = index % columns;
    const node = document.createElement("div");
    node.className = `file-entry ${panel.view}${panel.selected?.path === entry.path ? " selected" : ""}`;
    node.style.transform = `translate(${column * width}px, ${row * cellHeight}px)`;
    node.style.width = `${list ? viewport.clientWidth : width - 6}px`;
    node.draggable = true;
    node.title = entry.path;
    const visual = fileVisual(entry, side, panel.view);
    node.dataset.index = String(index);
    node.innerHTML = `<span class="file-name"><i class="file-icon${visual.preview ? " image-preview" : ""}">${visual.html}</i><b class="file-label">${escapeHTML(entry.name)}</b></span><span class="file-size">${entry.is_dir ? "" : formatBytes(entry.size)}</span><span class="file-time">${formatTime(entry.modified)}</span><span class="file-mode">${escapeHTML(entry.mode)}</span>`;
    node.addEventListener("click", () => selectEntry(side, index));
    node.addEventListener("dblclick", () => openEntry(side, index));
    node.addEventListener("dragstart", (event) => {
      selectEntry(side, index);
      node.classList.add("dragging");
      event.dataTransfer.effectAllowed = "copy";
      event.dataTransfer.setData(DND_FILE, JSON.stringify({ side, index }));
    });
    node.addEventListener("dragend", () => { node.classList.remove("dragging"); clearDropTargets(); });
    node.addEventListener("contextmenu", (event) => {
      event.preventDefault(); event.stopPropagation(); selectEntry(side, index); showFileMenu(event, side, entry);
    });
    canvas.append(node);
  }
}

function queueRender(side) {
  const panel = state.panels[side];
  if (panel.renderQueued) return;
  panel.renderQueued = true;
  requestAnimationFrame(() => { panel.renderQueued = false; renderPanel(side); });
}

function selectEntry(side, index) {
  const panel = state.panels[side];
  panel.selected = panel.entries[index];
  panelElements(side).selection.textContent = panel.selected.name;
  setActivePane(side);
  renderPanel(side);
}

async function openEntry(side, index) {
  const entry = state.panels[side].entries[index];
  if (entry.is_dir) {
    currentTab(side).path = entry.path;
    await loadPanel(side);
  } else {
    await openFile(side, entry);
  }
}

function fileExtension(name) { return name.includes(".") ? name.split(".").pop().toLowerCase() : ""; }
function isImageEntry(entry) { return ["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif"].includes(fileExtension(entry.name)); }
function isTextEntry(entry) { return ["txt", "md", "log", "conf", "ini", "yaml", "yml", "json", "xml", "csv", "sh", "ps1", "py", "go", "rs", "js", "ts", "css", "html", "c", "cpp", "h"].includes(fileExtension(entry.name)); }
function isMediaEntry(entry) { return ["mp4", "m4v", "m3u8", "m3u"].includes(fileExtension(entry.name)); }

async function openFile(side, entry) {
  if (isImageEntry(entry)) { openImage(side, entry); return; }
  if (isMediaEntry(entry)) { openMedia(side, entry); return; }
  await openEditor(side, entry);
}

function setActivePane(side) {
  state.activePane = side;
  for (const item of ["left", "right"]) panelElements(item).root.classList.toggle("active-pane", item === side);
  renderSessionTree();
}

function parentPath(value) {
  const parts = value.split("/").filter(Boolean); parts.pop();
  return `/${parts.join("/")}` || "/";
}
function joinPath(parent, name) { return `${parent === "/" ? "" : parent}/${name}`; }

async function transferEntry(fromSide, toSide, entry) {
  if (!entry) { toast("请选择文件", "error"); return; }
  const source = currentTab(fromSide);
  const target = currentTab(toSide);
  if (!source || !target) return;
  const targetPath = joinPath(target.path, entry.name);
  if (source.provider === target.provider && entry.path === targetPath) { toast("源文件和目标位置相同", "error"); return; }
  try {
    await api("/api/v1/transfers", {
      method: "POST",
      body: JSON.stringify({ source_provider: source.provider, source_path: entry.path, target_provider: target.provider, target_path: targetPath, concurrency: 4 }),
    });
    $("#transferQueue").classList.remove("collapsed");
    state.taskFilter = "queue";
    renderTaskList();
    toast(`开始传输 ${entry.name}`);
  } catch (error) { toast(error.message, "error"); }
}

async function deleteEntry(side, entry) {
  if (!confirm(`确定删除“${entry.name}”？${entry.is_dir ? "\n目录及其内容会一起删除。" : ""}`)) return;
  try {
    await api("/api/v1/files", { method: "DELETE", body: JSON.stringify({ provider: currentProvider(side), path: entry.path }) });
    await loadPanel(side); toast("已删除");
  } catch (error) { toast(error.message, "error"); }
}

async function createDirectory(side) {
  const name = prompt("目录名称"); if (!name) return;
  try {
    await api("/api/v1/files/mkdir", { method: "POST", body: JSON.stringify({ provider: currentProvider(side), path: joinPath(currentPath(side), name) }) });
    await loadPanel(side);
  } catch (error) { toast(error.message, "error"); }
}

async function createFile(side) {
  const name = prompt("文件名称"); if (!name) return;
  const filePath = joinPath(currentPath(side), name);
  try {
    await api("/api/v1/files/create", { method: "POST", body: JSON.stringify({ provider: currentProvider(side), path: filePath }) });
    await loadPanel(side);
    const entry = state.panels[side].entries.find((item) => item.path === filePath);
    if (entry) openEditor(side, entry);
  } catch (error) { toast(error.message, "error"); }
}

async function copyText(text, successMessage) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      throw new Error("Clipboard API unavailable");
    }
  } catch (_) {
    const field = document.createElement("textarea");
    field.value = text;
    field.readOnly = true;
    field.style.position = "fixed";
    field.style.opacity = "0";
    document.body.append(field);
    field.select();
    const copied = document.execCommand("copy");
    field.remove();
    if (!copied) { toast("无法写入剪贴板", "error"); return; }
  }
  toast(successMessage);
}

function entryDisplayPath(side, entry) {
  const provider = providerByID(currentProvider(side));
  if (provider?.kind !== "local") return entry.path;
  const separator = provider.location.includes("\\") ? "\\" : "/";
  const root = provider.location.replace(/[\\/]+$/, "");
  const relative = entry.path.replace(/^[\\/]+/, "").replaceAll("/", separator);
  return relative ? `${root}${separator}${relative}` : root;
}

async function copyEntryURL(side, entry) {
  const provider = providerByID(currentProvider(side));
  if (!provider || !["ftp", "sftp"].includes(provider.kind)) return;
  try {
    const details = await api(`/api/v1/sessions/${encodeURIComponent(provider.id)}`);
    const host = details.host.includes(":") && !details.host.startsWith("[") ? `[${details.host}]` : details.host;
    const user = encodeURIComponent(details.user || (details.protocol === "ftp" ? "anonymous" : ""));
    const filePath = `/${entry.path.split("/").filter(Boolean).map(encodeURIComponent).join("/")}`;
    const url = `${details.protocol}://${user}@${host}:${details.port}${filePath}`;
    await copyText(url, "URL 已复制");
  } catch (error) { toast(error.message, "error"); }
}

function showFileMenu(event, side, entry) {
  const provider = providerByID(currentProvider(side));
  const items = [
    { label: side === "left" ? "上传到右侧" : "下载到左侧", action: () => transferEntry(side, side === "left" ? "right" : "left", entry) },
    { label: "打开", action: () => openEntry(side, state.panels[side].entries.indexOf(entry)) },
    { separator: true },
    { label: "复制路径", action: () => copyText(entryDisplayPath(side, entry), "路径已复制") },
  ];
  if (["ftp", "sftp"].includes(provider?.kind)) items.push({ label: "复制 URL", action: () => copyEntryURL(side, entry) });
  items.push(
    { separator: true },
    { label: "删除", danger: true, action: () => deleteEntry(side, entry) },
    { separator: true },
    { label: "新增目录", action: () => createDirectory(side) },
    { label: "新增文件", action: () => createFile(side) },
  );
  showContextMenu(event.clientX, event.clientY, items);
}

function showBlankMenu(event, side) {
  showContextMenu(event.clientX, event.clientY, [
    { label: "新增目录", action: () => createDirectory(side) },
    { label: "新增文件", action: () => createFile(side) },
  ]);
}

function showContextMenu(x, y, items) {
  const menu = $("#contextMenu"); menu.replaceChildren();
  for (const item of items) {
    if (item.separator) { menu.append(document.createElement("hr")); continue; }
    if (item.heading) {
      const heading = document.createElement("span");
      heading.className = "menu-heading";
      heading.textContent = item.heading;
      menu.append(heading);
      continue;
    }
    const button = document.createElement("button");
    button.textContent = item.label;
    if (item.danger) button.className = "danger";
    button.addEventListener("click", () => { hideContextMenu(); item.action(); });
    menu.append(button);
  }
  menu.classList.add("open");
  const width = menu.offsetWidth, height = menu.offsetHeight;
  menu.style.left = `${Math.min(x, window.innerWidth - width - 6)}px`;
  menu.style.top = `${Math.min(y, window.innerHeight - height - 6)}px`;
}
function hideContextMenu() { $("#contextMenu").classList.remove("open"); }

function swapPanels() {
  const left = state.panels.left;
  state.panels.left = state.panels.right;
  state.panels.right = left;
  state.panels.left.renderQueued = false;
  state.panels.right.renderQueued = false;
  const local = providerByID("local") || state.providers.find((provider) => provider.kind === "local");
  if (local && !state.panels.left.tabs.some((tab) => tab.provider === local.id)) state.panels.left.tabs.unshift({ provider: local.id, path: "/" });
  renderTabs("left"); renderTabs("right"); saveWorkspace();
  Promise.all([loadPanel("left"), loadPanel("right")]);
}

function bindDrops(side) {
  const el = panelElements(side);
  el.tabs.addEventListener("dragover", (event) => {
    if (hasType(event, DND_SESSION) || hasType(event, DND_TAB)) { event.preventDefault(); el.tabs.classList.add("drop-target"); }
  });
  el.tabs.addEventListener("dragleave", () => el.tabs.classList.remove("drop-target"));
  el.tabs.addEventListener("drop", (event) => {
    event.preventDefault(); event.stopPropagation(); el.tabs.classList.remove("drop-target");
    const sessionID = event.dataTransfer.getData(DND_SESSION);
    if (sessionID) return openSession(side, sessionID);
    const value = event.dataTransfer.getData(DND_TAB);
    if (value) { const tab = JSON.parse(value); moveTab(tab.side, side, tab.provider); }
  });
  el.root.addEventListener("dragover", (event) => {
    if ([DND_FILE, DND_SESSION, DND_TAB].some((type) => hasType(event, type))) {
      event.preventDefault();
      if (hasType(event, DND_FILE)) el.root.classList.add("drop-target");
      el.mask.textContent = "释放以传输到这里";
    }
  });
  el.root.addEventListener("dragleave", (event) => { if (!el.root.contains(event.relatedTarget)) el.root.classList.remove("drop-target"); });
  el.root.addEventListener("drop", (event) => {
    event.preventDefault(); clearDropTargets();
    const fileData = event.dataTransfer.getData(DND_FILE);
    if (fileData) {
      const source = JSON.parse(fileData);
      if (source.side !== side) transferEntry(source.side, side, state.panels[source.side].entries[source.index]);
      return;
    }
    const sessionID = event.dataTransfer.getData(DND_SESSION);
    if (sessionID) { openSession(side, sessionID); return; }
    const tabValue = event.dataTransfer.getData(DND_TAB);
    if (tabValue) { const tab = JSON.parse(tabValue); moveTab(tab.side, side, tab.provider); }
  });
}

function hasType(event, type) { return [...event.dataTransfer.types].includes(type); }
function clearDropTargets() { $$(".drop-target").forEach((node) => node.classList.remove("drop-target")); }

async function openEditor(side, entry) {
	if (state.editor?.saving) { toast("文件正在保存，请稍候"); return; }
  if (state.editor?.dirty && !confirm("当前文件有未保存的修改，确定放弃并打开其他文件？")) return;
  const tab = currentTab(side);
  state.editor = null;
  closeEditorFind();
  $("#editorTitle").textContent = entry.path;
	$("#editorTitle").title = entry.path;
	$("#editorDirty").classList.add("hidden");
  $("#editorContent").value = "";
  $("#editorHighlight").textContent = "";
	$("#editorLineNumbers").textContent = "1";
	$("#editorPosition").textContent = "Ln 1, Col 1";
	$("#editorFormat").textContent = "UTF-8";
  $("#editorState").textContent = "正在读取…";
  $("#saveEditor").disabled = true;
  if (!$("#editorDialog").open) $("#editorDialog").showModal();
  try {
    const result = await api(`/api/v1/files/content?provider=${encodeURIComponent(tab.provider)}&path=${encodeURIComponent(entry.path)}`);
    state.editor = {
	  provider: tab.provider, path: entry.path, etag: result.etag, side, language: syntaxForPath(entry.path),
	  originalContent: result.content, dirty: false, saving: false, encoding: result.encoding || "utf-8",
	  bom: Boolean(result.bom), newline: result.newline || "lf", mixedNewlines: Boolean(result.mixed_newlines),
	  size: result.size ?? new TextEncoder().encode(result.content).length, lineCount: 0, matchCase: false,
	};
    $("#editorTitle").textContent = entry.path;
	$("#editorTitle").title = entry.path;
    $("#editorContent").value = result.content;
	$("#syntaxMode").value = "auto";
	refreshEditorDisplay();
    $("#editorContent").focus();
  } catch (error) {
    $("#editorState").textContent = `读取失败：${error.message}`;
    toast(error.message, "error");
  }
}

function syntaxForPath(filePath) {
  const extension = fileExtension(filePath);
  const mapping = {
    json: "json", jsonl: "json", yaml: "yaml", yml: "yaml", md: "markdown", markdown: "markdown",
    js: "javascript", jsx: "javascript", ts: "javascript", tsx: "javascript", mjs: "javascript",
    py: "python", go: "go", rs: "rust", sh: "shell", bash: "shell", zsh: "shell", ps1: "shell",
    html: "markup", htm: "markup", xml: "markup", svg: "markup", css: "css", scss: "css",
    sql: "sql", c: "c", h: "c", cpp: "c", cc: "c", hpp: "c",
  };
  return mapping[extension] || "plain";
}

const syntaxKeywords = {
  json: new Set(["true", "false", "null"]),
  yaml: new Set(["true", "false", "null", "yes", "no", "on", "off"]),
  javascript: new Set(["async", "await", "break", "case", "catch", "class", "const", "continue", "default", "delete", "do", "else", "export", "extends", "false", "finally", "for", "from", "function", "if", "import", "in", "instanceof", "let", "new", "null", "of", "return", "static", "super", "switch", "this", "throw", "true", "try", "typeof", "undefined", "var", "while", "yield"]),
  python: new Set(["and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "False", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "None", "nonlocal", "not", "or", "pass", "raise", "return", "True", "try", "while", "with", "yield"]),
  go: new Set(["break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var"]),
  rust: new Set(["as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while"]),
  shell: new Set(["case", "do", "done", "elif", "else", "esac", "fi", "for", "function", "if", "in", "select", "then", "time", "until", "while"]),
  css: new Set(["@media", "@import", "@supports", "important"]),
  sql: new Set(["alter", "and", "as", "asc", "begin", "between", "by", "case", "create", "delete", "desc", "distinct", "drop", "else", "end", "exists", "from", "group", "having", "in", "index", "inner", "insert", "into", "is", "join", "left", "like", "limit", "not", "null", "on", "or", "order", "outer", "right", "select", "set", "table", "then", "union", "update", "values", "when", "where"]),
  c: new Set(["auto", "break", "case", "char", "class", "const", "continue", "default", "delete", "do", "double", "else", "enum", "extern", "false", "float", "for", "friend", "if", "inline", "int", "long", "namespace", "new", "nullptr", "private", "protected", "public", "return", "short", "signed", "sizeof", "static", "struct", "switch", "template", "this", "throw", "true", "try", "typedef", "typename", "union", "unsigned", "using", "virtual", "void", "volatile", "while"]),
};

function refreshEditorHighlight() {
  const editor = $("#editorContent");
  const highlight = $("#editorHighlight");
  const selected = $("#syntaxMode").value;
  const language = selected === "auto" ? (state.editor?.language || "plain") : selected;
  try {
    highlight.innerHTML = highlightSource(editor.value, language) + "\n";
    editor.classList.add("syntax-overlay");
  } catch (error) {
    highlight.textContent = "";
    editor.classList.remove("syntax-overlay");
  }
  syncEditorScroll();
}

function refreshEditorDisplay() {
	refreshEditorHighlight();
	refreshEditorLineNumbers();
	updateEditorStatus();
	refreshFindMatches();
}

function refreshEditorLineNumbers() {
	const count = $("#editorContent").value.split("\n").length;
	if (state.editor?.lineCount === count) return;
	if (state.editor) state.editor.lineCount = count;
	$("#editorLineNumbers").textContent = Array.from({ length: count }, (_, index) => index + 1).join("\n");
}

function newlineLabel(value) {
	return ({ crlf: "CRLF", cr: "CR", none: "无换行" })[value] || "LF";
}

function currentEditorByteSize() {
	if (!state.editor) return 0;
	const value = $("#editorContent").value;
	let size = new TextEncoder().encode(value).length + (state.editor.bom ? 3 : 0);
	if (state.editor.newline === "crlf") size += (value.match(/\n/g) || []).length;
	return size;
}

function updateEditorStatus() {
	if (!state.editor) return;
	const editor = $("#editorContent"), before = editor.value.slice(0, editor.selectionStart);
	const lastNewline = before.lastIndexOf("\n");
	const line = (before.match(/\n/g) || []).length + 1;
	const column = editor.selectionStart - lastNewline;
	const selection = editor.selectionEnd - editor.selectionStart;
	$("#editorPosition").textContent = `Ln ${line}, Col ${column}${selection ? ` (${selection})` : ""}`;
	const encoding = state.editor.bom ? "UTF-8 BOM" : "UTF-8";
	const mixed = state.editor.mixedNewlines ? " · 混合" : "";
	$("#editorFormat").textContent = `${encoding} · ${newlineLabel(state.editor.newline)}${mixed} · ${formatBytes(currentEditorByteSize())}`;
	$("#editorState").textContent = state.editor.saving ? "正在保存…" : (state.editor.dirty ? "已修改" : "未修改");
	$("#editorDirty").classList.toggle("hidden", !state.editor.dirty);
	$("#saveEditor").disabled = state.editor.saving || !state.editor.dirty;
}

function editorChanged() {
	if (!state.editor) return;
	state.editor.dirty = $("#editorContent").value !== state.editor.originalContent;
	refreshEditorDisplay();
}

function highlightSource(text, language) {
  if (language === "plain") return escapeHTML(text);
  const patterns = [];
  if (language === "markup") patterns.push("<!--[\\s\\S]*?-->", "<\\/?[A-Za-z][^>]*>");
  if (language === "markdown") patterns.push("^#{1,6}[^\\n]*", "`[^`\\n]+`");
  if (["javascript", "go", "rust", "c", "css"].includes(language)) patterns.push("\\/\\*[\\s\\S]*?\\*\\/", "\\/\\/[^\\n]*");
  if (["python", "shell", "yaml"].includes(language)) patterns.push("#[^\\n]*");
  if (language === "sql") patterns.push("--[^\\n]*", "\\/\\*[\\s\\S]*?\\*\\/");
  patterns.push('"(?:\\\\.|[^"\\\\])*"', "'(?:\\\\.|[^'\\\\])*'", "`(?:\\\\.|[^`\\\\])*`", "\\b(?:0x[\\da-fA-F]+|\\d+(?:\\.\\d+)?)\\b", "\\b[A-Za-z_$][\\w$]*\\b");
  const matcher = new RegExp(patterns.map((part) => `(${part})`).join("|"), "gm");
  const keywords = syntaxKeywords[language] || new Set();
  let output = "", cursor = 0, match;
  while ((match = matcher.exec(text))) {
    output += escapeHTML(text.slice(cursor, match.index));
    const value = match[0];
    let token = "";
    if (value.startsWith("//") || value.startsWith("/*") || value.startsWith("<!--") || value.startsWith("#") || value.startsWith("--")) token = language === "markdown" && value.startsWith("#") ? "heading" : "comment";
    else if (value.startsWith("<")) token = "tag";
    else if (["\"", "'", "`"].includes(value[0])) token = language === "json" && /^\s*:/.test(text.slice(matcher.lastIndex)) ? "property" : "string";
    else if (/^(?:0x[\da-f]+|\d)/i.test(value)) token = "number";
    else if (keywords.has(language === "sql" ? value.toLowerCase() : value)) token = "keyword";
    output += token ? `<span class="tok-${token}">${escapeHTML(value)}</span>` : escapeHTML(value);
    cursor = matcher.lastIndex;
  }
  return output + escapeHTML(text.slice(cursor));
}

function syncEditorScroll() {
  const editor = $("#editorContent"), highlight = $("#editorHighlight");
  highlight.scrollTop = editor.scrollTop;
  highlight.scrollLeft = editor.scrollLeft;
	$("#editorLineNumbers").scrollTop = editor.scrollTop;
}

function showEditorFind(replace = false) {
	const bar = $("#editorFindBar"), find = $("#editorFind"), selected = $("#editorContent").value.slice($("#editorContent").selectionStart, $("#editorContent").selectionEnd);
	bar.classList.remove("hidden");
	for (const id of ["#editorReplace", "#editorReplaceOne", "#editorReplaceAll"]) $(id).classList.toggle("hidden", !replace);
	if (selected && !selected.includes("\n") && selected.length <= 200) find.value = selected;
	find.focus(); find.select();
	refreshFindMatches();
}

function closeEditorFind() {
	$("#editorFindBar").classList.add("hidden");
	if (state.editor) $("#editorContent").focus();
}

function editorMatches() {
	const needle = $("#editorFind").value;
	if (!needle) return [];
	const original = $("#editorContent").value;
	const source = state.editor?.matchCase ? original : original.toLocaleLowerCase();
	const query = state.editor?.matchCase ? needle : needle.toLocaleLowerCase();
	const matches = [];
	for (let index = source.indexOf(query); index >= 0; index = source.indexOf(query, index + Math.max(1, query.length))) {
		matches.push({ start: index, end: index + needle.length });
	}
	return matches;
}

function refreshFindMatches(activeIndex = -1) {
	if ($("#editorFindBar").classList.contains("hidden")) return;
	const matches = editorMatches(), editor = $("#editorContent");
	if (activeIndex < 0) activeIndex = matches.findIndex((match) => match.start === editor.selectionStart && match.end === editor.selectionEnd);
	$("#editorFindCount").textContent = matches.length ? `${Math.max(1, activeIndex + 1)}/${matches.length}` : "0/0";
	return matches;
}

function scrollEditorSelectionIntoView() {
	const editor = $("#editorContent"), lineHeight = parseFloat(getComputedStyle(editor).lineHeight) || 20;
	const line = (editor.value.slice(0, editor.selectionStart).match(/\n/g) || []).length;
	editor.scrollTop = Math.max(0, line * lineHeight - editor.clientHeight / 2);
	syncEditorScroll();
}

function findEditorMatch(direction = 1) {
	const editor = $("#editorContent"), matches = editorMatches();
	if (!matches.length) { refreshFindMatches(); return; }
	let index = matches.findIndex((match) => match.start === editor.selectionStart && match.end === editor.selectionEnd);
	if (index >= 0) index = (index + direction + matches.length) % matches.length;
	else if (direction > 0) {
		index = matches.findIndex((match) => match.start >= editor.selectionEnd);
		if (index < 0) index = 0;
	} else {
		index = -1;
		for (let candidate = matches.length - 1; candidate >= 0; candidate--) {
			if (matches[candidate].end <= editor.selectionStart) { index = candidate; break; }
		}
		if (index < 0) index = matches.length - 1;
	}
	editor.focus(); editor.setSelectionRange(matches[index].start, matches[index].end);
	scrollEditorSelectionIntoView(); refreshFindMatches(index); updateEditorStatus();
}

function replaceEditorMatch() {
	const editor = $("#editorContent"), needle = $("#editorFind").value;
	if (!needle) return;
	const selected = editor.value.slice(editor.selectionStart, editor.selectionEnd);
	const equal = state.editor?.matchCase ? selected === needle : selected.toLocaleLowerCase() === needle.toLocaleLowerCase();
	if (!equal) { findEditorMatch(1); return; }
	editor.setRangeText($("#editorReplace").value, editor.selectionStart, editor.selectionEnd, "end");
	editorChanged(); findEditorMatch(1);
}

function replaceAllEditorMatches() {
	const matches = editorMatches();
	if (!matches.length) return;
	const editor = $("#editorContent"), replacement = $("#editorReplace").value;
	let value = editor.value;
	for (let index = matches.length - 1; index >= 0; index--) value = value.slice(0, matches[index].start) + replacement + value.slice(matches[index].end);
	editor.setRangeText(value, 0, editor.value.length, "end");
	editorChanged();
	toast(`已替换 ${matches.length} 处`);
}

function jumpToEditorLine() {
	if (!state.editor) return;
	const editor = $("#editorContent"), before = editor.value.slice(0, editor.selectionStart);
	const current = (before.match(/\n/g) || []).length + 1;
	const input = prompt("跳转到行，可输入 行:列", String(current));
	if (input === null) return;
	const match = input.trim().match(/^(\d+)(?::(\d+))?$/);
	if (!match) { toast("请输入行号，例如 120 或 120:8", "error"); return; }
	const lines = editor.value.split("\n"), line = Math.min(Math.max(1, Number(match[1])), lines.length), column = Math.min(Math.max(1, Number(match[2] || 1)), lines[line - 1].length + 1);
	let offset = 0;
	for (let index = 0; index < line - 1; index++) offset += lines[index].length + 1;
	offset += column - 1;
	editor.focus(); editor.setSelectionRange(offset, offset); scrollEditorSelectionIntoView(); updateEditorStatus();
}

function requestCloseEditor() {
	if (state.editor?.saving) { toast("文件正在保存，请稍候"); return; }
	if (state.editor?.dirty && !confirm("文件尚未保存，确定不保存并关闭？")) return;
	state.editor = null;
	$("#editorDialog").close();
}

async function saveEditor() {
  if (!state.editor) return;
	if (!state.editor.dirty || state.editor.saving) return;
	const button = $("#saveEditor"), active = state.editor, content = $("#editorContent").value;
	active.saving = true; updateEditorStatus();
  try {
    const result = await api("/api/v1/files/content", {
      method: "PUT",
	  body: JSON.stringify({
		provider: active.provider, path: active.path, etag: active.etag, content,
		encoding: active.encoding, bom: active.bom, newline: active.newline,
	  }),
    });
	active.etag = result.etag;
	active.originalContent = result.content;
	active.dirty = $("#editorContent").value !== result.content;
	active.bom = Boolean(result.bom);
	active.newline = result.newline || active.newline;
	active.mixedNewlines = Boolean(result.mixed_newlines);
	active.size = result.size;
    toast("文件已保存");
	await loadPanel(active.side);
  } catch (error) { toast(error.message, "error"); }
	finally { active.saving = false; if (state.editor === active) updateEditorStatus(); else button.disabled = false; }
}

function toggleDialogMaximized(dialog, button) {
  const maximized = dialog.classList.toggle("maximized");
  button.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">fullscreen</span>';
  button.title = maximized ? "还原窗口" : "窗口内全屏";
  if (dialog === $("#imageDialog") && state.image) requestAnimationFrame(fitImage);
}

function openImage(side, entry) {
  const provider = currentProvider(side);
  const entries = state.panels[side].entries.filter((item) => !item.is_dir && isImageEntry(item));
  const index = Math.max(0, entries.findIndex((item) => item.path === entry.path));
  state.image = { side, provider, entries, index, zoom: 1 };
  const dialog = $("#imageDialog");
  if (!dialog.open) dialog.showModal();
  showImageAt(index);
}

function showImageAt(index) {
  if (!state.image || !state.image.entries.length) return;
  const count = state.image.entries.length;
  state.image.index = (index + count) % count;
  const entry = state.image.entries[state.image.index];
  const image = $("#imagePreview");
  image.removeAttribute("width"); image.removeAttribute("height");
  image.src = `/api/v1/files/raw?provider=${encodeURIComponent(state.image.provider)}&path=${encodeURIComponent(entry.path)}`;
  image.alt = entry.name;
  $("#imageTitle").textContent = entry.path;
  $("#imageTitle").title = entry.path;
  $("#imagePosition").textContent = `${state.image.index + 1} / ${count}　${entry.name}`;
  $("#previousImage").disabled = count < 2;
  $("#nextImage").disabled = count < 2;
}

function fitImage() {
  if (!state.image) return;
  const image = $("#imagePreview"), stage = $("#imageStage");
  if (!image.naturalWidth || !image.naturalHeight) return;
  const zoom = Math.min(1, (stage.clientWidth - 40) / image.naturalWidth, (stage.clientHeight - 40) / image.naturalHeight);
  setImageZoom(Math.max(0.1, zoom));
}

function setImageZoom(value) {
  if (!state.image) return;
  state.image.zoom = Math.min(8, Math.max(0.1, value));
  const image = $("#imagePreview");
  if (image.naturalWidth && image.naturalHeight) {
    image.style.width = `${image.naturalWidth * state.image.zoom}px`;
    image.style.height = `${image.naturalHeight * state.image.zoom}px`;
  }
  $("#zoomLabel").textContent = `${Math.round(state.image.zoom * 100)}%`;
}

function changeImage(step) {
  if (state.image) showImageAt(state.image.index + step);
}

function mediaURL(provider, filePath) {
  return `/api/v1/files/media?provider=${encodeURIComponent(provider)}&path=${encodeURIComponent(filePath)}`;
}

function openMedia(side, entry) {
  closeMedia(false);
  const provider = currentProvider(side);
  const player = $("#mediaPlayer");
  const message = $("#mediaMessage");
  const source = mediaURL(provider, entry.path);
  $("#mediaTitle").textContent = entry.path;
  $("#mediaTitle").title = entry.path;
  $("#mediaState").textContent = "正在加载";
  message.classList.remove("visible");
  message.textContent = "";
  const dialog = $("#mediaDialog");
  if (!dialog.open) dialog.showModal();
  const extension = fileExtension(entry.name);
  if (["m3u8", "m3u"].includes(extension)) {
    if (window.Hls?.isSupported()) {
      const hls = new window.Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 60 });
      state.hls = hls;
      hls.loadSource(source);
      hls.attachMedia(player);
      hls.on(window.Hls.Events.MANIFEST_PARSED, () => {
        $("#mediaState").textContent = "HLS · 已就绪";
        player.play().catch(() => { $("#mediaState").textContent = "HLS · 点击播放"; });
      });
      hls.on(window.Hls.Events.ERROR, (_event, data) => {
        if (!data.fatal) return;
        const detail = `${data.type || "HLS"} / ${data.details || "未知错误"}`;
        showMediaError(`M3U8 播放失败：${detail}`);
        reportClientLog("error", "media", "M3U8 播放失败", `${entry.path} · ${detail}`);
        if (data.type === window.Hls.ErrorTypes.NETWORK_ERROR) hls.startLoad();
        else if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError();
      });
    } else if (player.canPlayType("application/vnd.apple.mpegurl")) {
      player.src = source;
      player.play().catch(() => { $("#mediaState").textContent = "HLS · 点击播放"; });
    } else {
      showMediaError("当前浏览器不支持 HLS/M3U8 播放");
    }
  } else {
    player.src = source;
    player.play().catch(() => { $("#mediaState").textContent = "MP4 · 点击播放"; });
  }
}

function showMediaError(message) {
  $("#mediaState").textContent = "播放失败";
  $("#mediaMessage").textContent = message;
  $("#mediaMessage").classList.add("visible");
  toast(message, "error");
}

function closeMedia(closeDialog = true) {
  if (state.hls) {
    state.hls.destroy();
    state.hls = null;
  }
  const player = $("#mediaPlayer");
  player.pause();
  player.removeAttribute("src");
  player.load();
  if (closeDialog && $("#mediaDialog").open) $("#mediaDialog").close();
}

async function reportClientLog(level, category, message, detail) {
  try {
    await api("/api/v1/logs", { method: "POST", body: JSON.stringify({ level, category, message, detail }) });
  } catch (_) { /* the visible player error remains available */ }
}

function refreshTasks(tasks) {
  const sampledAt = Date.now();
  for (const task of tasks) updateTransferMetric(task, sampledAt);
  state.tasks = tasks;
  const queue = tasks.filter((task) => taskCategory(task) === "queue");
  const success = tasks.filter((task) => taskCategory(task) === "success");
  const failed = tasks.filter((task) => taskCategory(task) === "failed");
  const active = queue.filter((task) => ["running", "verifying"].includes(task.status)).length;
  if (active > 0) {
    $("#transferQueue").classList.remove("collapsed");
    state.taskFilter = "queue";
  }
  $("#queueCount").textContent = queue.length;
  $("#successCount").textContent = success.length;
  $("#failedCount").textContent = failed.length;
  for (const task of tasks) {
    const previous = state.taskStatus.get(task.id);
    if (task.status === "completed" && previous && previous !== "completed") {
      for (const side of ["left", "right"]) if (currentProvider(side) === task.target_provider) loadPanel(side);
    }
    state.taskStatus.set(task.id, task.status);
  }
  renderTaskList();
}

function updateTransferMetric(task, sampledAt) {
  const bytes = Math.max(task.bytes_verified || 0, task.bytes_transferred || 0);
  const previous = state.transferMetrics.get(task.id);
  let speed = previous?.speed || 0;
  if (["running", "verifying"].includes(task.status)) {
    if (previous && bytes > previous.bytes && sampledAt > previous.sampledAt) {
      const instantaneous = (bytes - previous.bytes) * 1000 / (sampledAt - previous.sampledAt);
      speed = speed > 0 ? speed * 0.65 + instantaneous * 0.35 : instantaneous;
    } else if (!previous && bytes > 0 && task.status === "running") {
      const elapsed = Math.max(1, (sampledAt - new Date(task.created_at).getTime()) / 1000);
      speed = bytes / elapsed;
    }
  } else if (task.status === "completed") {
    const duration = Math.max(1, (new Date(task.updated_at).getTime() - new Date(task.created_at).getTime()) / 1000);
    speed = task.size / duration;
  } else {
    speed = 0;
  }
  const progressSampledAt = previous && bytes === previous.bytes ? previous.sampledAt : sampledAt;
  state.transferMetrics.set(task.id, { bytes, sampledAt: progressSampledAt, speed });
}

function taskCategory(task) {
  if (task.status === "completed") return "success";
  if (task.status === "failed") return "failed";
  return "queue";
}

function renderTaskList() {
  $$(".task-tab").forEach((tab) => tab.classList.toggle("active", tab.dataset.taskFilter === state.taskFilter));
  const list = $("#taskList");
	const clearButton = $("#clearTaskHistory");
	clearButton.hidden = !["success", "failed", "logs"].includes(state.taskFilter);
	clearButton.textContent = state.taskFilter === "logs" ? "清空日志" : "清空";
	if (state.taskFilter === "logs") {
		$("#taskSummary").textContent = state.logs.length ? `最近 ${state.logs.length} 条操作记录` : "暂无日志";
		renderLogList();
		return;
	}
  const tasks = state.tasks.filter((task) => taskCategory(task) === state.taskFilter);
	const active = state.tasks.filter((task) => ["running", "verifying"].includes(task.status)).length;
	$("#taskSummary").textContent = state.tasks.length ? `${active} 进行中 · ${state.tasks.length} 总计` : "暂无任务";
  if (!tasks.length) {
    const messages = { queue: "队列中没有任务", success: "还没有成功任务", failed: "还没有失败任务" };
    list.innerHTML = `<div class="empty-row">${messages[state.taskFilter]}</div>`;
    return;
  }
  list.replaceChildren(...[...tasks].sort((a, b) => b.created_at.localeCompare(a.created_at)).map((task) => {
    const transferred = Math.max(task.bytes_verified || 0, task.bytes_transferred || 0);
    const transferPercent = task.size ? Math.min(100, transferred / task.size * 100) : 100;
    const verifiedPercent = task.size ? Math.min(100, task.bytes_verified / task.size * 100) : 100;
    const row = document.createElement("div");
    row.className = "task";
    const metric = state.transferMetrics.get(task.id) || { speed: 0 };
    const terminalTime = ["completed", "failed"].includes(task.status) ? new Date(task.updated_at).getTime() : Date.now();
    const elapsedSeconds = Math.max(0, (terminalTime - new Date(task.created_at).getTime()) / 1000);
    const remainingSeconds = metric.speed > 0 && ["running", "verifying"].includes(task.status) ? Math.max(0, (task.size - transferred) / metric.speed) : NaN;
    const direction = task.source_provider === currentProvider("left") && task.target_provider === currentProvider("right") ? "上传" : task.source_provider === currentProvider("right") && task.target_provider === currentProvider("left") ? "下载" : "传输";
    row.innerHTML = `<span class="task-route" title="${escapeHTML(task.source_path)} → ${escapeHTML(task.target_path)}">${direction}　${escapeHTML(task.source_path)} → ${escapeHTML(task.target_path)}</span><span class="progress-track" title="${statusLabel(task.status)} · 已传输 ${transferPercent.toFixed(1)}% · 已校验 ${verifiedPercent.toFixed(1)}%"><i class="progress-transferred" style="width:${transferPercent.toFixed(2)}%"></i><i class="progress-verified" style="width:${verifiedPercent.toFixed(2)}%"></i></span><span class="task-status">${formatBytes(transferred)} / ${formatBytes(task.size)}</span><span class="task-status" title="${statusLabel(task.status)} · 平均速度">${statusLabel(task.status)} ${metric.speed > 0 ? `${formatBytes(metric.speed)}/s` : "--"}</span><span class="task-status" title="预计剩余时间">剩余 ${formatDuration(remainingSeconds)}</span><span class="task-status" title="已经过时间">已用 ${formatDuration(elapsedSeconds)}</span>`;
    row.addEventListener("contextmenu", (event) => { event.preventDefault(); showTaskMenu(event, task); });
    return row;
  }));
}

function formatDuration(seconds) {
  if (!Number.isFinite(seconds)) return "--";
  seconds = Math.max(0, Math.round(seconds));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor(seconds % 3600 / 60);
  const remainder = seconds % 60;
  if (hours > 0) return `${hours}:${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`;
  return `${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`;
}

function refreshLogs(logs) {
  state.logs = logs || [];
	$("#logCount").textContent = state.logs.length || "";
  if (state.taskFilter === "logs") renderTaskList();
}

function renderLogList() {
  const list = $("#taskList");
  if (!state.logs.length) {
	list.innerHTML = '<div class="empty-row">还没有操作日志</div>';
	return;
  }
  list.replaceChildren(...state.logs.map((entry) => {
	const row = document.createElement("div");
	row.className = `log-row ${entry.level}`;
	const level = ({ info: "信息", warning: "警告", error: "错误" })[entry.level] || entry.level;
	row.innerHTML = `<span class="log-time">${formatTime(entry.time)}</span><span class="log-level">${escapeHTML(level)}</span><span class="log-category">${escapeHTML(entry.category)}</span><span class="log-message">${escapeHTML(entry.message)}${entry.detail ? `<small class="log-detail">${escapeHTML(entry.detail)}</small>` : ""}</span>`;
	return row;
  }));
}

function showTaskMenu(event, task) {
  const items = [];
  if (["running", "verifying"].includes(task.status)) items.push({ label: "暂停任务", action: () => taskAction(task.id, "pause") });
  else if (["paused", "failed"].includes(task.status)) items.push({ label: "继续任务", action: () => taskAction(task.id, "resume") });
  items.push({ separator: true }, { label: "删除任务", danger: true, action: () => deleteTask(task.id) });
  showContextMenu(event.clientX, event.clientY, items);
}

async function taskAction(id, action) {
  try { await api(`/api/v1/transfers/${id}/${action}`, { method: "POST", body: "{}" }); }
  catch (error) { toast(error.message, "error"); }
}
async function deleteTask(id) {
  try { await api(`/api/v1/transfers/${id}`, { method: "DELETE", body: "{}" }); }
  catch (error) { toast(error.message, "error"); }
}

async function clearTaskHistory() {
	if (state.taskFilter === "logs") {
		if (!confirm("确定清空全部操作日志？")) return;
		try {
			const result = await api("/api/v1/logs", { method: "DELETE" });
			toast(`已清空 ${result.removed} 条日志`);
		} catch (error) { toast(error.message, "error"); }
		return;
	}
  const status = state.taskFilter === "success" ? "completed" : state.taskFilter === "failed" ? "failed" : "";
  if (!status) return;
  const label = status === "completed" ? "成功" : "失败";
  if (!confirm(`确定清空全部${label}任务记录？`)) return;
  try {
	const result = await api(`/api/v1/transfers?status=${status}`, { method: "DELETE" });
	toast(`已清空 ${result.removed} 条${label}任务`);
  } catch (error) { toast(error.message, "error"); }
}

function statusLabel(status) { return ({ running: "传输", verifying: "校验", paused: "暂停", failed: "失败", completed: "完成" })[status] || status; }

async function saveConnection(form, connectAfterSave) {
  const data = Object.fromEntries(new FormData(form));
  data.port = Number(data.port || (data.protocol === "ftp" ? 21 : 22));
  data.ssh_keep_alive = data.protocol === "sftp" && form.elements.ssh_keep_alive.checked;
  data.terminal_auto_password = data.protocol === "sftp" && form.elements.terminal_auto_password.checked;
  data.server_alive_interval = Number(form.elements.server_alive_interval.value || 60);
  data.server_alive_count_max = Number(form.elements.server_alive_count_max.value || 3);
  if (!data.host.trim()) { toast("主机不能为空", "error"); form.elements.host.focus(); return; }
  if (data.protocol === "sftp" && !data.user.trim()) { toast("SFTP 用户名不能为空", "error"); form.elements.user.focus(); return; }
  try {
    if (data.protocol === "sftp" && data.auth_method === "key" && $("#privateKeyUpload").files[0]) {
      const file = $("#privateKeyUpload").files[0];
      if (file.size > 2 * 1024 * 1024) throw new Error("SSH 私钥文件不能超过 2 MB");
      const imported = await api("/api/v1/sessions/private-key", {
        method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: file,
      });
      data.private_key = imported.path;
    }
    const provider = await api("/api/v1/sessions", { method: "POST", body: JSON.stringify(data) });
    hideSessionGroupMenu();
    $("#connectionDialog").close(); form.reset();
	prepareNewSession(false);
    await loadProviders();
    toast("会话已保存");
    if (connectAfterSave) await openSession("right", provider.id);
  } catch (error) {
    toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
  }
}

function prepareNewSession(show = true) {
	const form = $("#connectionForm");
	hideSessionGroupMenu();
	form.reset();
	form.elements.id.value = "";
	$("#connectionDialogTitle").textContent = "添加会话";
	form.elements.password.placeholder = "";
	form.elements.auth_method.value = "password";
	form.dataset.hasSecret = "false";
	form.dataset.originalAuth = "password";
	$("#privateKeyUpload").value = "";
	$("#privateKeySelection").textContent = "请选择客户端私钥，不能使用 .pub 公钥文件";
	form.elements.ssh_keep_alive.checked = false;
	form.elements.terminal_auto_password.checked = true;
	form.elements.server_alive_interval.value = "60";
	form.elements.server_alive_count_max.value = "3";
	setConnectionProtocol("sftp", true);
	if (show) $("#connectionDialog").showModal();
}

async function editSession(id) {
	try {
		const details = await api(`/api/v1/sessions/${encodeURIComponent(id)}`);
		const form = $("#connectionForm");
		hideSessionGroupMenu();
		form.reset();
		for (const field of ["id", "name", "group", "host", "port", "user", "private_key", "fingerprint"]) {
			if (form.elements[field] && details[field] !== undefined) form.elements[field].value = details[field] ?? "";
		}
		form.dataset.hasSecret = details.has_password ? "true" : "false";
		form.dataset.originalAuth = details.auth_method || (details.private_key ? "key" : "password");
		setConnectionProtocol(details.protocol, false);
		form.elements.auth_method.value = details.auth_method || (details.private_key ? "key" : "password");
		$("#privateKeyUpload").value = "";
		$("#privateKeySelection").textContent = details.private_key ? "已保存密钥；重新选择文件可替换" : "请选择客户端私钥，不能使用 .pub 公钥文件";
		updateConnectionAuthFields();
		form.elements.ssh_keep_alive.checked = details.ssh_keep_alive === true;
		form.elements.server_alive_interval.value = details.server_alive_interval || 60;
		form.elements.server_alive_count_max.value = details.server_alive_count_max || 3;
		form.elements.terminal_auto_password.checked = details.terminal_auto_password !== false;
		updateSSHKeepAliveFields();
		form.elements.password.value = "";
		$("#connectionDialogTitle").textContent = `会话属性 · ${details.name}`;
		$("#connectionDialog").showModal();
	} catch (error) { toast(error.message, "error"); }
}

async function copySession(provider) {
	try {
		const duplicate = await api(`/api/v1/sessions/${encodeURIComponent(provider.id)}/copy`, { method: "POST" });
		await loadProviders();
		toast(`已复制为“${duplicate.name}”`);
		await editSession(duplicate.id);
	} catch (error) {
		toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
	}
}

async function deleteSession(provider) {
	if (!confirm(`确定删除会话“${provider.name}”？\n保存的连接配置会被删除，使用该会话的在途任务可能失败。`)) return;
	try {
		await api(`/api/v1/sessions/${encodeURIComponent(provider.id)}`, { method: "DELETE" });
		for (const side of ["left", "right"]) {
			const panel = state.panels[side];
			panel.tabs = panel.tabs.filter((tab) => tab.provider !== provider.id);
			if (panel.active === provider.id) panel.active = panel.tabs[0]?.provider || "";
		}
		await loadProviders();
		initializeWorkspace();
		for (const side of ["left", "right"]) renderTabs(side);
		saveWorkspace();
		await Promise.all([loadPanel("left"), loadPanel("right")]);
		toast("会话已删除");
	} catch (error) { toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error"); }
}

function setConnectionProtocol(protocol, resetPort = true) {
  const form = $("#connectionForm");
  form.elements.protocol.value = protocol;
	if (protocol === "ftp") form.elements.auth_method.value = "password";
	$("#authMethodField").classList.toggle("hidden", protocol === "ftp");
	$("#sshKeepAliveToggle").classList.toggle("hidden", protocol === "ftp");
	$("#terminalAutoPasswordToggle").classList.toggle("hidden", protocol === "ftp");
	if (resetPort) form.elements.port.value = protocol === "ftp" ? "21" : "22";
  form.elements.user.placeholder = protocol === "ftp" ? "anonymous（可留空）" : "root";
	updateConnectionAuthFields();
	updateSSHKeepAliveFields();
}

function updateConnectionAuthFields() {
	const form = $("#connectionForm");
	const keyAuthentication = form.elements.protocol.value === "sftp" && form.elements.auth_method.value === "key";
	$("#privateKeyField").classList.toggle("hidden", !keyAuthentication);
	$("#passwordFieldLabel").textContent = keyAuthentication ? "私钥口令（可留空）" : "密码";
	const hasSecret = form.dataset.hasSecret === "true" && form.dataset.originalAuth === form.elements.auth_method.value;
	form.elements.password.placeholder = hasSecret
		? (keyAuthentication ? "留空则保留现有私钥口令" : "留空则保留现有密码")
		: (keyAuthentication ? "未加密私钥可留空" : "尚未设置密码");
}

function updateSSHKeepAliveFields() {
	const form = $("#connectionForm");
	const enabled = form.elements.protocol.value === "sftp" && form.elements.ssh_keep_alive.checked;
	$("#sshKeepAliveFields").classList.toggle("hidden", !enabled);
}

async function showLocalTree(side, anchor) {
  state.localTreeSide = side;
  const popup = $("#localTreePopup");
  popup.innerHTML = '<div class="empty-row">正在读取磁盘…</div>';
  popup.classList.add("open");
  const rect = anchor.getBoundingClientRect();
  popup.style.left = `${Math.min(rect.left, window.innerWidth - 336)}px`;
  popup.style.top = `${Math.min(rect.bottom + 2, window.innerHeight - 300)}px`;
  try {
    const roots = await api("/api/v1/local/tree");
    popup.replaceChildren(...roots.map((directory) => createTreeNode(directory, 0, true)));
  } catch (error) { popup.innerHTML = `<div class="empty-row">${escapeHTML(error.message)}</div>`; }
}

function createTreeNode(directory, depth, drive = false) {
  const node = document.createElement("div");
  node.className = "local-tree-node";
  const row = document.createElement("div");
  row.className = "local-tree-row";
  row.style.paddingLeft = `${depth * 16}px`;
  row.innerHTML = `<button class="local-tree-toggle" title="展开" aria-label="展开"><span class="material-symbols-rounded" aria-hidden="true">chevron_right</span></button><span class="local-tree-icon material-symbols-rounded" aria-hidden="true">${drive ? "hard_drive" : "folder"}</span><span class="local-tree-name" title="${escapeHTML(directory.path)}">${escapeHTML(directory.name)}</span>`;
  const children = document.createElement("div");
  children.className = "local-tree-children";
  $(".local-tree-toggle", row).addEventListener("click", async (event) => {
    event.stopPropagation();
    if (node.dataset.loaded) {
      const expanded = node.classList.toggle("expanded");
      $(".local-tree-toggle .material-symbols-rounded", row).textContent = expanded ? "expand_more" : "chevron_right";
      return;
    }
    row.classList.add("loading");
    try {
      const result = await api(`/api/v1/local/tree?path=${encodeURIComponent(directory.path)}`);
      children.replaceChildren(...result.map((child) => createTreeNode(child, depth + 1)));
      node.dataset.loaded = "true";
      node.classList.add("expanded");
      $(".local-tree-toggle .material-symbols-rounded", row).textContent = "expand_more";
    } catch (error) { toast(error.message, "error"); }
    finally { row.classList.remove("loading"); }
  });
  $(".local-tree-name", row).addEventListener("click", () => {
    hideLocalTree(); createLocalTab(state.localTreeSide || "left", directory.path);
  });
  node.append(row, children);
  return node;
}

function hideLocalTree() { $("#localTreePopup").classList.remove("open"); }

async function createLocalTab(side, root) {
  if (!root) { toast("没有可用的本地目录", "error"); return; }
  try {
    const provider = await api("/api/v1/local/tabs", { method: "POST", body: JSON.stringify({ path: root }) });
    await loadProviders();
    await openSession(side, provider.id);
  } catch (error) { toast(error.message, "error"); }
}

function navigatePathInput(side, value) {
  const tab = currentTab(side);
  const provider = providerByID(tab.provider);
  value = value.trim();
  if (!value) return;
  if (provider?.kind !== "local") {
    tab.path = value; loadPanel(side); return;
  }
  const providerPath = localProviderPath(provider, value);
  if (providerPath !== null) {
    tab.path = providerPath;
    loadPanel(side);
    return;
  }
  createLocalTab(side, value);
}

function localProviderPath(provider, value) {
  const root = (provider.location || "").replace(/[\\/]+$/, "");
  const normalizedValue = value.replaceAll("/", "\\").toLowerCase();
  const normalizedRoot = root.replaceAll("/", "\\").toLowerCase();
  if (!normalizedRoot || (normalizedValue !== normalizedRoot && !normalizedValue.startsWith(`${normalizedRoot}\\`))) return null;
  const relative = value.slice(root.length).replaceAll("\\", "/").replace(/^\/+/, "");
  return relative ? `/${relative}` : "/";
}

function legacyBookmarkStore() {
  try {
    const value = JSON.parse(localStorage.getItem(BOOKMARKS_STORAGE));
    return value && typeof value === "object" && !Array.isArray(value) ? value : {};
  } catch (_) { return {}; }
}

function bookmarkProviderKey(provider) {
  return provider?.kind === "local" ? "local" : provider?.id || "";
}

function providerBookmarks(key) {
  const entries = state.bookmarks[key];
  if (!Array.isArray(entries)) return [];
  return entries.filter((entry) => entry && typeof entry.path === "string" && typeof entry.label === "string").map((entry) => ({ ...entry }));
}

async function saveProviderBookmarks(key, entries) {
  const previous = state.bookmarks[key];
  if (entries.length) state.bookmarks[key] = entries.map((entry) => ({ ...entry }));
  else delete state.bookmarks[key];
  try {
    await api("/api/v1/bookmarks", { method: "PUT", body: JSON.stringify({ key, entries }) });
  } catch (error) {
    if (previous) state.bookmarks[key] = previous;
    else delete state.bookmarks[key];
    throw error;
  }
}

function localBookmarkPath(value) {
  return value.replaceAll("/", "\\").replace(/\\+$/, "").toLocaleLowerCase();
}

function bookmarkPathsEqual(key, left, right) {
  return key === "local" ? localBookmarkPath(left) === localBookmarkPath(right) : left === right;
}

function isAbsoluteWindowsPath(value) {
  return /^\\\\/.test(value) || /^[a-zA-Z]:[\\/]/.test(value);
}

async function loadBookmarks() {
  const stored = await api("/api/v1/bookmarks");
  state.bookmarks = stored && typeof stored === "object" && !Array.isArray(stored) ? stored : {};
  try {
    await migrateLegacyBookmarks();
  } catch (error) {
    toast(`旧书签迁移失败：${error.message}`, "error");
  }
}

async function migrateLegacyBookmarks() {
  const legacy = legacyBookmarkStore();
  if (!Object.keys(legacy).length) return;
  const merged = {};
  for (const [key, entries] of Object.entries(state.bookmarks)) merged[key] = providerBookmarks(key);
  for (const [key, entries] of Object.entries(legacy)) {
    if (!Array.isArray(entries)) continue;
    const provider = providerByID(key);
    for (const entry of entries) {
      if (!entry || typeof entry.path !== "string" || typeof entry.label !== "string") continue;
      const local = key === "local" || provider?.kind === "local" || isAbsoluteWindowsPath(entry.label);
      const target = local ? "local" : key;
      const candidate = local ? { path: entry.label, label: entry.label } : { path: entry.path, label: entry.label };
      if (!merged[target]) merged[target] = [];
      const targetEntries = merged[target];
      if (!targetEntries.some((item) => bookmarkPathsEqual(target, item.path, candidate.path))) targetEntries.push(candidate);
    }
  }
  for (const [key, entries] of Object.entries(merged)) {
    entries.sort((a, b) => a.label.localeCompare(b.label, "zh-CN", { numeric: true }));
    await saveProviderBookmarks(key, entries);
  }
  localStorage.removeItem(BOOKMARKS_STORAGE);
}

function currentBookmarkCandidate(side) {
  const tab = currentTab(side);
  if (!tab) return null;
  const provider = providerByID(tab.provider);
  const input = panelElements(side).path.value.trim();
  if (!input) return { path: tab.path, label: tab.displayPath || tab.path, valid: true };
  if (provider?.kind === "local") {
    return { path: input, label: input, valid: true };
  }
  const path = input.startsWith("/") ? input : `/${input}`;
  return { path, label: input, valid: true };
}

function updateBookmarkControl(side) {
  const tab = currentTab(side);
  const button = $(".bookmark-toggle", panelElements(side).root);
  if (!button) return;
  const provider = tab ? providerByID(tab.provider) : null;
  const key = bookmarkProviderKey(provider);
  const candidate = currentBookmarkCandidate(side);
  const active = Boolean(tab && candidate?.valid && providerBookmarks(key).some((entry) => bookmarkPathsEqual(key, entry.path, candidate.path)));
  button.disabled = !tab;
  button.classList.toggle("active", active);
  $("span", button).textContent = active ? "★" : "☆";
  button.title = active ? "删除当前路径书签" : "添加当前路径书签";
  button.setAttribute("aria-label", button.title);
}

async function toggleBookmark(side) {
  const tab = currentTab(side);
  if (!tab) return;
  const provider = providerByID(tab.provider);
  const key = bookmarkProviderKey(provider);
  const candidate = currentBookmarkCandidate(side);
  const entries = providerBookmarks(key);
  const index = entries.findIndex((entry) => bookmarkPathsEqual(key, entry.path, candidate.path));
  try {
    if (index >= 0) {
      entries.splice(index, 1);
      await saveProviderBookmarks(key, entries);
      toast("已删除当前路径书签");
    } else {
      entries.push({ path: candidate.path, label: candidate.label });
      entries.sort((a, b) => a.label.localeCompare(b.label, "zh-CN", { numeric: true }));
      await saveProviderBookmarks(key, entries);
      toast("已添加当前路径书签");
    }
  } catch (error) {
    toast(error.message, "error");
  }
  for (const item of ["left", "right"]) updateBookmarkControl(item);
}

function showBookmarkMenu(side, anchor) {
  const tab = currentTab(side);
  if (!tab) return;
  const provider = providerByID(tab.provider);
  const key = bookmarkProviderKey(provider);
  state.bookmarkSide = side;
  const menu = $("#bookmarkMenu");
  const entries = providerBookmarks(key);
  if (!entries.length) {
    menu.innerHTML = '<div class="empty-row">当前服务器还没有书签</div>';
  } else {
    menu.replaceChildren(...entries.map((entry) => {
      const button = document.createElement("button");
      button.textContent = entry.label;
      button.title = entry.label;
      button.addEventListener("click", () => {
        hideBookmarkMenu();
        const active = currentTab(side);
        if (!active || active.provider !== tab.provider) return;
        if (provider?.kind === "local") navigatePathInput(side, entry.path);
        else { active.path = entry.path; loadPanel(side); }
      });
      return button;
    }));
  }
  menu.classList.add("open");
  anchor.setAttribute("aria-expanded", "true");
  const rect = anchor.getBoundingClientRect();
  menu.style.left = `${Math.max(6, Math.min(rect.right - menu.offsetWidth, window.innerWidth - menu.offsetWidth - 6))}px`;
  menu.style.top = `${Math.min(rect.bottom + 2, window.innerHeight - menu.offsetHeight - 6)}px`;
}

function toggleBookmarkMenu(side, anchor) {
  if ($("#bookmarkMenu").classList.contains("open") && state.bookmarkSide === side) {
    hideBookmarkMenu();
    return;
  }
  showBookmarkMenu(side, anchor);
}

function hideBookmarkMenu() {
  $("#bookmarkMenu").classList.remove("open");
  $$(".bookmark-menu-button").forEach((button) => button.setAttribute("aria-expanded", "false"));
}

function showTerminalMenu(side, anchor) {
  const provider = providerByID(side ? currentProvider(side) : "");
  if (!provider || provider.kind !== "sftp" || !provider.connected) {
    toast("请先在面板中连接一个 SFTP 会话", "error");
    return;
  }
  state.terminalProvider = provider.id;
  const menu = $("#terminalMenu");
  $("#terminalMenuTab").value = localStorage.getItem("floe.terminal.tab") || "1";
  menu.classList.add("open");
  const rect = anchor.getBoundingClientRect();
  const width = 230;
  menu.style.left = `${Math.max(6, Math.min(rect.right - width, window.innerWidth - width - 6))}px`;
  menu.style.top = `${Math.min(rect.bottom + 2, window.innerHeight - menu.offsetHeight - 6)}px`;
}

function hideTerminalMenu() {
  $("#terminalMenu").classList.remove("open");
}

async function openSessionTerminal(providerID, placement) {
  let provider = providerByID(providerID);
  if (!provider || provider.kind !== "sftp") return;
  if (!provider.connected) {
    if (!await connectSavedSession(providerID)) return;
    await loadProviders();
    renderTabs("left");
    renderTabs("right");
    provider = providerByID(providerID);
  }
  if (!provider?.connected) return;
  state.terminalProvider = providerID;
  await launchTerminal(placement);
}

async function launchTerminal(placement) {
  const provider = providerByID(state.terminalProvider);
  if (!provider || provider.kind !== "sftp" || !provider.connected) {
    hideTerminalMenu();
    toast("SSH 会话已经断开，请重新连接", "error");
    return;
  }
  const tab = Number($("#terminalMenuTab").value || 1);
  if (placement.startsWith("tab-") && (tab < 1 || tab > 100)) {
    toast("标签编号必须在 1–100 之间", "error");
    return;
  }
  localStorage.setItem("floe.terminal.placement", placement);
  localStorage.setItem("floe.terminal.tab", String(tab));
  hideTerminalMenu();
  try {
    const result = await api("/api/v1/terminal/tabs", {
      method: "POST",
      body: JSON.stringify({
        kind: "ssh", provider: provider.id, cwd: "", placement, tab,
      }),
    });
    toast(result.askpass ? "已打开 SSH，正在自动输入密码" : "已打开 SSH");
  } catch (error) {
    toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
  }
}

function bindPanel(side) {
  const el = panelElements(side);
  el.root.addEventListener("mousedown", () => setActivePane(side));
  el.path.addEventListener("input", () => updateBookmarkControl(side));
  el.path.addEventListener("keydown", (event) => { if (event.key === "Enter") navigatePathInput(side, el.path.value); });
  $(".local-tree-button", el.root).addEventListener("click", (event) => { event.stopPropagation(); showLocalTree(side, event.currentTarget); });
  $(".bookmark-toggle", el.root).addEventListener("click", (event) => { event.stopPropagation(); toggleBookmark(side); });
  $(".bookmark-menu-button", el.root).addEventListener("click", (event) => { event.stopPropagation(); toggleBookmarkMenu(side, event.currentTarget); });
  $(".up-button", el.root).addEventListener("click", () => { currentTab(side).path = parentPath(currentPath(side)); loadPanel(side); });
  $(".refresh-button", el.root).addEventListener("click", () => loadPanel(side));
  $(".terminal-button", el.root).addEventListener("click", (event) => {
	event.stopPropagation();
	showTerminalMenu(side, event.currentTarget);
  });
  $(".transfer-file-button", el.root).addEventListener("click", () => transferEntry(side, side === "left" ? "right" : "left", state.panels[side].selected));
  el.viewButton.addEventListener("click", () => {
    const current = state.panels[side];
    current.view = current.view === "list" ? "grid" : "list";
    saveWorkspace(); loadPanel(side);
  });
  $$(".column-head button[data-sort]", el.root).forEach((button) => button.addEventListener("click", () => changeSort(side, button.dataset.sort)));
  el.viewport.addEventListener("scroll", () => queueRender(side));
  new ResizeObserver(() => queueRender(side)).observe(el.viewport);
  el.viewport.addEventListener("contextmenu", (event) => {
    if (event.target.closest(".file-entry")) return;
    event.preventDefault(); showBlankMenu(event, side);
  });
  bindDrops(side);
}

function fileVisual(entry, side, view) {
  const ext = fileExtension(entry.name);
  const images = ["png", "jpg", "jpeg", "gif"];
  if (!entry.is_dir && view === "grid" && isImageEntry(entry)) {
    const endpoint = images.includes(ext) ? "/api/v1/files/thumbnail" : "/api/v1/files/raw";
    const url = `${endpoint}?provider=${encodeURIComponent(currentProvider(side))}&path=${encodeURIComponent(entry.path)}&size=160`;
    return { preview: true, html: `<img src="${url}" alt="">` };
  }
  if (entry.is_dir) return { html: iconSVG("folder") };
  if (isImageEntry(entry)) return { html: iconSVG("image") };
	if (isMediaEntry(entry)) return { html: iconSVG("video") };
  if (["zip", "gz", "xz", "7z", "tar", "rar"].includes(ext)) return { html: iconSVG("archive") };
  if (["sh", "ps1", "py", "go", "rs", "js", "ts", "c", "cpp", "h"].includes(ext)) return { html: iconSVG("code") };
  if (isTextEntry(entry)) return { html: iconSVG("text") };
  return { html: iconSVG("file") };
}

function iconSVG(kind) {
  const names = { folder: "folder", file: "description", text: "description", image: "image", video: "movie", archive: "archive", code: "code" };
  return `<span class="material-symbols-rounded file-type-icon ${kind}" aria-hidden="true">${names[kind] || names.file}</span>`;
}
function formatBytes(value) {
  if (!Number.isFinite(value) || value < 1024) return `${value || 0} B`;
  const units = ["KB", "MB", "GB", "TB"]; let size = value; let unit = -1;
  do { size /= 1024; unit++; } while (size >= 1024 && unit < units.length - 1);
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[unit]}`;
}
function formatTime(value) { return new Date(value).toLocaleString([], { year: "2-digit", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }); }
function escapeHTML(value) { const node = document.createElement("span"); node.textContent = value ?? ""; return node.innerHTML; }

function initializeQueueResize() {
  const mainArea = $(".main-area");
  const saved = Number(localStorage.getItem("floe.queue.height"));
  if (saved >= 90) mainArea.style.setProperty("--queue-height", `${saved}px`);
  const handle = $("#queueResizer");
  handle.addEventListener("pointerdown", (event) => {
    $("#transferQueue").classList.remove("collapsed");
    handle.classList.add("dragging");
    handle.setPointerCapture(event.pointerId);
  });
  handle.addEventListener("pointermove", (event) => {
    if (!handle.hasPointerCapture(event.pointerId)) return;
    const height = Math.max(90, Math.min(window.innerHeight * 0.65, window.innerHeight - event.clientY));
    mainArea.style.setProperty("--queue-height", `${height}px`);
  });
  handle.addEventListener("pointerup", (event) => {
    if (!handle.hasPointerCapture(event.pointerId)) return;
    handle.releasePointerCapture(event.pointerId);
    handle.classList.remove("dragging");
    const height = parseInt(getComputedStyle(mainArea).getPropertyValue("--queue-height"), 10);
    localStorage.setItem("floe.queue.height", String(height));
  });
}

async function main() {
  try {
    const session = await api("/api/v1/session"); state.csrf = session.csrf;
    await loadProviders(false); await loadBookmarks(); initializeWorkspace(); renderSessionTree();
    bindPanel("left"); bindPanel("right"); renderTabs("left"); renderTabs("right");
    initializeQueueResize();
	await loadPanel("left");
	await loadPanel("right");
	await Promise.all([api("/api/v1/transfers").then(refreshTasks), api("/api/v1/logs").then(refreshLogs)]);
    const events = new EventSource("/api/v1/events");
    events.addEventListener("tasks", (event) => refreshTasks(JSON.parse(event.data)));
	events.addEventListener("logs", (event) => refreshLogs(JSON.parse(event.data)));
  } catch (error) { toast(error.message, "error"); }
}

$("#sidebarAdd").addEventListener("click", () => prepareNewSession());
$("#sessionGroupControl > button").addEventListener("click", toggleSessionGroupMenu);
$("#sessionGroupControl input[name=group]").addEventListener("keydown", handleSessionGroupKey);
$("#connectionForm").addEventListener("submit", (event) => { event.preventDefault(); saveConnection(event.currentTarget, event.submitter?.value === "connect"); });
$("#connectionProtocol").addEventListener("change", (event) => setConnectionProtocol(event.target.value));
$("#connectionAuthMethod").addEventListener("change", updateConnectionAuthFields);
$("#choosePrivateKey").addEventListener("click", () => $("#privateKeyUpload").click());
$("#privateKeyUpload").addEventListener("change", (event) => {
	const file = event.target.files[0];
	$("#privateKeySelection").textContent = file ? `已选择：${file.name}` : "请选择客户端私钥，不能使用 .pub 公钥文件";
});
$("#connectionForm").elements.ssh_keep_alive.addEventListener("change", updateSSHKeepAliveFields);
$$('#connectionDialog .dialog-close, #connectionDialog .dialog-cancel').forEach((button) => button.addEventListener("click", () => $("#connectionDialog").close()));
$$('#terminalMenu button[data-terminal-placement]').forEach((button) => button.addEventListener("click", () => launchTerminal(button.dataset.terminalPlacement)));
$("#sessionSearch").addEventListener("input", renderSessionTree);
$("#swapPanes").addEventListener("click", swapPanels);
$("#closeEditor").addEventListener("click", requestCloseEditor);
$("#maximizeEditor").addEventListener("click", () => toggleDialogMaximized($("#editorDialog"), $("#maximizeEditor")));
$("#saveEditor").addEventListener("click", saveEditor);
$("#editorSearch").addEventListener("click", () => showEditorFind(false));
$("#editorFindClose").addEventListener("click", closeEditorFind);
$("#editorFindPrevious").addEventListener("click", () => findEditorMatch(-1));
$("#editorFindNext").addEventListener("click", () => findEditorMatch(1));
$("#editorReplaceOne").addEventListener("click", replaceEditorMatch);
$("#editorReplaceAll").addEventListener("click", replaceAllEditorMatches);
$("#editorMatchCase").addEventListener("click", () => {
	if (!state.editor) return;
	state.editor.matchCase = !state.editor.matchCase;
	$("#editorMatchCase").classList.toggle("active", state.editor.matchCase);
	$("#editorMatchCase").setAttribute("aria-pressed", String(state.editor.matchCase));
	refreshFindMatches();
});
$("#editorFind").addEventListener("input", () => refreshFindMatches());
for (const field of [$("#editorFind"), $("#editorReplace")]) field.addEventListener("keydown", (event) => {
	if (event.key === "Escape") { event.preventDefault(); closeEditorFind(); }
	if (event.key === "Enter") { event.preventDefault(); findEditorMatch(event.shiftKey ? -1 : 1); }
});
$("#editorContent").addEventListener("input", editorChanged);
$("#editorContent").addEventListener("scroll", syncEditorScroll);
for (const eventName of ["click", "keyup", "select"]) $("#editorContent").addEventListener(eventName, updateEditorStatus);
$("#editorContent").addEventListener("keydown", (event) => {
	const command = event.ctrlKey || event.metaKey;
	if (command && event.key.toLowerCase() === "s") { event.preventDefault(); saveEditor(); }
	else if (command && event.key.toLowerCase() === "f") { event.preventDefault(); showEditorFind(false); }
	else if (command && event.key.toLowerCase() === "h") { event.preventDefault(); showEditorFind(true); }
	else if (command && event.key.toLowerCase() === "g") { event.preventDefault(); jumpToEditorLine(); }
	else if (event.key === "F3") { event.preventDefault(); findEditorMatch(event.shiftKey ? -1 : 1); }
	else if (event.key === "Escape" && !$("#editorFindBar").classList.contains("hidden")) { event.preventDefault(); closeEditorFind(); }
});
$("#editorPosition").addEventListener("click", jumpToEditorLine);
$("#editorDialog").addEventListener("cancel", (event) => { event.preventDefault(); requestCloseEditor(); });
$("#syntaxMode").addEventListener("change", refreshEditorHighlight);
$("#queueToggle").addEventListener("click", () => $("#transferQueue").classList.toggle("collapsed"));
$$('.task-tab').forEach((tab) => tab.addEventListener("click", () => { state.taskFilter = tab.dataset.taskFilter; $("#transferQueue").classList.remove("collapsed"); renderTaskList(); }));
$("#clearTaskHistory").addEventListener("click", clearTaskHistory);
$("#imagePreview").addEventListener("load", fitImage);
$("#previousImage").addEventListener("click", () => changeImage(-1));
$("#nextImage").addEventListener("click", () => changeImage(1));
$("#zoomOut").addEventListener("click", () => setImageZoom(state.image.zoom / 1.2));
$("#zoomIn").addEventListener("click", () => setImageZoom(state.image.zoom * 1.2));
$("#resetZoom").addEventListener("click", () => setImageZoom(1));
$("#maximizeImage").addEventListener("click", () => toggleDialogMaximized($("#imageDialog"), $("#maximizeImage")));
$("#closeImage").addEventListener("click", () => $("#imageDialog").close());
$("#imageStage").addEventListener("wheel", (event) => { event.preventDefault(); setImageZoom(state.image.zoom * (event.deltaY < 0 ? 1.12 : 1 / 1.12)); }, { passive: false });
$("#maximizeMedia").addEventListener("click", () => toggleDialogMaximized($("#mediaDialog"), $("#maximizeMedia")));
$("#closeMedia").addEventListener("click", () => closeMedia());
$("#mediaDialog").addEventListener("cancel", () => closeMedia(false));
$("#mediaPlayer").addEventListener("playing", () => { $("#mediaState").textContent = "正在播放"; $("#mediaMessage").classList.remove("visible"); });
$("#mediaPlayer").addEventListener("error", () => {
	if (state.hls) return;
	const error = $("#mediaPlayer").error;
	const detail = error ? `MediaError ${error.code}${error.message ? ` · ${error.message}` : ""}` : "未知媒体错误";
	showMediaError(`视频播放失败：${detail}`);
	reportClientLog("error", "media", "视频播放失败", detail);
});
window.addEventListener("beforeunload", (event) => {
	if (!state.editor?.dirty) return;
	event.preventDefault();
	event.returnValue = "";
});
document.addEventListener("dragend", clearDropTargets);
document.addEventListener("mousedown", (event) => {
  if (!event.target.closest("#sessionGroupControl")) hideSessionGroupMenu();
  if (!event.target.closest("#contextMenu")) hideContextMenu();
  if (!event.target.closest("#localTreePopup, .local-tree-button, .new-local-tab")) hideLocalTree();
  if (!event.target.closest("#bookmarkMenu, .bookmark-control")) hideBookmarkMenu();
  if (!event.target.closest("#terminalMenu, .terminal-button")) hideTerminalMenu();
});
document.addEventListener("keydown", (event) => {
  if (!$("#imageDialog").open) return;
  if (event.key === "ArrowLeft") changeImage(-1);
  if (event.key === "ArrowRight") changeImage(1);
  if (event.key === "+" || event.key === "=") setImageZoom(state.image.zoom * 1.2);
  if (event.key === "-") setImageZoom(state.image.zoom / 1.2);
});
window.addEventListener("blur", () => { hideContextMenu(); hideLocalTree(); hideBookmarkMenu(); hideTerminalMenu(); });
setInterval(() => {
  if (state.taskFilter === "queue" && state.tasks.some((task) => ["running", "verifying"].includes(task.status))) renderTaskList();
}, 1000);

main();
