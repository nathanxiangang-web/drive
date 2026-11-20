"use strict";

const play = document.getElementById("share-play");
if (play) {
  const video = document.getElementById("shared-video");
  const status = document.getElementById("share-play-status");
  const player = bindMedia(video, (phase) => {
    status.hidden = phase === "ready";
    status.textContent = playbackStatus[phase] || "";
    play.hidden = phase !== "failed";
    video.hidden = phase === "failed";
  });
  warmMediaConnection(document.body.dataset.mediaOrigin);
  play.addEventListener("click", () => player.start(play.dataset.url));
}
