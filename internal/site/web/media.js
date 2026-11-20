"use strict";

const playbackStatus = {
  connecting: "正在获取播放地址…",
  buffering: "正在缓冲首帧…",
  refreshing: "正在重新获取播放地址…",
  manual: "点击播放器上的播放按钮继续。",
  failed: "视频暂时无法播放，可重新加载或下载到设备。",
};

function bindMedia(video, report) {
  let generation = 0;
  let starting = false;
  let active = false;
  let preparedAt = 0;
  let retryURL = "";
  const ready = () => {
    if (!starting || video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA)
      return;
    starting = false;
    report("ready");
  };
  const failed = () => {
    if (!active || !video.hasAttribute("src")) return;
    if (starting && retryURL) {
      const next = retryURL;
      retryURL = "";
      play(next, false, "refreshing");
      return;
    }
    starting = false;
    report("failed");
  };
  const play = (url, reuse, phase) => {
    const attempt = ++generation;
    active = starting = true;
    preparedAt = 0;
    report(phase);
    if (!reuse) video.src = url;
    if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) ready();
    else if (video.readyState >= HTMLMediaElement.HAVE_METADATA)
      report("buffering");
    video.play().catch((error) => {
      if (attempt !== generation || error.name === "AbortError") return;
      if (error.name === "NotAllowedError") report("manual");
      else failed();
    });
  };
  video.addEventListener("loadedmetadata", () => {
    if (starting) report("buffering");
  });
  video.addEventListener("loadeddata", ready);
  video.addEventListener("playing", ready);
  video.addEventListener("error", () => {
    if (video.error) failed();
  });
  return {
    prepare(url) {
      if (
        active ||
        (video.getAttribute("src") === url &&
          !video.error &&
          Date.now() - preparedAt < 45000)
      )
        return;
      preparedAt = Date.now();
      video.preload = "metadata";
      video.src = url;
    },
    start(url, freshURL = "") {
      retryURL = freshURL;
      const reuse =
        video.getAttribute("src") === url &&
        !video.error &&
        Date.now() - preparedAt < 45000;
      play(url, reuse, "connecting");
    },
    stop() {
      generation++;
      active = starting = false;
      retryURL = "";
      preparedAt = 0;
      video.pause();
      video.preload = "none";
      if (video.hasAttribute("src")) {
        video.removeAttribute("src");
        video.load();
      }
      report("ready");
    },
  };
}

function warmMediaConnection(origin) {
  if (!origin || document.querySelector("link[data-drive-media]")) return;
  const hint = document.createElement("link");
  hint.rel = "preconnect";
  hint.href = origin;
  hint.dataset.driveMedia = "";
  document.head.append(hint);
}
