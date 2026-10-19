(() => {
  // Keep login callbacks on their exact path so app.js can complete the intent.
  if (window.location.pathname === "/_veyra/return") return;
  const params = new URLSearchParams(window.location.search);
  if (params.get("desktop") === "1") return;
  const mobileUa = /Android|iPhone|iPod|BlackBerry|IEMobile|Opera Mini|Windows Phone|Mobile/i.test(
    navigator.userAgent || "",
  );
  const compactTouch =
    window.matchMedia &&
    window.matchMedia("(hover: none) and (pointer: coarse) and (max-width: 820px)").matches;
  if (mobileUa || compactTouch) {
    window.location.replace(`/_veyra/mobile.html${window.location.search}${window.location.hash}`);
  }
})();
