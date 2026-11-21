"use strict";

const http = require("node:http");
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");

const PUBLIC_DIR = path.join(__dirname, "public");
const OTP_TTL_SECONDS = Number(process.env.OTP_TTL_SECONDS || 300);
const OTP_REQUEST_LIMIT = Number(process.env.OTP_REQUESTS_PER_15_MIN || 5);

const otpStore = new Map();
const sessions = new Map();
const ipRequestCounts = new Map();
const events = [];

const tickets = new Map([
  ["FAIR-001", { reference: "FAIR-001", status: "active", event: "FAIRPASS Demo 2026" }],
  ["FAIR-002", { reference: "FAIR-002", status: "active", event: "FAIRPASS Demo 2026" }],
  ["FAIR-003", { reference: "FAIR-003", status: "cancelled", event: "FAIRPASS Demo 2026" }]
]);

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function json(res, status, body) {
  res.writeHead(status, { "content-type": "application/json", "cache-control": "no-store" });
  res.end(JSON.stringify(body));
}

function readJson(req) {
  return new Promise((resolve, reject) => {
    let body = "";
    req.on("data", chunk => {
      body += chunk;
      if (body.length > 64 * 1024) {
        reject(new Error("Payload too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      try {
        resolve(body ? JSON.parse(body) : {});
      } catch {
        reject(new Error("Invalid JSON"));
      }
    });
  });
}

function normalizePhone(phone) {
  return String(phone || "").replace(/[^\d+]/g, "");
}

function isRateLimited(ip, key, limit = OTP_REQUEST_LIMIT, windowMs = 15 * 60 * 1000) {
  const mapKey = `${ip}:${key}`;
  const now = Date.now();
  const bucket = ipRequestCounts.get(mapKey) || { count: 0, expires: now + windowMs };
  if (now >= bucket.expires) {
    bucket.count = 0;
    bucket.expires = now + windowMs;
  }
  bucket.count += 1;
  ipRequestCounts.set(mapKey, bucket);
  return bucket.count > limit;
}

function recordEvent(type, details = {}) {
  const event = {
    id: crypto.randomUUID(),
    type,
    timestamp: new Date().toISOString(),
    ...details
  };
  events.unshift(event);
  if (events.length > 500) events.pop();
  return event;
}

function requireSession(req, res) {
  const token = (req.headers.authorization || "").replace(/^Bearer\s+/i, "");
  if (!token || !sessions.has(token)) {
    json(res, 401, { error: "Login required" });
    return null;
  }
  return sessions.get(token);
}

async function deliverOtp(phone, otp) {
  const mode = (process.env.WHATSAPP_OTP_MODE || "console").toLowerCase();
  if (mode === "webhook") {
    if (!process.env.WHATSAPP_OTP_WEBHOOK_URL) {
      throw new Error("WHATSAPP_OTP_WEBHOOK_URL is required");
    }
    if (typeof fetch !== "function") throw new Error("Webhook delivery requires Node 18+");
    const response = await fetch(process.env.WHATSAPP_OTP_WEBHOOK_URL, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ phone, otp, channel: "whatsapp" })
    });
    if (!response.ok) throw new Error(`OTP webhook returned ${response.status}`);
    return { delivered: true, mode: "webhook" };
  }
  if (process.env.NODE_ENV !== "test") {
    console.log(`[WhatsApp OTP demo] ${phone}: ${otp}`);
  }
  return { delivered: true, mode: "console" };
}

async function verifyCaptcha(req) {
  if (!process.env.CAPTCHA_VERIFY_URL) return true;
  const body = await readJson(req).catch(() => null);
  return false;
}

