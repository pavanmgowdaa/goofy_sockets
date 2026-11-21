"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createServer, tickets } = require("../server");

async function startServer() {
  const server = createServer();
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return { server, base: `http://127.0.0.1:${server.address().port}` };
}

async function stopServer(server) {
  await new Promise(resolve => server.close(resolve));
}

async function login(base) {
  let response = await fetch(`${base}/api/auth/request-otp`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ phone: "+15550000001" })
  });
  assert.equal(response.status, 200);
  response = await fetch(`${base}/api/auth/verify-otp`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ phone: "+15550000001", otp: process.env.DEMO_OTP || "123456" })
  });
  if (response.status !== 200) {
    throw new Error(`OTP verification setup failed: ${await response.text()}`);
  }
  return (await response.json()).token;
}

test("WhatsApp OTP login is the only organizer login", async () => {
  process.env.DEMO_OTP = "123456";
  process.env.NODE_ENV = "test";
  const { server, base } = await startServer();
  try {
    const token = await login(base);
    assert.ok(token);
    const bad = await fetch(`${base}/api/auth/verify-otp`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ phone: "+15550000001", otp: "000000" })
    });
    assert.equal(bad.status, 401);
  } finally {
    await stopServer(server);
  }
});

test("valid, duplicate, invalid, and sensor redemption cases", async () => {
  process.env.DEMO_OTP = "123456";
  process.env.BLOCKCHAIN_MODE = "mock";
  process.env.NODE_ENV = "test";
  const { server, base } = await startServer();
  try {
    const token = await login(base);
    tickets.set("TEST-001", { reference: "TEST-001", status: "active", event: "Test" });

    let response = await fetch(`${base}/api/tickets/redeem`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({ reference: "TEST-001", sensor: "clear" })
    });
    assert.equal(response.status, 200);
    assert.equal((await response.json()).result, "accepted");

    response = await fetch(`${base}/api/tickets/redeem`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({ reference: "TEST-001", sensor: "clear" })
    });
    assert.equal((await response.json()).result, "duplicate");

    response = await fetch(`${base}/api/tickets/redeem`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({ reference: "NOT-REGISTERED", sensor: "clear" })
    });
    assert.equal((await response.json()).gate, "deny");

    response = await fetch(`${base}/api/tickets/redeem`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({ reference: "TEST-001", sensor: "tailgating" })
    });
    assert.equal((await response.json()).result, "sensor_alert");
  } finally {
    await stopServer(server);
  }
});

test("live blockchain configuration reports errors and pending states do not claim confirmation", async () => {
  process.env.BLOCKCHAIN_MODE = "quai";
  process.env.NODE_ENV = "test";
  delete process.env.QUAI_RPC_URL;
  delete process.env.QUAI_CHAIN_ID;
  delete process.env.QUAI_CONTRACT_ADDRESS;
  process.env.DEMO_OTP = "123456";
  const { server, base } = await startServer();
  try {
    const token = await login(base);
    const response = await fetch(`${base}/api/tickets/redeem`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({ reference: "FAIR-002", sensor: "clear" })
    });
    assert.equal(response.status, 503);
    const body = await response.json();
    assert.equal(body.state, "error");
  } finally {
    await stopServer(server);
  }
});
