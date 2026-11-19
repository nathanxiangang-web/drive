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
};
const titles = {
  public: "全部文件",
  private: "云空间",
  tasks: "云下载",
  admin: "管理",
};
const statuses = {
  QUEUED: "等待提交",
  DISPATCHING: "提交中",
  RUNNING: "网盘下载中",
  VERIFYING: "正在确认目标文件",
  READY: "已保存到云空间",
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
  el("login").hidden = false;
  el("login-form").hidden = false;
  el("register-form").hidden = true;
  el("show-register").hidden = document.body.dataset.registration !== "true";
  el("workspace").hidden = true;
  el("file-rows").replaceChildren();

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
  state.downloads = account.downloads === true;
  state.admin = account.role === "admin";
  el("nav-admin").hidden = !state.admin;
  el("login").hidden = true;
  el("workspace").hidden = false;
  show(state.admin && account.setup ? "admin" : "public");
}
function show(view) {
  if (view === "admin" && !state.admin) return;
  state.request++;
  state.view = view;
  state.parts = [];
  state.directory = "";
  clearSearch();
  resetPage();
  el("title").textContent = titles[view];
  el("files").hidden = view !== "public" && view !== "private";
  el("admin").hidden = view !== "admin";
  el("tasks").hidden = view !== "tasks";
  el("new-task").hidden =
    !state.downloads || view === "tasks" || view === "admin";
  el("task-form").hidden = !state.downloads;
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
async function loadFiles(refresh = false) {
  const ticket = ++state.request;
  const query = new URLSearchParams({
    area: state.view,
    dir: state.directory,
    offset: state.offset,
  });
  if (state.keyword) query.set("q", state.keyword);
  if (refresh) query.set("refresh", "1");
  el("files-status").textContent = state.keyword
    ? "正在搜索…"
    : "正在读取目录…";
  el("refresh").disabled = true;
  el("prev").disabled = true;
  el("next").disabled = true;
  crumbs();
  try {
    const data = await request("/api/files?" + query);
    if (ticket !== state.request) return;
    const rows = data.items.map((item) => {
      const row = document.createElement("tr"),
        icon = document.createElement("span"),
        name = action(item.name, () => {
          if (item.directory) {
            const parent = item.dir ?? state.directory;
            browse(parent ? parent + "/" + item.name : item.name);
          } else openFile(item);
        });
      icon.className = "row-icon" + (item.directory ? " folder" : "");
      icon.textContent = item.directory
        ? "目录"
        : (extension(item.name) || "文件").slice(0, 4).toUpperCase();
      icon.setAttribute("aria-hidden", "true");
      name.className = "row-name";
      const first = cell(name);
      first.prepend(icon);
      if (state.keyword && item.dir) {
        const where = visibleParts(item.dir).join(" / ");
        if (where) {
          const breadcrumb = document.createElement("span");
          breadcrumb.className = "search-result-path";
          breadcrumb.textContent = where;
          first.append(breadcrumb);
        }
      }
      const ops = document.createElement("div");
      ops.className = "row-ops";
      if (!item.directory) {
        const link = document.createElement("a");
        link.href = fileURL(item);
        link.textContent = "下载";
        link.rel = "noreferrer";
        ops.append(link);
      }
      row.append(
        first,
        cell(item.directory ? "—" : size(item.size)),
        cell(ops),
      );
      return row;
    });
    el("file-rows").replaceChildren(...rows);
    state.next = data.next || 0;
    el("page-number").textContent = "第 " + state.page + " 页";
    el("files-status").textContent = rows.length
      ? state.keyword
        ? "搜索结果 · " + state.keyword
        : ""
      : state.keyword
        ? data.next
          ? "本页没有匹配项，可查看下一页。"
          : "没有找到匹配的文件或目录。"
        : state.view === "private"
          ? "此目录暂无文件，云下载结果会保存在云空间。"
          : "此目录暂无文件";
    el("prev").disabled = state.previous.length === 0;
    el("next").disabled = state.next === 0;
  } catch (error) {
    if (ticket !== state.request) return;
    if (error.status === 401) showLogin();
    else {
      el("file-rows").replaceChildren();
      el("files-status").textContent = error.message;
    }
  } finally {
    if (ticket === state.request) el("refresh").disabled = false;
  }
}
function openFile(item) {
  const ext = extension(item.name);
  if (!videoTypes.has(ext) && !audioTypes.has(ext)) {
    location.assign(fileURL(item));
    return;
  }
  el("player-name").textContent = item.name;
  el("player-error").textContent = "";
  el("video").src = fileURL(item);
  el("player").showModal();
}
function closePlayer() {
  el("video").pause();
  el("video").removeAttribute("src");
  el("video").load();
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
async function loadTasks() {
  const ticket = ++state.request;
  el("refresh-tasks").disabled = true;
  try {
    const jobs = await request("/api/tasks");
    if (ticket !== state.request) return;
    el("task-rows").replaceChildren(
      ...jobs.map((job) => {
        const row = document.createElement("li"),
          name = document.createElement("strong"),
          status = document.createElement("span");
        name.textContent = job.name || "云下载 " + timestamp(job.created);
        status.textContent = taskStatus(job);
        const controls = document.createElement("div");
        controls.className = "row-ops";
        if (job.status === "READY")
          controls.append(action("打开云空间", () => show("private")));
        controls.append(
          action("清除记录", () =>
            clearTaskRecords("/api/tasks/" + encodeURIComponent(job.id)),
          ),
        );
        row.append(name, status, controls);
        return row;
      }),
    );
    el("clear-tasks").disabled = jobs.length === 0;
    if (!jobs.length && el("task-status").textContent === "")
      el("task-status").textContent = "暂无任务";
  } catch (error) {
    if (ticket !== state.request) return;
    if (error.status === 401) showLogin();
    else el("task-status").textContent = error.message;
  } finally {
    if (ticket === state.request) el("refresh-tasks").disabled = false;
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
el("new-task").addEventListener("click", () => {
  show("tasks");
  el("source").focus();
});
el("refresh").addEventListener("click", () => loadFiles(true));
el("file-search").addEventListener("submit", (event) => {
  event.preventDefault();
  state.keyword = el("search-keyword").value.trim();
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
  el("task-status").textContent = "正在保存任务…";
  try {
    await post("/api/tasks", state.pending);
    state.pending = null;
    el("source").value = "";
    el("task-status").textContent = "任务已保存";
    if (state.view === "tasks") await loadTasks();
  } catch (error) {
    el("task-status").textContent = error.status
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
  if (el("player").open)
    el("player-error").textContent = "当前直链暂不可播放，请关闭后重试。";
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

el("nav-admin").addEventListener("click", () => show("admin"));

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
