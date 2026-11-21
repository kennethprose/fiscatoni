"use strict";

const el = id => document.getElementById(id);
const form = el("filters");
const dateFormat = new Intl.DateTimeFormat(undefined, {year: "numeric", month: "short", day: "numeric", timeZone: "UTC"});
const numberFormat = new Intl.NumberFormat();
const banks = {chase: "Chase", capitalone: "Capital One", robinhood: "Robinhood", "robinhood-card": "Robinhood"};
let applied = new URLSearchParams();
let currentPage = 1;
let activeRequest;
let metadataReady = false;
let categoryNames = [];
let merchant = "";

async function json(url, signal) {
  const response = await fetch(url, {signal});
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || "Try again.");
  return data;
}

function filterMessage(text) {
  el("filter-error").textContent = text;
  el("filter-error").hidden = !text;
}

async function loadFilterOptions() {
  try {
    const [accounts, categories] = await Promise.all([
      json("/api/accounts"), json("/api/transaction-categories")
    ]);
    const accountField = form.elements.account;
    const categoryField = form.elements.category;
    const accountValue = accountField.value;
    const categoryValue = categoryField.value;
    accountField.length = 1;
    categoryField.length = 2;
    for (const account of accounts) {
      accountField.add(new Option(account.label + " · " + bankName(account.bank), account.id));
    }
    categoryNames = categories;
    for (const category of categories) {
      categoryField.add(new Option(category, "name:" + category));
    }
    // Keep a selected filter visible when its last matching row is changed or deleted.
    if (categoryValue.startsWith("name:") && !categories.includes(categoryValue.slice(5))) {
      categoryField.add(new Option(categoryValue.slice(5), categoryValue));
    }
    accountField.value = accountValue;
    categoryField.value = categoryValue;
    metadataReady = true;
    filterMessage("");
  } catch {
    filterMessage("Could not load account and category choices. Apply filters to retry.");
  }
}

function bankName(bank) {
  return Object.hasOwn(banks, bank) ? banks[bank] : bank;
}

function applyLinkedFilters() {
  const linked = new URLSearchParams(location.search);
  for (const key of ["from", "to", "direction", "account", "category", "merchant"]) {
    if (linked.has(key)) applied.set(key, linked.get(key));
  }
  for (const key of ["from", "to", "direction"]) {
    form.elements[key].value = applied.get(key) || "";
  }
  const account = applied.get("account");
  if (account) {
    form.elements.account.add(new Option("Selected account", account));
    form.elements.account.value = account;
  }
  if (applied.has("category")) {
    const name = applied.get("category");
    if (name) form.elements.category.add(new Option(name, "name:" + name));
    form.elements.category.value = name ? "name:" + name : "!";
  }
  merchant = applied.get("merchant") || "";
  showMerchantFilter();
}

function showMerchantFilter() {
  el("merchant-filter-label").textContent = "Merchant · " + merchant;
  el("merchant-filter").hidden = !merchant;
}

function shownAmount(row) {
  const cents = BigInt(row.cents);
  const magnitude = cents < 0n ? -cents : cents;
  const sign = cents < 0n ? "−" : cents > 0n ? "+" : "";
  const currency = row.currency === "USD" ? "$" : row.currency + " ";
  return sign + currency + numberFormat.format(magnitude / 100n) + "." + String(magnitude % 100n).padStart(2, "0");
}

function appendSecondary(cell, text) {
  const secondary = document.createElement("small");
  secondary.textContent = text;
  cell.append(secondary);
}

