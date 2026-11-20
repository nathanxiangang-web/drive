"use strict";

const playbackStatus = {
  connecting: "正在获取播放地址…",
  buffering: "正在缓冲首帧…",
  manual: "点击播放器上的播放按钮继续。",
  failed: "视频暂时无法播放，可重新加载或下载到设备。",
};

function bindMedia(video, report) {
  let generation = 0;
  let starting = false;
  const ready = () => {
    if (!starting || video.readyState < HTMLMediaElement.HAVE_CURRENT_DATA)
      return;
    starting = false;
    report("ready");
  };
  const failed = () => {
    if (!video.hasAttribute("src")) return;
    starting = false;
    report("failed");
  };
  video.addEventListener("loadedmetadata", () => {
    if (starting) report("buffering");
  });
  video.addEventListener("loadeddata", ready);
  video.addEventListener("playing", ready);
  video.addEventListener("error", failed);
  return {
    start(url) {
      const attempt = ++generation;
      starting = true;
      report("connecting");
      video.src = url;
      video.play().catch((error) => {
        if (attempt !== generation || error.name === "AbortError") return;
        if (error.name === "NotAllowedError") {
          report("manual");
        } else failed();
      });
    },
    stop() {
      generation++;
      starting = false;
      video.pause();
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
