"use strict";

const el = id => document.getElementById(id);
const form = el("overview-filters");
const seriesToggles = document.querySelectorAll("[data-monthly-series]");
const monthFormat = new Intl.DateTimeFormat(undefined, {month: "long", year: "numeric", timeZone: "UTC"});
const shortMonth = new Intl.DateTimeFormat(undefined, {month: "short", year: "2-digit", timeZone: "UTC"});
const wholeNumber = new Intl.NumberFormat("en-US");
const compactUSD = new Intl.NumberFormat("en-US", {style: "currency", currency: "USD", notation: "compact", maximumFractionDigits: 1});
const banks = {chase: "Chase", capitalone: "Capital One", robinhood: "Robinhood", "robinhood-card": "Robinhood"};
let displayedReport;
let showAllCategories = false;
let pendingReport;

function usd(cents) {
  const value = BigInt(cents);
  return "$" + wholeNumber.format(value / 100n) + "." + String(value % 100n).padStart(2, "0");
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

function renderMonthly(account, series) {
  const maximum = series.flatMap(({groups}) => groups)
    .reduce((max, group) => BigInt(group.cents) > max ? BigInt(group.cents) : max, 0n);
  const step = maximum ? tickStep(maximum) : 2500n;
  const axis = element("div", "month-axis");
  axis.setAttribute("aria-hidden", "true");
  for (let i = 4; i >= 0; i--) {
    const cents = Number(step * BigInt(i));
    const tick = cents >= 100000 ? compactUSD.format(cents / 100) : usd(cents).replace(/\.00$/, "");
    axis.append(element("span", "", tick));
  }
  const columns = element("ul", series.length > 1 ? "monthly-bars grouped-bars" : "monthly-bars");
  for (const [index, {label}] of series[0].groups.entries()) {
    const month = monthDate(label);
    const li = element("li", "month-column");
    const bars = element("div", "month-pair");
    for (const {name, direction, groups} of series) {
      const group = groups[index];
      const mark = element(group.count ? "a" : "div", "month-mark bar-" + direction);
      const bar = element("div", "month-fill");
      bar.style.height = percent(group.cents, step * 4n);
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
  const series = active.map(({dataset}) => dataset.monthlySeries === "income"
    ? {name: "income", direction: "income", groups: report.income}
    : {name: "spending", direction: "spend", groups: report.months});
  const chart = el("monthly-chart");
  const ledgerLink = el("report-ledger");
  chart.hidden = ledgerLink.hidden = !series.length;
  el("monthly-empty").hidden = series.length > 0;
  if (!series.length) {
    chart.replaceChildren();
    return;
  }
  renderMonthly(account, series);
  const {direction, name} = series.at(-1);
  ledgerLink.href = transactionsLink(account, from, to, {direction});
  ledgerLink.textContent = "View " + name + " transactions";
}

function renderRank(kind, groups, linkFor) {
  const empty = el(kind + "-empty");
  const list = el(kind + "-chart");
  empty.hidden = groups.length > 0;
  list.replaceChildren();
  if (!groups.length) return;
  const maximum = BigInt(groups[0].cents);
  for (const group of groups) {
    const li = element("li", "");
    const link = element("a", "rank-link");
    link.href = linkFor(group);
    const label = group.label || "Uncategorized";
    const line = element("span", "rank-line");
    line.append(element("span", "rank-label", label), element("strong", "rank-amount", usd(group.cents)));
    const rail = element("span", "rank-rail");
    const fill = element("span", "rank-fill");
    rail.setAttribute("aria-hidden", "true");
    fill.hidden = BigInt(group.cents) === 0n;
    if (!fill.hidden) fill.style.width = percent(group.cents, maximum);
    rail.append(fill);
    link.append(line, rail);
    li.append(link);
    list.append(li);
  }
}

function renderCategories() {
  const {report, account, from, to} = displayedReport;
  const groups = showAllCategories ? report.categories : report.categories.slice(0, 6);
  renderRank("category", groups, ({label}) => transactionsLink(account, from, to, {category: label}));
  const toggle = el("show-categories");
  toggle.hidden = report.categories.length <= 6;
  toggle.textContent = showAllCategories ? "Show fewer categories" : "Show all categories";
  toggle.setAttribute("aria-expanded", String(showAllCategories));
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
    const start = report.months[0].label;
    const from = start + "-01";
    const to = lastDay(report.end);
    if (!fields.get("end") && !el("report-end").value) el("report-end").value = report.end;
    displayedReport = {report, account, from, to};
    showAllCategories = false;
    const total = report.months.reduce((sum, group) => sum + BigInt(group.cents), 0n);
    const count = report.months.reduce((sum, group) => sum + group.count, 0);
    const largest = report.categories[0];
    el("total-spend").textContent = usd(total);
    el("spend-count").textContent = wholeNumber.format(count);
    el("spend-period").textContent = monthFormat.format(monthDate(start)) + " – " + monthFormat.format(monthDate(report.end));
    el("largest-category").textContent = largest ? (largest.label || "Uncategorized") : "—";
    el("largest-amount").textContent = largest ? usd(largest.cents) : "";
    renderSelectedMonthly();
    renderCategories();
    renderRank("merchant", report.merchants, ({label}) => transactionsLink(account, from, to, {merchant: label}));
    const accountGroups = report.accounts.map(group => ({...group, label: accountName(group)}));
    renderRank("account", accountGroups, ({id}) => transactionsLink(id, from, to));
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
  if (!el("account-error").hidden) loadAccounts();
  loadReport();
};
for (const button of seriesToggles) {
  button.onclick = () => {
    button.setAttribute("aria-pressed", String(button.getAttribute("aria-pressed") !== "true"));
    renderSelectedMonthly();
  };
}
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
