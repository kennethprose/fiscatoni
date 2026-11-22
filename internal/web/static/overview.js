"use strict";

const el = id => document.getElementById(id);
const form = el("overview-filters");
const seriesToggles = document.querySelectorAll("[data-monthly-series]");
const chartToggles = document.querySelectorAll("[data-chart-type]");
const pieCharts = new Set();
const monthlyPageSize = 60;
const pieColors = ["#39775b", "#517994", "#aa6950", "#8465a1", "#a4792d", "#47877e", "#94617b", "#758749"];
const shareFormat = new Intl.NumberFormat(undefined, {style: "percent", maximumFractionDigits: 1});
const monthFormat = new Intl.DateTimeFormat(undefined, {month: "long", year: "numeric", timeZone: "UTC"});
const shortMonth = new Intl.DateTimeFormat(undefined, {month: "short", year: "2-digit", timeZone: "UTC"});
const wholeNumber = new Intl.NumberFormat("en-US");
const compactUSD = new Intl.NumberFormat("en-US", {style: "currency", currency: "USD", notation: "compact", maximumFractionDigits: 1});
const banks = {chase: "Chase", capitalone: "Capital One", robinhood: "Robinhood", "robinhood-card": "Robinhood"};
let displayedReport;
let showAllCategories = false;
let monthlyPage = 0;
let pendingReport;

function usd(cents) {
  const value = BigInt(cents);
  const magnitude = value < 0n ? -value : value;
  const sign = value < 0n ? "−" : "";
  return sign + "$" + wholeNumber.format(magnitude / 100n) + "." + String(magnitude % 100n).padStart(2, "0");
}

function monthDate(month) {
  return new Date(month + "-01T00:00:00Z");
}

function lastDay(month) {
  const date = monthDate(month);
  date.setUTCMonth(date.getUTCMonth() + 1, 0);
  return month + "-" + date.getUTCDate();
}

function transactionsLink(account, from, to, extra = {}) {
  const filters = new URLSearchParams({from, to, direction: "spend", ...extra});
  if (account) filters.set("account", account);
  return "/transactions?" + filters;
}

function element(tag, style, text) {
  const node = document.createElement(tag);
  node.className = style;
  if (text !== undefined) node.textContent = text;
  return node;
}

function tickStep(maximum) {
  const quarter = (maximum + 3n) / 4n;
  const scale = 10n ** BigInt(Math.max(0, quarter.toString().length - 1));
  return [1n, 2n, 5n, 10n].map(step => step * scale).find(step => step >= quarter);
}

function percent(value, maximum) {
  return Number(BigInt(value) * 10000n / maximum) / 100 + "%";
}

function renderMonthly(account, series, start, end) {
  let high = 0n;
  let low = 0n;
  for (const {groups} of series) {
    for (const {cents} of groups) {
      const value = BigInt(cents);
      if (value > high) high = value;
      if (value < low) low = value;
    }
  }
  const step = high > low ? tickStep(high - low) : 2500n;
  const positiveTicks = high > low ? (high + step - 1n) / step : 4n;
  const negativeTicks = (-low + step - 1n) / step;
  const totalTicks = positiveTicks + negativeTicks;
  const range = step * totalTicks;
  const axis = element("div", "month-axis");
  axis.setAttribute("aria-hidden", "true");
  for (let tickIndex = positiveTicks; tickIndex >= -negativeTicks; tickIndex--) {
    const cents = step * tickIndex;
    const magnitude = cents < 0n ? -cents : cents;
    const tick = magnitude >= 100000n
      ? (cents < 0n ? "−" : "") + compactUSD.format(Number(magnitude) / 100)
      : usd(cents).replace(/\.00$/, "");
    axis.append(element("span", "", tick));
  }
  const columns = element("ul", series.length > 1 ? "monthly-bars grouped-bars" : "monthly-bars");
  columns.style.setProperty("--zero-ratio", Number(positiveTicks) / Number(totalTicks));
  columns.style.setProperty("--tick-gap", 100 / Number(totalTicks) + "%");
  for (let index = start; index < end; index++) {
    const {label} = series[0].groups[index];
    const month = monthDate(label);
    const li = element("li", "month-column");
    const bars = element("div", "month-pair");
    for (const {name, direction, groups} of series) {
      const group = groups[index];
      const cents = BigInt(group.cents);
      const mark = element(group.count ? "a" : "div", "month-mark bar-" + direction);
      const bar = element("div", "month-fill");
      mark.classList.toggle("negative-bar", cents < 0n);
      mark.classList.toggle("has-value", cents !== 0n);
      bar.style.height = percent(cents < 0n ? -cents : cents, range);
      const description = monthFormat.format(month) + " " + name + ", " + usd(group.cents);
      if (group.count) {
        mark.href = transactionsLink(account, label + "-01", lastDay(label), {direction});
        mark.setAttribute("aria-label", description + ". View transactions.");
      } else {
        mark.setAttribute("role", "img");
        mark.setAttribute("aria-label", description);
      }
      mark.title = description;
      mark.append(bar);
      bars.append(mark);
    }
    li.append(bars, element("span", "month-label", shortMonth.format(month)));
    columns.append(li);
  }
  el("monthly-chart").replaceChildren(axis, columns);
}

