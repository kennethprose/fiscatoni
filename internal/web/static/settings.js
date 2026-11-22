"use strict";

const dialog = document.getElementById("reset-dialog");
const openReset = document.getElementById("open-reset");
const cancelReset = document.getElementById("cancel-reset");
const submitReset = document.getElementById("submit-reset");
const errorMessage = document.getElementById("reset-error");
const statusMessage = document.getElementById("reset-status");

openReset.onclick = () => {
  errorMessage.hidden = true;
  dialog.showModal();
};
cancelReset.onclick = () => dialog.close();
dialog.oncancel = event => {
  if (submitReset.disabled) event.preventDefault();
};

document.getElementById("confirm-reset").onsubmit = async event => {
  event.preventDefault();
  if (submitReset.disabled) return;

  submitReset.disabled = cancelReset.disabled = true;
  submitReset.textContent = "Deleting data…";
  errorMessage.hidden = true;
  statusMessage.hidden = true;
  try {
    const response = await fetch("/api/settings/reset", {
      method: "POST",
      headers: {"Content-Type": "application/json", "X-Fiscatoni": "settings"},
      body: JSON.stringify({confirmed: true})
    });
    if (!response.ok) throw new Error("Could not confirm that all data was deleted. Try again.");
    dialog.close();
    statusMessage.textContent = "All ledger data was deleted.";
    statusMessage.hidden = false;
    rulesStatus.hidden = true;
    loadRules();
  } catch (error) {
    errorMessage.textContent = error.message;
    errorMessage.hidden = false;
  } finally {
    submitReset.disabled = cancelReset.disabled = false;
    submitReset.textContent = "Permanently delete data";
  }
};

const rulesEndpoint = "/api/settings/merchant-rules";
const rulesState = document.getElementById("rules-state");
const rulesStatus = document.getElementById("rules-status");
const rulesTable = document.getElementById("rules-table");
const rulesList = document.getElementById("rules-list");
const rulesRetry = document.getElementById("rules-retry");
const ruleDialog = document.getElementById("rule-dialog");
const ruleCategory = document.getElementById("rule-category");
const ruleError = document.getElementById("rule-error");
const ruleKind = document.getElementById("rule-kind");
const ruleMatch = document.getElementById("rule-match");
const ruleHistory = document.getElementById("rule-history");
const testRule = document.getElementById("test-rule");
const previewStatus = document.getElementById("rule-preview-status");
const previewList = document.getElementById("rule-preview-list");
const previewPages = document.getElementById("rule-preview-pages");
const previewPrevious = document.getElementById("rule-preview-prev");
const previewNext = document.getElementById("rule-preview-next");
const submitRule = document.getElementById("submit-rule");
const cancelRule = document.getElementById("cancel-rule");
const numberFormat = new Intl.NumberFormat();
let listedRules = [];
let pendingRules;
let pendingPreview;
let movingRule = false;
let selectedRule;
let ruleAction;

function merchantLabel(rule) {
  return rule.pattern || rule.merchant || "Merchant name unavailable · " + rule.id.slice(0, 8);
}

function renderRule(rule, previous, next) {
  const name = merchantLabel(rule);
  const row = rulesList.insertRow();
  const match = row.insertCell();
  const label = document.createElement(rule.pattern ? "code" : "span");
  label.textContent = name;
  if (rule.pattern) {
    const kind = document.createElement("small");
    kind.textContent = "Regular expression";
    match.append(kind);
  }
  match.append(label);
  row.insertCell().textContent = rule.category;
  const actions = row.insertCell();
  actions.className = "rule-actions";
  if (rule.pattern) {
    row.dataset.ruleId = rule.id;
    const order = document.createElement("div");
    order.className = "rule-order-actions";
    for (const [direction, icon, neighbor] of [["up", "↑", previous], ["down", "↓", next]]) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "rule-action rule-move";
      button.dataset.direction = direction;
      button.textContent = icon;
      button.title = "Move " + direction;
      button.setAttribute("aria-label", `Move ${direction} regex rule ${name}`);
      button.disabled = !neighbor?.pattern;
      button.onclick = () => moveRegexRule(rule, neighbor, direction);
      order.append(button);
    }
    actions.append(order);
  }
  for (const [action, label] of [["edit", "Edit"], ["delete", "Delete"]]) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "rule-action rule-" + action;
    button.textContent = label;
    button.setAttribute("aria-label", label + " rule for " + name);
    button.onclick = () => openRuleDialog(rule, action);
    actions.append(button);
  }
}

