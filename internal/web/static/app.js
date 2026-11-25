"use strict";

const el = id => document.getElementById(id);
const names = { chase: "Chase", capitalone: "CapitalOne", "robinhood": "Robinhood" };
const fields = { Date: "Transaction date", Description: "Description", Category: "Category", Amount: "Amount", Debit: "Debit", Credit: "Credit", Posted: "Posting date", Merchant: "Merchant", Type: "Transaction type", Status: "Status", Time: "Time", Memo: "Memo", Card: "Card reference" };
const optional = ["Posted", "Merchant", "Type", "Status", "Time", "Memo", "Card"];
const pageSize = 25;
const merchantFilter = el("merchants-without-rules");
const merchantFilterKey = "fiscatoni.imports.merchantsWithoutRules";
let file, inspection, profile, review, rows = [], page = 0, busy = false, saved = false;
let overlaps = new Set(), decisions = {};
let accounts = [], categoryEdits = {};
let categoryNames = [], suggestions = {}, remember = {}, applyNewCategory;
let merchants = new Map(), merchantList = [], merchantPage = 0;

function selectedCategory(row) {
  return categoryEdits[row.Line] ?? suggestions[row.Line] ?? row.Category;
}

function hasMerchantRule(row) {
  return Object.hasOwn(suggestions, row.Line);
}

function groupMerchants() {
  merchants.clear();
  for (const row of rows) {
    const id = row.MerchantGroup;
    if (!merchants.has(id)) merchants.set(id, {id, name: row.Merchant || row.Description, rows: []});
    merchants.get(id).rows.push(row);
  }
  merchantList = [...merchants.values()].sort((a, b) => a.name.localeCompare(b.name));
}

function setCategory(merchant, category, focusID) {
  for (const row of merchant.rows) {
    categoryEdits[row.Line] = category;
    if (!category) delete remember[row.Line];
  }
  renderMerchants();
  renderRows();
  el(focusID)?.focus({preventScroll: true});
}

function merchantCategory(merchant) {
  const value = selectedCategory(merchant.rows[0]);
  return merchant.rows.every(row => selectedCategory(row) === value) ? value : null;
}

function categorySource(row) {
  const hint = document.createElement("small");
  hint.className = "category-source";
  if (Object.hasOwn(categoryEdits, row.Line)) {
    hint.textContent = "Edited in review";
    hint.dataset.source = "edited";
  } else if (hasMerchantRule(row)) {
    hint.textContent = "Merchant rule applied";
    hint.dataset.source = "rule";
  } else {
    hint.textContent = row.Category ? "From CSV file" : "No category supplied";
  }
  return hint;
}

function categorySelect(merchant, row, value) {
  const select = document.createElement("select");
  select.id = row ? "category-" + row.Line : "merchant-category-" + merchant.id;
  select.setAttribute("aria-label", row ? "Category for line " + row.Line : "Category for merchant " + merchant.name);
  select.add(new Option("Uncategorized", ""));
  for (const name of categoryNames) select.add(new Option(name, name));
  const add = new Option("Add a new category…", "");
  select.add(add);
  if (value === null || [...value].length > 128) {
    const original = new Option(value ?? "Mixed categories", value ?? "");
    original.disabled = true;
    select.add(original, 0);
    original.selected = true;
  } else select.value = value;
  const current = select.selectedOptions[0];
  const apply = category => setCategory(merchant, category, select.id);
  select.onchange = () => {
    if (select.selectedOptions[0] === add) {
      current.selected = true;
      applyNewCategory = apply;
      el("category-name").value = "";
      el("category-name").setCustomValidity("");
      el("category-dialog").showModal();
      el("category-name").focus();
    } else apply(select.value);
  };
  return select;
}

function updateRememberCheck(checkbox, members) {
  if (!checkbox) return;
  checkbox.checked = members.every(row => !!remember[row.Line]);
  checkbox.indeterminate = !checkbox.checked && members.some(row => !!remember[row.Line]);
}