function renderSelectedMonthly() {
  const {report, account, from, to} = displayedReport;
  const active = [...seriesToggles].filter(button => button.getAttribute("aria-pressed") === "true");
  const series = active.map(({dataset}) => {
    switch (dataset.monthlySeries) {
    case "income":
      return {name: "income", direction: "income", groups: report.income};
    case "spend":
      return {name: "spending", direction: "spend", groups: report.months};
    case "net":
      return {name: "net savings", direction: "net", groups: report.months.map((spending, index) => {
        const income = report.income[index];
        return {
          label: spending.label,
          cents: BigInt(income.cents) - BigInt(spending.cents),
          count: income.count + spending.count
        };
      })};
    }
  });
  const chart = el("monthly-chart");
  const ledgerLink = el("report-ledger");
  const count = report.months.length;
  chart.hidden = ledgerLink.hidden = !series.length;
  el("monthly-pages").hidden = !series.length || count <= monthlyPageSize;
  el("monthly-empty").hidden = series.length > 0;
  if (!series.length) {
    chart.replaceChildren();
    return;
  }
  const start = monthlyPage * monthlyPageSize;
  const end = Math.min(start + monthlyPageSize, count);
  renderMonthly(account, series, start, end);
  el("monthly-previous").disabled = start === 0;
  el("monthly-next").disabled = end === count;
  el("monthly-page-range").textContent = monthFormat.format(monthDate(report.months[start].label)) + " – " +
    monthFormat.format(monthDate(report.months[end - 1].label));
  const {direction, name} = series.find(({direction}) => direction === "spend") ?? series.at(-1);
  ledgerLink.href = transactionsLink(account, from, to, {direction});
  ledgerLink.textContent = "View " + (direction === "net" ? "income and spending" : name) + " transactions";
}

function pieColor(index) {
  return pieColors[index] ?? `hsl(${(index * 137.5) % 360} 39% 44%)`;
}

function shareText(cents, total) {
  const share = total === 0n ? 0 : Number(cents) / Number(total);
  return share > 0 && share < .001 ? "<0.1%" : shareFormat.format(share);
}

function svgElement(tag) {
  return document.createElementNS("http://www.w3.org/2000/svg", tag);
}