function showRulesStatus(message, failed = false) {
  rulesStatus.textContent = message;
  rulesStatus.classList.toggle("error", failed);
  rulesStatus.hidden = false;
}

async function moveRegexRule(rule, neighbor, direction) {
  if (movingRule) return;
  movingRule = true;
  rulesStatus.hidden = true;
  const [first, second] = direction === "up" ? [neighbor.id, rule.id] : [rule.id, neighbor.id];
  let message = `Regex rule moved ${direction}.`;
  let failed = false;
  try {
    const response = await fetch(rulesEndpoint + "/order", {
      method: "POST",
      headers: {"Content-Type": "application/json", "X-Fiscatoni": "settings"},
      body: JSON.stringify({first, second})
    });
    if (!response.ok) {
      const problem = await response.json();
      throw new Error(problem.error || "Could not save the rule order.");
    }
  } catch (error) {
    message = error.message;
    failed = true;
  }
  const restoreFocus = rulesList.contains(document.activeElement);
  await loadRules();
  movingRule = false;
  showRulesStatus(message, failed);
  if (restoreFocus && !ruleDialog.open && !dialog.open) {
    const row = rulesList.querySelector(`[data-rule-id="${CSS.escape(rule.id)}"]`);
    const target = row?.querySelector(`[data-direction="${direction}"]:enabled`) || row?.querySelector(".rule-move:enabled");
    (target || (!rulesRetry.hidden ? rulesRetry : document.getElementById("create-rule"))).focus();
  }
}

async function loadRules() {
  pendingRules?.abort();
  const request = new AbortController();
  pendingRules = request;
  rulesState.textContent = "Loading merchant rules…";
  rulesState.hidden = false;
  rulesRetry.hidden = rulesTable.hidden = true;
  try {
    const response = await fetch(rulesEndpoint, {signal: request.signal});
    if (!response.ok) throw new Error("Could not load merchant rules.");
    const rules = await response.json();
    if (request.signal.aborted) return;
    listedRules = rules.filter(rule => !rule.pattern)
      .sort((a, b) => Number(!a.merchant) - Number(!b.merchant) || merchantLabel(a).localeCompare(merchantLabel(b)))
      .concat(rules.filter(rule => rule.pattern));
    rulesList.replaceChildren();
    for (const [index, rule] of listedRules.entries()) {
      renderRule(rule, listedRules[index - 1], listedRules[index + 1]);
    }
    rulesTable.hidden = !listedRules.length;
    rulesState.hidden = listedRules.length > 0;
    rulesState.textContent = listedRules.length ? "" : "No remembered merchant rules yet.";
  } catch {
    if (request.signal.aborted) return;
    rulesState.textContent = "Could not load merchant rules.";
    rulesRetry.hidden = false;
  }
}

async function loadRuleCategories() {
  const list = document.getElementById("rule-categories");
  const names = new Set(listedRules.map(rule => rule.category));
  try {
    const response = await fetch("/api/transaction-categories");
    if (response.ok) for (const name of await response.json()) names.add(name);
  } catch {
    // The category field also accepts names directly.
  }
  list.replaceChildren();
  for (const name of [...names].sort((a, b) => a.localeCompare(b))) {
    if ([...name].length <= 128) list.append(new Option(name, name));
  }
}

