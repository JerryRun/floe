// Headless state-machine check for the preview host switching (window <-> task
// tab). No browser is available here, so a permissive DOM shim stands in: every
// selector resolves to a node, and <dialog> semantics (open/show/showModal/close
// plus the close event) are modelled faithfully because that is what the feature
// actually depends on.
"use strict";
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const source = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
const nodes = new Map();

function makeNode(key) {
  const node = {
    key,
    tagName: key.startsWith("#") ? "DIV" : "DIV",
    children: [],
    parentElement: null,
    dataset: {},
    style: { setProperty() {}, removeProperty() {} },
    hidden: false,
    open: false,
    value: "",
    textContent: "",
    innerHTML: "",
    title: "",
    disabled: false,
    paused: true,
    currentTime: 0,
    listeners: new Map(),
    classes: new Set(),
  };
  node.classList = {
    add: (...names) => names.forEach((name) => node.classes.add(name)),
    remove: (...names) => names.forEach((name) => node.classes.delete(name)),
    toggle: (name, force) => {
      const next = force === undefined ? !node.classes.has(name) : Boolean(force);
      if (next) node.classes.add(name); else node.classes.delete(name);
      return next;
    },
    contains: (name) => node.classes.has(name),
  };
  node.addEventListener = (type, handler) => {
    if (!node.listeners.has(type)) node.listeners.set(type, []);
    node.listeners.get(type).push(handler);
  };
  node.removeEventListener = () => {};
  node.dispatch = (type, event = {}) => {
    for (const handler of node.listeners.get(type) || []) handler({ preventDefault() {}, stopPropagation() {}, target: node, currentTarget: node, ...event });
  };
  node.appendChild = (child) => {
    if (child.parentElement) child.parentElement.children = child.parentElement.children.filter((item) => item !== child);
    child.parentElement = node;
    node.children.push(child);
    return child;
  };
  node.append = (...children) => children.forEach((child) => node.appendChild(child));
  node.removeChild = (child) => { node.children = node.children.filter((item) => item !== child); child.parentElement = null; };
  node.querySelector = (selector) => resolve(selector);
  node.querySelectorAll = () => [];
  node.closest = () => null;
  node.contains = (other) => {
    for (let cursor = other; cursor; cursor = cursor.parentElement) if (cursor === node) return true;
    return false;
  };
  node.focus = () => {};
  node.getBoundingClientRect = () => ({ width: 800, height: 400, top: 0, left: 0, right: 800, bottom: 400 });
  node.setAttribute = () => {};
  node.removeAttribute = () => {};
  node.getAttribute = () => null;
  node.insertAdjacentHTML = () => {};
  node.replaceChildren = (...children) => { node.children = children; };
  node.scrollIntoView = () => {};
  node.setSelectionRange = (start, end) => { node.selectionStart = start; node.selectionEnd = end; };
  node.play = () => { node.paused = false; return Promise.resolve(); };
  node.pause = () => { node.paused = true; };
  node.load = () => {};
  node.canPlayType = () => "";
  // Form controls are reached as form.elements.<name>, so hand back a node per
  // name on demand rather than modelling the real markup.
  const fields = new Map();
  node.elements = new Proxy({}, {
    get: (_target, name) => {
      if (typeof name !== "string") return undefined;
      if (!fields.has(name)) fields.set(name, makeNode(`${key}[name=${name}]`));
      return fields.get(name);
    },
    has: () => true,
  });
  // Dialog behaviour, which is the whole point of the test.
  node.show = () => { node.open = true; node.modal = false; };
  node.showModal = () => { node.open = true; node.modal = true; };
  node.close = () => {
    if (!node.open) return;
    node.open = false;
    node.modal = false;
    node.dispatch("close");
  };
  return node;
}

function resolve(selector) {
  if (!selector) return null;
  if (!nodes.has(selector)) nodes.set(selector, makeNode(selector));
  return nodes.get(selector);
}