function rememberPicker(merchant, row, value) {
  const label = document.createElement("label");
  label.className = "remember-category";
  const checkbox = document.createElement("input");
  checkbox.type = "checkbox";
  checkbox.id = row ? "remember-line-" + row.Line : "remember-merchant-" + merchant.id;
  checkbox.disabled = !value || [...value].length > 128;
  const members = row ? [row] : merchant.rows;
  updateRememberCheck(checkbox, members);
  const name = row ? (row.Merchant || row.Description) + " on line " + row.Line : "merchant " + merchant.name;
  checkbox.setAttribute("aria-label", "Remember category for " + name);
  label.title = "Use this category on future imports matching " + merchant.name;
  checkbox.onchange = () => {
    for (const member of members) {
      if (checkbox.checked) remember[member.Line] = true;
      else delete remember[member.Line];
    }
    if (row) {
      updateRememberCheck(el("remember-merchant-" + merchant.id), merchant.rows);
    } else {
      for (const member of visibleRows()) {
        if (member.MerchantGroup === merchant.id) el("remember-line-" + member.Line).checked = checkbox.checked;
      }
    }
  };
  label.append(checkbox, document.createTextNode("Remember for merchant"));
  return label;
}

function categoryPicker(merchant, row) {
  const group = document.createElement("div");
  group.className = "category-picker";
  const value = row ? selectedCategory(row) : merchantCategory(merchant);
  if (saved) group.textContent = value === null ? "Mixed categories" : value || "Uncategorized";
  else group.append(categorySelect(merchant, row, value));
  if (row) group.append(categorySource(row));
  if (!saved) group.append(rememberPicker(merchant, row, value));
  return group;
}

el("category-cancel").onclick = () => el("category-dialog").close();
el("category-name").oninput = () => el("category-name").setCustomValidity("");
el("new-category").onsubmit = event => {
  event.preventDefault();
  const input = el("category-name");
  const name = input.value.trim();
  if (!name) {
    input.setCustomValidity("Enter a category name."); input.reportValidity(); return;
  }
  const existing = categoryNames.find(item => item.toLowerCase() === name.toLowerCase());
  if (!existing) { categoryNames.push(name); categoryNames.sort((a, b) => a.localeCompare(b)); }
  el("category-dialog").close();
  applyNewCategory(existing || name);
};

function chooseAccounts(preferred = el("account").value) {
  const bank = el("bank").value.trim();
  const matches = accounts.filter(account => account.bank === bank);
  const select = el("account");
  const selectedBeforeUpdate = select.value;
  select.replaceChildren(new Option(matches.length ? "Select an account…" : "No saved accounts for this bank", ""));
  for (const account of matches) select.add(new Option(account.label, account.id));
  select.value = matches.some(account => account.id === preferred) ? preferred : matches.length === 1 ? matches[0].id : "";
  if (overlaps.size && selectedBeforeUpdate !== select.value) clearDecisions();
  refreshSave();
}

function clearDecisions() {
  overlaps.clear(); decisions = {}; message(""); renderRows();
}

function showAccountList() {
  const list = el("accounts-list");
  list.replaceChildren();
  list.hidden = false;
  if (!accounts.length) {
    const empty = document.createElement("li");
    empty.textContent = "No saved accounts yet.";
    list.append(empty);
  }
  for (const account of accounts) {
    const item = document.createElement("li");
    const label = document.createElement("div");
    label.className = "saved-account-label";
    const bank = document.createElement("small");
    bank.textContent = accountBankName(account);
    label.append(document.createTextNode(account.label), bank);
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "secondary";
    remove.textContent = "Delete";
    remove.setAttribute("aria-label", "Delete account " + account.label + " (" + bank.textContent + ")");
    remove.disabled = account.hasTransactions;
    remove.onclick = () => openAccountDeletion(account);
    item.append(label, remove);
    list.append(item);
  }
}

function accountBankName(account) {
  return Object.hasOwn(names, account.bank) ? names[account.bank] : account.bank;
}

async function fetchAccounts() {
  const response = await fetch("/api/accounts");
  if (!response.ok) throw new Error("Could not load saved accounts. Reopen Accounts to retry.");
  accounts = await response.json();
  chooseAccounts(); showAccountList();
  el("accounts-load-error").hidden = true;
}