function openRuleDialog(rule, action) {
  selectedRule = rule;
  ruleAction = action;
  const deleting = action === "delete";
  const creating = action === "create";
  document.getElementById("rule-title").textContent = creating ? "Create merchant rule" :
    deleting ? "Delete merchant rule?" : "Edit merchant rule";
  const merchant = document.getElementById("rule-merchant");
  merchant.textContent = rule ? merchantLabel(rule) : "";
  merchant.hidden = creating || (action === "edit" && !!rule.pattern);
  document.getElementById("rule-kind-field").hidden = !creating;
  ruleKind.value = rule?.pattern ? "regex" : "exact";
  ruleMatch.value = rule?.pattern || "";
  updateRuleMatch();
  document.getElementById("rule-history-field").hidden = deleting;
  ruleHistory.checked = false;
  document.getElementById("rule-category-field").hidden = deleting;
  document.getElementById("rule-delete-help").hidden = !deleting;
  ruleCategory.required = !deleting;
  ruleCategory.value = rule?.category || "";
  ruleCategory.setCustomValidity("");
  ruleError.hidden = true;
  submitRule.textContent = creating ? "Create rule" : deleting ? "Delete rule" : "Save rule";
  submitRule.className = deleting ? "danger" : "primary";
  ruleDialog.showModal();
  (deleting ? cancelRule : ruleMatch.required ? ruleMatch : ruleCategory).focus();
  if (!deleting) loadRuleCategories();
}

function updateRuleMatch() {
  const pattern = ruleKind.value === "regex";
  const visible = ruleAction === "create" || (ruleAction === "edit" && pattern);
  document.getElementById("rule-match-field").hidden = !visible;
  document.getElementById("rule-match-label").textContent = pattern ? "Pattern" : "Merchant name";
  document.getElementById("rule-pattern-help").hidden = !visible || !pattern;
  document.getElementById("rule-test").hidden = !visible || !pattern;
  document.getElementById("rule-history-help").textContent = pattern ?
    "Replace categories across all accounts where this pattern takes precedence." :
    "Replace the category on matching transactions across all accounts.";
  ruleMatch.required = visible;
  ruleMatch.placeholder = pattern ? "^STOP & SHOP [0-9]{4}$" : "e.g. Stop & Shop 0541";
  ruleMatch.setCustomValidity("");
  clearRulePreview();
}

function clearRulePreview() {
  pendingPreview?.abort();
  previewStatus.hidden = previewList.hidden = previewPages.hidden = true;
  previewList.replaceChildren();
}

function renderPreviewRow(row) {
  const item = document.createElement("li");
  const merchant = document.createElement("strong");
  merchant.textContent = row.merchant || "No merchant or description";
  item.append(merchant);
  const cents = BigInt(row.cents);
  const magnitude = cents < 0n ? -cents : cents;
  const amount = (cents < 0n ? "−" : "") + row.currency + " " +
    numberFormat.format(magnitude / 100n) + "." + String(magnitude % 100n).padStart(2, "0");
  const effect = row.blocked ? "Existing rule takes precedence" +
    (row.existingCategory ? " · " + row.existingCategory : "") : "Eligible for update";
  const details = [row.date + " · " + row.account.label + " · " + amount,
    "Current category · " + (row.category || "Uncategorized"), effect];
  if (row.description && row.description !== row.merchant) details.unshift(row.description);
  for (const detail of details) {
    const line = document.createElement("small");
    line.textContent = detail;
    item.append(line);
  }
  return item;
}

