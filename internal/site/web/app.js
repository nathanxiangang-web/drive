"use strict";

const el = (id) => document.getElementById(id);
const state = {
  view: "public",
  parts: [],
  directory: "",
  keyword: "",
  offset: 0,
  previous: [],
  page: 1,
  next: 0,
  request: 0,
  downloads: false,
  admin: false,
  busy: false,
  pending: null,
  rows: [],
  descending: false,
  file: null,
  tasks: [],
  filter: "all",
  locate: null,
  drafts: new Map(),
};
const titles = {
  public: "公共资源",
  private: "我的空间",
  tasks: "云下载",
  admin: "站点管理",
};
const statuses = {
  QUEUED: "等待提交",
  DISPATCHING: "提交中",
  RUNNING: "网盘下载中",
  VERIFYING: "正在确认目标文件",
  READY: "已保存到我的空间",
  UNCERTAIN: "提交待确认",
  FAILED: "下载失败",
  AUTH_REQUIRED: "115 需要重新授权",
  REJECTED: "115 拒绝了此任务",
  DIRECTORY_UNAVAILABLE: "私人目录暂不可用",
};
const videoTypes = new Set(["mp4", "webm", "mkv", "mov", "m4v", "avi"]);
const audioTypes = new Set(["mp3", "m4a", "aac", "flac", "ogg", "wav"]);

async function request(url, options = {}) {
  const response = await fetch(url, {
    cache: "no-store",
    credentials: "same-origin",
    ...options,
  });
  if (response.status === 204) return null;
  const data = await response.json();
  if (!response.ok) {
    const error = new Error(data.error || "请求失败");
    error.status = response.status;
    throw error;
  }
  return data;
}
function post(url, data) {
  return request(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(data),
  });
}
function action(label, handler) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = label;
  button.addEventListener("click", handler);
  return button;
}
function cell(value) {
  const td = document.createElement("td");
  if (typeof value === "string") td.textContent = value;
  else td.append(value);
  return td;
}
function size(n) {
  if (!Number.isFinite(n) || n < 0) return "—";
  const unit = ["B", "KB", "MB", "GB", "TB"];
  let u = 0;
  while (n >= 1024 && u < unit.length - 1) {
    n /= 1024;
    u++;
  }
  return (u ? n.toFixed(1) : n) + " " + unit[u];
}
function extension(name) {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1).toLowerCase() : "";
}
function fileURL(item) {
  if (item.ref) return "/api/file?" + new URLSearchParams({ ref: item.ref });
  return (
    "/api/file?" +
    new URLSearchParams({
      area: state.view,
      dir: item.dir ?? state.directory,
      offset: item.offset ?? state.offset,
      id: item.id,
    })
  );
}
function resetPage() {
  state.offset = 0;
  state.previous = [];
  state.page = 1;
  state.next = 0;
}
function showLogin() {
  state.request++;
  closePlayer();
  for (const dialog of document.querySelectorAll("dialog[open]"))
    dialog.close();
  el("account-menu").open = false;
  el("task-form").reset();
  el("task-toast").hidden = true;
  el("login-password").type = "password";
  el("password-visible").setAttribute("aria-pressed", "false");
  el("password-visible").textContent = "显示密码";
  state.tasks = [];
  state.filter = "all";
  state.rows = [];
  state.drafts.clear();
  el("login").hidden = false;
  el("login-form").hidden = false;
  el("register-form").hidden = true;
  el("show-register").hidden = document.body.dataset.registration !== "true";
  el("workspace").hidden = true;
  el("file-rows").replaceChildren();
  el("file-rows").classList.remove("skeleton");
  el("task-rows").replaceChildren();
  el("user-rows").replaceChildren();
  el("status-grid").replaceChildren();
  el("new-user-form").reset();
  el("user-filter").reset();
  el("openlist-form").reset();
  el("manage-message").textContent = "";
  el("openlist-message").textContent = "";
  el("connection-state").textContent = "";
  state.admin = false;
  state.pending = null;
}
function showWorkspace(account) {
  el("account").textContent = account.login;
  el("avatar").textContent = account.login.slice(0, 1).toUpperCase();
  state.downloads = account.downloads === true;
  state.admin = account.role === "admin";
  el("nav-admin").hidden = !state.admin;
  el("login").hidden = true;
  el("workspace").hidden = false;
  show(state.admin && account.setup ? "admin" : "public");
  if (state.admin && account.setup) selectAdminPanel("openlist");
  request("/api/tasks")
    .then(updateTaskBadge)
    .catch(() => {});
}
function show(view, locate = null) {
  if (view === "admin" && !state.admin) return;
  state.request++;
  state.view = view;
  state.parts = [];
  state.directory = "";
  state.rows = [];
  el("file-rows").replaceChildren();
  clearSearch();
  state.locate = locate;
  if (locate?.keyword) {
    state.keyword = locate.keyword.slice(0, 100);
    el("search-keyword").value = state.keyword;
  }
  resetPage();
  el("account-menu").open = false;
  el("file-search").hidden = view !== "public" && view !== "private";
  el("files").hidden = view !== "public" && view !== "private";
  el("admin").hidden = view !== "admin";
  el("tasks").hidden = view !== "tasks";
  el("new-task").hidden = !state.downloads;
  el("empty-download").hidden = !state.downloads;
  for (const key of Object.keys(titles))
    el("nav-" + key).setAttribute("aria-pressed", String(view === key));
  if (view === "tasks") loadTasks();
  else if (view === "admin") loadAdmin();
  else loadFiles();
}
function clearSearch() {
  state.keyword = "";
  el("search-keyword").value = "";
}
function visibleParts(directory) {
  if (!directory) return [];
  const parts = directory.split("/");
  if (parts[0] !== "!legacy") return parts;
  parts.shift();
  if (/^task-[a-f0-9]{12}$/.test(parts[0] || "")) parts.shift();
  return parts;
}
function browse(directory) {
  state.directory = directory;
  state.parts = visibleParts(directory);
  state.rows = [];
  el("file-rows").replaceChildren();
  state.locate = null;
  clearSearch();
  resetPage();
  loadFiles();
}
function crumbs() {
  const items = [action(titles[state.view], () => browse(""))];
  const physical = state.directory ? state.directory.split("/") : [];
  const hidden = physical.length - state.parts.length;
  state.parts.forEach((part, i) => {
    const divider = document.createElement("span");
    divider.textContent = "/";
    items.push(
      divider,
      action(part, () => {
        browse(physical.slice(0, hidden + i + 1).join("/"));
      }),
    );
  });
  el("crumbs").replaceChildren(...items);
}
const nameOrder = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
});
const dateFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "numeric",
  day: "numeric",
});