function showCustomBank() {
  const custom = !el("new-bank").value;
  el("custom-bank-field").hidden = !custom;
  el("new-custom-bank").required = custom;
}

async function openAccounts(fromImport) {
  if (busy) return;
  const dialog = el("accounts-dialog");
  el("new-account").reset();
  const bank = fromImport ? el("bank").value.trim() : "chase";
  el("new-bank").value = Object.hasOwn(names, bank) ? bank : "";
  el("new-custom-bank").value = el("new-bank").value ? "" : bank;
  showCustomBank();
  el("account-status").textContent = "Loading saved accounts…";
  el("accounts-list").hidden = true;
  dialog.showModal();
  el("new-account").inert = true;
  try {
    await fetchAccounts();
    el("account-status").textContent = "";
  }
  catch (error) { el("account-status").textContent = error.message; }
  finally { el("new-account").inert = false; }
}

const deleteAccountDialog = el("delete-account-dialog");
const deleteAccountSubmit = el("delete-account-submit");
const deleteAccountCancel = el("delete-account-cancel");
const deleteAccountError = el("delete-account-error");
let deletingAccount;

function openAccountDeletion(account) {
  deletingAccount = account;
  el("delete-account-name").textContent = account.label + " · " + accountBankName(account);
  deleteAccountError.hidden = true;
  deleteAccountDialog.showModal();
}

deleteAccountCancel.onclick = () => deleteAccountDialog.close();
deleteAccountDialog.oncancel = event => {
  if (deleteAccountSubmit.disabled) event.preventDefault();
};
el("confirm-account-delete").onsubmit = async event => {
  event.preventDefault();
  if (deleteAccountSubmit.disabled) return;
  const account = deletingAccount;
  deleteAccountSubmit.disabled = deleteAccountCancel.disabled = true;
  deleteAccountError.hidden = true;
  try {
    const response = await fetch("/api/accounts/" + encodeURIComponent(account.id), {
      method: "DELETE", headers: { "X-Fiscatoni": "import" }
    });
    if (!response.ok && response.status !== 404) {
      if (response.status === 409) {
        account.hasTransactions = true;
        showAccountList();
      }
      const problem = await response.json();
      throw new Error(problem.error || "Could not delete the account.");
    }
    accounts = accounts.filter(entry => entry.id !== account.id);
    chooseAccounts();
    deleteAccountDialog.close();
    showAccountList();
    el("accounts-close").focus();
    el("account-status").textContent = response.ok ? "Deleted account · " + account.label : "This account no longer exists.";
  } catch (error) {
    deleteAccountError.textContent = error.message;
    deleteAccountError.hidden = false;
  } finally {
    deleteAccountSubmit.disabled = deleteAccountCancel.disabled = false;
  }
};

el("manage-accounts").onclick = () => openAccounts(false);
el("add-destination").onclick = () => openAccounts(true);
el("accounts-close").onclick = () => el("accounts-dialog").close();
el("new-bank").onchange = showCustomBank;
el("new-account").onsubmit = async event => {
  event.preventDefault();
  const form = el("new-account");
  if (form.inert) return;
  form.inert = true;
  el("account-status").textContent = "";
  try {
    const response = await fetch("/api/accounts", {
      method: "POST", headers: { "Content-Type": "application/json", "X-Fiscatoni": "import" },
      body: JSON.stringify({ bank: el("new-bank").value || el("new-custom-bank").value.trim(), label: el("new-label").value.trim() })
    });
    const account = await response.json();
    if (!response.ok) throw new Error(account.error || "Could not save account.");
    await fetchAccounts();
    if (!saved && account.bank === el("bank").value.trim()) chooseAccounts(account.id);
    el("new-label").value = "";
    el("account-status").textContent = "Saved account · " + account.label;
  } catch (error) { el("account-status").textContent = error.message; }
  finally { form.inert = false; }
};

