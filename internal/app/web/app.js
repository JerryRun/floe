const state = {
  providers: [],
  activePane: "left",
  sessionSelected: "",
  sessionGroups: [],
  sessionGroupIndex: 0,
  groupState: {},
  bookmarks: {},
  taskStatus: new Map(),
  transferMetrics: new Map(),
  transferTemplates: [],
  sidebarTab: "sessions",
  publishExpanded: new Set(),
  editingPublishTemplate: null,
  templateChooseResolve: null,
  transferOptionsResolve: null,
  transferConflictPolicy: "overwrite",
  tasks: [],
  taskSelection: new Set(),
  logs: [],
  taskFilter: "queue",
  // The preview is either its own modal window (host "dialog", the default) or a
  // tab in the bottom task area (host "panel"). Both hosts share the same file
  // tabs and cached per-file view state.
  preview: { host: "dialog", items: [], activeID: "", maximized: false },
  memories: [],
  memorySelected: "",
  memoryLoaded: false,
  memoryLoading: false,
  memoryMode: "view",
  memoryDraft: "",
  memoryEditing: null,
  memorySelectionText: "",
  memoryLoadID: 0,
  memoryContextLoadID: 0,
  memoryContextLoading: false,
  memoryBlocks: [],
  memoryHasBefore: false,
  memoryHasAfter: false,
  memoryLoadingBefore: false,
  memoryLoadingAfter: false,
  memoryAnchor: "",
  memorySettings: null,
  memorySearchHistory: [],
  memorySearchHistoryLoaded: false,
  memorySearchHistoryOpen: false,
  memorySearchHistorySort: "frequency",
  localTreeSide: "",
  bookmarkSide: "",
  terminalProvider: "",
  panels: {
    left: { tabs: [], active: "", entries: [], selection: new Set(), selectionAnchor: -1, view: "list", sort: { field: "name", direction: "asc" }, renderQueued: false, loadID: 0 },
    right: { tabs: [], active: "", entries: [], selection: new Set(), selectionAnchor: -1, view: "list", sort: { field: "name", direction: "asc" }, renderQueued: false, loadID: 0 },
  },
  editor: null,
  image: null,
  hls: null,
};

const DND_FILE = "application/x-floe-file";
const DND_SESSION = "application/x-floe-session";
const DND_TAB = "application/x-floe-tab";
const MEMORY_SEARCH_HISTORY_LIMIT = 100;
const BOOKMARKS_STORAGE = "floe.bookmarks.v1";
const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => [...root.querySelectorAll(selector)];
let editorPreviewTimer = 0;
let memorySearchTimer = 0;
let memorySearchHistoryMutation = Promise.resolve();

