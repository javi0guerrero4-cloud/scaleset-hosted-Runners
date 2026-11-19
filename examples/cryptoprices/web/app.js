(() => {
  const POLL_MS = 15000;

  const grid = document.getElementById("grid");
  const empty = document.getElementById("empty");
  const banner = document.getElementById("banner");
  const statusEl = document.getElementById("status");
  const statusText = document.getElementById("status-text");

  let coins = [];
  let updatedAt = null;

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }

  function stat(label, value, field) {
    const wrap = el("div", "stat");
    const dd = el("dd", null, value);
    if (field) dd.dataset.field = field;
    wrap.append(el("dt", null, label), dd);
    return wrap;
  }

  function sparkline(coin) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "spark " + (coin.spark_up ? "spark-up" : "spark-down"));
    svg.setAttribute("viewBox", "0 0 100 32");
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");

    const poly = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
    poly.setAttribute("points", coin.points);
    poly.setAttribute("fill", "none");
    poly.dataset.field = "spark";
    svg.append(poly);
    return svg;
  }

  function buildCard(coin) {
    const card = el("article", "card");
    card.dataset.coin = coin.id;

    const head = el("div", "card-head");
    const iconWrap = el("span", "icon-wrap");
    iconWrap.append(el("span", "icon-letter", coin.initial));

    const img = el("img", "icon");
    img.src = coin.image;
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    img.addEventListener("error", () => img.remove());
    iconWrap.append(img);

    const title = el("div", "card-title");
    title.append(el("span", "name", coin.name), el("span", "symbol", coin.symbol));

    head.append(iconWrap, title, el("span", "rank", "#" + coin.rank));

    const priceRow = el("div", "price-row");
    const price = el("div", "price", coin.price);
    price.dataset.field = "price";
    const change = el("span", "pill " + (coin.up ? "up" : "down"), coin.change);
    change.dataset.field = "change";
    priceRow.append(price, change);

    const stats = el("dl", "stats");
    stats.append(
      stat("Market cap", coin.market_cap, "market_cap"),
      stat("Volume 24h", coin.volume, "volume")
    );

    const range = el("div", "stat stat-wide");
    const rangeValue = el("dd", null, coin.range);
    rangeValue.dataset.field = "range";
    range.append(el("dt", null, "24h range"), rangeValue);

    card.append(head, priceRow, sparkline(coin), stats, range);
    return card;
  }

  function render() {
    const frag = document.createDocumentFragment();
    for (const coin of coins) frag.append(buildCard(coin));
    grid.replaceChildren(frag);
    empty.hidden = coins.length > 0;
  }

  function update() {
    for (const coin of coins) {
      const card = grid.querySelector('[data-coin="' + coin.id + '"]');
      if (!card) continue;

      card.querySelector('[data-field="price"]').textContent = coin.price;

      const change = card.querySelector('[data-field="change"]');
      change.textContent = coin.change;
      change.className = "pill " + (coin.up ? "up" : "down");

      card.querySelector('[data-field="market_cap"]').textContent = coin.market_cap;
      card.querySelector('[data-field="volume"]').textContent = coin.volume;
      card.querySelector('[data-field="range"]').textContent = coin.range;

      const poly = card.querySelector('[data-field="spark"]');
      if (poly) {
        poly.setAttribute("points", coin.points);
        poly.closest("svg").setAttribute(
          "class",
          "spark " + (coin.spark_up ? "spark-up" : "spark-down")
        );
      }
    }
  }

  function tickAgo() {
    if (!updatedAt) return;
    const seconds = Math.max(0, Math.round((Date.now() - updatedAt.getTime()) / 1000));
    statusText.textContent =
      seconds < 5
        ? "Updated just now"
        : "Updated " + (seconds < 60 ? seconds + "s ago" : Math.round(seconds / 60) + "m ago");
  }

  function showError(message) {
    statusEl.classList.add("error");
    statusText.textContent = message;
    if (message !== "Reconnecting…") {
      banner.textContent = message;
      banner.hidden = false;
    }
  }

  function clearError() {
    statusEl.classList.remove("error");
    banner.hidden = true;
  }

  async function poll() {
    try {
      const res = await fetch("/api/prices", { cache: "no-store" });
      if (!res.ok) throw new Error("HTTP " + res.status);

      const data = await res.json();
      const incoming = Array.isArray(data.coins) ? data.coins : [];

      if (incoming.length) {
        const needsRender =
          coins.length !== incoming.length ||
          grid.childElementCount !== incoming.length;
        coins = incoming;
        if (needsRender) render();
        else update();

        updatedAt = data.updated_at ? new Date(data.updated_at) : null;
        clearError();
        tickAgo();
      } else if (data.error) {
        showError(data.error);
      }
    } catch (err) {
      showError("Reconnecting…");
    }
  }

  poll();
  setInterval(poll, POLL_MS);
  setInterval(tickAgo, 1000);
})();