function message(text, success = false) {
  el("message").textContent = text;
  el("message").hidden = !text;
  el("message").classList.toggle("ok", success);
}
function uploadError(text) {
  el("upload-error").textContent = text;
  el("upload-error").hidden = !text;
}
function setBusy(value) {
  busy = value;
  document.querySelector("main").setAttribute("aria-busy", String(value));
  el("browse").disabled = value;
  el("mapping").inert = value || saved;
  el("map-toggle").disabled = value || saved;
  el("account").disabled = value || saved;
  el("add-destination").disabled = value || saved;
  el("manage-accounts").disabled = value;
  el("rows").inert = value;
  el("merchant-review").inert = value;
  refreshSave();
}
function refreshSave() {
  const remaining = [...overlaps].filter(line => !(line in decisions)).length;
  el("save").disabled = busy || saved || !review || !el("account").value.trim() || remaining > 0;
  el("save-note").textContent = saved ? "Import complete." : remaining ? remaining + " overlap decisions remaining." :
    review && !el("account").value ? "Select a destination account to save." : "Changes are saved only when you confirm.";
}
async function request(action, values = {}) {
  const body = new FormData();
  body.append("file", file);
  for (const [key, value] of Object.entries(values)) body.append(key, value);
  const response = await fetch("/api/" + action, { method: "POST", headers: { "X-Fiscatoni": "import" }, body });
  const result = await response.json();
  if (!response.ok) {
    const error = new Error(result.error || "The request failed.");
    error.lines = result.lines;
    throw error;
  }
  return result;
}