function renderRow(row, body) {
  const tr = body.insertRow();
  const date = document.createElement("time");
  date.dateTime = row.date;
  date.textContent = dateFormat.format(new Date(row.date + "T00:00:00Z"));
  tr.insertCell().append(date);

  const description = tr.insertCell();
  description.textContent = row.merchant || row.description;
  if (row.merchant && row.description && row.description !== row.merchant) {
    appendSecondary(description, row.description);
  }
  const account = tr.insertCell();
  account.textContent = row.account.label;
  appendSecondary(account, bankName(row.account.bank));

  const category = document.createElement("span");
  category.className = row.category ? "ledger-category" : "ledger-category uncategorized";
  category.textContent = row.category || "Uncategorized";
  tr.insertCell().append(category);
  const amount = tr.insertCell();
  amount.className = BigInt(row.cents) > 0n ? "money credit" : "money";
  amount.textContent = shownAmount(row);
  const actions = tr.insertCell();
  actions.className = "ledger-actions";
  for (const [action, text] of [["edit", "Edit category"], ["delete", "Delete"]]) {
    const button = document.createElement("button");
    button.type = "button";
    button.id = action + "-row-" + row.id;
    button.className = "ledger-row-action";
    button.textContent = text;
    button.setAttribute("aria-label", text + " for " + (row.merchant || row.description));
    button.onclick = () => openTransactionDialog(row, action);
    actions.append(button);
  }
}

const dialogs = {edit: el("edit-category"), delete: el("delete-transaction")};
const categoryChoice = el("edit-choice");
const newCategory = el("edit-new");
let selectedTransaction;
let updating = false;

function showEditError(action, text) {
  const error = el(action + "-error");
  error.textContent = text;
  error.hidden = !text;
}

function openTransactionDialog(row, action) {
  selectedTransaction = row;
  showEditError(action, "");
  const name = row.merchant || row.description;
  el(action + "-summary").textContent = `${name} · ${dateFormat.format(new Date(row.date + "T00:00:00Z"))} · ${shownAmount(row)} · ${row.account.label}`;
  if (action === "edit") {
    const names = new Set(categoryNames);
    if (row.category) names.add(row.category);
    categoryChoice.replaceChildren(new Option("Uncategorized", ""));
    for (const category of names) categoryChoice.add(new Option(category, "name:" + category));
    categoryChoice.add(new Option("Add a new category…", "!"));
    categoryChoice.value = row.category ? "name:" + row.category : "";
    newCategory.value = "";
    updateCategoryFields();
  }
  dialogs[action].showModal();
  if (action === "edit") categoryChoice.focus();
}

function updateCategoryFields() {
  const adding = categoryChoice.value === "!";
  el("edit-new-label").hidden = !adding;
  newCategory.required = adding;
  newCategory.setCustomValidity("");
}

async function updateTransaction(action, category) {
  if (updating) return;
  updating = true;
  const submit = el(action + "-submit");
  const cancel = el(action + "-cancel");
  submit.disabled = cancel.disabled = true;
  categoryChoice.disabled = newCategory.disabled = action === "edit";
  showEditError(action, "");
  const {id} = selectedTransaction;
  const request = {method: action === "delete" ? "DELETE" : "PATCH", headers: {"X-Fiscatoni": "ledger"}};
  if (action === "edit") {
    request.headers["Content-Type"] = "application/json";
    request.body = JSON.stringify({category});
  }
  try {
    const response = await fetch("/api/transactions/" + encodeURIComponent(id), request);
    if (!response.ok && response.status !== 404) {
      const details = await response.json();
      throw new Error(details.error || "Try again.");
    }
    dialogs[action].close();
    const notice = el("ledger-notice");
    notice.textContent = response.status === 404 ? "This transaction is no longer in the ledger." :
      action === "delete" ? "Transaction deleted." : "Category updated.";
    notice.hidden = false;
    await Promise.all([loadPage(currentPage), loadFilterOptions()]);
    if (document.activeElement === document.body) {
      (el("edit-row-" + id) || el("result-count")).focus();
    }
  } catch (error) {
    showEditError(action, error.message);
  } finally {
    updating = false;
    submit.disabled = cancel.disabled = false;
    categoryChoice.disabled = newCategory.disabled = false;
  }
}