function renderPie(container, groups, total, linkFor) {
  container.replaceChildren();
  if (total === 0n) {
    container.append(element("p", "chart-empty", "No spending in this period."));
    return;
  }
  // Each slice also has a keyboard-accessible link in the legend.
  const svg = svgElement("svg");
  svg.setAttribute("viewBox", "0 0 200 200");
  svg.setAttribute("aria-hidden", "true");
  const turn = 2 * Math.PI;
  const pointAt = angle => `${100 + 96 * Math.cos(angle)} ${100 + 96 * Math.sin(angle)}`;
  let spent = 0n;
  for (const [index, group] of groups.entries()) {
    const cents = BigInt(group.cents);
    if (cents === 0n) continue;
    const start = Number(spent) / Number(total) * turn - Math.PI / 2;
    spent += cents;
    const end = Number(spent) / Number(total) * turn - Math.PI / 2;
    const angle = end - start;
    if (angle <= 0) continue;
    const whole = angle >= turn - 1e-10;
    const slice = svgElement(whole ? "circle" : "path");
    if (whole) {
      slice.setAttribute("cx", "100");
      slice.setAttribute("cy", "100");
      slice.setAttribute("r", "96");
    } else {
      slice.setAttribute("d", `M 100 100 L ${pointAt(start)} A 96 96 0 ${angle > Math.PI ? 1 : 0} 1 ${pointAt(end)} Z`);
    }
    slice.setAttribute("fill", pieColor(index));
    const link = svgElement("a");
    link.setAttribute("href", linkFor(group));
    link.setAttribute("tabindex", "-1");
    const title = svgElement("title");
    title.textContent = (group.label || "Uncategorized") + " · " + usd(cents) + " · " + shareText(cents, total);
    link.append(title, slice);
    svg.append(link);
  }
  container.append(svg);
}

function renderRank(kind, groups, linkFor, limit = groups.length) {
  const empty = el(kind + "-empty");
  const list = el(kind + "-chart");
  const pie = el(kind + "-pie");
  const isPie = pieCharts.has(kind);
  const total = isPie ? groups.reduce((sum, group) => sum + BigInt(group.cents), 0n) : 0n;
  if (pie) {
    pie.hidden = !isPie || !groups.length;
    if (!pie.hidden) renderPie(pie, groups, total, linkFor);
  }
  list.classList.toggle("pie-legend", isPie);
  empty.hidden = groups.length > 0;
  list.replaceChildren();
  if (!groups.length) return;
  const maximum = BigInt(groups[0].cents);
  for (const [index, group] of groups.slice(0, limit).entries()) {
    const li = element("li", "");
    const link = element("a", "rank-link");
    link.href = linkFor(group);
    const label = group.label || "Uncategorized";
    const line = element("span", "rank-line");
    const name = element("span", "rank-label", label);
    const amount = element("strong", "rank-amount", usd(group.cents));
    if (isPie) {
      const key = element("span", "pie-key");
      const swatch = element("span", "pie-swatch");
      swatch.style.backgroundColor = BigInt(group.cents) ? pieColor(index) : "#e8eee8";
      swatch.setAttribute("aria-hidden", "true");
      key.append(swatch, name);
      const value = element("span", "pie-value");
      value.append(amount, element("small", "pie-share", shareText(group.cents, total)));
      line.append(key, value);
      link.append(line);
    } else {
      line.append(name, amount);
      const rail = element("span", "rank-rail");
      const fill = element("span", "rank-fill");
      rail.setAttribute("aria-hidden", "true");
      fill.hidden = BigInt(group.cents) === 0n;
      if (!fill.hidden) fill.style.width = percent(group.cents, maximum);
      rail.append(fill);
      link.append(line, rail);
    }
    li.append(link);
    list.append(li);
  }
}

function renderCategories() {
  const {report, account, from, to} = displayedReport;
  const limit = showAllCategories ? report.categories.length : 6;
  renderRank("category", report.categories, ({label}) => transactionsLink(account, from, to, {category: label}), limit);
  const toggle = el("show-categories");
  toggle.hidden = report.categories.length <= 6;
  toggle.textContent = showAllCategories ? "Show fewer categories" : "Show all categories";
  toggle.setAttribute("aria-expanded", String(showAllCategories));
}

function renderAccounts() {
  const {report, from, to} = displayedReport;
  const groups = report.accounts.map(group => ({...group, label: accountName(group)}));
  renderRank("account", groups, ({id}) => transactionsLink(id, from, to));
}

async function getJSON(url, signal) {
  const response = await fetch(url, {signal});
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || "Try again.");
  return data;
}

function accountName({label, bank}) {
  return label + " · " + (Object.hasOwn(banks, bank) ? banks[bank] : bank);
}