function playable(item) {
  const ext = extension(item.name);
  return videoTypes.has(ext) || audioTypes.has(ext);
}

function downloadLink(item, className = "") {
  const link = document.createElement("a");
  link.href = fileURL(item);
  link.download = "";
  link.rel = "noreferrer";
  link.textContent = "下载到设备";
  link.className = className;
  return link;
}

function renderFiles(items) {
  const ordered = [...items].sort(
    (a, b) =>
      Number(b.directory) - Number(a.directory) ||
      (state.descending ? -1 : 1) * nameOrder.compare(a.name, b.name),
  );
  const rows = ordered.map((item) => {
    const row = document.createElement("tr");
    const icon = document.createElement("span");
    const open = action(item.name, () => {
      if (!item.directory) {
        openFile(item);
        return;
      }
      const parent = item.dir ?? state.directory;
      browse(parent ? parent + "/" + item.name : item.name);
    });
    icon.className =
      "row-icon" +
      (item.directory ? " folder" : playable(item) ? " media" : "");
    icon.setAttribute("aria-hidden", "true");
    if (!item.directory)
      icon.textContent = playable(item)
        ? "▸"
        : (extension(item.name) || "文件").slice(0, 4).toUpperCase();
    open.className = "row-name";
    const label = document.createElement("span");
    label.textContent = item.name;
    open.replaceChildren(icon, label);
    const name = document.createElement("div");
    name.className = "file-name";
    name.append(open);
    if (state.keyword && item.dir) {
      const parent = visibleParts(item.dir).join(" / ");
      if (parent) {
        const location = document.createElement("span");
        location.className = "search-result-path";
        location.textContent = parent;
        name.append(location);
      }
    }
    const ops = document.createElement("div");
    ops.className = "row-ops file-row-ops";
    if (item.directory) {
      const arrow = action("›", () => open.click());
      arrow.setAttribute("aria-label", "打开 " + item.name);
      arrow.className = "ghost-button";
      ops.append(arrow);
    } else {
      if (playable(item)) {
        const play = action("播放", () => openFile(item, true));
        play.className = "inline-play ghost-button";
        ops.append(play);
      }
      const more = action("⋯", () => showFileActions(item));
      more.className = "ghost-button file-menu-trigger";
      more.setAttribute("aria-label", item.name + " 的更多操作");
      ops.append(more);
    }
    row.append(
      cell(name),
      cell(item.directory ? "—" : size(item.size)),
      cell(item.modified ? dateFormat.format(new Date(item.modified)) : "—"),
      cell(ops),
    );
    if (state.locate?.id === item.id) {
      row.classList.add("located");
      row.dataset.located = "true";
    }
    return row;
  });
  el("file-rows").replaceChildren(...rows);
  el("files-count").textContent = rows.length + " 项";
}

