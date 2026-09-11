package orders

// extractJS pulls orders out of the rendered order list. AliExpress
// still ships readable class names on this page (order-item and
// friends), so the selectors below follow its own structure rather than
// guessing at the shape of the DOM. Prices are rendered one character
// per span to frustrate scrapers, which innerText reassembles for us.
const extractJS = `(() => {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const pick = (root, sel) => {
    const el = root.querySelector(sel);
    return el ? clean(el.innerText) : "";
  };
  const strip = (s, label) => clean(s.replace(new RegExp("^\\s*" + label + "\\s*:?\\s*", "i"), ""));

  const out = [];
  for (const card of document.querySelectorAll(".order-item")) {
    const info = card.querySelector(".order-item-header-right-info");
    let date = "";
    let id = "";
    if (info) {
      for (const line of clean(info.innerText).split(/(?=Date:|Ref\. Number:|Fecha:|N\.º de ref)/i)) {
        if (/^(date|fecha)/i.test(line)) date = strip(line, "(?:date|fecha)");
        if (/ref/i.test(line)) id = (line.match(/(\d{6,})/) || [])[1] || id;
      }
    }
    if (!id) {
      const link = card.querySelector("a[href*='orderId=']");
      id = link ? (link.href.match(/orderId=(\d+)/) || [])[1] || "" : "";
    }
    if (!id) continue;

    const items = [];
    for (const row of card.querySelectorAll(".order-item-content-body")) {
      const title = pick(row, ".order-item-content-info-name");
      if (!title) continue;
      const link = row.querySelector("a[href*='/item/']");
      let href = link ? link.getAttribute("href") || "" : "";
      if (href.startsWith("//")) href = "https:" + href;
      const number = pick(row, ".order-item-content-info-number");
      const qty = pick(row, ".order-item-content-info-number-quantity");
      items.push({
        title,
        url: href,
        image: (row.querySelector(".order-item-content-img img") || {}).src || "",
        variant: pick(row, ".order-item-content-info-sku"),
        qty: qty.replace(/^x/i, ""),
        price: clean(number.replace(qty, "")),
      });
    }

    out.push({
      id,
      date,
      status: pick(card, ".order-item-header-status-text"),
      total: strip(pick(card, ".order-item-content-opt-price-total"), "(?:total|importe total)"),
      store: pick(card, ".order-item-store-name"),
      url: "https://www.aliexpress.com/p/order/detail.html?orderId=" + id,
      items,
    });
  }
  return JSON.stringify(out);
})()`

// detailJS reads the item rows off a single order's detail page, which
// is the only place AliExpress prints titles for a multi-item order.
const detailJS = `(() => {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim();
  const pick = (root, sel) => {
    const el = root.querySelector(sel);
    return el ? clean(el.innerText) : "";
  };
  const out = [];
  for (const row of document.querySelectorAll(".order-detail-item-content")) {
    const title = pick(row, ".item-title");
    if (!title) continue;
    const link = row.querySelector("a[href*='/item/']");
    let href = link ? link.getAttribute("href") || "" : "";
    if (href.startsWith("//")) href = "https:" + href;
    const qty = pick(row, ".item-price-quantity");
    out.push({
      title,
      url: href,
      image: (row.querySelector(".order-detail-item-content-img img") || {}).src || "",
      variant: pick(row, ".item-sku-attr"),
      qty: qty.replace(/^x/i, ""),
      price: clean(pick(row, ".item-price").replace(qty, "")),
    });
  }
  return JSON.stringify(out);
})()`