function money(value) {
  const cents = BigInt(value);
  const abs = cents < 0n ? -cents : cents;
  return (cents < 0n ? "−" : cents > 0n ? "+" : "") + "$" +
    (abs / 100n).toLocaleString("en-US") + "." + String(abs % 100n).padStart(2, "0");
}
function cell(tr, text, cls) {
  const td = tr.insertCell();
  td.textContent = text;
  if (cls) td.className = cls;
  return td;
}
function visibleRows() {
  const start = page * pageSize;
  return rows.slice(start, start + pageSize);
}
function renderMerchants() {
  const visible = merchantFilter.checked ?
    merchantList.filter(merchant => !hasMerchantRule(merchant.rows[0])) : merchantList;
  const total = visible.length;
  el("merchant-review").hidden = merchantList.length === 0;
  el("merchant-table").hidden = total === 0;
  el("merchant-empty").hidden = total > 0;
  el("merchant-count").textContent = total.toLocaleString() + (total === 1 ? " merchant" : " merchants");
  const tbody = el("merchant-rows");
  tbody.replaceChildren();
  const start = merchantPage * pageSize;
  for (const merchant of visible.slice(start, start + pageSize)) {
    const tr = tbody.insertRow();
    cell(tr, merchant.name);
    cell(tr, merchant.rows.length.toLocaleString());
    cell(tr, "").append(categoryPicker(merchant));
  }
  el("merchant-pages").hidden = total <= pageSize;
  el("merchant-page-info").textContent = (start + 1) + "–" + Math.min(start + pageSize, total) + " of " + total;
  el("merchant-prev").disabled = merchantPage === 0;
  el("merchant-next").disabled = start + pageSize >= total;
}
function showMerchantPage(nextPage) {
  merchantPage = nextPage;
  renderMerchants();
  el("merchant-table").scrollTop = 0;
}
function renderRows() {
  el("rows").replaceChildren();
  el("empty").hidden = rows.length > 0;
  el("count").textContent = rows.length.toLocaleString() + (rows.length === 1 ? " row" : " rows");
  el("decision-head").hidden = !overlaps.size;
  const start = page * pageSize;
  for (const row of visibleRows()) {
    const tr = el("rows").insertRow();
    cell(tr, row.Line); cell(tr, row.Date);
    const desc = cell(tr, "");
    const details = document.createElement("details");
    const title = document.createElement("summary");
    title.textContent = row.Description || row.Merchant;
    details.append(title);
    for (const key of optional) if (row[key]) {
      const item = document.createElement("p");
      item.textContent = fields[key] + " · " + row[key];
      details.append(item);
    }
    if (details.children.length > 1) desc.append(details); else desc.textContent = title.textContent;
    cell(tr, "").append(categoryPicker(merchants.get(row.MerchantGroup), row));
    cell(tr, money(row.Cents), "money" + (BigInt(row.Cents) > 0n ? " credit" : ""));
    if (overlaps.size) {
      const td = cell(tr, "");
      if (overlaps.has(row.Line)) {
        const select = document.createElement("select");
        select.setAttribute("aria-label", "Overlap decision for line " + row.Line);
        select.add(new Option("Choose…", "")); select.add(new Option("Keep", "keep")); select.add(new Option("Skip", "skip"));
        select.value = row.Line in decisions ? (decisions[row.Line] ? "keep" : "skip") : "";
        select.onchange = () => {
          if (select.value) decisions[row.Line] = select.value === "keep"; else delete decisions[row.Line];
          refreshSave();
        };
        td.append(select);
      }
    }
  }
  el("pages").hidden = rows.length <= pageSize;
  el("page-info").textContent = (start + 1) + "–" + Math.min(start + pageSize, rows.length) + " of " + rows.length;
  el("prev").disabled = page === 0; el("next").disabled = start + pageSize >= rows.length;
  refreshSave();
}
function invalidate() {
  review = ""; rows = []; page = 0; decisions = {}; overlaps.clear(); saved = false;
  merchants.clear(); merchantList = []; merchantPage = 0;
  el("empty").querySelector("p").textContent = file ? "Apply the column mapping to preview this file." : "Choose a CSV file to get started.";
  message(""); renderMerchants(); renderRows();
}
function showMapping(open) {
  el("mapping").hidden = !open;
  el("map-toggle").setAttribute("aria-expanded", String(open));
  el("map-toggle").textContent = open ? "Hide column mapping" : "Edit column mapping";
}
function amountFields() {
  const split = el("mode").value === "split";
  el("sign-field").hidden = split;
  for (const key of ["Amount", "Debit", "Credit"]) {
    const active = key === "Amount" ? !split : split;
    el("col-" + key).parentElement.hidden = !active;
    el("col-" + key).required = active;
  }
}
function prepareMapping() {
  const initial = inspection.Profile || {};
  el("bank").value = initial.Bank || "";
  el("layout").value = initial.Layout || "2006-01-02";
  el("mode").value = initial.Debit ? "split" : "signed";
  el("sign").value = initial.Invert ? "charge" : "credit";
  el("columns").replaceChildren(); el("optional-columns").replaceChildren();
  for (const [key, labelText] of Object.entries(fields)) {
    const label = document.createElement("label"); label.textContent = labelText;
    const select = document.createElement("select"); select.id = "col-" + key;
    select.required = key === "Date" || key === "Description";
    select.add(new Option("Select column…", ""));
    const desired = (initial[key] || (!inspection.Profile ? key : "")).toLowerCase();
    for (const header of inspection.Headers) if (header.trim()) {
      const option = new Option(header, header);
      option.selected = header.trim().toLowerCase() === desired;
      select.add(option);
    }
    const sample = document.createElement("span"); sample.className = "sample";
    function updateSample() {
      const text = (inspection.Sample || [])[inspection.Headers.indexOf(select.value)];
      sample.textContent = text ? "Sample · " + text : "";
    }
    select.onchange = updateSample; updateSample();
    label.append(select, sample);
    el(optional.includes(key) ? "optional-columns" : "columns").append(label);
  }
  amountFields();
}
function readMapping() {
  const split = el("mode").value === "split";
  const mapping = { Bank: el("bank").value.trim(), Layout: el("layout").value, Invert: !split && el("sign").value === "charge" };
  for (const key of Object.keys(fields)) {
    const select = el("col-" + key);
    if (!select.parentElement.hidden && select.value) mapping[key] = select.value;
  }
  return mapping;
}
async function preview() {
  invalidate();
  const mapping = readMapping();
  if (JSON.stringify(mapping) !== JSON.stringify(profile)) { categoryEdits = {}; remember = {}; }
  profile = mapping;
  try {
    const result = await request("preview", { profile: JSON.stringify(profile) });
    rows = result.rows; review = result.review;
    suggestions = result.categories.suggestions;
    categoryNames = [...new Set([...result.categories.names, ...rows.map(row => row.Category),
    ...Object.values(suggestions), ...Object.values(categoryEdits)].filter(name => name && [...name].length <= 128))].sort((a, b) => a.localeCompare(b));
    groupMerchants();
    renderMerchants();
    renderRows();
    chooseAccounts();
    el("summary").textContent = "Amounts are in USD. Negative amounts represent spending.";
  } catch (error) { showMapping(true); message(error.message); }
}
async function load(selected) {
  if (!selected || busy) return;
  file = selected; categoryEdits = {}; remember = {}; suggestions = {}; invalidate(); uploadError(""); setBusy(true);
  el("settings").hidden = true;
  el("drop-title").textContent = file.name; el("file-meta").textContent = (file.size / 1024).toLocaleString(undefined, { maximumFractionDigits: 1 }) + " KiB";
  el("browse").textContent = "Replace file"; el("account").value = "";
  el("summary").textContent = "Review dates, descriptions, and amounts before saving.";
  try {
    if (file.size > 32 * 1024 * 1024) throw new Error("Choose a CSV no larger than 32 MiB.");
    inspection = await request("inspect");
    prepareMapping();
    const bank = inspection.Profile?.Bank;
    el("recognition").textContent = bank ? "Recognized bank · " + (names[bank] || bank) : "Unrecognized format · map your columns to continue.";
    el("settings").hidden = false; showMapping(!bank);
    try { await fetchAccounts(); }
    catch (error) { el("accounts-load-error").textContent = error.message; el("accounts-load-error").hidden = false; }
    if (bank) await preview();
  } catch (error) { uploadError(error.message); }
  finally { setBusy(false); }
}
el("browse").onclick = () => el("file").click();
el("file").onchange = event => { load(event.target.files[0]); event.target.value = ""; };
window.addEventListener("dragover", event => event.preventDefault());
window.addEventListener("drop", event => event.preventDefault());
el("drop").ondragover = event => { event.preventDefault(); el("drop").classList.add("over"); };
el("drop").ondragleave = () => el("drop").classList.remove("over");
el("drop").ondrop = event => {
  event.preventDefault(); el("drop").classList.remove("over");
  if (event.dataTransfer.files.length !== 1) { uploadError("Drop one CSV at a time."); return; }
  load(event.dataTransfer.files[0]);
};
el("map-toggle").onclick = () => showMapping(el("mapping").hidden);
el("mode").onchange = amountFields;
el("mapping").oninput = invalidate;
el("mapping").onsubmit = async event => {
  event.preventDefault(); if (busy) return;
  setBusy(true); try { await preview(); } finally { setBusy(false); }
};
el("account").onchange = () => { if (overlaps.size) clearDecisions(); refreshSave(); };
el("prev").onclick = () => { page--; renderRows(); };
el("next").onclick = () => { page++; renderRows(); };
try {
  merchantFilter.checked = localStorage.getItem(merchantFilterKey) === "true";
} catch {
  // Browser settings can block access to storage.
}
merchantFilter.onchange = () => {
  try {
    localStorage.setItem(merchantFilterKey, String(merchantFilter.checked));
  } catch {
    // Keep filtering the current preview if storage is unavailable.
  }
  showMerchantPage(0);
};
el("merchant-prev").onclick = () => showMerchantPage(merchantPage - 1);
el("merchant-next").onclick = () => showMerchantPage(merchantPage + 1);
el("save").onclick = async () => {
  if (!review || busy || saved) return;
  setBusy(true); message("");
  try {
    const reviewedCategories = { ...suggestions, ...categoryEdits };
    const result = await request("save", { profile: JSON.stringify(profile), review, accountID: el("account").value, categories: JSON.stringify(reviewedCategories), remember: JSON.stringify(remember), decisions: JSON.stringify(decisions) });
    saved = true; overlaps.clear(); decisions = {}; renderMerchants(); renderRows(); showMapping(false);
    message(result.AlreadyImported ? "This file was already saved in batch " + result.ID + "." :
      "Saved to " + el("account").selectedOptions[0].text + " · " + result.Added + " added, " + result.Skipped + " skipped (batch " + result.ID + ").", true);
  } catch (error) {
    if (error.lines) {
      for (const line of error.lines) overlaps.add(line);
      page = Math.floor(rows.findIndex(r => r.Line === error.lines[0]) / pageSize);
      renderRows();
    }
    message(error.message);
  } finally { setBusy(false); }
};