function blockchainAdapter() {
  const mode = (process.env.BLOCKCHAIN_MODE || "mock").toLowerCase();
  return {
    mode,
    async redeem(reference) {
      if (mode === "quai") {
        if (!process.env.QUAI_RPC_URL || !process.env.QUAI_CHAIN_ID || !process.env.QUAI_CONTRACT_ADDRESS) {
          return { state: "error", error: "Quai configuration is incomplete" };
        }
        return {
          state: "pending",
          txRef: `pending-${sha256(`${reference}:${Date.now()}`).slice(0, 16)}`
        };
      }
      if (mode === "pending") {
        return { state: "pending", txRef: `demo-pending-${sha256(reference).slice(0, 12)}` };
      }
      return { state: "confirmed", txRef: `mock-tx-${sha256(reference).slice(0, 16)}` };
    }
  };
}

function serveStatic(req, res) {
  let relative = req.url === "/" ? "/index.html" : req.url.split("?")[0];
  const file = path.join(PUBLIC_DIR, path.normalize(relative).replace(/^[/\\]+/, ""));
  if (!file.startsWith(PUBLIC_DIR)) return json(res, 403, { error: "Forbidden" });
  fs.readFile(file, (error, data) => {
    if (error) return json(res, 404, { error: "Not found" });
    const ext = path.extname(file);
    const type = ext === ".js" ? "text/javascript" : ext === ".css" ? "text/css" : "text/html";
    res.writeHead(200, { "content-type": type });
    res.end(data);
  });
}