const body = resolve("body");
const previewStrips = [resolve("#previewTabs"), resolve("#editorPreviewTabs"), resolve("#imagePreviewTabs"), resolve("#mediaPreviewTabs")];
const document = {
  body,
  documentElement: resolve("html"),
  querySelector: resolve,
  querySelectorAll: (selector) => selector === "[data-preview-strip]" ? previewStrips : [],
  createElement: (tag) => {
    const node = makeNode(`<${tag}>${Math.random()}`);
    if (tag === "span") {
      Object.defineProperty(node, "textContent", {
        get: () => node._textContent || "",
        set: (value) => { node._textContent = String(value ?? ""); node.innerHTML = node._textContent; },
      });
    }
    return node;
  },
  addEventListener() {},
  removeEventListener() {},
  createDocumentFragment: () => makeNode("#fragment"),
  createTextNode: (text) => ({ textContent: text }),
  activeElement: body,
  hidden: false,
  visibilityState: "visible",
  cookie: "",
  title: "",
};

const storage = { getItem: () => null, setItem() {}, removeItem() {} };
const sandbox = {
  document,
  window: null,
  console,
  localStorage: storage,
  sessionStorage: storage,
  location: { protocol: "http:", host: "127.0.0.1:7777", href: "http://127.0.0.1:7777/", origin: "http://127.0.0.1:7777", pathname: "/", search: "" },
  navigator: { userAgent: "node", clipboard: { writeText: () => Promise.resolve() }, platform: "Win32" },
  fetch: () => Promise.reject(new Error("offline")),
  Headers: class { constructor() {} has() { return false; } set() {} },
  WebSocket: class { constructor() { this.readyState = 0; } send() {} close() {} addEventListener() {} },
  EventSource: class { addEventListener() {} close() {} },
  requestAnimationFrame: () => 0,
  cancelAnimationFrame: () => {},
  setTimeout: () => 0,
  clearTimeout: () => {},
  setInterval: () => 0,
  clearInterval: () => {},
  getComputedStyle: () => ({ getPropertyValue: () => "160px", lineHeight: "20px" }),
  TextEncoder,
  TextDecoder,
  URL,
  URLSearchParams,
  Intl,
  matchMedia: () => ({ matches: false, addEventListener() {} }),
  alert() {},
  confirm: () => true,
  prompt: () => null,
  performance: { now: () => 0 },
  crypto: { getRandomValues: (array) => array, randomUUID: () => "id" },
  Date,
  Math,
  JSON,
  Promise,
  Error,
  DataTransfer: class { setData() {} getData() { return ""; } },
  Element: class {},
  HTMLElement: class {},
  Node: class {},
  Image: class { set src(_v) {} },
  ResizeObserver: class { observe() {} disconnect() {} },
  IntersectionObserver: class { observe() {} disconnect() {} },
  MutationObserver: class { observe() {} disconnect() {} },
  AbortController: class { constructor() { this.signal = {}; } abort() {} },
  Hls: undefined,
  addEventListener() {},
  removeEventListener() {},
};
sandbox.window = sandbox;
sandbox.globalThis = sandbox;

const context = vm.createContext(sandbox);
try {
  vm.runInContext(source, context, { filename: "app.js" });
} catch (error) {
  console.error("app.js failed to evaluate in the shim:", error.message);
  process.exit(2);
}

const failures = [];
function check(label, condition) {
  if (!condition) failures.push(label);
}

const state = vm.runInContext("state", context);
const editor = resolve("#editorDialog");
const image = resolve("#imageDialog");
const media = resolve("#mediaDialog");
const hosts = resolve("#previewHosts");
const queue = resolve("#transferQueue");
const previewTab = resolve('.task-tab[data-task-filter="preview"]');

// 1. Window mode is the default and stays modal, exactly as before.
sandbox.openPreview("text", "local", "/srv/app/config.yaml");
check("window mode opens the editor as a modal", editor.open && editor.modal === true);
check("window mode leaves the editor in the body", editor.parentElement === null || editor.parentElement === body);
check("the preview task tab appears", previewTab.hidden === false);
check("one preview item is tracked", state.preview.items.length === 1 && sandbox.activePreview().kind === "text");
check("window strips render the first file", previewStrips.every((strip) => strip.innerHTML.includes("config.yaml")));

