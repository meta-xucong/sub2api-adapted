(() => {
  const authTokenKey = "auth_token";
  const refreshTokenKey = "refresh_token";
  const authUserKey = "auth_user";
  const authTokenExpiresKey = "token_expires_at";
  const alchemyTokenKey = "alchemy_veyra_access_token";
  const alchemyAccountKey = "alchemy_veyra_account";
  const state = {
    authenticated: false,
    user: null,
    alchemyBaseUrl: "https://alchemy.aiself.vip",
  };

  const routeTargets = {
    login: "/login",
    logout: "/",
    "sub2api-console": "/_veyra/return?target=sub2api-console",
    alchemy: "/_veyra/return?target=alchemy",
    "alchemy-mobile": "/_veyra/return?target=alchemy-mobile",
    home: "/_veyra/return?target=home",
  };

  function loginUrl(next = routeTargets.home) {
    const url = new URL("/login", window.location.origin);
    url.searchParams.set("redirect", next);
    return `${url.pathname}${url.search}`;
  }

  function readAuthToken() {
    try {
      return localStorage.getItem(authTokenKey) || "";
    } catch {
      return "";
    }
  }

  function readRefreshToken() {
    try {
      return localStorage.getItem(refreshTokenKey) || "";
    } catch {
      return "";
    }
  }

  function readStoredUser() {
    try {
      const raw = localStorage.getItem(authUserKey);
      return raw ? JSON.parse(raw) : null;
    } catch {
      return null;
    }
  }

  function userLabel(user) {
    return user?.username || user?.email || user?.name || "账户";
  }

  function renderSession() {
    const sessionState = document.getElementById("sessionState");
    const loginLink = document.getElementById("loginLink");
    const logoutButton = document.getElementById("logoutButton");
    if (sessionState) {
      sessionState.textContent = state.authenticated ? `已登录：${userLabel(state.user)}` : "未登录";
      sessionState.dataset.authenticated = state.authenticated ? "true" : "false";
    }
    if (loginLink) {
      loginLink.textContent = state.authenticated ? "账户" : "登录";
      loginLink.setAttribute("href", state.authenticated ? "/dashboard" : loginUrl(routeTargets.home));
    }
    if (logoutButton) {
      logoutButton.hidden = !state.authenticated;
    }
  }

  async function loadPortalConfig() {
    try {
      const response = await fetch("/api/veyra/portal/config", { credentials: "same-origin" });
      const payload = await response.json().catch(() => ({}));
      const baseUrl = payload?.data?.alchemy_base_url;
      if (response.ok && typeof baseUrl === "string" && baseUrl.trim()) {
        state.alchemyBaseUrl = baseUrl.trim().replace(/\/+$/, "");
      }
    } catch {
      state.alchemyBaseUrl = "https://alchemy.aiself.vip";
    }
  }

  async function refreshSession() {
    const token = readAuthToken();
    state.authenticated = Boolean(token);
    state.user = readStoredUser();
    renderSession();
  }

  function clearStoredSession() {
    try {
      localStorage.removeItem(authTokenKey);
      localStorage.removeItem(refreshTokenKey);
      localStorage.removeItem(authUserKey);
      localStorage.removeItem(authTokenExpiresKey);
      localStorage.removeItem(alchemyTokenKey);
      localStorage.removeItem(alchemyAccountKey);
    } catch {
      // Local storage can be unavailable in restricted browser modes.
    }
    state.authenticated = false;
    state.user = null;
    renderSession();
  }

  async function logout() {
    const refreshToken = readRefreshToken();
    const token = readAuthToken();
    if (token || refreshToken) {
      try {
        await fetch("/api/v1/auth/logout", {
          method: "POST",
          credentials: "same-origin",
          headers: {
            ...(token ? { Authorization: `Bearer ${token}` } : {}),
            "Content-Type": "application/json",
          },
          body: JSON.stringify(refreshToken ? { refresh_token: refreshToken } : {}),
        });
      } catch {
        // Logout remains local even if the network request fails.
      }
    }
    clearStoredSession();
    window.location.href = "/";
  }

  async function issueAlchemyTicket(target) {
    const token = readAuthToken();
    if (!token) {
      window.location.href = loginUrl(routeTargets[target] || routeTargets.alchemy);
      return null;
    }
    const response = await fetch("/api/veyra/login-ticket", {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ intent: "alchemy" }),
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      if (response.status === 401 || response.status === 403) {
        window.location.href = loginUrl(routeTargets[target] || routeTargets.alchemy);
        return null;
      }
      throw new Error(payload?.message || "无法创建 Alchemy 登录票据");
    }
    return payload?.data?.ticket || "";
  }

  async function launchAlchemy({ mobile = false } = {}) {
    const ticket = await issueAlchemyTicket(mobile ? "alchemy-mobile" : "alchemy");
    if (!ticket) return;
    const destination = new URL(mobile ? "/h5" : "/", state.alchemyBaseUrl);
    destination.searchParams.set("ticket", ticket);
    window.location.href = destination.toString();
  }

  async function handleRoute(route) {
    if (route === "logout") {
      await logout();
      return;
    }
    if (route === "login") {
      window.location.href = state.authenticated ? "/dashboard" : loginUrl(routeTargets.home);
      return;
    }
    if (route === "sub2api-console") {
      window.location.href = state.authenticated ? "/dashboard" : loginUrl(routeTargets["sub2api-console"]);
      return;
    }
    if (route === "alchemy" || route === "alchemy-mobile") {
      if (!state.authenticated) {
        window.location.href = loginUrl(routeTargets[route]);
        return;
      }
      await launchAlchemy({ mobile: route === "alchemy-mobile" });
      return;
    }
    if (route === "home") {
      window.location.href = "/";
      return;
    }
  }

  function targetFromReturnUrl() {
    if (window.location.pathname !== "/_veyra/return") return "";
    const target = new URLSearchParams(window.location.search).get("target") || "home";
    if (target === "alchemy" || target === "alchemy-mobile" || target === "sub2api-console") return target;
    return "home";
  }

  document.addEventListener("click", (event) => {
    const target = event.target instanceof Element ? event.target.closest("[data-route]") : null;
    if (!target) return;
    const route = target.getAttribute("data-route");
    if (!route || !routeTargets[route]) return;

    event.preventDefault();
    handleRoute(route).catch((error) => {
      const sessionState = document.getElementById("sessionState");
      if (sessionState) sessionState.textContent = error.message || "跳转失败";
    });
  });

  async function init() {
    await loadPortalConfig();
    await refreshSession();
    const returnTarget = targetFromReturnUrl();
    if (returnTarget) {
      await handleRoute(returnTarget);
    }
  }

  init().catch(() => renderSession());
})();
