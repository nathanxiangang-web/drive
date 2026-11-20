"use strict";

const play = document.getElementById("share-play");
if (play) {
  const video = document.getElementById("shared-video");
  const status = document.getElementById("share-play-status");
  play.addEventListener("click", () => {
    status.hidden = true;
    play.hidden = true;
    video.hidden = false;
    video.src = play.dataset.url;
    video.play().catch(() => {});
  });
  video.addEventListener("error", () => {
    video.hidden = true;
    play.hidden = false;
    status.hidden = false;
  });
}