function showFileActions(item) {
  const dialog = el("file-actions");
  el("actions-name").textContent = item.name;
  const open = (fn) => () => {
    dialog.close();
    fn();
  };
  el("actions-buttons").replaceChildren(
    action(
      playable(item) ? "播放" : "查看文件",
      open(() => openFile(item, playable(item))),
    ),
    downloadLink(item),
    action(
      "分享文件",
      open(() => showShare(item)),
    ),
    action(
      "分享记录",
      open(() => showShare(item, true)),
    ),
  );
  dialog.showModal();
}

async function loadFiles(refresh = false) {
  const ticket = ++state.request;
  const query = new URLSearchParams({
    area: state.view,
    dir: state.directory,
    offset: state.offset,
  });
  if (state.keyword) query.set("q", state.keyword);
  if (refresh) query.set("refresh", "1");
  el("files-title").textContent = state.keyword
    ? "搜索结果"
    : state.parts.at(-1) || titles[state.view];
  el("clear-search").hidden = !state.keyword;
  el("search-scope").hidden = !state.keyword;
  el("empty-space").hidden = true;
  el("files-status").textContent = state.keyword
    ? "正在搜索…"
    : "正在读取目录…";
  el("refresh").disabled = true;
  el("prev").disabled = true;
  el("next").disabled = true;
  el("files").setAttribute("aria-busy", "true");
  if (!state.rows.length) el("file-rows").classList.add("skeleton");
  crumbs();
  try {
    const data = await request("/api/files?" + query);
    if (ticket !== state.request) return;
    state.rows = data.items;
    renderFiles(state.rows);
    state.next = data.next || 0;
    el("page-number").textContent = "第 " + state.page + " 页";
    const emptySpace =
      !state.rows.length &&
      !state.keyword &&
      state.view === "private" &&
      !state.directory &&
      !state.next;
    el("empty-space").hidden = !emptySpace;
    el("files-status").textContent =
      state.rows.length || emptySpace
        ? ""
        : state.keyword
          ? state.next
            ? "本页没有匹配项，可查看下一页。"
            : "没有匹配的文件。可以缩短关键词，或清除搜索。"
          : "此目录暂无文件";
    const located = el("file-rows").querySelector('[data-located="true"]');
    if (located && !state.locate.scrolled) {
      state.locate.scrolled = true;
      el("files-status").textContent = "已定位到云下载结果";
      located.scrollIntoView({ block: "nearest" });
    }
    el("prev").disabled = state.previous.length === 0;
    el("next").disabled = state.next === 0;
  } catch (error) {
    if (ticket !== state.request) return;
    if (error.status === 401) showLogin();
    else el("files-status").textContent = error.message + "。可点击刷新重试。";
  } finally {
    if (ticket === state.request) {
      el("refresh").disabled = false;
      el("files").removeAttribute("aria-busy");
      el("file-rows").classList.remove("skeleton");
    }
  }
}