async function api(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const response = await fetch(url, { ...options, headers });
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
      item.title = sessionTooltip(provider);
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

function setSidebarTab(tab) {
  state.sidebarTab = tab === "publishes" ? "publishes" : "sessions";
  $$(".sidebar-tab").forEach((button) => {
    const active = button.dataset.sidebarTab === state.sidebarTab;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });
  const sessions = state.sidebarTab === "sessions";
  $(".session-search").hidden = !sessions;
  $("#sessionTree").hidden = !sessions;
  $("#publishTaskPane").hidden = sessions;
  if (sessions) hidePublishTaskDetails();
  if (!sessions) renderPublishSidebar();
}

function templateTasks(template) {
  return template?.tasks?.length ? template.tasks : (template ? [template] : []);
}

function templateTaskProviderName(item, role) {
  const id = item?.[`${role}_provider`];
  return providerByID(id)?.name || item?.[`${role}_provider_name`] || id || "未指定";
}

function latestPublishTask(item) {
  return [...state.tasks].reverse().find((task) => task.source_provider === item.source_provider && task.source_path === item.source_path && task.target_provider === item.target_provider && task.target_path === item.target_path);
}

function renderPublishSidebar() {
  const list = $("#publishTaskList");
  const count = $("#publishTaskCount");
  if (!list) return;
  count.textContent = state.transferTemplates.length ? `(${state.transferTemplates.length})` : "";
  if (!state.transferTemplates.length) {
    list.innerHTML = '<div class="publish-empty">还没有发布任务<br>在成功列表中右键保存模板</div>';
    return;
  }
  list.replaceChildren(...state.transferTemplates.map((template) => {
    const row = document.createElement("div");
    row.className = "publish-task-row";
    const name = document.createElement("button");
    name.type = "button";
    name.className = "publish-task-name";
    name.textContent = template.name;
    name.title = template.name;
    name.addEventListener("focus", (event) => showPublishTaskDetails(template, event.currentTarget));
    name.addEventListener("blur", hidePublishTaskDetails);
    row.addEventListener("mouseenter", () => showPublishTaskDetails(template, name));
    row.addEventListener("mouseleave", hidePublishTaskDetails);
    const actions = document.createElement("span");
    actions.className = "publish-task-row-actions";
    actions.addEventListener("mouseenter", hidePublishTaskDetails);
    const run = document.createElement("button");
    run.type = "button";
    run.className = "icon-button";
    run.setAttribute("aria-label", "执行发布任务");
    run.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">play_arrow</span>';
    run.addEventListener("click", () => runTransferTemplate(template.id));
    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "icon-button";
    edit.setAttribute("aria-label", "编辑发布任务");
    edit.innerHTML = '<svg class="icon-svg" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 17.25V20h2.75L17.8 8.95l-2.75-2.75L4 17.25Zm15.7-9.3c.4-.4.4-1.05 0-1.45l-2.2-2.2a1.03 1.03 0 0 0-1.45 0l-1.35 1.35 2.75 2.75L19.7 7.95Z"/></svg>';
    edit.addEventListener("click", () => openPublishTemplateEditor(template));
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "icon-button danger";
    remove.setAttribute("aria-label", "删除发布任务");
    remove.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 7h14M9 7V4h6v3m-8 0 1 13h6l1-13M10 11v5m4-5v5"/></svg>';
    remove.addEventListener("click", () => deletePublishTemplate(template));
    actions.append(run, edit, remove);
    row.append(name, actions);
    return row;
  }));
}

function publishTaskGroups(template) {
  const groups = new Map();
  for (const item of templateTasks(template)) {
    const key = `${item.source_provider}\u0000${item.target_provider}`;
    if (!groups.has(key)) groups.set(key, { sourceName: templateTaskProviderName(item, "source"), targetName: templateTaskProviderName(item, "target"), items: [] });
    groups.get(key).items.push(item);
  }
  return [...groups.values()];
}

function publishTaskDetailsHTML(template, editable = false) {
  const groups = publishTaskGroups(template);
  const groupsHTML = groups.map((group) => {
    const heading = `${group.sourceName} -> ${group.targetName}:`;
    const items = group.items.map((item) => `<div class="publish-detail-item"><span>- ${escapeHTML(item.source_path || "/")} -> ${escapeHTML(item.target_path || "/")}</span></div>`).join("");
    return `<section class="publish-detail-group"><strong>${escapeHTML(heading)}</strong>${items}</section>`;
  }).join("");
  return `<div class="publish-detail-title">${escapeHTML(template.name)}</div>${groupsHTML || '<div class="publish-empty">没有发布项</div>'}`;
}

function showPublishTaskDetails(template, anchor) {
  const details = $("#publishTaskDetails");
  if (!details) return;
  details.innerHTML = publishTaskDetailsHTML(template);
  details.hidden = false;
  const rect = anchor.getBoundingClientRect();
  const width = Math.min(470, Math.max(300, window.innerWidth - rect.right - 14));
  details.style.width = `${width}px`;
  details.style.left = `${Math.min(rect.right + 8, window.innerWidth - width - 8)}px`;
  details.style.top = `${Math.max(8, Math.min(rect.top, window.innerHeight - details.offsetHeight - 8))}px`;
}

function hidePublishTaskDetails() {
  const details = $("#publishTaskDetails");
  if (details) details.hidden = true;
}

function renderPublishTemplateEditor() {
  const list = $("#publishTemplateItems");
  const template = state.editingPublishTemplate;
  if (!list || !template) return;
  const tasks = template.tasks || [];
  const groups = publishTaskGroups(template);
  list.replaceChildren(...groups.flatMap((group) => {
    const heading = document.createElement("div");
    heading.className = "publish-editor-group";
    heading.textContent = group.sourceName + " -> " + group.targetName + ":";
    return [heading, ...group.items.map((item) => {
      const index = tasks.indexOf(item);
      const row = document.createElement("div");
      row.className = "publish-editor-item";
      row.innerHTML = '<span title="' + escapeHTML((item.source_path || "/") + " -> " + (item.target_path || "/")) + '">- ' + escapeHTML(item.source_path || "/") + " -> " + escapeHTML(item.target_path || "/") + "</span>";
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "icon-button danger";
      remove.title = "删除此发布项";
      remove.setAttribute("aria-label", "删除此发布项");
      remove.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">delete</span>';
      remove.addEventListener("click", () => {
        template.tasks.splice(index, 1);
        renderPublishTemplateEditor();
      });
      row.append(remove);
      return row;
    })];
  }));
}

function openPublishTemplateEditor(template) {
  state.editingPublishTemplate = JSON.parse(JSON.stringify(template));
  if (!Array.isArray(state.editingPublishTemplate.tasks)) {
    const legacyTask = { ...state.editingPublishTemplate };
    delete legacyTask.tasks;
    state.editingPublishTemplate.tasks = [legacyTask];
  }
  const form = $("#publishTemplateForm");
  form.elements.name.value = state.editingPublishTemplate.name || "";
  renderPublishTemplateEditor();
  $("#publishTemplateDialog").showModal();
}

async function deletePublishTemplate(template) {
  if (!confirm("确定删除发布任务“" + template.name + "”？")) return;
  try {
    await api("/api/v1/transfer-templates/" + encodeURIComponent(template.id), { method: "DELETE" });
    await loadTransferTemplates();
    toast("已删除发布任务“" + template.name + "”");
  } catch (error) {
    toast(error.message, "error");
  }
}

function sessionTooltip(provider) {
  if (provider.kind === "local") return "本地文件系统\n双击打开，或拖到任一标签栏";
  const protocol = (provider.kind || "session").toUpperCase();
  const endpoint = provider.host ? `${provider.host}:${provider.port || (provider.kind === "ftp" ? 21 : 22)}` : (provider.location || "未连接");
  const lines = [`协议：${protocol}`, `地址：${endpoint}`];
  if (provider.user) lines.push(`用户：${provider.user}`);
  if (provider.auth_method) lines.push(`认证：${provider.auth_method === "key" ? "SSH 密钥" : "密码"}`);
  if (provider.group) lines.push(`分组：${provider.group}`);
  lines.push(provider.connected ? "状态：已连接" : "状态：未连接（双击连接）");
  return lines.join("\n");
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
  } else if (path) {
    tab.path = initialPath;
  } else if (justConnected && !path && provider?.kind === "sftp") {
    tab.path = initialPath;
  }
  panel.active = providerID;
  panel.selection.clear();
  panel.selectionAnchor = -1;
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
    node.title = sessionTooltip(provider);
    const pinned = side === "left" && provider.id === "local";
    node.innerHTML = `<span class="tab-icon">${sessionIcon(provider)}</span><span class="tab-name">${escapeHTML(provider.name)}</span>${pinned ? "" : '<span class="tab-close" aria-label="关闭"><span class="material-symbols-rounded" aria-hidden="true">close</span></span>'}`;
    node.addEventListener("click", (event) => {
      if (event.target.closest(".tab-close")) return;
      panel.active = tab.provider; setActivePane(side); renderTabs(side); saveWorkspace(); loadPanel(side);
    });
    $(".tab-close", node)?.addEventListener("click", (event) => { event.stopPropagation(); closeTab(side, tab.provider); });
    node.addEventListener("contextmenu", (event) => {
      event.preventDefault(); event.stopPropagation();
      showTabMenu(event, side, tab.provider);
    });
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

function primaryLocalProvider() {
  return providerByID("local") || state.providers.find((provider) => provider.kind === "local");
}

function showTabMenu(event, side, providerID) {
  const panel = state.panels[side];
  const provider = providerByID(providerID);
  const pinned = side === "left" && provider?.id === "local";
  const local = primaryLocalProvider();
  const onlyPrimaryLocal = panel.tabs.length === 1 && panel.tabs[0].provider === local?.id;
  showContextMenu(event.clientX, event.clientY, [
    { label: "关闭标签", disabled: pinned || panel.tabs.length <= 1, action: () => closeTab(side, providerID) },
    { label: "关闭全部标签", disabled: onlyPrimaryLocal || (!local && panel.tabs.length <= 1), action: () => closeAllTabs(side) },
  ]);
}

function closeAllTabs(side) {
  const panel = state.panels[side];
  const local = primaryLocalProvider();
  let fallback = local && panel.tabs.find((tab) => tab.provider === local.id);
  if (!fallback && local) fallback = { provider: local.id, path: "/" };
  if (!fallback) fallback = currentTab(side);
  if (!fallback) return;
  panel.tabs = [fallback];
  panel.active = fallback.provider;
  panel.selection.clear();
  panel.selectionAnchor = -1;
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
  const loadID = ++panel.loadID;
  const tab = currentTab(side);
  const el = panelElements(side);
  if (!tab) return;
  const provider = providerByID(tab.provider);
  if (provider && provider.kind !== "local" && !provider.connected) {
    if (allowReconnect) {
      setPanelLoading(el, "正在连接…");
      await nextFrame();
      const connected = await connectSavedSession(tab.provider);
      await loadProviders();
      renderTabs("left"); renderTabs("right");
      if (connected) return loadPanel(side, false);
    }
    panel.entries = [];
    panel.selection.clear();
    panel.selectionAnchor = -1;
    el.path.value = tab.path;
    updateBookmarkControl(side);
    el.canvas.innerHTML = '<div class="empty-files">双击左侧会话以连接</div>';
    el.count.textContent = "未连接";
    updateSelectionLabel(side);
    el.root.classList.remove("local-provider");
    return;
  }
  el.path.value = tab.path;
  updateBookmarkControl(side);
  setPanelLoading(el, "正在读取目录…");
  // Let the browser paint the loading state before a slow local/remote
  // directory request (or a large JSON response) occupies the UI thread.
  await nextFrame();
  try {
    const result = await api(`/api/v1/files?provider=${encodeURIComponent(tab.provider)}&path=${encodeURIComponent(tab.path)}`);
    if (loadID !== panel.loadID) return;
    tab.path = result.path;
    tab.displayPath = result.display_path || result.path;
    const entries = result.entries || [];
    if (entries.length > 500) {
      setPanelLoading(el, `正在整理 ${entries.length.toLocaleString()} 项…`);
      await nextFrame();
      if (loadID !== panel.loadID) return;
    }
    panel.entries = sortEntries(entries, panel.sort);
    panel.selection.clear();
    panel.selectionAnchor = -1;
    el.path.value = tab.displayPath;
    updateBookmarkControl(side);
    const provider = providerByID(tab.provider);
    el.root.classList.toggle("local-provider", provider?.kind === "local");
    $(".terminal-button", el.root).disabled = provider?.kind !== "sftp";
    el.count.textContent = `${panel.entries.length.toLocaleString()} 项`;
    updateSelectionLabel(side);
    el.viewport.scrollTop = 0;
    el.root.classList.toggle("grid-view", panel.view === "grid");
    el.viewButton.innerHTML = `<span class="material-symbols-rounded" aria-hidden="true">${panel.view === "grid" ? "view_list" : "grid_view"}</span>`;
    updateSortHeaders(side);
    renderPanel(side);
    saveWorkspace();
    resolvePanelLinks(side);
  } catch (error) {
    if (loadID !== panel.loadID) return;
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

function nextFrame() {
  return new Promise((resolve) => requestAnimationFrame(resolve));
}

function setPanelLoading(el, message) {
  el.canvas.innerHTML = `<div class="empty-files loading"><span class="loading-spinner" aria-hidden="true"></span>${escapeHTML(message)}</div>`;
  el.count.textContent = "正在读取…";
}

function locateEntryByKey(side, key) {
  if (!/^[a-z]$/i.test(key)) return false;
  const panel = state.panels[side];
  if (!panel.entries.length) return false;
  const needle = key.toLocaleLowerCase();
  const start = panel.selectionAnchor >= 0 ? panel.selectionAnchor + 1 : 0;
  let index = panel.entries.findIndex((entry, offset) => offset >= start && entry.name.toLocaleLowerCase().startsWith(needle));
  if (index < 0) index = panel.entries.findIndex((entry) => entry.name.toLocaleLowerCase().startsWith(needle));
  if (index < 0) return false;
  selectEntry(side, index);
  const viewport = panelElements(side).viewport;
  const list = panel.view === "list";
  const cellHeight = list ? 29 : 138;
  const columns = list ? 1 : Math.max(1, Math.floor(viewport.clientWidth / 152));
  const row = Math.floor(index / columns);
  const top = row * cellHeight;
  const bottom = top + cellHeight;
  if (top < viewport.scrollTop) viewport.scrollTop = top;
  else if (bottom > viewport.scrollTop + viewport.clientHeight) viewport.scrollTop = bottom - viewport.clientHeight;
  return true;
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
  panel.selection.clear();
  panel.selectionAnchor = -1;
  updateSelectionLabel(side);
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
    node.className = `file-entry ${panel.view}${panel.selection.has(entry.path) ? " selected" : ""}`;
    node.setAttribute("role", "option");
    node.setAttribute("aria-selected", String(panel.selection.has(entry.path)));
    node.style.transform = `translate(${column * width}px, ${row * cellHeight}px)`;
    node.style.width = `${list ? viewport.clientWidth : width - 6}px`;
    node.draggable = true;
    node.title = entryTitle(entry);
    const visual = fileVisual(entry, side, panel.view);
    node.dataset.index = String(index);
    node.innerHTML = `<span class="file-name"><i class="file-icon${visual.preview ? " image-preview" : ""}${entryIconClass(entry)}">${visual.html}</i><b class="file-label">${escapeHTML(entry.name)}</b></span><span class="file-size">${entrySizeLabel(entry)}</span><span class="file-time">${formatTime(entry.modified)}</span><span class="file-mode">${escapeHTML(entry.mode)}</span>`;
    node.addEventListener("click", (event) => selectEntry(side, index, event));
    node.addEventListener("dblclick", () => openEntry(side, index));
    node.addEventListener("dragstart", (event) => {
      if (!panel.selection.has(entry.path)) selectEntry(side, index);
      node.classList.add("dragging");
      event.dataTransfer.effectAllowed = "copy";
      event.dataTransfer.setData(DND_FILE, JSON.stringify({ side, paths: selectedEntries(side).map((item) => item.path) }));
    });
    node.addEventListener("dragend", () => { node.classList.remove("dragging"); clearDropTargets(); });
    node.addEventListener("contextmenu", (event) => {
      event.preventDefault(); event.stopPropagation();
      if (!panel.selection.has(entry.path)) selectEntry(side, index);
      showFileMenu(event, side, entry);
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

function selectedEntries(side) {
  const panel = state.panels[side];
  return panel.entries.filter((entry) => panel.selection.has(entry.path));
}

function updateSelectionLabel(side) {
  const entries = selectedEntries(side);
  const label = panelElements(side).selection;
  label.textContent = entries.length > 1 ? `已选择 ${entries.length.toLocaleString()} 项` : entries[0]?.name || "";
  label.title = entries.map((entry) => entry.name).join("\n");
}

function clearSelection(side) {
  const panel = state.panels[side];
  if (!panel.selection.size) return;
  panel.selection.clear();
  panel.selectionAnchor = -1;
  updateSelectionLabel(side);
  renderPanel(side);
}

function selectEntry(side, index, event = {}) {
  const panel = state.panels[side];
  const entry = panel.entries[index];
  if (!entry) return;
  const additive = Boolean(event.ctrlKey || event.metaKey);
  if (event.shiftKey && panel.selectionAnchor >= 0) {
    const next = additive ? new Set(panel.selection) : new Set();
    const start = Math.min(panel.selectionAnchor, index);
    const end = Math.max(panel.selectionAnchor, index);
    for (let item = start; item <= end; item++) next.add(panel.entries[item].path);
    panel.selection = next;
  } else if (additive) {
    if (panel.selection.has(entry.path)) panel.selection.delete(entry.path);
    else panel.selection.add(entry.path);
    panel.selectionAnchor = index;
  } else {
    panel.selection = new Set([entry.path]);
    panel.selectionAnchor = index;
  }
  updateSelectionLabel(side);
  setActivePane(side);
  renderPanel(side);
}

async function openEntry(side, index) {
  let entry = state.panels[side].entries[index];
  if (entry.is_link && entry.link_unresolved) {
    // A large listing deferred this link, so ask for just this one before
    // deciding between navigating and previewing.
    await resolvePanelLinks(side, [entry.path]);
    entry = state.panels[side].entries[index] || entry;
  }
  if (entry.is_link && entry.link_broken) {
    toast(entry.link_target ? `链接目标不存在：${entry.link_target}` : "链接目标不存在", "error");
    return;
  }
  if (entry.is_dir) {
    currentTab(side).path = entry.path;
    await loadPanel(side);
  } else {
    await openFile(side, entry);
  }
}

// resolvePanelLinks fills in symlinks the listing left unresolved. A directory
// made almost entirely of links renders immediately and the real target types
// arrive right afterwards.
async function resolvePanelLinks(side, paths) {
  const panel = state.panels[side];
  const tab = currentTab(side);
  if (!tab?.provider) return;
  const wanted = paths || panel.entries.filter((entry) => entry.link_unresolved).map((entry) => entry.path);
  if (!wanted.length) return;
  const loadID = panel.loadID;
  try {
    const result = await api("/api/v1/files/resolve-links", {
      method: "POST",
      body: JSON.stringify({ provider: tab.provider, paths: wanted }),
    });
    if (loadID !== panel.loadID) return;
    const resolved = new Map((result.links || []).map((link) => [link.path, link]));
    let changed = false;
    for (const entry of panel.entries) {
      const link = resolved.get(entry.path);
      if (!link) continue;
      entry.link_unresolved = false;
      entry.is_dir = Boolean(link.is_dir);
      entry.link_broken = Boolean(link.link_broken);
      if (link.link_target) entry.link_target = link.link_target;
      if (!link.is_dir && !link.link_broken) entry.size = link.size;
      changed = true;
    }
    if (!changed) return;
    panel.entries = sortEntries(panel.entries, panel.sort);
    renderPanel(side);
  } catch (_) {
    // Unresolved links keep their neutral icon; nothing else depends on this.
  }
}

// entryIconClass marks a symlink on top of its target's icon, so a link to a
// directory looks like a folder and still reads as a link.
function entryIconClass(entry) {
  if (!entry.is_link) return "";
  return entry.link_broken ? " is-link link-broken" : " is-link";
}

function entrySizeLabel(entry) {
  if (entry.is_dir) return "";
  // A symlink's own size is the length of its target text, so hide it until the
  // target size is known.
  if (entry.is_link && (entry.link_broken || entry.link_unresolved)) return "";
  return formatBytes(entry.size);
}

function entryTitle(entry) {
  if (!entry.is_link) return entry.path;
  const target = entry.link_target ? ` → ${entry.link_target}` : "";
  if (entry.link_broken) return `${entry.path}${target}（链接目标不存在）`;
  if (entry.link_unresolved) return `${entry.path}${target}（链接，正在解析目标）`;
  return `${entry.path}${target}${entry.is_dir ? "（目录链接）" : ""}`;
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

async function loadTransferTemplates() {
  try {
    state.transferTemplates = await api("/api/v1/transfer-templates");
    for (const template of state.transferTemplates) state.publishExpanded.add(template.id);
    renderPublishSidebar();
  } catch (error) {
    toast(`读取传输模板失败：${error.message}`, "error");
  }
}

function selectedTransferOptions() {
  return {
    template: null,
    concurrency: Math.max(1, Math.min(8, Number(localStorage.getItem("floe.transfer.concurrency") || 4))),
    verify: localStorage.getItem("floe.transfer.verify") !== "false",
    preserve_structure: localStorage.getItem("floe.transfer.preserve") !== "false",
    filter: localStorage.getItem("floe.transfer.filter") || "",
  };
}

function transferConflictOptions(entry, error) {
  const dialog = $("#transferOptionsDialog");
  const form = $("#transferOptionsForm");
  const payload = error?.payload || {};
  const source = payload.source || { path: entry.path, name: entry.name, size: entry.size, modified: entry.modified };
  const target = payload.target || { path: payload.target_path || entry.name, name: payload.target_path?.split("/").pop() || entry.name };
  const fileCard = (label, info, kind) => {
    const size = Number.isFinite(Number(info.size)) ? formatBytes(Number(info.size)) : "未知";
    const modified = info.modified ? formatTime(info.modified) : "未知";
    return `<section class="conflict-file-card ${kind}"><h3>${label}</h3><div class="conflict-file-name" title="${escapeHTML(info.name || info.path || "")}">${escapeHTML(info.name || info.path || "未知文件")}</div><div class="conflict-file-path">${escapeHTML(info.path || "")}</div><div class="conflict-file-meta">大小：${size}<br>更新时间：${modified}</div></section>`;
  };
  $("#transferConflictSummary").innerHTML = fileCard("源文件", source, "source") + fileCard("目标文件（已存在）", target, "target");
  state.transferConflictPolicy = "overwrite";
  form.elements.always.checked = false;
  $$("#conflictActionList button").forEach((button) => {
    const selected = button.dataset.conflictPolicy === state.transferConflictPolicy;
    button.classList.toggle("selected", selected);
    button.setAttribute("aria-selected", String(selected));
  });
  dialog.showModal();
  return new Promise((resolve) => { state.transferOptionsResolve = resolve; });
}

function closeTransferOptions(result) {
  const dialog = $("#transferOptionsDialog");
  if (dialog.open) dialog.close();
  const resolve = state.transferOptionsResolve;
  state.transferOptionsResolve = null;
  resolve?.(result);
}

async function transferEntries(fromSide, toSide, entries) {
  const uniqueEntries = [...new Map((entries || []).map((entry) => [entry.path, entry])).values()];
  if (!uniqueEntries.length) { toast("请选择文件", "error"); return; }
  const source = currentTab(fromSide);
  const target = currentTab(toSide);
  if (!source || !target) return;
  const transferable = uniqueEntries.filter((entry) => source.provider !== target.provider || entry.path !== joinPath(target.path, entry.name));
  if (!transferable.length) { toast("所选项目的源和目标位置相同", "error"); return; }
  const options = selectedTransferOptions();
  const initialPolicy = options.template?.conflict_policy || "ask";
  const create = (entry, policy = initialPolicy) => api("/api/v1/transfers", {
    method: "POST",
    body: JSON.stringify({ source_provider: source.provider, source_path: entry.path, target_provider: target.provider, target_path: joinPath(target.path, entry.name), concurrency: options.concurrency, conflict_policy: policy, verify: options.verify, preserve_structure: options.preserve_structure, filter: options.filter }),
  });
  const results = await Promise.allSettled(transferable.map((entry) => create(entry)));
  // All non-conflicting files are queued immediately. Only the rejected
  // conflict items are handled one by one so one modal never races another.
  let batchConflictPolicy = "";
  for (let index = 0; index < results.length; index++) {
    const result = results[index];
    // Treat every HTTP 409 from the transfer endpoint as a conflict. This
    // keeps the dialog compatible with older Floe backends that only returned
    // the status/message fields and omitted the newer error code.
    if (result.status !== "rejected" || result.reason?.status !== 409) continue;
    try {
      const decision = batchConflictPolicy
        ? { conflict_policy: batchConflictPolicy, always: true }
        : await transferConflictOptions(transferable[index], result.reason);
      if (!decision) {
        results[index] = { status: "fulfilled", value: await create(transferable[index], "skip") };
        continue;
      }
      if (decision.always) batchConflictPolicy = decision.conflict_policy;
      results[index] = { status: "fulfilled", value: await create(transferable[index], decision.conflict_policy) };
    } catch (reason) {
      results[index] = { status: "rejected", reason };
    }
  }
    const accepted = results.filter((result) => result.status === "fulfilled").length;
    const skipped = results.filter((result) => result.status === "fulfilled" && result.value?.status === "skipped").length;
    const started = accepted - skipped;
    const failed = results.length - accepted;
    if (started) {
      // Directory transfers create the destination directory before their
      // child tasks are queued. Reflect that server-side mkdir immediately;
      // individual files will be appended as their transfers complete.
      if (currentProvider(toSide) === target.provider && currentPath(toSide) === target.path) {
        results.forEach((result, index) => {
          if (result.status !== "fulfilled" || !result.value?.directory || !transferable[index].is_dir) return;
          upsertPanelEntry(toSide, {
            name: transferable[index].name,
            path: joinPath(target.path, transferable[index].name),
            size: 0,
            mode: "drwxr-xr-x",
            modified: new Date().toISOString(),
            is_dir: true,
            is_link: false,
          });
        });
      }
      $("#transferQueue").classList.remove("collapsed");
      state.taskFilter = "queue";
      renderTaskList();
    }
    if (failed) {
      const failedDetails = results.map((result, index) => {
        if (result.status !== "rejected") return null;
        const message = result.reason?.message || "创建任务失败";
        return `${transferable[index].name}: ${message}`;
      }).filter(Boolean);
      const prefix = started ? `已开始传输 ${started} 项，${failed} 项失败` : "传输任务创建失败";
      toast(`${prefix} · ${failedDetails.slice(0, 2).join("；")}${failedDetails.length > 2 ? "；…" : ""}`, "error");
    } else if (started) {
      const skippedText = skipped ? `，跳过 ${skipped} 项` : "";
      toast(started === 1 && !skipped ? `开始传输 ${transferable.find((_, index) => results[index].status === "fulfilled" && results[index].value?.status !== "skipped")?.name || "文件"}` : `已开始传输 ${started} 项${skippedText}`);
    } else if (skipped) {
      toast(`已跳过 ${skipped} 项`);
    }
}

async function deleteEntries(side, entries) {
  const unique = [...new Map((entries || []).map((entry) => [entry.path, entry])).values()];
  if (!unique.length) return;
  const directories = unique.filter((entry) => entry.is_dir).length;
  const promptText = unique.length === 1
    ? `确定删除“${unique[0].name}”？${unique[0].is_dir ? "\n目录及其内容会一起删除。" : ""}`
    : `确定删除选中的 ${unique.length} 项？${directories ? `\n其中包含 ${directories} 个目录，目录内容也会一起删除。` : ""}`;
  if (!confirm(promptText)) return;

  // Delete deeper paths first so selecting both a directory and one of its
  // descendants cannot make the child request fail after its parent vanished.
  const ordered = [...unique].sort((a, b) => b.path.split("/").length - a.path.split("/").length);
  const deleted = new Set();
  const failures = [];
  for (const entry of ordered) {
    try {
      await api("/api/v1/files", {
        method: "DELETE",
        body: JSON.stringify({ provider: currentProvider(side), path: entry.path }),
      });
      deleted.add(entry.path);
    } catch (error) {
      failures.push(`${entry.name}: ${error.message}`);
    }
  }
  const panel = state.panels[side];
  panel.entries = panel.entries.filter((entry) => !deleted.has(entry.path));
  for (const path of deleted) panel.selection.delete(path);
  panel.selectionAnchor = -1;
  panelElements(side).count.textContent = `${panel.entries.length.toLocaleString()} 项`;
  updateSelectionLabel(side);
  renderPanel(side);
  if (failures.length) {
    toast(`已删除 ${deleted.size} 项，${failures.length} 项失败 · ${failures.slice(0, 2).join("；")}${failures.length > 2 ? "；…" : ""}`, "error");
  } else {
    toast(unique.length === 1 ? "已删除" : `已删除 ${deleted.size} 项`);
  }
}

async function renameEntry(side, entry) {
  const nextName = prompt(entry.is_dir ? "重命名目录" : "重命名文件", entry.name)?.trim();
  if (!nextName || nextName === entry.name) return;
  if (nextName === "." || nextName === ".." || /[\\/]/.test(nextName)) {
    toast("名称不能包含 / 或 \\，也不能是 . 或 ..", "error");
    return;
  }
  const oldPath = entry.path;
  const newPath = joinPath(parentPath(oldPath), nextName);
  try {
    await api("/api/v1/files/rename", {
      method: "POST",
      body: JSON.stringify({ provider: currentProvider(side), path: oldPath, new_path: newPath }),
    });
    const panel = state.panels[side];
    const index = panel.entries.findIndex((item) => item.path === oldPath);
    if (index >= 0) panel.entries[index] = { ...panel.entries[index], name: nextName, path: newPath, modified: new Date().toISOString() };
    if (panel.selection.delete(oldPath)) panel.selection.add(newPath);
    panel.entries = sortEntries(panel.entries, panel.sort);
    updateSelectionLabel(side);
    renderPanel(side);

    // Keep tabs opened inside a renamed directory usable.
    if (entry.is_dir) {
      for (const tabSide of ["left", "right"]) {
        let changed = false;
        for (const tab of state.panels[tabSide].tabs) {
          if (tab.provider !== currentProvider(side)) continue;
          if (tab.path === oldPath || tab.path.startsWith(`${oldPath}/`)) {
            tab.path = newPath + tab.path.slice(oldPath.length);
            changed = true;
          }
        }
        if (changed) { renderTabs(tabSide); loadPanel(tabSide); }
      }
      saveWorkspace();
    }
    toast(`已重命名为“${nextName}”`);
  } catch (error) { toast(error.message, "error"); }
}

async function createDirectory(side) {
  const name = prompt("目录名称")?.trim(); if (!name) return;
  if (name === "." || name === ".." || /[\\/]/.test(name)) {
    toast("目录名称不能包含 / 或 \\，也不能是 . 或 ..", "error");
    return;
  }
  const directoryPath = joinPath(currentPath(side), name);
  try {
    await api("/api/v1/files/mkdir", { method: "POST", body: JSON.stringify({ provider: currentProvider(side), path: directoryPath }) });
    upsertPanelEntry(side, {
      name, path: directoryPath, size: 0, mode: "drwxr-xr-x",
      modified: new Date().toISOString(), is_dir: true, is_link: false,
    });
    toast(`目录“${name}”已创建`);
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
  const selection = selectedEntries(side);
  const selected = selection.length ? selection : [entry];
  const transferLabel = selected.length > 1
    ? `${side === "left" ? "上传" : "下载"}选中的 ${selection.length} 项`
    : side === "left" ? "上传到右侧" : "下载到左侧";
  const items = [
    { label: transferLabel, action: () => transferEntries(side, side === "left" ? "right" : "left", selected) },
    { label: "打开", action: () => openEntry(side, state.panels[side].entries.indexOf(entry)) },
    { separator: true },
    { label: "复制路径", action: () => copyText(entryDisplayPath(side, entry), "路径已复制") },
  ];
  if (["ftp", "sftp"].includes(provider?.kind)) items.push({ label: "复制 URL", action: () => copyEntryURL(side, entry) });
  const templateTasks = selected.map((item) => ({
    source_provider: currentProvider(side),
    source_path: item.path,
    target_provider: currentProvider(side === "left" ? "right" : "left"),
    target_path: joinPath(currentPath(side === "left" ? "right" : "left"), item.name),
    conflict_policy: "overwrite", concurrency: 4, verify: true, preserve_structure: true, filter: "",
  }));
  items.push(
    { separator: true },
    { label: selected.length > 1 ? "保存选中的 " + selected.length + " 项为模板…" : "保存为模板…", action: () => saveTemplateTaskSet(templateTasks) },
    { label: "添加到模板", action: () => appendTasksToTemplate(templateTasks) },
  );
  items.push(
    { separator: true },
    ...(selected.length === 1 ? [{ label: "重命名", action: () => renameEntry(side, selected[0]) }] : []),
    { label: selected.length > 1 ? `删除选中的 ${selected.length} 项` : "删除", danger: true, action: () => deleteEntries(side, selected) },
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
    button.disabled = Boolean(item.disabled);
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
      if (source.side !== side) {
        const paths = new Set(source.paths || []);
        transferEntries(source.side, side, state.panels[source.side].entries.filter((entry) => paths.has(entry.path)));
      }
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

async function openEditor(side, entry, providerID = "") {
  const tab = currentTab(side);
  providerID = providerID || tab?.provider || "";
  if (!providerID) return;
  const loaded = state.preview.items.find((item) => item.kind === "text" && item.provider === providerID && item.path === entry.path && item.editor);
  const item = openPreview("text", providerID, entry.path);
  if (loaded) {
    // Already open in another tab, so bring it forward instead of re-reading it.
    applyPreview(item);
    $("#editorContent").focus();
    return;
  }
  clearTimeout(editorPreviewTimer);
  closeEditorFind();
  state.editor = null;
  const previewKind = isMarkdownPath(entry.path) ? "markdown" : (isHTMLPath(entry.path) ? "html" : "");
  setDocumentPreview(false, previewKind);
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
  try {
    const result = await api(`/api/v1/files/content?provider=${encodeURIComponent(providerID)}&path=${encodeURIComponent(entry.path)}`);
    const loadedEditor = {
	  provider: providerID, path: entry.path, etag: result.etag, side, language: syntaxForPath(entry.path),
	  originalContent: result.content, dirty: false, saving: false, encoding: result.encoding || "utf-8",
	  bom: Boolean(result.bom), newline: result.newline || "lf", mixedNewlines: Boolean(result.mixed_newlines),
	  size: result.size ?? new TextEncoder().encode(result.content).length, lineCount: 0, matchCase: false,
	  previewKind, previewOpen: false, previewFocus: false, htmlPreviewToken: "", htmlPreviewSequence: 0,
	  htmlViewportMode: "responsive", htmlViewportWidth: 1280, htmlViewportHeight: 720, htmlViewportScale: 1,
	};
    item.editor = loadedEditor;
    item.text = { content: result.content, selectionStart: 0, selectionEnd: 0, scrollTop: 0, syntax: "auto" };
    // The user may have switched to another tab while the file was loading.
    if (state.preview.activeID !== item.id) return;
    state.editor = loadedEditor;
    $("#editorTitle").textContent = entry.path;
	$("#editorTitle").title = entry.path;
    $("#editorContent").value = result.content;
	$("#syntaxMode").value = "auto";
	refreshEditorDisplay();
    $("#editorContent").focus();
  } catch (error) {
    if (state.preview.activeID === item.id) {
      setDocumentPreview(false, "");
      $("#editorState").textContent = `读取失败：${error.message}`;
    }
    toast(error.message, "error");
  }
}

function isMarkdownPath(filePath) {
  return ["md", "markdown"].includes(fileExtension(filePath));
}

function isHTMLPath(filePath) {
  return ["html", "htm"].includes(fileExtension(filePath));
}

function setDocumentPreview(open, kind = state.editor?.previewKind || "") {
  open = Boolean(open && kind);
  if (state.editor) state.editor.previewOpen = open;
  const dialog = $("#editorDialog"), button = $("#editorPreviewToggle");
  if (!open) {
    clearTimeout(editorPreviewTimer);
    if (state.editor) state.editor.previewFocus = false;
    dialog.classList.remove("preview-focus");
    releaseHTMLPreview(state.editor);
  }
  dialog.classList.toggle("preview-open", open);
  button.classList.toggle("hidden", !kind);
  button.classList.toggle("active", open);
  button.setAttribute("aria-pressed", String(open));
  const label = kind === "html" ? "HTML" : "Markdown";
  button.setAttribute("aria-label", open ? `关闭 ${label} 预览` : `打开 ${label} 预览`);
  button.title = open ? `关闭 ${label} 预览` : `打开 ${label} 预览`;
  $("#editorPreview").classList.toggle("hidden", kind !== "markdown");
  $("#htmlPreview").classList.toggle("hidden", kind !== "html");
  if (!open) {
    $("#editorPreview").replaceChildren();
    $("#htmlPreviewFrameContent").src = "about:blank";
    return;
  }
  if (kind === "markdown") renderMarkdownPreview();
  if (kind === "html") {
    updateHTMLViewportControls();
    requestAnimationFrame(() => { fitHTMLViewport(); renderHTMLPreview(); });
  }
}

function queueDocumentPreview() {
  if (!state.editor?.previewOpen) return;
  clearTimeout(editorPreviewTimer);
  const render = state.editor.previewKind === "html" ? renderHTMLPreview : renderMarkdownPreview;
  editorPreviewTimer = setTimeout(render, state.editor.previewKind === "html" ? 300 : 150);
}

function normalizedMarkdownPath(documentPath, resource) {
  let value = resource.split(/[?#]/, 1)[0];
  try { value = decodeURIComponent(value); } catch (_) {}
  value = value.replaceAll("\\", "/");
  const base = value.startsWith("/") ? value : `${parentPath(documentPath)}/${value}`;
  const parts = [];
  for (const part of base.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") parts.pop();
    else parts.push(part);
  }
  return `/${parts.join("/")}`;
}

function localMarkdownResource(value) {
  return value && !value.startsWith("#") && !value.startsWith("//") && !/^[a-z][a-z\d+.-]*:/i.test(value);
}

function renderMarkdownPreview() {
  if (state.editor?.previewKind !== "markdown" || !state.editor.previewOpen) return;
  const preview = $("#editorPreview"), source = $("#editorContent").value;
  preview.replaceChildren();
  if (!source.trim()) {
    const empty = document.createElement("div");
    empty.className = "markdown-preview-empty";
    empty.textContent = "文档为空，左侧输入内容后将在这里显示预览。";
    preview.append(empty);
    return;
  }
  try {
    if (!window.marked?.parse || !window.DOMPurify?.sanitize) throw new Error("Markdown 渲染组件未加载");
    const documentNode = document.createElement("div");
    documentNode.className = "markdown-document";
    const rendered = window.marked.parse(source, { gfm: true, breaks: false });
    documentNode.innerHTML = window.DOMPurify.sanitize(rendered, {
      USE_PROFILES: { html: true }, FORBID_TAGS: ["form", "iframe", "object", "embed"], FORBID_ATTR: ["style"],
    });
    for (const image of $$("img[src]", documentNode)) {
      const value = image.getAttribute("src");
      if (!localMarkdownResource(value)) continue;
      const path = normalizedMarkdownPath(state.editor.path, value);
      image.src = `/api/v1/files/raw?provider=${encodeURIComponent(state.editor.provider)}&path=${encodeURIComponent(path)}`;
      image.loading = "lazy";
      image.decoding = "async";
    }
    for (const link of $$("a[href]", documentNode)) {
      const value = link.getAttribute("href");
      if (/^https?:/i.test(value)) {
        link.target = "_blank";
        link.rel = "noopener noreferrer";
      } else if (localMarkdownResource(value)) {
        link.dataset.markdownPath = normalizedMarkdownPath(state.editor.path, value);
        link.href = "#";
      }
    }
    preview.append(documentNode);
  } catch (error) {
    const message = document.createElement("div");
    message.className = "markdown-preview-error";
    message.textContent = `Markdown 预览失败：${error.message}`;
    preview.append(message);
  }
}

function openMarkdownPreviewLink(event) {
  const link = event.target.closest("a[data-markdown-path]");
  if (!link || !state.editor) return;
  event.preventDefault();
  const active = state.editor;
  openEditor(active.side, { path: link.dataset.markdownPath }, active.provider);
}

async function renderHTMLPreview() {
  const active = state.editor;
  if (active?.previewKind !== "html" || !active.previewOpen) return;
  const sequence = ++active.htmlPreviewSequence;
  const message = $("#htmlPreviewMessage");
  message.textContent = "正在渲染 HTML…";
  message.classList.remove("hidden");
  try {
    const result = await api("/api/v1/files/html-preview", {
      method: "POST",
      body: JSON.stringify({
        provider: active.provider, path: active.path, content: $("#editorContent").value,
        token: active.htmlPreviewToken,
      }),
    });
    if (state.editor !== active || sequence !== active.htmlPreviewSequence || !active.previewOpen) return;
    active.htmlPreviewToken = result.token;
    $("#htmlPreviewFrameContent").src = `${result.url}?render=${sequence}`;
  } catch (error) {
    if (state.editor !== active || sequence !== active.htmlPreviewSequence) return;
    message.textContent = `HTML 预览失败：${error.message}`;
    message.classList.remove("hidden");
  }
}

function releaseHTMLPreview(editor) {
  const token = editor?.htmlPreviewToken;
  if (!token) return;
  editor.htmlPreviewToken = "";
  fetch(`/api/v1/files/html-preview/${encodeURIComponent(token)}`, { method: "DELETE", keepalive: true }).catch(() => {});
}

const htmlViewportPresets = {
  phone: [375, 812], tablet: [768, 1024], desktop: [1280, 720],
};

function setHTMLViewportPreset(mode) {
  if (state.editor?.previewKind !== "html") return;
  state.editor.htmlViewportMode = mode;
  if (htmlViewportPresets[mode]) {
    [state.editor.htmlViewportWidth, state.editor.htmlViewportHeight] = htmlViewportPresets[mode];
  }
  updateHTMLViewportControls();
  requestAnimationFrame(fitHTMLViewport);
}

function updateHTMLViewportControls() {
  if (state.editor?.previewKind !== "html") return;
  const responsive = state.editor.htmlViewportMode === "responsive";
  $("#htmlViewportPreset").value = state.editor.htmlViewportMode;
  $("#htmlViewportWidth").disabled = responsive;
  $("#htmlViewportHeight").disabled = responsive;
  if (!responsive) {
    $("#htmlViewportWidth").value = state.editor.htmlViewportWidth;
    $("#htmlViewportHeight").value = state.editor.htmlViewportHeight;
  }
  $("#htmlPreview").classList.toggle("responsive", responsive);
}

function fitHTMLViewport() {
  if (state.editor?.previewKind !== "html" || !state.editor.previewOpen) return;
  const stage = $("#htmlPreviewStage"), frame = $("#htmlPreviewFrame"), iframe = $("#htmlPreviewFrameContent");
  const availableWidth = Math.max(1, stage.clientWidth - 24), availableHeight = Math.max(1, stage.clientHeight - 24);
  if (state.editor.htmlViewportMode === "responsive") {
    frame.style.width = `${availableWidth}px`;
    frame.style.height = `${availableHeight}px`;
    iframe.style.width = `${availableWidth}px`;
    iframe.style.height = `${availableHeight}px`;
    iframe.style.transform = "none";
    $("#htmlViewportWidth").value = Math.round(availableWidth);
    $("#htmlViewportHeight").value = Math.round(availableHeight);
    $("#htmlViewportScale").textContent = "自适应";
    state.editor.htmlViewportScale = 1;
    return;
  }
  const width = state.editor.htmlViewportWidth, height = state.editor.htmlViewportHeight;
  const scale = Math.min(1, availableWidth / width, availableHeight / height);
  state.editor.htmlViewportScale = scale;
  frame.style.width = `${Math.max(1, width * scale)}px`;
  frame.style.height = `${Math.max(1, height * scale)}px`;
  iframe.style.width = `${width}px`;
  iframe.style.height = `${height}px`;
  iframe.style.transform = `scale(${scale})`;
  $("#htmlViewportScale").textContent = `${Math.round(scale * 100)}%`;
}

function applyCustomHTMLViewport() {
  if (state.editor?.previewKind !== "html") return;
  const width = Math.min(3840, Math.max(240, Number($("#htmlViewportWidth").value) || 240));
  const height = Math.min(2160, Math.max(200, Number($("#htmlViewportHeight").value) || 200));
  state.editor.htmlViewportMode = "custom";
  state.editor.htmlViewportWidth = width;
  state.editor.htmlViewportHeight = height;
  updateHTMLViewportControls();
  fitHTMLViewport();
}

function rotateHTMLViewport() {
  if (state.editor?.previewKind !== "html") return;
  if (state.editor.htmlViewportMode === "responsive") {
    state.editor.htmlViewportMode = "custom";
    state.editor.htmlViewportWidth = Number($("#htmlViewportHeight").value) || 720;
    state.editor.htmlViewportHeight = Number($("#htmlViewportWidth").value) || 1280;
  } else {
    [state.editor.htmlViewportWidth, state.editor.htmlViewportHeight] = [state.editor.htmlViewportHeight, state.editor.htmlViewportWidth];
    state.editor.htmlViewportMode = "custom";
  }
  updateHTMLViewportControls();
  fitHTMLViewport();
}

function toggleHTMLPreviewFocus(force) {
  if (state.editor?.previewKind !== "html" || !state.editor.previewOpen) return;
  const next = force ?? !state.editor.previewFocus;
  state.editor.previewFocus = next;
  $("#editorDialog").classList.toggle("preview-focus", next);
  $("#focusHTMLPreview").classList.toggle("active", next);
  $("#focusHTMLPreview").setAttribute("aria-pressed", String(next));
  $("#focusHTMLPreview").title = next ? "退出预览全屏" : "预览全屏";
  requestAnimationFrame(fitHTMLViewport);
}

function initializeHTMLViewportResize() {
  const handle = $("#resizeHTMLViewport");
  let start = null;
  handle.addEventListener("pointerdown", (event) => {
    if (state.editor?.previewKind !== "html") return;
    event.preventDefault();
    start = {
      x: event.clientX, y: event.clientY,
      width: state.editor.htmlViewportWidth, height: state.editor.htmlViewportHeight,
      scale: state.editor.htmlViewportScale || 1,
    };
    handle.setPointerCapture(event.pointerId);
  });
  handle.addEventListener("pointermove", (event) => {
    if (!start || !handle.hasPointerCapture(event.pointerId) || !state.editor) return;
    state.editor.htmlViewportMode = "custom";
    state.editor.htmlViewportWidth = Math.min(3840, Math.max(240, Math.round(start.width + (event.clientX - start.x) / start.scale)));
    state.editor.htmlViewportHeight = Math.min(2160, Math.max(200, Math.round(start.height + (event.clientY - start.y) / start.scale)));
    updateHTMLViewportControls();
    fitHTMLViewport();
  });
  handle.addEventListener("pointerup", (event) => {
    if (handle.hasPointerCapture(event.pointerId)) handle.releasePointerCapture(event.pointerId);
    start = null;
  });
  new ResizeObserver(() => fitHTMLViewport()).observe($("#htmlPreviewStage"));
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
	syncPreviewTabDirty();
}

function editorChanged() {
	if (!state.editor) return;
	state.editor.dirty = $("#editorContent").value !== state.editor.originalContent;
	refreshEditorDisplay();
	queueDocumentPreview();
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
	closePreviewWindow("text");
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
  state.preview.maximized = !state.preview.maximized;
  for (const selector of Object.values(PREVIEW_DIALOGS)) $(selector).classList.toggle("maximized", state.preview.maximized);
  $$("#maximizeEditor, #maximizeImage, #maximizeMedia").forEach((control) => {
    control.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">fullscreen</span>';
    control.title = state.preview.maximized ? "还原窗口" : "窗口内全屏";
  });
  if (dialog === $("#imageDialog") && state.image) requestAnimationFrame(fitImage);
  if (dialog === $("#mediaDialog")) requestAnimationFrame(fitMediaPlayer);
  if (dialog === $("#editorDialog") && state.editor?.previewKind === "html") requestAnimationFrame(fitHTMLViewport);
}

function openImage(side, entry) {
  const provider = currentProvider(side);
  const entries = state.panels[side].entries.filter((item) => !item.is_dir && isImageEntry(item));
  const index = Math.max(0, entries.findIndex((item) => item.path === entry.path));
  const item = openPreview("image", provider, entry.path);
  item.image = { ...(item.image || {}), side, provider, entries, index, zoom: item.image?.zoom || 1 };
  applyPreview(item);
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
  // Arrow keys walk the folder, so the tab has to follow the picture on screen.
  const item = activePreview();
  if (item?.kind === "image" && item.path !== entry.path) {
    item.path = entry.path;
    item.name = entry.name;
    renderPreviewTabs();
  }
}

function fitImage() {
  if (!state.image) return;
  const image = $("#imagePreview"), stage = $("#imageStage");
  if (!image.naturalWidth || !image.naturalHeight) return;
  const zoom = Math.min(1, (stage.clientWidth - 40) / image.naturalWidth, (stage.clientHeight - 40) / image.naturalHeight);
  setImageZoom(Math.max(0.1, zoom));
}

function fitMediaPlayer() {
  const player = $("#mediaPlayer"), stage = $(".media-stage");
  if (!player.videoWidth || !player.videoHeight || !stage.clientWidth || !stage.clientHeight) return;
  const scale = Math.min(1, stage.clientWidth / player.videoWidth, stage.clientHeight / player.videoHeight);
  player.style.width = `${Math.max(1, Math.round(player.videoWidth * scale))}px`;
  player.style.height = `${Math.max(1, Math.round(player.videoHeight * scale))}px`;
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
  const provider = currentProvider(side);
  const item = openPreview("media", provider, entry.path);
  item.media = {
    ...(item.media || {}),
    source: mediaURL(provider, entry.path),
    hls: ["m3u8", "m3u"].includes(fileExtension(entry.name)),
    currentTime: item.media?.currentTime || 0,
    playing: item.media?.playing !== false,
  };
  startMediaSource(item);
}

// Loads (or reloads) one media tab into the single <video> element, seeking back to
// where the tab was when it lost the player.
function startMediaSource(item) {
  closeMedia(false);
  const loadSequence = mediaLoadSequence;
  const player = $("#mediaPlayer");
  const message = $("#mediaMessage");
  const source = item.media.source;
  const resumeAt = item.media.currentTime || 0;
  const autoplay = item.media.playing !== false;
  player.style.width = "100%";
  player.style.height = "100%";
  $("#mediaTitle").textContent = item.path;
  $("#mediaTitle").title = item.path;
  $("#mediaState").textContent = "正在加载";
  message.classList.remove("visible");
  message.textContent = "";
  const fallbackLabel = item.media.hls ? "HLS · 点击播放" : "MP4 · 点击播放";
  // currentTime only sticks once the metadata is in, so seek on that event rather
  // than straight after assigning the source.
  player.addEventListener("loadedmetadata", () => {
    if (loadSequence !== mediaLoadSequence || activePreview()?.id !== item.id) return;
    fitMediaPlayer();
    if (resumeAt > 0.5 && Number.isFinite(player.duration) && resumeAt < player.duration) {
      try { player.currentTime = resumeAt; } catch (_) { /* seeking is best effort */ }
    }
    if (autoplay) player.play().catch(() => { $("#mediaState").textContent = fallbackLabel; });
  }, { once: true });
  if (item.media.hls) {
    if (window.Hls?.isSupported()) {
      const hls = new window.Hls({ enableWorker: true, lowLatencyMode: true, backBufferLength: 60, startPosition: resumeAt > 0.5 ? resumeAt : -1 });
      state.hls = hls;
      hls.loadSource(source);
      hls.attachMedia(player);
      hls.on(window.Hls.Events.MANIFEST_PARSED, () => {
        if (loadSequence !== mediaLoadSequence || activePreview()?.id !== item.id) return;
        $("#mediaState").textContent = "HLS · 已就绪";
        if (autoplay) player.play().catch(() => { $("#mediaState").textContent = "HLS · 点击播放"; });
      });
      hls.on(window.Hls.Events.ERROR, (_event, data) => {
        if (loadSequence !== mediaLoadSequence || activePreview()?.id !== item.id) return;
        if (!data.fatal) return;
        const detail = `${data.type || "HLS"} / ${data.details || "未知错误"}`;
        showMediaError(`M3U8 播放失败：${detail}`);
        reportClientLog("error", "media", "M3U8 播放失败", `${item.path} · ${detail}`);
        if (data.type === window.Hls.ErrorTypes.NETWORK_ERROR) hls.startLoad();
        else if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError();
      });
      return;
    }
    if (player.canPlayType("application/vnd.apple.mpegurl")) {
      player.src = source;
      return;
    }
    showMediaError("当前浏览器不支持 HLS/M3U8 播放");
    return;
  }
  player.src = source;
}

function showMediaError(message) {
  $("#mediaState").textContent = "播放失败";
  $("#mediaMessage").textContent = message;
  $("#mediaMessage").classList.add("visible");
  toast(message, "error");
}

function closeMedia(closeDialog = true) {
  mediaLoadSequence++;
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

const PREVIEW_DIALOGS = { text: "#editorDialog", image: "#imageDialog", media: "#mediaDialog" };
const PREVIEW_KIND_LABELS = { text: "文本", image: "图片", media: "视频" };
let previewSequence = 0;
let previewMediaResume = false;
let mediaLoadSequence = 0;

function previewDialogFor(kind) { return PREVIEW_DIALOGS[kind] ? $(PREVIEW_DIALOGS[kind]) : null; }
function findPreview(id) { return state.preview.items.find((item) => item.id === id); }
function activePreview() { return findPreview(state.preview.activeID); }
function previewHosts() { return $("#previewHosts"); }
function previewName(filePath) { return filePath.split("/").filter(Boolean).pop() || filePath; }

// close() queues its `close` event as a task instead of firing it synchronously, so
// a plain flag would already be cleared by the time the listener runs. Mark the
// dialog itself and let the listener consume the mark.
function closeForRelocation(dialog) {
  if (!dialog?.open) return;
  dialog.dataset.previewRelocating = "1";
  dialog.close();
}

// Opening a file adds a tab in whichever host the user last chose, so several
// files can be previewed at once in the window as well as in the task area.
function openPreview(kind, provider, path) {
  let item = state.preview.items.find((entry) => entry.kind === kind && entry.provider === provider && entry.path === path);
  const outgoing = activePreview();
  if (!item) {
    item = { id: `preview-${++previewSequence}`, kind, provider, path, name: previewName(path), editor: null, text: null, image: null, media: null };
    state.preview.items.push(item);
  }
  item.provider = provider;
  // Capture before the caller starts writing the new file into the shared dialog.
  if (outgoing && outgoing.id !== item.id) capturePreview(outgoing);
  // The dialog still holds the outgoing file, so point the live state at the
  // incoming one before the host switch runs any fit or re-render pass.
  state.editor = kind === "text" ? item.editor || null : null;
  state.image = kind === "image" ? item.image || null : null;
  state.preview.activeID = item.id;
  hostPreviewDialog(kind);
  renderPreviewTabs();
  return item;
}

function activatePreview(id) {
  const item = findPreview(id);
  if (!item) return;
  const outgoing = activePreview();
  if (outgoing?.id === item.id) { hostPreviewDialog(item.kind); return; }
  if (outgoing) capturePreview(outgoing);
  state.preview.activeID = item.id;
  hostPreviewDialog(item.kind);
  applyPreview(item);
  renderPreviewTabs();
}

function hostPreviewDialog(kind) {
  if (state.preview.host === "panel") { mountPreviewInPanel(kind); return; }
  const dialog = previewDialogFor(kind);
  for (const otherKind of Object.keys(PREVIEW_DIALOGS)) {
    if (otherKind === kind) continue;
    const other = previewDialogFor(otherKind);
    if (other?.open) closeForRelocation(other);
  }
  if (!dialog) return;
  dialog.classList.toggle("maximized", state.preview.maximized);
  if (dialog.parentElement === previewHosts()) {
    closeForRelocation(dialog);
    document.body.appendChild(dialog);
  }
  if (!dialog.open) dialog.showModal();
}

// There is one dialog per kind, so a tab switch means saving what the dialog holds
// now and putting the other file's state back afterwards.
function capturePreview(item, preserveMediaPlayback = false) {
  if (!item) return;
  if (item.kind === "text") {
    if (state.editor) item.editor = state.editor;
    const editor = $("#editorContent");
    item.text = { content: editor.value, selectionStart: editor.selectionStart, selectionEnd: editor.selectionEnd, scrollTop: editor.scrollTop, syntax: $("#syntaxMode").value };
  } else if (item.kind === "image") {
    if (state.image) item.image = state.image;
  } else if (item.kind === "media" && item.media) {
    const player = $("#mediaPlayer");
    item.media = { ...item.media, currentTime: player.currentTime || 0, playing: preserveMediaPlayback && !player.paused };
    // One <video> element serves every tab, so the outgoing stream is torn down and
    // rebuilt from the saved position when its tab comes back.
    closeMedia(false);
  }
}

function applyPreview(item) {
  if (!item) return;
  if (item.kind !== "text") state.editor = null;
  if (item.kind !== "image") state.image = null;
  if (item.kind === "text") applyTextPreview(item);
  else if (item.kind === "image") applyImagePreview(item);
  else if (item.kind === "media") applyMediaPreview(item);
}

function applyTextPreview(item) {
  clearTimeout(editorPreviewTimer);
  closeEditorFind();
  state.editor = item.editor || null;
  const editor = $("#editorContent");
  editor.value = item.text?.content ?? "";
  $("#syntaxMode").value = item.text?.syntax || "auto";
  $("#editorTitle").textContent = item.path;
  $("#editorTitle").title = item.path;
  if (!state.editor) {
    $("#editorHighlight").textContent = "";
    $("#editorLineNumbers").textContent = "1";
    $("#editorState").textContent = "正在读取…";
    $("#saveEditor").disabled = true;
    setDocumentPreview(false, isMarkdownPath(item.path) ? "markdown" : (isHTMLPath(item.path) ? "html" : ""));
    return;
  }
  setDocumentPreview(state.editor.previewOpen, state.editor.previewKind);
  // The cached count belongs to the file that was on screen a moment ago.
  state.editor.lineCount = 0;
  refreshEditorDisplay();
  if (item.text) {
    editor.setSelectionRange(item.text.selectionStart, item.text.selectionEnd);
    editor.scrollTop = item.text.scrollTop;
    syncEditorScroll();
  }
}

function applyImagePreview(item) {
  state.image = item.image || null;
  if (state.image) showImageAt(state.image.index);
}

function applyMediaPreview(item) {
  if (!item.media?.source) return;
  startMediaSource(item);
}

function mountPreviewInPanel(kind) {
  const dialog = previewDialogFor(kind), hosts = previewHosts();
  if (!dialog) return;
  state.preview.host = "panel";
  for (const other of Object.keys(PREVIEW_DIALOGS)) {
    const parked = previewDialogFor(other);
    if (other !== kind && parked.open) closeForRelocation(parked);
    if (other !== kind && parked.parentElement === hosts) document.body.appendChild(parked);
  }
  if (dialog.parentElement !== hosts) {
    closeForRelocation(dialog);
    hosts.appendChild(dialog);
  }
  if (!dialog.open) dialog.show();
  state.taskFilter = "preview";
  const queue = $("#transferQueue");
  queue.classList.remove("collapsed");
  const mainArea = $(".main-area");
  const height = parseInt(getComputedStyle(mainArea).getPropertyValue("--queue-height"), 10) || 0;
  // A preview needs far more room than a task row, so raise the floor once.
  if (height < 300) mainArea.style.setProperty("--queue-height", "340px");
  renderPreviewTabs();
  renderTaskList();
  restorePreviewViewState(kind);
}

// Moving the preview between window and tab mode keeps the same tab collection;
// only the active kind's shared dialog changes parent and modal state.
function setPreviewHost(host) {
  const item = activePreview();
  if (!item) return;
  const restoreMedia = item.kind === "media" && Boolean(item.media?.source);
  if (restoreMedia) capturePreview(item, true);
  if (host === "panel") {
    mountPreviewInPanel(item.kind);
    if (restoreMedia) applyMediaPreview(item);
    return;
  }
  state.preview.host = "dialog";
  // Every parked dialog goes home too, so none is left orphaned and closed inside
  // the panel while its tab still lists it.
  for (const parked of [...previewHosts().children]) {
    closeForRelocation(parked);
    document.body.appendChild(parked);
  }
  hostPreviewDialog(item.kind);
  if (state.taskFilter === "preview") state.taskFilter = "queue";
  renderPreviewTabs();
  renderTaskList();
  restorePreviewViewState(item.kind);
  if (restoreMedia) applyMediaPreview(item);
}

// Reparenting reloads an iframe and can interrupt a video, so the view state that
// does not survive the move is rebuilt here.
function restorePreviewViewState(kind) {
  if (kind === "image" && state.image) requestAnimationFrame(fitImage);
  if (kind === "text" && state.editor?.previewOpen && state.editor.previewKind === "html") {
    renderHTMLPreview();
    requestAnimationFrame(fitHTMLViewport);
  }
  if (kind === "media") {
    requestAnimationFrame(fitMediaPlayer);
    updatePreviewVisibility();
  }
}

function previewVisible() {
  return state.preview.host === "panel" && state.taskFilter === "preview" && !$("#transferQueue").classList.contains("collapsed");
}

// A preview tab the user cannot see must not keep streaming, but the buffer is
// kept so playback resumes exactly where it stopped.
function updatePreviewVisibility() {
  if (state.preview.host !== "panel" || activePreview()?.kind !== "media") return;
  if (!previewVisible()) pauseActiveMedia();
}

function pauseActiveMedia() {
  const item = activePreview();
  if (item?.kind !== "media") return;
  const player = $("#mediaPlayer");
  if (item.media) item.media = { ...item.media, currentTime: player.currentTime || item.media.currentTime || 0, playing: false };
  previewMediaResume = false;
  if (!player.paused) player.pause();
  if ($("#mediaState").textContent === "正在播放") $("#mediaState").textContent = "已暂停";
}

function parkPreviewDialog(kind) {
  const dialog = previewDialogFor(kind);
  if (!dialog) return;
  closeForRelocation(dialog);
  if (dialog.parentElement === previewHosts()) document.body.appendChild(dialog);
}

function closePreviewItem(id) {
  const item = findPreview(id);
  if (!item) return;
  const active = state.preview.activeID === item.id;
  const editor = active && item.kind === "text" ? state.editor : item.editor;
  if (item.kind === "text") {
    if (editor?.saving) { toast("文件正在保存，请稍候"); return; }
    if (editor?.dirty && !confirm(`${item.name} 尚未保存，确定不保存并关闭？`)) return;
  }
  if (active) {
    if (item.kind === "text") { clearTimeout(editorPreviewTimer); setDocumentPreview(false, ""); state.editor = null; }
    else if (item.kind === "image") state.image = null;
    else if (item.kind === "media") { closeMedia(false); previewMediaResume = false; }
  }
  releaseHTMLPreview(editor);
  state.preview.items = state.preview.items.filter((entry) => entry.id !== item.id);
  if (!state.preview.items.some((entry) => entry.kind === item.kind)) parkPreviewDialog(item.kind);
  if (!active) { renderPreviewTabs(); return; }
  const next = state.preview.items[0];
  state.preview.activeID = next?.id || "";
  if (next) {
    hostPreviewDialog(next.kind);
    applyPreview(next);
  }
  renderPreviewTabs();
}

function closePreviewWindow(kind) {
  const active = activePreview();
  if (active) capturePreview(active);
  const editors = state.preview.items.map((item) => item.editor).filter(Boolean);
  if (editors.some((editor) => editor.saving)) { toast("文件正在保存，请稍候"); return; }
  if (editors.some((editor) => editor.dirty) && !confirm("打开的文件有未保存的修改，确定全部关闭？")) return;
  for (const previewKind of Object.keys(PREVIEW_DIALOGS)) {
    const dialog = previewDialogFor(previewKind);
    if (dialog?.open) {
      dialog.dataset.previewWindowClosing = "1";
      dialog.close();
    }
    if (dialog?.parentElement === previewHosts()) document.body.appendChild(dialog);
  }
  clearTimeout(editorPreviewTimer);
  if (active?.kind === "text") {
    setDocumentPreview(false, "");
    state.editor = null;
  } else if (active?.kind === "image") {
    state.image = null;
  } else if (active?.kind === "media") {
    previewMediaResume = false;
  }
  for (const item of state.preview.items) releaseHTMLPreview(item.editor);
  state.preview.items = [];
  state.preview.activeID = "";
  state.preview.host = "dialog";
  state.preview.maximized = false;
  renderPreviewTabs();
  if (state.taskFilter === "preview") state.taskFilter = "queue";
  renderTaskList();
}

function renderPreviewTabs() {
  const items = state.preview.items;
  const tab = $('.task-tab[data-task-filter="preview"]');
  tab.hidden = !items.length;
  $("#previewCount").textContent = String(items.length);
  const strips = $$('[data-preview-strip]');
  if (!items.length) {
    strips.forEach((strip) => { strip.innerHTML = ""; });
    updatePreviewTabNavigation();
    if (state.taskFilter === "preview") { state.taskFilter = "queue"; renderTaskList(); }
    return;
  }
  const markup = items.map((item) => {
    const active = item.id === state.preview.activeID;
    const dirty = item.kind === "text" && (active ? state.editor : item.editor)?.dirty === true;
    return `<button type="button" class="preview-tab${active ? " active" : ""}" data-preview-id="${item.id}" title="${escapeHTML(item.path)}"><span class="preview-tab-kind">${escapeHTML(PREVIEW_KIND_LABELS[item.kind])}</span><b>${escapeHTML(item.name)}</b><em class="preview-dirty"${dirty ? "" : " hidden"}>●</em><i data-preview-close="${item.id}" title="关闭">×</i></button>`;
  }).join("");
  strips.forEach((strip) => { strip.innerHTML = markup; });
  ensureActivePreviewTabVisible();
  updatePreviewTabNavigation();
}

function ensureActivePreviewTabVisible() {
  $$('[data-preview-strip]').forEach((tabs) => {
    const active = tabs.querySelector('.preview-tab.active');
    if (!active || !tabs.clientWidth) return;
    const left = active.offsetLeft;
    const right = left + active.offsetWidth;
    if (left < tabs.scrollLeft) tabs.scrollLeft = left;
    else if (right > tabs.scrollLeft + tabs.clientWidth) tabs.scrollLeft = right - tabs.clientWidth;
  });
}

function updatePreviewTabNavigation() {
  $$('[data-preview-tab-strip]').forEach((strip) => {
    const tabs = strip.querySelector('[data-preview-strip]');
    const previous = strip.querySelector('[data-preview-nav="previous"]');
    const next = strip.querySelector('[data-preview-nav="next"]');
    if (!tabs || !previous || !next) return;
    const overflowing = tabs.scrollWidth > tabs.clientWidth + 1;
    previous.disabled = !overflowing || tabs.scrollLeft <= 1;
    next.disabled = !overflowing || tabs.scrollLeft + tabs.clientWidth >= tabs.scrollWidth - 1;
  });
}

function handlePreviewTabNavigation(event) {
  const button = event.target.closest('[data-preview-nav]');
  if (!button || button.disabled) return;
  const tabs = button.parentElement.querySelector('[data-preview-strip]');
  if (!tabs) return;
  const direction = button.dataset.previewNav === "next" ? 1 : -1;
  tabs.scrollLeft += direction * Math.max(140, tabs.clientWidth * .7);
  window.setTimeout(updatePreviewTabNavigation, 220);
}

// Called from the editor status refresh, so the tab marker tracks typing without
// rebuilding the whole strip on every keystroke.
function syncPreviewTabDirty() {
  const item = activePreview();
  if (item?.kind !== "text") return;
  $$(`[data-preview-strip] .preview-tab[data-preview-id="${item.id}"] .preview-dirty`).forEach((marker) => {
    marker.hidden = !state.editor?.dirty;
  });
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
  const taskIDs = new Set(tasks.map((task) => task.id));
  for (const id of state.taskSelection) if (!taskIDs.has(id)) state.taskSelection.delete(id);
  const queue = tasks.filter((task) => taskCategory(task) === "queue");
  const success = tasks.filter((task) => taskCategory(task) === "success");
  const failed = tasks.filter((task) => taskCategory(task) === "failed");
  const active = queue.filter((task) => ["running", "verifying"].includes(task.status)).length;
  if (active > 0) {
    $("#transferQueue").classList.remove("collapsed");
    if (state.taskFilter !== "memories") state.taskFilter = "queue";
  }
  $("#queueCount").textContent = queue.length;
  $("#successCount").textContent = success.length;
  $("#failedCount").textContent = failed.length;
  for (const task of tasks) {
    const previous = state.taskStatus.get(task.id);
    if (task.status === "completed" && previous && previous !== "completed") {
      appendCompletedEntry(task);
    }
    state.taskStatus.set(task.id, task.status);
  }
  renderTaskList();
  renderPublishSidebar();
}

// A completed transfer changes only one directory entry. Re-reading the
// entire remote directory for every completed file causes a large batch to
// keep resetting the loading view, and a slow LIST request can hide files
// that have already arrived. Update the visible target directory in place.
function appendCompletedEntry(task) {
  const targetPath = task.target_path || "";
  const name = targetPath.split("/").filter(Boolean).pop();
  if (!name) return;
  const parent = parentPath(targetPath);
  for (const side of ["left", "right"]) {
    const tab = currentTab(side);
    if (!tab || tab.provider !== task.target_provider || tab.path !== parent) continue;
    upsertPanelEntry(side, {
      name,
      path: targetPath,
      size: task.size || 0,
      mode: "-rw-r--r--",
      modified: task.updated_at || new Date().toISOString(),
      is_dir: false,
      is_link: false,
    });
  }
}

function upsertPanelEntry(side, entry) {
  const panel = state.panels[side];
  const index = panel.entries.findIndex((item) => item.path === entry.path);
  if (index >= 0) panel.entries[index] = { ...panel.entries[index], ...entry };
  else panel.entries.push(entry);
  panel.entries = sortEntries(panel.entries, panel.sort);
  panelElements(side).count.textContent = `${panel.entries.length.toLocaleString()} 项`;
  updateSelectionLabel(side);
  renderPanel(side);
}

function updateTransferMetric(task, sampledAt) {
  const bytes = Math.max(task.bytes_verified || 0, task.bytes_transferred || 0);
  // Read/write counters are physical I/O and can differ when a provider
  // retries a block or when verification is performed. Keep a separate
  // smoothed rate for each direction instead of presenting one ambiguous
  // aggregate speed.
  const readBytes = task.bytes_read || bytes;
  const writeBytes = task.bytes_written || bytes;
  const previous = state.transferMetrics.get(task.id);
  let readSpeed = previous?.readSpeed || 0;
  let writeSpeed = previous?.writeSpeed || 0;
  if (["running", "verifying"].includes(task.status)) {
    if (previous && sampledAt > previous.sampledAt) {
      const elapsed = sampledAt - previous.sampledAt;
      if (readBytes > previous.readBytes) {
        const instantaneous = (readBytes - previous.readBytes) * 1000 / elapsed;
        readSpeed = readSpeed > 0 ? readSpeed * 0.65 + instantaneous * 0.35 : instantaneous;
      }
      if (writeBytes > previous.writeBytes) {
        const instantaneous = (writeBytes - previous.writeBytes) * 1000 / elapsed;
        writeSpeed = writeSpeed > 0 ? writeSpeed * 0.65 + instantaneous * 0.35 : instantaneous;
      }
    } else if (!previous && bytes > 0 && task.status === "running") {
      const elapsed = Math.max(1, (sampledAt - new Date(task.created_at).getTime()) / 1000);
      readSpeed = readBytes / elapsed;
      writeSpeed = writeBytes / elapsed;
    }
  } else if (task.status === "completed") {
    const duration = Math.max(1, (new Date(task.updated_at).getTime() - new Date(task.created_at).getTime()) / 1000);
    readSpeed = readBytes / duration;
    writeSpeed = writeBytes / duration;
  } else {
    readSpeed = 0;
    writeSpeed = 0;
  }
  const progressSampledAt = previous && bytes === previous.bytes && readBytes === previous.readBytes && writeBytes === previous.writeBytes ? previous.sampledAt : sampledAt;
  state.transferMetrics.set(task.id, { bytes, readBytes, writeBytes, sampledAt: progressSampledAt, readSpeed, writeSpeed });
}

function taskCategory(task) {
  if (task.status === "completed" || task.status === "skipped") return "success";
  if (task.status === "failed") return "failed";
  return "queue";
}

function memorySourceLabel(item) {
  if (item.source === "import") return item.source_path ? `导入 · ${item.source_path}` : "导入内容";
  if (item.source === "onenote") return item.source_path ? `OneNote · ${item.source_path}` : "OneNote";
  return "Floe 随手记";
}

function maskSensitiveContent(content) {
  let sensitive = false;
  const text = content.replace(/((?:密码|口令|password|passwd|pwd)\s*[:=：]\s*)([^\s,，;；]+)/gi, (_, prefix) => {
    sensitive = true;
    return `${prefix}••••••••`;
  });
  return { text, sensitive };
}

function appendHighlightedText(node, text, query) {
  const terms = memoryQueryHighlights(query);
  if (!terms.length) {
    node.textContent = text;
    return;
  }
  const lower = text.toLowerCase();
  let offset = 0;
  while (offset < text.length) {
    let nextIndex = -1;
    let nextTerm = "";
    for (const term of terms) {
      const found = lower.indexOf(term, offset);
      if (found >= 0 && (nextIndex < 0 || found < nextIndex)) {
        nextIndex = found;
        nextTerm = term;
      }
    }
    if (nextIndex < 0) {
      node.append(document.createTextNode(text.slice(offset)));
      break;
    }
    if (nextIndex > offset) node.append(document.createTextNode(text.slice(offset, nextIndex)));
    const mark = document.createElement("mark");
    mark.textContent = text.slice(nextIndex, nextIndex + nextTerm.length);
    node.append(mark);
    offset = nextIndex + nextTerm.length;
  }
}

function memoryQueryHighlights(query) {
  const normalized = query.toLowerCase().replace(/[！-～]/g, (char) => String.fromCharCode(char.charCodeAt(0) - 0xfee0)).replace(/　/g, " ");
  const phrases = [];
  const plain = normalized.replace(/["“”]([^"“”]+)["“”]/g, (_, phrase) => {
    const value = phrase.trim().replace(/\s+/g, " ");
    if (value) phrases.push(value);
    return " ";
  });
  const terms = plain.replace(/[,，;；。.!！?？、]/g, " ").split(/\s+/).filter(Boolean);
  if (!phrases.length && terms.length > 1) phrases.push(terms.join(" "));
  for (const phrase of phrases) terms.push(...phrase.split(/\s+/));
  return [...new Set([...phrases, ...terms])].sort((left, right) => right.length - left.length);
}

function memoryMatchLabel(match) {
  return { exact: "精确短语", phrase: "完整短语", all: "全部词", partial: "部分匹配" }[match] || "";
}

function renderMemoryEditor() {
  hideMemorySelectionCopy();
  const detail = $("#memoryDetail");
  const editor = document.createElement("div");
  editor.className = "memory-editor";
  const head = document.createElement("div");
  head.className = "memory-editor-head";
  const title = document.createElement("strong");
  title.textContent = state.memoryMode === "new" ? "随手记" : "编辑原始记录";
  const hint = document.createElement("span");
  hint.className = "memory-document-meta";
  hint.textContent = state.memoryMode === "new"
    ? "不需要标题或标签，第一行会作为摘要"
    : `${memorySourceLabel(state.memoryEditing || {})} · Ctrl+Enter 保存`;
  head.append(title, hint);
  const textarea = document.createElement("textarea");
  textarea.id = "memoryEditorInput";
  textarea.placeholder = "输入任何需要记住的内容……";
  textarea.value = state.memoryDraft;
  textarea.addEventListener("input", () => { state.memoryDraft = textarea.value; });
  textarea.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault();
      saveMemoryEditor();
    }
  });
  const actions = document.createElement("div");
  actions.className = "memory-editor-actions";
  if (state.memoryMode === "edit" && state.memoryEditing) {
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "danger memory-editor-delete";
    remove.innerHTML = '<svg class="memory-action-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3m-8 0 1 13h8l1-13M10 11v5m4-5v5"/></svg><span>删除整条记录</span>';
    remove.addEventListener("click", deleteEditingMemory);
    actions.append(remove);
  }
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.textContent = "取消";
  cancel.addEventListener("click", closeMemoryEditor);
  const save = document.createElement("button");
  save.type = "button";
  save.className = "primary";
  save.textContent = "保存记录";
  save.addEventListener("click", saveMemoryEditor);
  actions.append(cancel, save);
  editor.append(head, textarea, actions);
  detail.replaceChildren(editor);
  requestAnimationFrame(() => textarea.focus());
}

function openMemoryEditor(mode, draft = "") {
  state.memoryMode = mode;
  state.memoryEditing = null;
  state.memoryDraft = draft;
  renderMemoryPanel();
}

async function editMemoryBlock(block) {
  try {
    const item = await api(`/api/v1/memories/${encodeURIComponent(block.memory_id)}`);
    state.memoryEditing = { ...item, block_id: block.id, anchor_line: block.line };
    state.memoryDraft = item.content;
    state.memoryMode = "edit";
    renderMemoryPanel();
  } catch (error) { toast(error.message, "error"); }
}

function closeMemoryEditor() {
  state.memoryMode = "view";
  state.memoryDraft = "";
  state.memoryEditing = null;
  renderMemoryPanel();
}

async function saveMemoryEditor() {
  const content = state.memoryDraft.trim();
  const editing = state.memoryMode === "edit" ? state.memoryEditing : null;
  if (!content) {
    if (editing) {
      await deleteEditingMemory();
      return;
    }
    toast("请输入需要记录的内容", "error");
    return;
  }
  try {
    const saved = await api(editing ? `/api/v1/memories/${encodeURIComponent(editing.id)}` : "/api/v1/memories", {
      method: editing ? "PUT" : "POST",
      body: JSON.stringify({ content, source: editing?.source, source_path: editing?.source_path }),
    });
    const returnLine = editing ? Math.min(editing.anchor_line, Math.max(0, saved.content.split("\n").length - 1)) : 0;
    state.memoryMode = "view";
    state.memoryDraft = "";
    state.memoryEditing = null;
    toast(editing ? "速查记录已更新" : "已记下");
    await loadMemories($("#memorySearch").value);
    if (editing) {
      const candidates = state.memories.filter((item) => item.id === saved.id);
      const target = candidates.sort((left, right) => Math.abs(left.anchor_line - returnLine) - Math.abs(right.anchor_line - returnLine))[0];
      if (target) await selectMemory(target);
      else await loadMemoryAnchor(`${saved.id}:${returnLine}`);
    }
  } catch (error) { toast(error.message, "error"); }
}

async function deleteEditingMemory() {
  const editing = state.memoryEditing;
  if (!editing || !confirm("确定删除这整条原始记录吗？此操作无法撤销。")) return;
  try {
    await api(`/api/v1/memories/${encodeURIComponent(editing.id)}`, { method: "DELETE" });
    state.memoryMode = "view";
    state.memoryDraft = "";
    state.memoryEditing = null;
    state.memorySelected = "";
    state.memoryBlocks = [];
    state.memoryAnchor = "";
    state.memoryHasBefore = false;
    state.memoryHasAfter = false;
    toast("原始记录已删除");
    await loadMemories($("#memorySearch").value);
  } catch (error) { toast(error.message, "error"); }
}

function hideMemorySelectionCopy() {
  const button = $("#memorySelectionCopy");
  button.hidden = true;
  state.memorySelectionText = "";
}

function updateMemorySelectionCopy() {
  const detail = $("#memoryDetail");
  const selection = window.getSelection();
  if (state.memoryMode !== "view" || !selection || selection.rangeCount === 0 || selection.isCollapsed
      || !detail.contains(selection.anchorNode) || !detail.contains(selection.focusNode)) {
    hideMemorySelectionCopy();
    return;
  }
  const text = selection.toString().trim();
  if (!text) {
    hideMemorySelectionCopy();
    return;
  }
  const rect = selection.getRangeAt(0).getBoundingClientRect();
  const button = $("#memorySelectionCopy");
  const width = 66;
  let top = rect.top - 34;
  if (top < 8) top = rect.bottom + 8;
  button.style.left = `${Math.max(8, Math.min(window.innerWidth - width - 8, rect.left + rect.width / 2 - width / 2))}px`;
  button.style.top = `${top}px`;
  state.memorySelectionText = text;
  button.hidden = false;
}

async function deleteSelectedMemory() {
  const item = selectedMemory();
  if (!item || !confirm(`删除“${item.title}”？`)) return;
  try {
    await api(`/api/v1/memories/${encodeURIComponent(item.id)}`, { method: "DELETE" });
    state.memorySelected = "";
    toast("速查记录已删除");
    await loadMemories($("#memorySearch").value);
  } catch (error) { toast(error.message, "error"); }
}

function toggleMemoryFullscreen(force) {
  const queue = $("#transferQueue");
  const enabled = force ?? !queue.classList.contains("memory-fullscreen");
  queue.classList.toggle("memory-fullscreen", enabled);
  const icon = $("#memoryFullscreen .material-symbols-rounded");
  icon.textContent = enabled ? "close" : "fullscreen";
  $("#memoryFullscreen").title = enabled ? "退出全屏" : "展开速查";
}

async function activateMemories() {
  state.taskFilter = "memories";
  const queue = $("#transferQueue");
  queue.classList.remove("collapsed");
  const mainArea = $(".main-area");
  const currentHeight = parseInt(getComputedStyle(mainArea).getPropertyValue("--queue-height"), 10) || 0;
  if (currentHeight < 260) mainArea.style.setProperty("--queue-height", "300px");
  renderTaskList();
  await Promise.all([
    state.memoryLoaded ? Promise.resolve() : loadMemories(),
    state.memorySearchHistoryLoaded ? Promise.resolve() : loadMemorySearchHistory(),
  ]);
  requestAnimationFrame(() => $("#memorySearch").focus());
}

async function importMemoryFiles(files) {
  const supported = [...files].filter((file) => file.size <= 1 << 20);
  if (!supported.length) {
    toast("请选择不超过 1 MB 的文本或 Markdown 文件", "error");
    return;
  }
  let imported = 0;
  for (const file of supported) {
    try {
      const content = await file.text();
      if (!content.trim()) continue;
      await api("/api/v1/memories", {
        method: "POST",
        body: JSON.stringify({ content, source: "import", source_path: file.name }),
      });
      imported++;
    } catch (error) {
      toast(`${file.name}：${error.message}`, "error");
    }
  }
  if (imported) {
    toast(`已导入 ${imported} 个文件；OneNote 直连导入将在后续版本提供`);
    await loadMemories($("#memorySearch").value);
  }
}

async function openMemorySettings() {
  try {
    const settings = await api("/api/v1/memory-settings");
    state.memorySettings = settings;
    const form = $("#memorySettingsForm");
    form.elements.path.value = settings.path;
    form.elements.copy_existing.checked = true;
    $("#memorySettingsSummary").textContent = `${settings.count} 条记录 · 默认位置：${settings.default_path}`;
    $("#memorySettingsDialog").showModal();
    requestAnimationFrame(() => form.elements.path.focus());
  } catch (error) { toast(error.message, "error"); }
}

async function saveMemorySettings(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const path = form.elements.path.value.trim();
  if (!path) {
    toast("请输入知识库存储目录", "error");
    return;
  }
  try {
    const settings = await api("/api/v1/memory-settings", {
      method: "PUT",
      body: JSON.stringify({ path, copy_existing: form.elements.copy_existing.checked }),
    });
    state.memorySettings = settings;
    state.memoryLoaded = false;
    state.memorySelected = "";
    state.memoryMode = "view";
    $("#memorySettingsDialog").close();
    toast(`知识库已切换到 ${settings.path}`);
    await loadMemories($("#memorySearch").value);
  } catch (error) { toast(error.message, "error"); }
}

// The knowledge base is presented as one logical stream. Search returns only
// hit anchors; the reader fetches a small window around the chosen anchor and
// extends that window while scrolling.
async function loadMemories(query = $("#memorySearch")?.value || "") {
  const loadID = ++state.memoryLoadID;
  query = query.trim();
  if (!query) {
    state.memories = [];
    state.memorySelected = "";
    state.memoryBlocks = [];
    state.memoryAnchor = "";
    state.memoryHasBefore = false;
    state.memoryHasAfter = false;
    state.memoryLoaded = true;
    state.memoryLoading = false;
    renderMemoryPanel();
    return;
  }
  state.memoryLoading = true;
  renderMemoryPanel();
  try {
    const params = new URLSearchParams({ q: query, limit: "100" });
    const items = await api(`/api/v1/memories?${params}`);
    if (loadID !== state.memoryLoadID) return;
    state.memories = items;
    state.memoryLoaded = true;
    if (!items.some((item) => item.hit_id === state.memorySelected)) {
      state.memorySelected = "";
      state.memoryBlocks = [];
      state.memoryAnchor = "";
      state.memoryHasBefore = false;
      state.memoryHasAfter = false;
    }
  } catch (error) {
    if (loadID === state.memoryLoadID) toast(error.message, "error");
  } finally {
    if (loadID === state.memoryLoadID) {
      state.memoryLoading = false;
      renderMemoryPanel();
    }
  }
}

function sortMemorySearchHistory(items) {
  return [...items].sort((left, right) => {
    const rightTime = Date.parse(right.last_used_at) || 0;
    const leftTime = Date.parse(left.last_used_at) || 0;
    if (state.memorySearchHistorySort === "recent") {
      if (rightTime !== leftTime) return rightTime - leftTime;
      if (right.use_count !== left.use_count) return right.use_count - left.use_count;
      return left.query.localeCompare(right.query);
    }
    if (right.use_count !== left.use_count) return right.use_count - left.use_count;
    if (rightTime !== leftTime) return rightTime - leftTime;
    return left.query.localeCompare(right.query);
  }).slice(0, MEMORY_SEARCH_HISTORY_LIMIT);
}

async function loadMemorySearchHistory() {
  try {
    const items = await api(`/api/v1/memory-search-history?limit=${MEMORY_SEARCH_HISTORY_LIMIT}`);
    state.memorySearchHistory = sortMemorySearchHistory(Array.isArray(items) ? items : []);
    state.memorySearchHistoryLoaded = true;
    renderMemorySearchHistory();
  } catch (error) {
    toast(error.message, "error");
  }
}

function renderMemorySearchHistory() {
  const list = $("#memoryHistoryList");
  if (!list) return;
  const filter = $("#memoryHistoryFilter").value.trim().toLocaleLowerCase();
  const items = sortMemorySearchHistory(state.memorySearchHistory).filter((item) => item.query.toLocaleLowerCase().includes(filter));
  $$('[data-memory-history-sort]').forEach((button) => button.classList.toggle("active", button.dataset.memoryHistorySort === state.memorySearchHistorySort));
  list.replaceChildren();
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "memory-history-empty";
    empty.textContent = state.memorySearchHistory.length ? "没有匹配的历史词条" : "暂无历史搜索词条";
    list.append(empty);
    return;
  }
  for (const item of items) {
    const row = document.createElement("div");
    row.className = "memory-history-row";
    const entry = document.createElement("button");
    entry.type = "button";
    entry.className = "memory-history-entry";
    entry.dataset.memoryHistoryQuery = item.query;
    const query = document.createElement("span");
    query.className = "memory-history-query";
    query.textContent = item.query;
    const meta = document.createElement("span");
    meta.className = "memory-history-meta";
    meta.textContent = `${item.use_count} 次 · ${formatTime(item.last_used_at)}`;
    entry.append(query, meta);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "memory-history-delete";
    remove.dataset.memoryHistoryDelete = item.query;
    remove.title = "删除历史词条";
    remove.setAttribute("aria-label", `删除历史词条：${item.query}`);
    remove.innerHTML = '<span class="material-symbols-rounded" aria-hidden="true">delete</span>';
    row.append(entry, remove);
    list.append(row);
  }
}

function setMemorySearchHistoryOpen(open) {
  const popover = $("#memorySearchHistory");
  if (!popover) return;
  state.memorySearchHistoryOpen = open;
  popover.hidden = !open;
  const toggle = $("#memoryHistoryToggle");
  toggle?.setAttribute("aria-expanded", String(open));
  toggle?.classList.toggle("active", open);
  if (open) {
    renderMemorySearchHistory();
    if (!state.memorySearchHistoryLoaded) void loadMemorySearchHistory();
  }
}

function recordMemorySearchHistory(query) {
  query = query.trim();
  if (!query) return;
  memorySearchHistoryMutation = memorySearchHistoryMutation
    .catch(() => {})
    .then(async () => {
      const item = await api("/api/v1/memory-search-history", {
        method: "POST",
        body: JSON.stringify({ query }),
      });
      const normalized = item.query.toLocaleLowerCase();
      state.memorySearchHistory = sortMemorySearchHistory([
        item,
        ...state.memorySearchHistory.filter((entry) => entry.query.toLocaleLowerCase() !== normalized),
      ]);
      state.memorySearchHistoryLoaded = true;
      renderMemorySearchHistory();
    })
    .catch((error) => toast(error.message, "error"));
}

function deleteMemorySearchHistory(query) {
  memorySearchHistoryMutation = memorySearchHistoryMutation
    .catch(() => {})
    .then(async () => {
      await api(`/api/v1/memory-search-history?query=${encodeURIComponent(query)}`, { method: "DELETE" });
      const normalized = query.toLocaleLowerCase();
      state.memorySearchHistory = state.memorySearchHistory.filter((item) => item.query.toLocaleLowerCase() !== normalized);
      renderMemorySearchHistory();
    })
    .catch((error) => toast(error.message, "error"));
}

function resetMemorySearch(query) {
  state.memoryMode = "view";
  state.memories = [];
  state.memorySelected = "";
  state.memoryBlocks = [];
  state.memoryAnchor = "";
  state.memoryHasBefore = false;
  state.memoryHasAfter = false;
  state.memoryLoading = Boolean(query.trim());
  $("#memoryClearSearch").hidden = !query;
  renderMemoryPanel();
  clearTimeout(memorySearchTimer);
  memorySearchTimer = setTimeout(() => loadMemories(query), 180);
}

function handleMemoryHistoryClick(event) {
  const remove = event.target.closest("[data-memory-history-delete]");
  if (remove) {
    event.preventDefault();
    event.stopPropagation();
    deleteMemorySearchHistory(remove.dataset.memoryHistoryDelete);
    return;
  }
  const entry = event.target.closest("[data-memory-history-query]");
  if (!entry) return;
  const query = entry.dataset.memoryHistoryQuery;
  $("#memorySearch").value = query;
  resetMemorySearch(query);
  recordMemorySearchHistory(query);
  setMemorySearchHistoryOpen(false);
  $("#memorySearch").focus();
}

function selectedMemory() { return state.memories.find((item) => item.hit_id === state.memorySelected); }

async function selectMemory(item) {
  state.memorySelected = item.hit_id;
  state.memoryMode = "view";
  renderMemoryResults();
  await loadMemoryAnchor(item.block_id);
}

function moveMemorySelection(direction) {
  if (!state.memories.length) return;
  let index = state.memories.findIndex((item) => item.hit_id === state.memorySelected);
  if (index < 0) index = direction > 0 ? 0 : state.memories.length - 1;
  else index = Math.max(0, Math.min(state.memories.length - 1, index + direction));
  selectMemory(state.memories[index]);
  requestAnimationFrame(() => $(`.memory-result[data-memory-hit="${CSS.escape(state.memories[index].hit_id)}"]`)?.scrollIntoView({ block: "nearest" }));
}

function renderMemoryResults() {
  const root = $("#memoryResults");
  root.replaceChildren();
  const query = $("#memorySearch").value.trim();
  if (state.memoryLoading) {
    root.innerHTML = '<div class="memory-empty"><span class="loading-spinner"></span><strong>正在搜索知识内容…</strong></div>';
    return;
  }
  if (!query) {
    root.innerHTML = '<div class="memory-empty"><strong>输入关键词开始搜索</strong><span>左侧只显示实际命中的内容片段</span></div>';
    return;
  }
  if (!state.memories.length) {
    const empty = document.createElement("div");
    empty.className = "memory-empty";
    const title = document.createElement("strong");
    title.textContent = "没有找到相关内容";
    const hint = document.createElement("span");
    hint.textContent = "换一个记得的词、命令片段、IP 或错误信息";
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = "记下当前内容";
    button.addEventListener("click", () => openMemoryEditor("new", query));
    empty.append(title, hint, button);
    root.append(empty);
    return;
  }
  for (const item of state.memories) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `memory-result${item.hit_id === state.memorySelected ? " selected" : ""}`;
    button.dataset.memoryHit = item.hit_id;
    button.setAttribute("role", "option");
    button.setAttribute("aria-selected", String(item.hit_id === state.memorySelected));
    const snippet = document.createElement("div");
    snippet.className = "memory-result-snippet";
    appendHighlightedText(snippet, maskSensitiveContent(item.snippet).text, query);
    const meta = document.createElement("div");
    meta.className = "memory-result-meta";
    meta.textContent = [memoryMatchLabel(item.match), memorySourceLabel(item)].filter(Boolean).join(" · ");
    button.append(snippet, meta);
    button.addEventListener("click", () => selectMemory(item));
    root.append(button);
  }
}

function renderMemoryDetail() {
  hideMemorySelectionCopy();
  if (state.memoryMode !== "view") {
    renderMemoryEditor();
    return;
  }
  const detail = $("#memoryDetail");
  if (state.memoryContextLoading) {
    detail.innerHTML = '<div class="memory-detail-empty"><span class="loading-spinner"></span><span>正在定位知识内容…</span></div>';
    return;
  }
  if (!state.memoryBlocks.length) {
    detail.innerHTML = '<div class="memory-detail-empty">搜索并选择左侧片段，右侧将在统一知识流中定位</div>';
    return;
  }
  const stream = document.createElement("div");
  stream.className = "memory-stream";
  const query = $("#memorySearch").value;
  if (state.memoryHasBefore) {
    const edge = document.createElement("div");
    edge.className = "memory-stream-edge";
    edge.textContent = state.memoryLoadingBefore ? "正在加载更早内容…" : "向上滚动加载更早内容";
    stream.append(edge);
  }
  state.memoryBlocks.forEach((block, index) => {
    if (block.document_start || index === 0) {
      const source = document.createElement("div");
      source.className = "memory-source-marker";
      const label = document.createElement("span");
      label.textContent = `${memorySourceLabel(block)} · ${formatTime(block.created_at)}`;
      const edit = document.createElement("button");
      edit.type = "button";
      edit.innerHTML = '<svg class="memory-action-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="m4 20 4.2-1 10.6-10.6-3.2-3.2L5 15.8 4 20Zm10.4-13.6 3.2 3.2M14.8 6l2-2a1.4 1.4 0 0 1 2 0l1.2 1.2a1.4 1.4 0 0 1 0 2l-2 2"/></svg><span>编辑</span>';
      edit.addEventListener("click", () => editMemoryBlock(block));
      source.append(label, edit);
      stream.append(source);
    }
    const row = document.createElement("div");
    row.className = `memory-content-line${block.id === state.memoryAnchor ? " memory-context-hit" : ""}`;
    row.dataset.memoryBlock = block.id;
    const text = maskSensitiveContent(block.text).text;
    if (text) appendHighlightedText(row, text, query);
    else row.append(document.createElement("br"));
    stream.append(row);
  });
  if (state.memoryHasAfter) {
    const edge = document.createElement("div");
    edge.className = "memory-stream-edge";
    edge.textContent = state.memoryLoadingAfter ? "正在加载后续内容…" : "向下滚动加载后续内容";
    stream.append(edge);
  }
  detail.replaceChildren(stream);
}

function renderMemoryPanel() {
  if (state.taskFilter !== "memories") return;
  $("#memoryClearSearch").hidden = !$("#memorySearch").value;
  renderMemorySearchHistory();
  renderMemoryResults();
  renderMemoryDetail();
  const query = $("#memorySearch").value.trim();
  $("#taskSummary").textContent = state.memoryLoading ? "正在搜索…" : state.memories.length ? `${state.memories.length} 个命中片段` : query ? "没有命中" : "输入关键词搜索";
}

async function loadMemoryAnchor(blockID) {
  const loadID = ++state.memoryContextLoadID;
  state.memoryContextLoading = true;
  state.memoryAnchor = blockID;
  renderMemoryDetail();
  try {
    const page = await api(`/api/v1/memory-stream?anchor=${encodeURIComponent(blockID)}&limit=80`);
    if (loadID !== state.memoryContextLoadID) return;
    state.memoryBlocks = page.blocks || [];
    state.memoryHasBefore = Boolean(page.has_before);
    state.memoryHasAfter = Boolean(page.has_after);
  } catch (error) {
    if (loadID === state.memoryContextLoadID) toast(error.message, "error");
  } finally {
    if (loadID === state.memoryContextLoadID) {
      state.memoryContextLoading = false;
      renderMemoryDetail();
      requestAnimationFrame(() => $(`[data-memory-block="${CSS.escape(blockID)}"]`, $("#memoryDetail"))?.scrollIntoView({ block: "center" }));
    }
  }
}

async function loadMemoryStreamEdge(direction) {
  if (state.memoryContextLoading || !state.memoryBlocks.length) return;
  const before = direction === "before";
  if (before ? (!state.memoryHasBefore || state.memoryLoadingBefore) : (!state.memoryHasAfter || state.memoryLoadingAfter)) return;
  const detail = $("#memoryDetail");
  const cursor = before ? state.memoryBlocks[0].id : state.memoryBlocks[state.memoryBlocks.length - 1].id;
  if (before) state.memoryLoadingBefore = true;
  else state.memoryLoadingAfter = true;
  const previousHeight = detail.scrollHeight;
  const previousTop = detail.scrollTop;
  try {
    const page = await api(`/api/v1/memory-stream?${before ? "before" : "after"}=${encodeURIComponent(cursor)}&limit=60`);
    const existing = new Set(state.memoryBlocks.map((block) => block.id));
    const additions = (page.blocks || []).filter((block) => !existing.has(block.id));
    state.memoryBlocks = before ? [...additions, ...state.memoryBlocks] : [...state.memoryBlocks, ...additions];
    if (before) state.memoryHasBefore = Boolean(page.has_before);
    else state.memoryHasAfter = Boolean(page.has_after);
  } catch (error) { toast(error.message, "error"); }
  finally {
    if (before) state.memoryLoadingBefore = false;
    else state.memoryLoadingAfter = false;
    renderMemoryDetail();
    if (before) requestAnimationFrame(() => { detail.scrollTop = previousTop + detail.scrollHeight - previousHeight; });
  }
}

function renderTaskList() {
  if (state.taskFilter !== "memories" && state.memorySearchHistoryOpen) setMemorySearchHistoryOpen(false);
  $$(".task-tab").forEach((tab) => tab.classList.toggle("active", tab.dataset.taskFilter === state.taskFilter));
  const list = $("#taskList");
	const memoryPanel = $("#memoryPanel");
	const clearButton = $("#clearTaskHistory");
	const showingMemories = state.taskFilter === "memories";
	const showingPreview = state.taskFilter === "preview";
	list.hidden = showingMemories || showingPreview;
	memoryPanel.hidden = !showingMemories;
	$("#previewPanel").hidden = !showingPreview;
	updatePreviewVisibility();
	if (showingPreview) {
		clearButton.hidden = true;
		$("#taskSummary").textContent = "支持多文件预览";
		return;
	}
	if (showingMemories) {
		clearButton.hidden = true;
		renderMemoryPanel();
		return;
	}
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
    const metric = state.transferMetrics.get(task.id) || { speed: 0, readSpeed: 0, writeSpeed: 0 };
    const terminalTime = ["completed", "skipped", "failed"].includes(task.status) ? new Date(task.updated_at).getTime() : Date.now();
    const elapsedSeconds = Math.max(0, (terminalTime - new Date(task.created_at).getTime()) / 1000);
    const progressSpeed = metric.writeSpeed > 0 ? metric.writeSpeed : metric.readSpeed;
    const remainingSeconds = progressSpeed > 0 && ["running", "verifying"].includes(task.status) ? Math.max(0, (task.size - transferred) / progressSpeed) : NaN;
    const direction = task.source_provider === currentProvider("left") && task.target_provider === currentProvider("right") ? "上传" : task.source_provider === currentProvider("right") && task.target_provider === currentProvider("left") ? "下载" : "传输";
    const selectable = taskCategory(task) === "success";
    const checkbox = selectable ? `<input class="task-select" type="checkbox" aria-label="选择任务" ${state.taskSelection.has(task.id) ? "checked" : ""}>` : "";
    row.innerHTML = `<span class="task-route" title="${escapeHTML(task.source_path)} → ${escapeHTML(task.target_path)}">${checkbox}${direction}　${escapeHTML(task.source_path)} → ${escapeHTML(task.target_path)}</span><span class="progress-track" title="${statusLabel(task.status)} · 已传输 ${transferPercent.toFixed(1)}% · 已校验 ${verifiedPercent.toFixed(1)}%"><i class="progress-transferred" style="width:${transferPercent.toFixed(2)}%"></i><i class="progress-verified" style="width:${verifiedPercent.toFixed(2)}%"></i></span><span class="task-status">${formatBytes(transferred)} / ${formatBytes(task.size)}</span><span class="task-status task-throughput" title="读取和写入平均速度">读 ${metric.readSpeed > 0 ? `${formatBytes(metric.readSpeed)}/s` : "--"}　写 ${metric.writeSpeed > 0 ? `${formatBytes(metric.writeSpeed)}/s` : "--"}</span><span class="task-status" title="预计剩余时间">剩余 ${formatDuration(remainingSeconds)}</span><span class="task-status" title="已经过时间">已用 ${formatDuration(elapsedSeconds)}</span>`;
    if (selectable) {
      $(".task-select", row).addEventListener("click", (event) => event.stopPropagation());
      $(".task-select", row).addEventListener("change", (event) => {
        if (event.target.checked) state.taskSelection.add(task.id);
        else state.taskSelection.delete(task.id);
        renderTaskList();
      });
    }
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
  if (["completed", "skipped"].includes(task.status)) {
    const selected = state.tasks.filter((item) => state.taskSelection.has(item.id) && ["completed", "skipped"].includes(item.status));
    const tasks = selected.length > 1 ? selected : [task];
    items.push(
      { label: tasks.length > 1 ? "保存选中的 " + tasks.length + " 个任务为模板…" : "保存为模板…", action: () => saveTasksAsTemplate(tasks) },
      { label: "添加到模板", action: () => appendTasksToTemplate(tasks.map(templateTaskFromTransfer)) },
    );
  }
  items.push({ separator: true }, { label: "删除任务", danger: true, action: () => deleteTask(task.id) });
  showContextMenu(event.clientX, event.clientY, items);
}

async function runTransferTemplate(id) {
  if (!id) return;
  const template = state.transferTemplates.find((item) => item.id === id);
  if (!template) return;
  const providers = new Map();
  for (const item of template.tasks || []) {
    if (item.source_provider) providers.set(item.source_provider, item.source_provider_kind || "");
    if (item.target_provider) providers.set(item.target_provider, item.target_provider_kind || "");
  }
  for (const [providerID, templateKind] of providers) {
    if (templateKind === "local" || providerID.startsWith("local-")) continue;
    const provider = providerByID(providerID);
    if (provider?.kind !== "local" && !provider?.connected) {
      if (!await connectSavedSession(providerID)) return;
    }
  }
  try {
    const result = await api(`/api/v1/transfer-templates/${encodeURIComponent(id)}/run`, { method: "POST", body: "{}" });
    state.taskFilter = "queue";
    $("#transferQueue").classList.remove("collapsed");
    renderTaskList();
    const detail = result.failed?.length ? `，${result.failed.length} 项失败` : "";
    toast(`模板“${template.name}”已执行 ${result.started || 0} 项${detail}`, result.failed?.length ? "error" : "info");
  } catch (error) {
    toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
  }
}

function templateTaskFromTransfer(task) {
  const sourceProvider = providerByID(task.source_provider);
  const targetProvider = providerByID(task.target_provider);
  return {
    source_provider: task.source_provider, source_path: task.source_path,
    source_provider_name: task.source_provider_name || sourceProvider?.name || "",
    source_provider_kind: task.source_provider_kind || sourceProvider?.kind || "",
    source_provider_location: task.source_provider_location || sourceProvider?.location || "",
    target_provider: task.target_provider, target_path: task.target_path,
    target_provider_name: task.target_provider_name || targetProvider?.name || "",
    target_provider_kind: task.target_provider_kind || targetProvider?.kind || "",
    target_provider_location: task.target_provider_location || targetProvider?.location || "",
    conflict_policy: task.conflict_policy === "ask" ? "overwrite" : (task.conflict_policy || "overwrite"),
    concurrency: task.concurrency || 4, verify: task.verify !== false,
    preserve_structure: task.preserve_structure !== false, filter: task.filter || "",
  };
}

function chooseTransferTemplate() {
  if (!state.transferTemplates.length) {
    toast("还没有发布模板，请先保存一个模板", "error");
    return Promise.resolve(null);
  }
  const dialog = $("#templateChooseDialog");
  const select = $("#templateChooseForm").elements.template;
  select.replaceChildren(...state.transferTemplates.map((template) => new Option(template.name, template.id)));
  dialog.showModal();
  return new Promise((resolve) => { state.templateChooseResolve = resolve; });
}

function closeTemplateChooser(result) {
  const dialog = $("#templateChooseDialog");
  if (dialog.open) dialog.close();
  const resolve = state.templateChooseResolve;
  state.templateChooseResolve = null;
  resolve?.(result || null);
}

async function appendTasksToTemplate(tasks) {
  if (!tasks.length) return;
  const templateID = await chooseTransferTemplate();
  const template = state.transferTemplates.find((item) => item.id === templateID);
  if (!template) return;
  const existing = (template.tasks?.length ? template.tasks : [template]).map(templateTaskFromTransfer);
  const keys = new Set(existing.map((item) => item.source_provider + "\u0000" + item.source_path + "\u0000" + item.target_provider + "\u0000" + item.target_path));
  const additions = tasks.filter((item) => {
    const normalized = templateTaskFromTransfer(item);
    const key = normalized.source_provider + "\u0000" + normalized.source_path + "\u0000" + normalized.target_provider + "\u0000" + normalized.target_path;
    if (keys.has(key)) return false;
    keys.add(key);
    return true;
  }).map(templateTaskFromTransfer);
  if (!additions.length) {
    toast("所选发布项已经在模板中");
    return;
  }
  try {
    const saved = await api("/api/v1/transfer-templates", {
      method: "POST",
      body: JSON.stringify({ ...template, tasks: existing.concat(additions) }),
    });
    await loadTransferTemplates();
    toast("已添加 " + additions.length + " 个发布项到模板“" + saved.name + "”");
  } catch (error) {
    toast(error.message, "error");
  }
}

async function saveTemplateTaskSet(tasks) {
  if (!tasks.length) return;
  const defaultName = tasks.length === 1 ? `${tasks[0].source_provider} → ${tasks[0].target_provider}` : `发布任务（${tasks.length}项）`;
  const name = prompt("模板名称", defaultName);
  if (!name?.trim()) return;
  try {
    const templateTasks = tasks.map(templateTaskFromTransfer);
    const first = templateTasks[0];
    const saved = await api("/api/v1/transfer-templates", {
      method: "POST",
      body: JSON.stringify({
        name: name.trim(), source_provider: first.source_provider, target_provider: first.target_provider,
        source_path: parentPath(first.source_path), target_path: parentPath(first.target_path),
        conflict_policy: first.conflict_policy, concurrency: first.concurrency,
        verify: first.verify, preserve_structure: first.preserve_structure, filter: first.filter,
        tasks: templateTasks,
      }),
    });
    await loadTransferTemplates();
    toast(`模板“${saved.name}”已保存`);
  } catch (error) {
    toast(`${error.message}${error.payload?.detail ? `：${error.payload.detail}` : ""}`, "error");
  }
}

function saveTasksAsTemplate(tasks) {
  return saveTemplateTaskSet(tasks);
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
		try {
			const result = await api("/api/v1/logs", { method: "DELETE" });
			toast(`已清空 ${result.removed} 条日志`);
		} catch (error) { toast(error.message, "error"); }
		return;
	}
  const status = state.taskFilter === "success" ? "completed" : state.taskFilter === "failed" ? "failed" : "";
  if (!status) return;
  const label = status === "completed" ? "成功" : "失败";
  try {
	const result = await api(`/api/v1/transfers?status=${status}`, { method: "DELETE" });
	toast(`已清空 ${result.removed} 条${label}任务`);
  } catch (error) { toast(error.message, "error"); }
}

function statusLabel(status) { return ({ running: "传输", verifying: "校验", paused: "暂停", failed: "失败", completed: "完成", skipped: "已跳过" })[status] || status; }

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

async function createLocalTab(side, root, initialPath = "") {
  if (!root) { toast("没有可用的本地目录", "error"); return; }
  try {
    const provider = await api("/api/v1/local/tabs", { method: "POST", body: JSON.stringify({ path: root }) });
    await loadProviders();
    const providerPath = initialPath ? localProviderPath(provider, initialPath) : "/";
    await openSession(side, provider.id, providerPath || "/");
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

function localBookmarkRoot(value) {
  const windowsPath = value.replaceAll("/", "\\");
  const drive = windowsPath.match(/^([A-Za-z]:)\\/);
  if (drive) return `${drive[1]}\\`;
  const share = windowsPath.match(/^\\\\([^\\]+)\\([^\\]+)(?:\\|$)/);
  if (share) return `\\\\${share[1]}\\${share[2]}\\`;
  return "/";
}

async function openLocalBookmark(side, value) {
  const candidates = state.providers
    .filter((provider) => provider.kind === "local")
    .map((provider) => ({ provider, path: localProviderPath(provider, value) }))
    .filter((candidate) => candidate.path && candidate.path !== "/")
    .sort((left, right) => (right.provider.location || "").length - (left.provider.location || "").length);
  if (candidates.length) {
    await openSession(side, candidates[0].provider.id, candidates[0].path);
    return;
  }
  await createLocalTab(side, localBookmarkRoot(value), value);
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
        if (provider?.kind === "local") openLocalBookmark(side, entry.path);
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
  $(".transfer-file-button", el.root).addEventListener("click", () => transferEntries(side, side === "left" ? "right" : "left", selectedEntries(side)));
  el.viewButton.addEventListener("click", () => {
    const current = state.panels[side];
    current.view = current.view === "list" ? "grid" : "list";
    saveWorkspace(); loadPanel(side);
  });
  $$(".column-head button[data-sort]", el.root).forEach((button) => button.addEventListener("click", () => changeSort(side, button.dataset.sort)));
  el.viewport.addEventListener("scroll", () => queueRender(side));
  el.viewport.addEventListener("click", (event) => { if (!event.target.closest(".file-entry")) clearSelection(side); });
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
    const minimum = state.preview.host === "panel" && state.taskFilter === "preview" ? 220 : 90;
    const height = Math.max(minimum, Math.min(window.innerHeight * 0.65, window.innerHeight - event.clientY));
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

function initializePreviewResize() {
  const observer = new ResizeObserver(() => {
    if (state.preview.host !== "panel") return;
    if (state.image) fitImage();
    if (activePreview()?.kind === "media") fitMediaPlayer();
  });
  observer.observe($("#previewHosts"));
  observer.observe($("#imageStage"));
  observer.observe($(".media-stage"));
}

async function main() {
  try {
    await loadProviders(false); await loadBookmarks(); await loadTransferTemplates(); initializeWorkspace(); renderSessionTree();
	    bindPanel("left"); bindPanel("right"); renderTabs("left"); renderTabs("right");
	    initializeQueueResize();
	    initializePreviewResize();
	    initializeHTMLViewportResize();
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
$("#transferOptionsForm").addEventListener("submit", (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  closeTransferOptions({
    conflict_policy: state.transferConflictPolicy,
    always: form.elements.always.checked,
  });
});
function skipTransferConflict() {
  closeTransferOptions({ conflict_policy: "skip", cancelled: true });
}
$("#transferOptionsCancel").addEventListener("click", skipTransferConflict);
$("#transferOptionsClose").addEventListener("click", skipTransferConflict);
$("#transferOptionsDialog").addEventListener("cancel", (event) => { event.preventDefault(); skipTransferConflict(); });
$$('#conflictActionList button').forEach((button) => button.addEventListener("click", () => {
  state.transferConflictPolicy = button.dataset.conflictPolicy;
  $$("#conflictActionList button").forEach((item) => {
    const selected = item === button;
    item.classList.toggle("selected", selected);
    item.setAttribute("aria-selected", String(selected));
  });
}));
$("#publishTemplateForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const template = state.editingPublishTemplate;
  const name = event.currentTarget.elements.name.value.trim();
  if (!template || !name) return;
  if (!template.tasks?.length) { toast("发布任务至少需要一个发布项", "error"); return; }
  template.name = name;
  try {
    const saved = await api("/api/v1/transfer-templates", { method: "POST", body: JSON.stringify(template) });
    $("#publishTemplateDialog").close();
    state.editingPublishTemplate = null;
    await loadTransferTemplates();
    toast("发布任务“" + saved.name + "”已保存");
  } catch (error) {
    toast(error.message, "error");
  }
});
$("#publishTemplateCancel").addEventListener("click", () => $("#publishTemplateDialog").close());
$("#publishTemplateClose").addEventListener("click", () => $("#publishTemplateDialog").close());
$("#templateChooseForm").addEventListener("submit", (event) => {
  event.preventDefault();
  closeTemplateChooser(event.currentTarget.elements.template.value);
});
$("#templateChooseCancel").addEventListener("click", () => closeTemplateChooser(null));
$("#templateChooseClose").addEventListener("click", () => closeTemplateChooser(null));
$("#templateChooseDialog").addEventListener("cancel", (event) => { event.preventDefault(); closeTemplateChooser(null); });
$$(".sidebar-tab").forEach((button) => button.addEventListener("click", () => setSidebarTab(button.dataset.sidebarTab)));
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
$("#closeEditor").addEventListener("click", () => closePreviewWindow("text"));
$("#maximizeEditor").addEventListener("click", () => toggleDialogMaximized($("#editorDialog"), $("#maximizeEditor")));
$("#editorPreviewToggle").addEventListener("click", () => setDocumentPreview(!state.editor?.previewOpen));
$("#editorPreview").addEventListener("click", openMarkdownPreviewLink);
$("#refreshHTMLPreview").addEventListener("click", renderHTMLPreview);
$("#htmlViewportPreset").addEventListener("change", (event) => setHTMLViewportPreset(event.target.value));
$("#htmlViewportWidth").addEventListener("change", applyCustomHTMLViewport);
$("#htmlViewportHeight").addEventListener("change", applyCustomHTMLViewport);
$("#rotateHTMLViewport").addEventListener("click", rotateHTMLViewport);
$("#focusHTMLPreview").addEventListener("click", () => toggleHTMLPreviewFocus());
$("#htmlPreviewFrameContent").addEventListener("load", () => $("#htmlPreviewMessage").classList.add("hidden"));
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
$("#editorDialog").addEventListener("cancel", (event) => {
  event.preventDefault();
  if (state.editor?.previewFocus) toggleHTMLPreviewFocus(false);
  else requestCloseEditor();
});
$("#syntaxMode").addEventListener("change", refreshEditorHighlight);
$("#queueToggle").addEventListener("click", () => {
  if ($("#transferQueue").classList.contains("memory-fullscreen")) toggleMemoryFullscreen(false);
  else $("#transferQueue").classList.toggle("collapsed");
  updatePreviewVisibility();
});
$$('.task-tab').forEach((tab) => tab.addEventListener("click", () => {
  if (tab.dataset.taskFilter === "memories") {
    activateMemories();
    return;
  }
  toggleMemoryFullscreen(false);
  if (tab.dataset.taskFilter === "preview") {
    const target = state.preview.activeID || state.preview.items[0]?.id;
    if (target) activatePreview(target);
    return;
  }
  state.taskFilter = tab.dataset.taskFilter;
  $("#transferQueue").classList.remove("collapsed");
  renderTaskList();
}));
$("#collapseEditor").addEventListener("click", () => setPreviewHost("panel"));
$("#collapseImage").addEventListener("click", () => setPreviewHost("panel"));
$("#collapseMedia").addEventListener("click", () => setPreviewHost("panel"));
$("#restorePreviewWindow").addEventListener("click", () => setPreviewHost("dialog"));
function handlePreviewTabClick(event) {
  const close = event.target.closest("[data-preview-close]");
  if (close) { closePreviewItem(close.dataset.previewClose); return; }
  const tab = event.target.closest("[data-preview-id]");
  if (tab) activatePreview(tab.dataset.previewId);
}
$$('[data-preview-strip]').forEach((strip) => strip.addEventListener("click", handlePreviewTabClick));
$$('[data-preview-tab-strip]').forEach((strip) => {
  strip.addEventListener("click", handlePreviewTabNavigation);
  strip.querySelector('[data-preview-strip]')?.addEventListener("scroll", updatePreviewTabNavigation, { passive: true });
});
window.addEventListener("resize", updatePreviewTabNavigation);
for (const [kind, selector] of Object.entries(PREVIEW_DIALOGS)) {
  // Escape and the native close button both end up here, so the tab strip stays in
  // step with what is actually open.
  $(selector).addEventListener("close", (event) => {
    const dialog = event.currentTarget;
    if (dialog.dataset.previewRelocating) { delete dialog.dataset.previewRelocating; return; }
    if (dialog.dataset.previewWindowClosing) {
      delete dialog.dataset.previewWindowClosing;
      return;
    }
    closePreviewWindow(kind);
  });
}
$("#memorySearch").addEventListener("blur", (event) => {
  recordMemorySearchHistory(event.currentTarget.value);
  window.setTimeout(() => {
    if (!$("#memorySearchWrap").contains(document.activeElement)) setMemorySearchHistoryOpen(false);
  }, 0);
});
$("#memorySearch").addEventListener("input", (event) => {
  resetMemorySearch(event.currentTarget.value);
});
$("#memoryHistoryToggle").addEventListener("click", () => {
  const open = !state.memorySearchHistoryOpen;
  setMemorySearchHistoryOpen(open);
  if (open) requestAnimationFrame(() => $("#memoryHistoryFilter").focus());
});
$("#memoryHistoryFilter").addEventListener("input", renderMemorySearchHistory);
$("#memoryHistoryList").addEventListener("click", handleMemoryHistoryClick);
$$('[data-memory-history-sort]').forEach((button) => button.addEventListener("click", () => {
  state.memorySearchHistorySort = button.dataset.memoryHistorySort;
  renderMemorySearchHistory();
}));
$("#memorySearch").addEventListener("keydown", (event) => {
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    moveMemorySelection(event.key === "ArrowDown" ? 1 : -1);
  } else if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
    event.preventDefault();
    openMemoryEditor("new", event.currentTarget.value);
  } else if (event.key === "Enter" && selectedMemory()) {
    event.preventDefault();
    $("#memoryDetail").focus();
  } else if (event.key === "Escape") {
    setMemorySearchHistoryOpen(false);
  }
});
$("#memoryHistoryFilter").addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    event.preventDefault();
    setMemorySearchHistoryOpen(false);
    $("#memorySearch").focus();
  }
});
$("#memoryClearSearch").addEventListener("click", () => {
  $("#memorySearch").value = "";
  $("#memoryClearSearch").hidden = true;
  loadMemories();
  $("#memorySearch").focus();
});
$("#memoryNew").addEventListener("click", () => openMemoryEditor("new"));
$("#memoryImport").addEventListener("click", () => $("#memoryImportFiles").click());
$("#memoryLocation").addEventListener("click", openMemorySettings);
$("#memoryImportFiles").addEventListener("change", async (event) => {
  await importMemoryFiles(event.currentTarget.files);
  event.currentTarget.value = "";
});
$("#memoryFullscreen").addEventListener("click", () => toggleMemoryFullscreen());
$("#memoryDetail").addEventListener("scroll", (event) => {
  hideMemorySelectionCopy();
  if (state.taskFilter !== "memories" || state.memoryMode !== "view") return;
  const node = event.currentTarget;
  if (node.scrollTop < 120) loadMemoryStreamEdge("before");
  if (node.scrollHeight - node.scrollTop - node.clientHeight < 160) loadMemoryStreamEdge("after");
});
$("#memoryDetail").addEventListener("mousedown", hideMemorySelectionCopy);
$("#memoryDetail").addEventListener("mouseup", () => requestAnimationFrame(updateMemorySelectionCopy));
$("#memoryDetail").addEventListener("keyup", () => requestAnimationFrame(updateMemorySelectionCopy));
$("#memorySelectionCopy").addEventListener("mousedown", (event) => event.preventDefault());
$("#memorySelectionCopy").addEventListener("click", async () => {
  if (!state.memorySelectionText) return;
  await copyText(state.memorySelectionText, "选中文字已复制");
  hideMemorySelectionCopy();
});
$("#memorySettingsForm").addEventListener("submit", saveMemorySettings);
$("#memoryUseDefault").addEventListener("click", () => {
  if (state.memorySettings) $("#memorySettingsForm").elements.path.value = state.memorySettings.default_path;
});
$("#memorySettingsCancel").addEventListener("click", () => $("#memorySettingsDialog").close());
$("#memorySettingsClose").addEventListener("click", () => $("#memorySettingsDialog").close());
$("#clearTaskHistory").addEventListener("click", clearTaskHistory);
$("#imagePreview").addEventListener("load", fitImage);
$("#previousImage").addEventListener("click", () => changeImage(-1));
$("#nextImage").addEventListener("click", () => changeImage(1));
$("#zoomOut").addEventListener("click", () => setImageZoom(state.image.zoom / 1.2));
$("#zoomIn").addEventListener("click", () => setImageZoom(state.image.zoom * 1.2));
$("#resetZoom").addEventListener("click", () => setImageZoom(1));
$("#maximizeImage").addEventListener("click", () => toggleDialogMaximized($("#imageDialog"), $("#maximizeImage")));
$("#closeImage").addEventListener("click", () => closePreviewWindow("image"));
$("#imageStage").addEventListener("wheel", (event) => { event.preventDefault(); setImageZoom(state.image.zoom * (event.deltaY < 0 ? 1.12 : 1 / 1.12)); }, { passive: false });
$("#maximizeMedia").addEventListener("click", () => toggleDialogMaximized($("#mediaDialog"), $("#maximizeMedia")));
$("#closeMedia").addEventListener("click", () => closePreviewWindow("media"));
$("#mediaDialog").addEventListener("cancel", (event) => { event.preventDefault(); closePreviewWindow("media"); });
$("#mediaPlayer").addEventListener("playing", () => { $("#mediaState").textContent = "正在播放"; $("#mediaMessage").classList.remove("visible"); });
$("#mediaPlayer").addEventListener("error", () => {
	if (state.hls) return;
	const error = $("#mediaPlayer").error;
	const detail = error ? `MediaError ${error.code}${error.message ? ` · ${error.message}` : ""}` : "未知媒体错误";
	showMediaError(`视频播放失败：${detail}`);
	reportClientLog("error", "media", "视频播放失败", detail);
});
window.addEventListener("beforeunload", (event) => {
	if (!state.editor?.dirty && !state.preview.items.some((item) => item.editor?.dirty)) return;
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
  if (!event.target.closest("#memorySearchWrap")) setMemorySearchHistoryOpen(false);
});
document.addEventListener("keydown", (event) => {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    activateMemories();
    return;
  }
  if (event.key === "Escape" && $("#transferQueue").classList.contains("memory-fullscreen")) {
    event.preventDefault();
    toggleMemoryFullscreen(false);
    return;
  }
  const imageDialog = $("#imageDialog");
  // In tab mode the dialog stays open while the focus is elsewhere, so only take
  // the arrow keys when the preview is the modal window or actually focused.
  if (imageDialog.open && state.image && (imageDialog.parentElement === document.body || imageDialog.contains(event.target))) {
    if (event.key === "ArrowLeft") changeImage(-1);
    if (event.key === "ArrowRight") changeImage(1);
    if (event.key === "+" || event.key === "=") setImageZoom(state.image.zoom * 1.2);
    if (event.key === "-") setImageZoom(state.image.zoom / 1.2);
    return;
  }
  if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return;
  if (event.target.closest("input, textarea, select, [contenteditable='true'], dialog")) return;
  const selection = selectedEntries(state.activePane);
  if (event.key === "Delete" && selection.length) {
    event.preventDefault();
    deleteEntries(state.activePane, selection);
    return;
  }
  if (event.key === "F2" && selection.length === 1) {
    event.preventDefault();
    renameEntry(state.activePane, selection[0]);
    return;
  }
  if (locateEntryByKey(state.activePane, event.key)) event.preventDefault();
});
window.addEventListener("blur", () => { hideContextMenu(); hideLocalTree(); hideBookmarkMenu(); hideTerminalMenu(); pauseActiveMedia(); });
document.addEventListener("visibilitychange", () => { if (document.hidden) pauseActiveMedia(); });
setInterval(() => {
  if (state.taskFilter === "queue" && state.tasks.some((task) => ["running", "verifying"].includes(task.status))) renderTaskList();
}, 1000);

main();