async function testPattern(page = 1) {
  if (submitRule.disabled) return;
  const pattern = ruleMatch.value.trim();
  if (!pattern) {
    ruleMatch.setCustomValidity("Enter a regular expression to test.");
    ruleMatch.reportValidity();
    return;
  }
  pendingPreview?.abort();
  const request = new AbortController();
  pendingPreview = request;
  previewStatus.textContent = "Testing against saved transactions…";
  previewStatus.hidden = false;
  try {
    const ruleId = ruleAction === "edit" ? selectedRule.id : undefined;
    const response = await fetch(rulesEndpoint + "/test", {
      method: "POST",
      headers: {"Content-Type": "application/json", "X-Fiscatoni": "settings"},
      signal: request.signal,
      body: JSON.stringify({pattern, page, ruleId})
    });
    const data = await response.json();
    if (request.signal.aborted) return;
    if (!response.ok) throw new Error(data.error || "Could not test this pattern.");
    const {rows, total, eligible, pageSize} = data;
    const skipped = total - eligible;
    previewStatus.textContent = total ?
      `${total} ${total === 1 ? "match" : "matches"}. ${eligible} eligible for update.` +
      (skipped ? ` Existing rules take precedence for ${skipped}.` : "") :
      "No saved transactions match this pattern.";
    previewList.replaceChildren(...rows.map(renderPreviewRow));
    previewList.hidden = rows.length === 0;
    previewPrevious.disabled = page === 1;
    previewNext.disabled = page * pageSize >= total;
    previewPages.hidden = previewPrevious.disabled && previewNext.disabled;
    previewPrevious.onclick = () => testPattern(page - 1);
    previewNext.onclick = () => testPattern(page + 1);
    const start = (page - 1) * pageSize;
    document.getElementById("rule-preview-range").textContent = rows.length ?
      `${start + 1}–${start + rows.length} of ${total}` : "No matches on this page";
  } catch (error) {
    if (request.signal.aborted) return;
    previewStatus.textContent = error.message;
    previewList.hidden = previewPages.hidden = true;
  }
}

document.getElementById("create-rule").onclick = () => openRuleDialog(null, "create");
ruleKind.onchange = updateRuleMatch;
ruleMatch.oninput = () => {
  ruleMatch.setCustomValidity("");
  clearRulePreview();
};
ruleCategory.oninput = () => ruleCategory.setCustomValidity("");
testRule.onclick = () => testPattern();
cancelRule.onclick = () => ruleDialog.close();
ruleDialog.onclose = clearRulePreview;
ruleDialog.oncancel = event => {
  if (submitRule.disabled) event.preventDefault();
};

document.getElementById("rule-form").onsubmit = async event => {
  event.preventDefault();
  if (submitRule.disabled) return;
  const deleting = ruleAction === "delete";
  const creating = ruleAction === "create";
  const category = ruleCategory.value.trim();
  if (!deleting && !category) {
    ruleCategory.setCustomValidity("Enter a category.");
    ruleCategory.reportValidity();
    return;
  }
  const match = ruleMatch.required ? ruleMatch.value.trim() : undefined;
  if (match === "") {
    ruleMatch.setCustomValidity("Enter a merchant name or pattern.");
    ruleMatch.reportValidity();
    return;
  }
  const applyToHistory = !deleting && ruleHistory.checked;
  submitRule.disabled = cancelRule.disabled = ruleCategory.disabled = ruleMatch.disabled = ruleKind.disabled =
    ruleHistory.disabled = testRule.disabled = true;
  ruleError.hidden = rulesStatus.hidden = true;
  const options = {method: deleting ? "DELETE" : creating ? "POST" : "PATCH", headers: {"X-Fiscatoni": "settings"}};
  if (!deleting) {
    options.headers["Content-Type"] = "application/json";
    options.body = JSON.stringify({category, match, kind: ruleKind.value, applyToHistory});
  }
  try {
    const url = creating ? rulesEndpoint : rulesEndpoint + "/" + encodeURIComponent(selectedRule.id);
    const response = await fetch(url, options);
    if (!response.ok && (creating || response.status !== 404)) {
      const problem = await response.json();
      throw new Error(problem.error || "Could not update the merchant rule.");
    }
    let message = response.status === 404 ? "This merchant rule no longer exists." :
      deleting ? "Merchant rule deleted." : creating ? "Merchant rule created." : "Merchant rule updated.";
    if (applyToHistory && response.ok) {
      const {updated} = await response.json();
      message += ` Updated ${updated} saved ${updated === 1 ? "transaction" : "transactions"}.`;
    }
    ruleDialog.close();
    showRulesStatus(message);
    loadRules();
  } catch (error) {
    ruleError.textContent = error.message;
    ruleError.hidden = false;
  } finally {
    submitRule.disabled = cancelRule.disabled = ruleCategory.disabled = ruleMatch.disabled = ruleKind.disabled =
      ruleHistory.disabled = testRule.disabled = false;
  }
};

rulesRetry.onclick = loadRules;
window.addEventListener("pageshow", event => {
  if (event.persisted) loadRules();
});
loadRules();