function openFile(item, play = false) {
  state.file = item;
  el("player-name").textContent = item.name;
  el("player-meta").textContent = [
    extension(item.name).toUpperCase(),
    size(item.size),
    visibleParts(item.dir ?? state.directory).join(" / "),
  ]
    .filter(Boolean)
    .join(" · ");
  el("player-download").href = fileURL(item);
  el("player-error").hidden = true;
  el("player-retry").hidden = true;
  const video = el("video");
  video.hidden = !playable(item);
  if (!video.hidden) video.src = fileURL(item);
  el("player").showModal();
  if (play && !video.hidden) video.play().catch(() => {});
}

function closePlayer() {
  const video = el("video");
  video.pause();
  video.removeAttribute("src");
  video.load();
  state.file = null;
  if (el("player").open) el("player").close();
}

function timestamp(iso) {
  return new Date(iso).toLocaleString("zh-CN");
}
function taskStatus(job) {
  if (
    job.provider_visible === "NOT_LISTED" &&
    ["RUNNING", "UNCERTAIN"].includes(job.status)
  )
    return "115 暂未返回该任务";
  const status = statuses[job.status] || job.status;
  if (
    job.status === "RUNNING" &&
    Number.isFinite(job.progress) &&
    job.progress >= 0
  )
    return status + " · " + Math.round(job.progress * 10) / 10 + "%";
  return status;
}
async function clearTaskRecords(endpoint) {
  try {
    await request(endpoint, { method: "DELETE" });
    state.pending = null;
    if (state.view !== "tasks") return;
    el("task-status").textContent = "已清除显示记录";
    await loadTasks();
  } catch (error) {
    if (error.status === 401) showLogin();
    else if (state.view === "tasks")
      el("task-status").textContent = error.message;
  }
}
const taskFilters = {
  all: "全部",
  active: "进行中",
  ready: "已完成",
  attention: "需关注",
};

function taskGroup(job) {
  if (job.status === "READY") return "ready";
  if (
    job.provider_visible !== "NOT_LISTED" &&
    ["QUEUED", "DISPATCHING", "RUNNING", "VERIFYING"].includes(job.status)
  )
    return "active";
  return "attention";
}

function updateTaskBadge(jobs) {
  const count = jobs.filter((job) => taskGroup(job) === "active").length;
  el("task-badge").textContent = String(count);
  el("task-badge").hidden = count === 0;
}

function showTaskDialog(source = "") {
  if (!state.downloads) return;
  if (source) el("source").value = source;
  el("submit-status").textContent = "";
  el("task-dialog").showModal();
  el("source").focus();
}

function showTaskDetail(job) {
  const dialog = el("task-detail");
  el("detail-name").textContent = job.name || "云下载任务";
  el("detail-status").textContent = taskStatus(job);
  el("detail-created").textContent = "提交时间 " + timestamp(job.created);
  const buttons = [];
  if (
    state.downloads &&
    ["FAILED", "REJECTED", "DIRECTORY_UNAVAILABLE", "AUTH_REQUIRED"].includes(
      job.status,
    )
  ) {
    buttons.push(
      action(state.drafts.has(job.id) ? "修改链接" : "重新提交", () => {
        dialog.close();
        el("source").value = state.drafts.get(job.id) || "";
        showTaskDialog();
      }),
    );
  } else {
    buttons.push(
      action("刷新任务", () => {
        dialog.close();
        loadTasks();
      }),
    );
  }
  buttons.push(
    action("清除记录", () => {
      dialog.close();
      clearTaskRecords("/api/tasks/" + encodeURIComponent(job.id));
    }),
  );
  el("detail-actions").replaceChildren(...buttons);
  dialog.showModal();
}