// 2. A second file of the same kind remains available in window mode.
sandbox.openPreview("text", "local", "/srv/app/notes.txt");
check("same-kind previews stay tracked in window mode", state.preview.items.length === 2);
check("same-kind window tab is active", sandbox.activePreview().path === "/srv/app/notes.txt" && editor.open === true);
check("window strips retain both files", previewStrips.every((strip) => strip.innerHTML.includes("config.yaml") && strip.innerHTML.includes("notes.txt")));
sandbox.toggleDialogMaximized(editor, resolve("#maximizeEditor"));
sandbox.activatePreview(state.preview.items.find((item) => item.path.endsWith("config.yaml")).id);
check("switching files preserves window fullscreen", editor.classes.has("maximized") && state.preview.maximized === true);

// 3. Collapsing into the task area: non-modal, reparented, tab selected.
sandbox.setPreviewHost("panel");
check("tab mode moves the dialog into the panel", editor.parentElement === hosts);
check("tab mode is non-modal", editor.open === true && editor.modal === false);
check("tab mode selects the preview tab", state.taskFilter === "preview" && state.preview.host === "panel");
check("tab mode expands the task area", queue.classes.has("collapsed") === false);
check("the editor survived the move", state.preview.items.length === 2);
check("task strip retains both files", resolve("#previewTabs").innerHTML.includes("config.yaml") && resolve("#previewTabs").innerHTML.includes("notes.txt"));

// 4. A second kind joins the strip instead of evicting the first.
sandbox.openPreview("image", "local", "/srv/pic/a.png");
check("both kinds and both files are tracked", state.preview.items.length === 3);
check("the new preview is active and shown", image.parentElement === hosts && image.open === true);
check("the previous preview is parked, not destroyed", editor.parentElement === body && editor.open === false);
check("the active kind followed the new file", sandbox.activePreview().kind === "image");
check("switching preview kind preserves fullscreen", image.classes.has("maximized") && state.preview.maximized === true);

// 5. Switching back through the strip re-shows the parked editor.
sandbox.activatePreview(state.preview.items.find((item) => item.kind === "text").id);
check("switching back shows the editor", editor.open === true && editor.modal === false);
check("switching back parks the image", image.open === false);
check("nothing was dropped", state.preview.items.length === 3);

// 6. Media pauses when its tab is not the visible one, resumes when it is.
sandbox.openPreview("media", "local", "/srv/v/clip.mp4");
const player = resolve("#mediaPlayer");
const mediaItem = sandbox.activePreview();
mediaItem.media = { source: "/media/clip.mp4", currentTime: 0, playing: true };
player.paused = false;
state.taskFilter = "queue";
sandbox.updatePreviewVisibility();
check("a hidden preview tab pauses playback", player.paused === true);
sandbox.mountPreviewInPanel("media");
check("returning to the tab stays paused", player.paused === true && mediaItem.media.playing === false);
player.paused = false;
mediaItem.media.playing = true;
queue.classList.add("collapsed");
sandbox.updatePreviewVisibility();
check("collapsing the task area pauses playback", player.paused === true);
queue.classList.remove("collapsed");
sandbox.updatePreviewVisibility();
check("expanding the task area stays paused", player.paused === true && mediaItem.media.playing === false);

// 7. Restoring to window mode: modal again, parked dialogs go home too.
sandbox.setPreviewHost("dialog");
check("window mode is modal again", media.open === true && media.modal === true);
check("window mode empties the panel", hosts.children.length === 0);
check("window mode leaves the preview tab", state.taskFilter !== "preview" && state.preview.host === "dialog");
check("no preview was dropped by the restore", state.preview.items.length === 4);

// 8. Closing any independent preview window closes every preview and its window.
media.close();
check("window close removes every preview tab", state.preview.items.length === 0);
check("window close closes every preview dialog", editor.open === false && image.open === false && media.open === false);
check("the preview task tab disappears when empty", previewTab.hidden === true);
check("no items remain", state.preview.items.length === 0);
check("the panel is empty", hosts.children.length === 0);

if (failures.length) {
  console.error("FAIL");
  for (const failure of failures) console.error("  - " + failure);
  process.exit(1);
}
console.log("preview host switching: all checks passed");
