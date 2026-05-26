// qrsgen — landing page live telemetry.
// Polls the public stats endpoint every 10s and updates the cards.
// Toggle persists in localStorage (key: qrsgenTelemetry). Default OFF
// (opt-in) to respect privacy of visitors who don't want background fetches.
(function () {
  const POLL_MS = 10_000;
  const STORAGE_KEY = "qrsgenTelemetry";

  function $(id) { return document.getElementById(id); }

  function init() {
    const container = $("qrsgen-stats");
    const controls = $("telemetry-controls");
    const toggleBtn = $("telemetry-toggle");
    const statusEl = $("telemetry-status");
    if (!container || !controls || !toggleBtn) return;

    const endpoint = container.dataset.endpoint;
    if (!endpoint || endpoint.indexOf("REPLACE") !== -1) {
      // Endpoint not configured yet. Don't show the controls at all.
      return;
    }

    controls.hidden = false;
    let intervalId = null;

    function setUI(enabled, ok) {
      toggleBtn.textContent = enabled ? "Pausar" : "Activar";
      container.hidden = !enabled;
      if (!enabled) {
        statusEl.textContent = "Telemetría pausada";
      } else if (ok === false) {
        statusEl.textContent = "Telemetría no disponible (endpoint inaccesible)";
      } else {
        statusEl.textContent = "Telemetría en vivo (actualiza cada 10 s)";
      }
    }

    function setVal(id, v) {
      const el = $(id);
      if (el) el.textContent = v;
    }

    async function fetchOnce() {
      try {
        const res = await fetch(endpoint, { cache: "no-store" });
        if (!res.ok) throw new Error("HTTP " + res.status);
        const d = await res.json();
        setVal("stat-connected", d.instances_connected ?? "—");
        setVal("stat-scanned", (d.qrs_scanned_total ?? 0).toLocaleString());
        setVal("stat-active", d.installations_active ?? "—");
        setVal("stat-total", d.instances_total ?? "—");
        setVal("stat-in", (d.messages_in_total ?? 0).toLocaleString());
        setVal("stat-out", (d.messages_out_total ?? 0).toLocaleString());
        setUI(true, true);
      } catch (err) {
        ["stat-connected", "stat-scanned", "stat-active",
         "stat-total", "stat-in", "stat-out"].forEach(id => setVal(id, "—"));
        setUI(true, false);
      }
    }

    function start() {
      localStorage.setItem(STORAGE_KEY, "on");
      setUI(true);
      fetchOnce();
      intervalId = setInterval(fetchOnce, POLL_MS);
    }

    function stop() {
      localStorage.setItem(STORAGE_KEY, "off");
      if (intervalId) {
        clearInterval(intervalId);
        intervalId = null;
      }
      setUI(false);
    }

    toggleBtn.addEventListener("click", () => {
      if (intervalId) stop();
      else start();
    });

    if (localStorage.getItem(STORAGE_KEY) === "on") {
      start();
    } else {
      setUI(false);
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