for (const action of ["edit", "delete"]) {
  el(action + "-cancel").onclick = () => dialogs[action].close();
  dialogs[action].oncancel = event => { if (updating) event.preventDefault(); };
}
categoryChoice.onchange = () => {
  updateCategoryFields();
  if (categoryChoice.value === "!") newCategory.focus();
};
newCategory.oninput = () => newCategory.setCustomValidity("");
el("edit-form").onsubmit = event => {
  event.preventDefault();
  let category = categoryChoice.value.slice(5);
  if (categoryChoice.value === "!") {
    category = newCategory.value.trim();
    if (!category) {
      newCategory.setCustomValidity("Enter a category name.");
      newCategory.reportValidity();
      return;
    }
    category = categoryNames.find(name => name.toLowerCase() === category.toLowerCase()) || category;
  }
  updateTransaction("edit", category);
};
el("delete-form").onsubmit = event => {
  event.preventDefault();
  updateTransaction("delete");
};

function showState(title, details, action) {
  el("state-title").textContent = title;
  el("state-details").textContent = details;
  el("first-import").hidden = action !== "import";
  el("retry").hidden = action !== "retry";
  el("result-state").hidden = false;
}

async function loadPage(page) {
  activeRequest?.abort();
  const request = new AbortController();
  activeRequest = request;
  const query = new URLSearchParams(applied);
  query.set("page", page);
  el("result-count").textContent = "Loading transactions…";
  el("result-state").hidden = true;
  el("transaction-table").hidden = true;
  el("sort-order").hidden = true;
  el("pagination").hidden = true;
  try {
    const result = await json("/api/transactions?" + query, request.signal);
    if (request.signal.aborted) return;
    currentPage = result.page;
    const body = el("transaction-rows");
    body.replaceChildren();
    for (const row of result.rows) renderRow(row, body);
    if (!result.total) {
      el("result-count").textContent = "0 transactions";
      if (applied.size) showState("No matching transactions", "Adjust the filters or clear them to see more.");
      else showState("No transactions yet", "Import a bank statement to populate your ledger.", "import");
      return;
    }
    const first = (result.page - 1) * result.pageSize + 1;
    const total = numberFormat.format(result.total);
    el("result-count").textContent = result.total > result.pageSize
      ? `${numberFormat.format(first)}–${numberFormat.format(first + result.rows.length - 1)} of ${total} transactions`
      : `${total} ${result.total === 1 ? "transaction" : "transactions"}`;
    el("transaction-table").hidden = false;
    el("sort-order").hidden = false;
    const pages = Math.ceil(result.total / result.pageSize);
    el("pagination").hidden = pages <= 1;
    el("page-number").textContent = `Page ${result.page} of ${pages}`;
    el("ledger-prev").disabled = result.page <= 1;
    el("ledger-next").disabled = result.page >= pages;
  } catch (error) {
    if (request.signal.aborted) return;
    el("result-count").textContent = "Transactions unavailable";
    showState("Could not load transactions", error.message, "retry");
    el("retry").onclick = () => loadPage(page);
  }
}

form.onsubmit = event => {
  event.preventDefault();
  if (!metadataReady) loadFilterOptions();
  const next = new URLSearchParams();
  for (const [key, value] of new FormData(form)) {
    if (key === "category") {
      if (value) next.set(key, value === "!" ? "" : value.slice("name:".length));
    } else if (value.trim()) next.set(key, value.trim());
  }
  if (merchant) next.set("merchant", merchant);
  applied = next;
  loadPage(1);
};

form.onreset = () => {
  merchant = "";
  showMerchantFilter();
  applied = new URLSearchParams();
  loadPage(1);
};
el("clear-merchant").onclick = () => {
  merchant = "";
  showMerchantFilter();
  applied.delete("merchant");
  loadPage(1);
};
el("ledger-prev").onclick = () => loadPage(currentPage - 1);
el("ledger-next").onclick = () => loadPage(currentPage + 1);

window.addEventListener("pageshow", event => {
  if (event.persisted) {
    loadFilterOptions();
    loadPage(currentPage);
  }
});
applyLinkedFilters();
loadFilterOptions();
loadPage(1);
