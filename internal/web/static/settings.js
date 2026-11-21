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
  } catch (error) {
    errorMessage.textContent = error.message;
    errorMessage.hidden = false;
  } finally {
    submitReset.disabled = cancelReset.disabled = false;
    submitReset.textContent = "Permanently delete data";
  }
};
