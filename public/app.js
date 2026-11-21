let token = sessionStorage.getItem("fairpass_token");

async function api(path, options = {}) {
  const headers = { "content-type": "application/json", ...(options.headers || {}) };
  if (token) headers.authorization = `Bearer ${token}`;
  const response = await fetch(path, { ...options, headers });
  const body = await response.json();
  if (!response.ok) throw new Error(body.error || body.reason || "Request failed");
  return body;
}

function showConsole(show) {
  document.querySelector("#login-panel").hidden = show;
  document.querySelector("#console-panel").hidden = !show;
  document.querySelector("#logout").hidden = !show;
}

showConsole(Boolean(token));

document.querySelector("#request-otp").addEventListener("click", async () => {
  const message = document.querySelector("#auth-message");
  try {
    await api("/api/auth/request-otp", {
      method: "POST",
      body: JSON.stringify({ phone: document.querySelector("#phone").value })
    });
    document.querySelector("#otp-step").hidden = false;
    message.textContent = "Demo OTP requested. Check the server console or configured delivery adapter.";
  } catch (error) {
    message.textContent = error.message;
  }
});

document.querySelector("#verify-otp").addEventListener("click", async () => {
  const message = document.querySelector("#auth-message");
  try {
    const result = await api("/api/auth/verify-otp", {
      method: "POST",
      body: JSON.stringify({
        phone: document.querySelector("#phone").value,
        otp: document.querySelector("#otp").value
      })
    });
    token = result.token;
    sessionStorage.setItem("fairpass_token", token);
    showConsole(true);
  } catch (error) {
    message.textContent = error.message;
  }
});

document.querySelector("#logout").addEventListener("click", () => {
  token = null;
  sessionStorage.removeItem("fairpass_token");
  showConsole(false);
});

async function refreshDashboard() {
  if (!token) return;
  try {
    const dashboard = await api("/api/dashboard");
    document.querySelector("#accepted").textContent = dashboard.summary.accepted;
    document.querySelector("#rejected").textContent = dashboard.summary.rejected;
    document.querySelector("#duplicate").textContent = dashboard.summary.duplicate;
    document.querySelector("#sensors").textContent = dashboard.summary.sensorAlerts;
    document.querySelector("#network-state").textContent =
      `Blockchain: ${dashboard.blockchain === "mock" ? "demo mock (confirmed)" : dashboard.blockchain}`;
    document.querySelector("#events").innerHTML = dashboard.events.map(event =>
      `<div class="event"><strong>${event.type}</strong><br>${event.timestamp}${event.txRef ? ` · ${event.txRef}` : ""}</div>`
    ).join("");
  } catch (error) {
    document.querySelector("#network-state").textContent = error.message;
  }
}

document.querySelector("#verify-ticket").addEventListener("click", async () => {
  try {
    const result = await api("/api/tickets/verify", {
      method: "POST",
      body: JSON.stringify({ reference: document.querySelector("#reference").value })
    });
    document.querySelector("#ticket-result").textContent = JSON.stringify(result, null, 2);
    await refreshDashboard();
  } catch (error) {
    document.querySelector("#ticket-result").textContent = error.message;
  }
});

document.querySelector("#redeem-ticket").addEventListener("click", async () => {
  const resultElement = document.querySelector("#ticket-result");
  try {
    resultElement.textContent = "Redeeming...";
    const result = await api("/api/tickets/redeem", {
      method: "POST",
      body: JSON.stringify({
        reference: document.querySelector("#reference").value,
        sensor: document.querySelector("#sensor").value
      })
    });
    resultElement.textContent = JSON.stringify(result, null, 2);
  } catch (error) {
    if (error.message.includes("Failed") || error.message.includes("fetch")) {
      resultElement.textContent = "ERROR: Network outage — gate denied; redemption not confirmed.";
    } else {
      resultElement.textContent = `ERROR: ${error.message}`;
    }
  }
  await refreshDashboard();
});

setInterval(refreshDashboard, 3000);
