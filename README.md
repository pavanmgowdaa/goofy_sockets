# FAIRPASS

FAIRPASS is a dependency-free prototype for one-time event ticket verification. It combines:

- WhatsApp OTP organizer login
- Ticket verify/redeem flow
- Mock or explicit-pending blockchain adapter
- Model gate allow/deny decisions
- Sensor tailgating/unusual-passage alerts
- Accepted/rejected/duplicate/sensor dashboard events

The repository was empty when implementation began, so this app provides the complete hackathon prototype in Node.js without third-party runtime dependencies.

## Run

```bash
cp .env.example .env
node --env-file=.env server.js
# Open http://localhost:3000
```

Node.js 18 or newer is required.

## WhatsApp OTP strategy

The app supports **only WhatsApp OTP login**. There is no Apple Sign-In, password login, Twilio, or Meta credential dependency.

The default `console` adapter writes the OTP to the server log. It is safe for local demos but is not live WhatsApp delivery.

- `WHATSAPP_OTP_MODE=console`: demo delivery, with `DEMO_OTP` controlling a fixed test code
- `WHATSAPP_OTP_MODE=webhook`: POSTs `{phone, otp, channel:"whatsapp"}` to `WHATSAPP_OTP_WEBHOOK_URL`

The generic webhook adapter lets you connect an approved WhatsApp provider later. It does not claim that a provider is free or send credentials automatically. Production WhatsApp Business delivery normally requires provider/account configuration and can incur cost. Do not enable a paid provider unless those costs and external credentials are approved. A live Meta integration is not implemented by default because it needs an approved WhatsApp Business account and credentials.

OTP codes expire after 300 seconds by default and are rate-limited. Phone numbers and OTP values are not stored as on-chain identity data.

## Ticket and gate behavior

Demo references are `FAIR-001`, `FAIR-002`, and `FAIR-003`. A successful mock redemption marks one reference as redeemed for the server process lifetime. Reusing it returns `duplicate` and denies the gate. `FAIR-003` is cancelled and is denied as invalid.

- `POST /api/tickets/verify`: valid, duplicate, or invalid lookup
- `POST /api/tickets/redeem`: valid/duplicate/invalid/sensor result plus gate decision
- `POST /api/sensors/report`: clear or unusual sensor model result
- `GET /api/dashboard`: counts and recent events

Network outages and blockchain confirmation delay are explicit:

- `result: "pending"` / HTTP 202 means no confirmation and no gate opening
- `result: "error"` / HTTP 503 means verification outcome is unknown and the gate is denied

The UI never treats pending or error as accepted.

## Blockchain adapter

The safe default is `BLOCKCHAIN_MODE=mock`. It records a synthetic transaction reference and confirms locally.

`BLOCKCHAIN_MODE=pending` produces explicit pending states for UI testing.

`BLOCKCHAIN_MODE=quai` is an integration guard, not a deployed contract. It only enables the pending/error behavior. It requires verified values for:

- `QUAI_RPC_URL`
- `QUAI_CHAIN_ID`
- `QUAI_CONTRACT_ADDRESS`

Do not deploy or invent Quai Orchard/Cyprus 1 RPC, chain ID, contract address, or private key settings. Confirm current official Quai documentation and the contract ABI before deployment. Private keys must remain outside source control and on a secure deployment host.

Only ticket reference/hash and status belong on-chain. Names, email addresses, phone numbers, OTP secrets, and dashboard personal data remain off-chain.

## Security hooks

- Per-IP OTP request and OTP verification rate limits
- Per-IP redemption rate limit
- OTP attempt limit
- Suspicious duplicate/invalid/sensor event recording
- Optional `CAPTCHA_VERIFY_URL` placeholder for external proof-of-human verification
- Optional `QUEUE_TOKEN_SECRET` reserved for signed queue-token validation
- Controlled transfer must be implemented only after wallet/organizer authorization is configured

The current code intentionally does not claim to prevent every bot or resale scenario.

## Test

```bash
npm test
```