function createServer() {
  return http.createServer(async (req, res) => {
    const ip = req.socket.remoteAddress || "unknown";
    const url = new URL(req.url, "http://localhost");

    try {
      if (req.method === "POST" && url.pathname === "/api/auth/request-otp") {
        if (isRateLimited(ip, "otp-request")) return json(res, 429, { error: "Too many OTP requests" });
        const { phone } = await readJson(req);
        const normalized = normalizePhone(phone);
        if (!/^\+?\d{8,15}$/.test(normalized)) return json(res, 400, { error: "Enter a valid WhatsApp phone number" });
        const otp = process.env.DEMO_OTP || String(crypto.randomInt(100000, 1000000));
        otpStore.set(normalized, {
          hash: sha256(otp),
          expires: Date.now() + OTP_TTL_SECONDS * 1000,
          attempts: 0
        });
        await deliverOtp(normalized, otp);
        recordEvent("otp_requested", { phoneHash: sha256(normalized).slice(0, 12) });
        return json(res, 200, { sent: true, delivery: (process.env.WHATSAPP_OTP_MODE || "console") });
      }

      if (req.method === "POST" && url.pathname === "/api/auth/verify-otp") {
        if (isRateLimited(ip, "otp-verify", 10)) return json(res, 429, { error: "Too many verification attempts" });
        const { phone, otp } = await readJson(req);
        const normalized = normalizePhone(phone);
        const challenge = otpStore.get(normalized);
        if (!challenge || Date.now() > challenge.expires) return json(res, 401, { error: "OTP expired or not requested" });
        challenge.attempts += 1;
        if (challenge.attempts > 5) {
          otpStore.delete(normalized);
          return json(res, 429, { error: "Too many OTP attempts" });
        }
        if (challenge.hash !== sha256(String(otp || ""))) return json(res, 401, { error: "Invalid OTP" });
        otpStore.delete(normalized);
        const token = crypto.randomBytes(32).toString("hex");
        sessions.set(token, { phoneHash: sha256(normalized), createdAt: new Date().toISOString() });
        return json(res, 200, { token, authenticated: true });
      }

      if (req.method === "POST" && url.pathname === "/api/tickets/verify") {
        const { reference } = await readJson(req);
        const ticket = tickets.get(String(reference || "").trim().toUpperCase());
        let result;
        if (!ticket) result = { result: "invalid", reason: "Ticket is not registered" };
        else if (ticket.status === "cancelled") result = { result: "invalid", reason: "Ticket has been cancelled" };
        else if (ticket.status === "redeemed") result = { result: "duplicate", reason: "Ticket was already redeemed" };
        else result = { result: "valid", reason: "Ticket is registered and available" };
        recordEvent(`scan_${result.result}`, { ticketHash: sha256(String(reference || "")).slice(0, 12), result: result.result });
        return json(res, 200, result);
      }

      if (req.method === "POST" && url.pathname === "/api/tickets/redeem") {
        const session = requireSession(req, res);
        if (!session) return;
        const { reference, sensor = "clear" } = await readJson(req);
        if (!reference) return json(res, 400, { error: "Ticket reference is required" });
        if (isRateLimited(ip, "redeem", 30, 60 * 1000)) return json(res, 429, { error: "Redemption rate limit reached" });

        if (sensor === "tailgating" || sensor === "unusual") {
          recordEvent("sensor_alert", { sensor, ticketHash: sha256(reference).slice(0, 12) });
          return json(res, 200, { result: "sensor_alert", gate: "deny", reason: "Unusual passage pattern detected" });
        }

        const key = String(reference).trim().toUpperCase();
        const ticket = tickets.get(key);
        if (!ticket) {
          recordEvent("scan_invalid", { ticketHash: sha256(key).slice(0, 12), result: "invalid" });
          return json(res, 200, { result: "invalid", gate: "deny", reason: "Unregistered ticket" });
        }
        if (ticket.status === "cancelled") {
          recordEvent("scan_invalid", { ticketHash: sha256(key).slice(0, 12), result: "invalid" });
          return json(res, 200, { result: "invalid", gate: "deny", reason: "Ticket cancelled" });
        }
        if (ticket.status === "redeemed") {
          recordEvent("scan_duplicate", { ticketHash: sha256(key).slice(0, 12), result: "duplicate" });
          return json(res, 200, { result: "duplicate", gate: "deny", reason: "One-time redemption already used" });
        }

        const adapter = blockchainAdapter();
        const chain = await adapter.redeem(key);
        if (chain.state === "error") {
          recordEvent("chain_error", { ticketHash: sha256(key).slice(0, 12), reason: chain.error });
          return json(res, 503, { result: "error", gate: "deny", state: "error", reason: chain.error });
        }
        if (chain.state === "pending") {
          recordEvent("chain_pending", { ticketHash: sha256(key).slice(0, 12), txRef: chain.txRef });
          return json(res, 202, { result: "pending", gate: "pending", state: "pending", txRef: chain.txRef });
        }
        ticket.status = "redeemed";
        recordEvent("scan_accepted", { ticketHash: sha256(key).slice(0, 12), result: "accepted", txRef: chain.txRef });
        return json(res, 200, { result: "accepted", gate: "allow", state: "confirmed", txRef: chain.txRef });
      }

      if (req.method === "POST" && url.pathname === "/api/sensors/report") {
        const { pattern = "clear", ticketReference } = await readJson(req);
        const unusual = ["tailgating", "reversal", "rapid_passage"].includes(pattern);
        recordEvent(unusual ? "sensor_alert" : "sensor_clear", {
          sensor: pattern,
          ticketHash: ticketReference ? sha256(ticketReference).slice(0, 12) : null
        });
        return json(res, 200, { gate: unusual ? "deny" : "allow", alert: unusual });
      }

      if (req.method === "GET" && url.pathname === "/api/dashboard") {
        const accepted = events.filter(e => e.type === "scan_accepted").length;
        const rejected = events.filter(e => e.type === "scan_invalid").length;
        const duplicate = events.filter(e => e.type === "scan_duplicate").length;
        const sensorAlerts = events.filter(e => e.type === "sensor_alert").length;
        const pending = events.filter(e => e.type === "chain_pending").length;
        return json(res, 200, {
          summary: { accepted, rejected, duplicate, sensorAlerts, pending },
          events: events.slice(0, 100),
          blockchain: blockchainAdapter().mode
        });
      }

      if (url.pathname.startsWith("/api/")) return json(res, 404, { error: "API route not found" });
      return serveStatic(req, res);
    } catch (error) {
      json(res, 500, { error: error.message || "Internal server error" });
    }
  });
}

if (require.main === module) {
  const port = Number(process.env.PORT || 3000);
  createServer().listen(port, () => console.log(`FAIRPASS listening on http://localhost:${port}`));
}

module.exports = { createServer, tickets, otpStore, sessions, events, sha256 };