function renderTasks(jobs) {
  const counts = { all: jobs.length, active: 0, ready: 0, attention: 0 };
  for (const job of jobs) counts[taskGroup(job)]++;
  for (const button of el("task-filters").querySelectorAll("button")) {
    const key = button.dataset.filter;
    button.textContent = taskFilters[key] + " " + counts[key];
    button.setAttribute("aria-pressed", String(state.filter === key));
  }
  const rows = jobs
    .filter((job) => state.filter === "all" || taskGroup(job) === state.filter)
    .map((job) => {
      const row = document.createElement("li");
      const name = document.createElement("strong");
      const status = document.createElement("span");
      const statusCell = document.createElement("div");
      const group = taskGroup(job);
      name.textContent = job.name || "云下载 · " + timestamp(job.created);
      status.className = "task-pill " + group;
      status.textContent = taskStatus(job);
      statusCell.className = "task-progress";
      statusCell.append(status);
      if (
        job.status === "RUNNING" &&
        group === "active" &&
        Number.isFinite(job.progress) &&
        job.progress >= 0
      ) {
        const progress = document.createElement("progress");
        progress.max = 100;
        progress.value = job.progress;
        progress.setAttribute("aria-label", "115 下载进度");
        statusCell.append(progress);
      }
      const controls = document.createElement("div");
      controls.className = "row-ops";
      if (job.status === "READY") {
        controls.append(
          action("打开位置", () =>
            show("private", { keyword: job.name, id: job.result_id }),
          ),
        );
      } else {
        controls.append(action("查看详情", () => showTaskDetail(job)));
      }
      controls.append(
        action("清除记录", () =>
          clearTaskRecords("/api/tasks/" + encodeURIComponent(job.id)),
        ),
      );
      row.append(name, statusCell, controls);
      return row;
    });
  el("clear-tasks").disabled = jobs.length === 0;
  if (!rows.length) {
    const empty = document.createElement("li");
    empty.className = "tasks-empty";
    empty.textContent = jobs.length ? "这个分类暂无任务" : "暂无云下载任务";
    rows.push(empty);
  }
  el("task-rows").replaceChildren(...rows);
  updateTaskBadge(jobs);
}

async function loadTasks() {
  const ticket = ++state.request;
  el("refresh-tasks").disabled = true;
  try {
    const jobs = await request("/api/tasks");
    if (ticket !== state.request) return;
    state.tasks = jobs;
    renderTasks(jobs);
  } catch (error) {
    if (ticket !== state.request) return;
    if (error.status === 401) showLogin();
    else el("task-status").textContent = error.message;
  } finally {
    el("refresh-tasks").disabled = false;
  }
}
el("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.submitter;
  if (button) button.disabled = true;
  el("login-error").textContent = "";
  try {
    await post("/api/login", {
      login: el("login-name").value,
      password: el("login-password").value,
    });
    showWorkspace(await request("/api/session"));
  } catch (error) {
    el("login-error").textContent = error.message;
  } finally {
    el("login-password").value = "";
    if (button) button.disabled = false;
  }
});
el("logout").addEventListener("click", async () => {
  try {
    await post("/api/logout", {});
    showLogin();
  } catch (error) {
    if (state.view === "tasks") el("task-status").textContent = error.message;
    else el("files-status").textContent = error.message;
  }
});
for (const key of Object.keys(titles))
  el("nav-" + key).addEventListener("click", () => show(key));
el("new-task").addEventListener("click", () => showTaskDialog());
el("empty-download").addEventListener("click", () => showTaskDialog());
el("browse-public").addEventListener("click", () => show("public"));
for (const id of ["task-close", "task-cancel"])
  el(id).addEventListener("click", () => el("task-dialog").close());