async function loadAccounts() {
  try {
    const accounts = await getJSON("/api/accounts");
    const select = el("report-account");
    const active = select.value;
    select.length = 1;
    for (const account of accounts) {
      select.add(new Option(accountName(account), account.id));
    }
    select.value = active;
    el("account-error").hidden = true;
  } catch {
    el("account-error").textContent = "Could not load account choices. Update charts to try again.";
    el("account-error").hidden = false;
  }
}

async function loadReport() {
  pendingReport?.abort();
  const request = new AbortController();
  pendingReport = request;
  const fields = new FormData(form);
  const query = new URLSearchParams(fields);
  const account = fields.get("account");
  el("report-content").hidden = true;
  el("report-error").hidden = true;
  el("report-status").textContent = "Loading spending…";
  el("report-status").hidden = false;
  try {
    const report = await getJSON("/api/spending?" + query, request.signal);
    if (request.signal.aborted) return;
    const start = report.start;
    const from = start + "-01";
    const to = lastDay(report.end);
    if (!fields.get("start") && !el("report-start").value) el("report-start").value = start;
    if (!fields.get("end") && !el("report-end").value) el("report-end").value = report.end;
    displayedReport = {report, account, from, to};
    showAllCategories = false;
    monthlyPage = 0;
    const total = report.months.reduce((sum, group) => sum + BigInt(group.cents), 0n);
    const count = report.months.reduce((sum, group) => sum + group.count, 0);
    const largest = report.categories[0];
    el("total-spend").textContent = usd(total);
    el("spend-count").textContent = wholeNumber.format(count);
    el("spend-period").textContent = monthFormat.format(monthDate(start)) + " – " + monthFormat.format(monthDate(report.end));
    el("largest-category").textContent = largest ? (largest.label || "Uncategorized") : "—";
    el("largest-amount").textContent = largest ? usd(largest.cents) : "";
    renderSelectedMonthly();
    el("monthly-chart").scrollLeft = 0;
    renderCategories();
    renderRank("merchant", report.merchants, ({label}) => transactionsLink(account, from, to, {merchant: label}));
    renderAccounts();
    el("report-content").hidden = false;
    el("report-status").hidden = true;
  } catch (error) {
    if (request.signal.aborted) return;
    el("report-status").hidden = true;
    el("report-error-text").textContent = error.message;
    el("report-error").hidden = false;
  }
}

form.onsubmit = event => {
  event.preventDefault();
  const start = el("report-start").value;
  const end = el("report-end");
  if (start && (!end.value || start > end.value)) {
    end.setCustomValidity("Choose an ending month on or after the starting month.");
    end.reportValidity();
    return;
  }
  if (!el("account-error").hidden) loadAccounts();
  loadReport();
};
for (const id of ["report-start", "report-end"]) {
  el(id).oninput = () => el("report-end").setCustomValidity("");
}
for (const button of seriesToggles) {
  button.onclick = () => {
    button.setAttribute("aria-pressed", String(button.getAttribute("aria-pressed") !== "true"));
    renderSelectedMonthly();
  };
}
for (const button of chartToggles) {
  button.onclick = () => {
    const {chartKind, chartType} = button.dataset;
    if (chartType === "pie") pieCharts.add(chartKind);
    else pieCharts.delete(chartKind);
    for (const option of chartToggles) {
      if (option.dataset.chartKind === chartKind) option.setAttribute("aria-pressed", String(option === button));
    }
    if (chartKind === "category") renderCategories();
    else renderAccounts();
  };
}
function changeMonthlyPage(step) {
  monthlyPage += step;
  renderSelectedMonthly();
  el("monthly-chart").scrollLeft = 0;
}
el("monthly-previous").onclick = () => changeMonthlyPage(-1);
el("monthly-next").onclick = () => changeMonthlyPage(1);
el("show-categories").onclick = () => {
  showAllCategories = !showAllCategories;
  renderCategories();
};
el("report-retry").onclick = loadReport;
window.addEventListener("pageshow", event => {
  if (event.persisted) {
    loadAccounts();
    loadReport();
  }
});
loadAccounts();
loadReport();