el("detail-close").addEventListener("click", () => el("task-detail").close());
el("actions-close").addEventListener("click", () => el("file-actions").close());
el("task-filters").addEventListener("click", (event) => {
  if (!event.target.dataset.filter) return;
  state.filter = event.target.dataset.filter;
  renderTasks(state.tasks);
});
el("clear-search").addEventListener("click", () => {
  clearSearch();
  state.locate = null;
  resetPage();
  loadFiles();
});
el("sort-files").addEventListener("click", () => {
  state.descending = !state.descending;
  el("sort-files").textContent = state.descending
    ? "本页名称降序"
    : "本页名称升序";
  renderFiles(state.rows);
});
document.addEventListener("keydown", (event) => {
  if (
    event.key !== "/" ||
    event.ctrlKey ||
    event.metaKey ||
    event.target.matches("input, textarea, select, [contenteditable]") ||
    document.querySelector("dialog[open]") ||
    el("file-search").hidden ||
    el("workspace").hidden
  )
    return;
  event.preventDefault();
  el("search-keyword").focus();
});
el("password-visible").addEventListener("click", () => {
  const input = el("login-password");
  const show = input.type === "password";
  input.type = show ? "text" : "password";
  el("password-visible").setAttribute("aria-pressed", String(show));
  el("password-visible").textContent = show ? "隐藏密码" : "显示密码";
});
el("refresh").addEventListener("click", () => loadFiles(true));
el("file-search").addEventListener("submit", (event) => {
  event.preventDefault();
  state.keyword = el("search-keyword").value.trim();
  state.locate = null;
  resetPage();
  loadFiles();
});
el("search-keyword").addEventListener("search", () => {
  if (state.keyword && !el("search-keyword").value) {
    clearSearch();
    resetPage();
    loadFiles();
  }
});
el("prev").addEventListener("click", () => {
  if (state.previous.length) {
    state.offset = state.previous.pop();
    state.page--;
    loadFiles();
  }
});
el("next").addEventListener("click", () => {
  if (state.next) {
    state.previous.push(state.offset);
    state.offset = state.next;
    state.page++;
    loadFiles();
  }
});
el("task-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (state.busy) return;
  const source = el("source").value;
  if (state.pending?.source !== source)
    state.pending = { source, operation_key: crypto.randomUUID() };
  state.busy = true;
  el("submit").disabled = true;
  el("submit-status").textContent = "正在保存任务…";
  try {
    const job = await post("/api/tasks", state.pending);
    if (state.drafts.size >= 20)
      state.drafts.delete(state.drafts.keys().next().value);
    state.drafts.set(job.id, source);
    state.pending = null;
    el("source").value = "";
    el("task-dialog").close();
    const toast = el("task-toast");
    toast.replaceChildren(
      "任务已提交。",
      action("查看任务", () => {
        toast.hidden = true;
        show("tasks");
      }),
    );
    toast.hidden = false;
    if (state.view === "tasks") await loadTasks();
    else
      request("/api/tasks")
        .then(updateTaskBadge)
        .catch(() => {});
  } catch (error) {
    el("submit-status").textContent = error.status
      ? error.message
      : "网络异常，任务状态可在列表里确认。";
    if (error.status >= 400 && error.status < 500) state.pending = null;
    if (error.status === 401) showLogin();
  } finally {
    state.busy = false;
    el("submit").disabled = false;
  }
});
el("refresh-tasks").addEventListener("click", loadTasks);
el("clear-tasks").addEventListener("click", () =>
  clearTaskRecords("/api/tasks"),
);
el("player-close").addEventListener("click", closePlayer);
el("player").addEventListener("cancel", (event) => {
  event.preventDefault();
  closePlayer();
});
el("video").addEventListener("error", () => {
  if (!el("player").open || el("video").hidden) return;
  el("player-error").hidden = false;
  el("player-retry").hidden = false;
});
el("player-share").addEventListener("click", () => {
  if (state.file) showShare(state.file);
});
el("player-retry").addEventListener("click", async () => {
  let item = state.file;
  if (!item) return;
  if (item.ref) {
    await loadFiles(true);
    if (!el("player").open) return;
    item = state.rows.find((row) => row.id === item.id) || item;
    state.file = item;
  }
  el("player-download").href = fileURL(item);
  el("player-error").hidden = true;
  el("player-retry").hidden = true;
  const video = el("video");
  video.src = fileURL(item);
  video.play().catch(() => {});
});
setInterval(() => {
  if (
    state.view === "tasks" &&
    !document.hidden &&
    !el("workspace").hidden &&
    !el("refresh-tasks").disabled &&
    !state.busy
  )
    loadTasks();
}, 20000);
request("/api/session").then(showWorkspace).catch(showLogin);

el("show-register").addEventListener("click", () => {
  el("login-form").hidden = true;
  el("register-form").hidden = false;
  el("register-name").focus();
});
el("back-login").addEventListener("click", () => {
  el("register-form").reset();
  showLogin();
  el("login-name").focus();
});
el("register-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = event.submitter;
  const message = el("register-error");
  submit.disabled = true;
  message.textContent = "";
  try {
    await post("/api/register", {
      login: el("register-name").value.trim(),
      password: el("register-password").value,
    });
    event.target.reset();
    showWorkspace(await request("/api/session"));
  } catch (error) {
    message.textContent = error.message;
  } finally {
    submit.disabled = false;
  }
});
