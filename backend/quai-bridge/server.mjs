import http from 'node:http';
import crypto from 'node:crypto';
import { Contract, JsonRpcProvider, Wallet, isAddress } from 'quais';

const port = Number(process.env.QUAI_BRIDGE_PORT || 8090);
const token = process.env.QUAI_BRIDGE_TOKEN || '';
const rpcURL = process.env.QUAI_RPC_URL || '';
const privateKey = process.env.QUAI_PRIVATE_KEY || '';
const contractAddress = process.env.QUAI_CONTRACT_ADDRESS || '';
const confirmationsNeeded = Math.max(1, Number(process.env.QUAI_CONFIRMATIONS || 1));
const abi = [
  'function register(bytes32 ticketId,address owner)',
  'function adminTransfer(bytes32 ticketId,address to)',
  'function redeem(bytes32 ticketId)',
  'function verify(bytes32 ticketId) view returns (address owner,bool redeemed,bool exists)'
];

let provider, wallet, contract;
if (rpcURL && privateKey && contractAddress) {
  provider = new JsonRpcProvider(rpcURL, undefined, { usePathing: true });
  wallet = new Wallet(privateKey, provider);
  contract = new Contract(contractAddress, abi, wallet);
}

function authOK(req) {
  const supplied = Buffer.from((req.headers.authorization || '').replace(/^Bearer\s+/i, ''));
  const expected = Buffer.from(token);
  return token.length >= 32 && supplied.length === expected.length && crypto.timingSafeEqual(supplied, expected);
}
function send(res, status, body) {
  res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' });
  res.end(JSON.stringify(body));
}
async function bodyJSON(req) {
  let data = '';
  for await (const chunk of req) { data += chunk; if (data.length > 8192) throw new Error('request too large'); }
  return JSON.parse(data || '{}');
}
function bytes32(input) {
  if (!/^0x[0-9a-fA-F]{64}$/.test(input || '')) throw new Error('ticket_id must be a 32-byte hex value');
  return input;
}
async function getState(id) {
  const [owner, redeemed, exists] = await contract.verify(id);
  return { owner, redeemed: Boolean(redeemed), exists: Boolean(exists) };
}

const server = http.createServer(async (req, res) => {
  try {
    const requestURL = new URL(req.url, 'http://localhost');
    if (req.method === 'GET' && req.url === '/healthz') {
      if (!provider || !contract) return send(res, 503, { status: 'not_configured' });
      await provider.getBlockNumber();
      return send(res, 200, { status: 'ok' });
    }
    if (!authOK(req)) return send(res, 401, { error: 'unauthorized' });
    if (!provider || !contract) return send(res, 503, { error: 'Quai bridge is not configured' });

    if (req.method === 'POST' && requestURL.pathname === '/v1/address/validate') {
      const input = await bodyJSON(req);
      return send(res, 200, { valid: isAddress(input.address || '') });
    }
    if (req.method === 'POST' && requestURL.pathname === '/v1/transactions') {
      const input = await bodyJSON(req);
      const id = bytes32(input.ticket_id);
      let tx;
      if (input.operation === 'register') {
        if (!isAddress(input.target_address)) return send(res, 400, { error: 'invalid target address' });
        const state = await getState(id);
        if (state.exists) {
          if (state.owner.toLowerCase() === input.target_address.toLowerCase()) return send(res, 200, { status: 'confirmed', tx_hash: null });
          return send(res, 409, { error: 'ticket already registered to another owner' });
        }
        tx = await contract.register(id, input.target_address);
      } else if (input.operation === 'transfer') {
        if (!isAddress(input.target_address)) return send(res, 400, { error: 'invalid target address' });
        const state = await getState(id);
        if (!state.exists || state.redeemed) return send(res, 409, { error: 'ticket is not transferable' });
        if (state.owner.toLowerCase() === input.target_address.toLowerCase()) return send(res, 200, { status: 'confirmed', tx_hash: null });
        tx = await contract.adminTransfer(id, input.target_address);
      } else if (input.operation === 'redeem') {
        const state = await getState(id);
        if (!state.exists) return send(res, 404, { error: 'ticket is not registered' });
        if (state.redeemed) return send(res, 200, { status: 'confirmed', tx_hash: null });
        tx = await contract.redeem(id);
      } else {
        return send(res, 400, { error: 'unsupported operation' });
      }
      return send(res, 202, { status: 'submitted', tx_hash: tx.hash });
    }

    const txMatch = req.method === 'GET' && requestURL.pathname.match(/^\/v1\/transactions\/(0x[0-9a-fA-F]+)$/);
    if (txMatch) {
      const receipt = await provider.getTransactionReceipt(txMatch[1]);
      if (!receipt) return send(res, 200, { status: 'submitted' });
      if(Number(receipt.status)!==1)return send(res,200,{status:'failed',block_number:Number(receipt.blockNumber)});
      const head=await provider.getBlockNumber();
      const confirmations=head-Number(receipt.blockNumber)+1;
      return send(res, 200, { status: confirmations>=Math.max(confirmationsNeeded,Number(requestURL.searchParams.get('confirmations')||1)) ? 'confirmed' : 'submitted', confirmations, block_number: Number(receipt.blockNumber) });
    }
    const verifyMatch = req.method === 'GET' && requestURL.pathname.match(/^\/v1\/tickets\/(0x[0-9a-fA-F]{64})$/);
    if (verifyMatch) return send(res, 200, await getState(verifyMatch[1]));
    return send(res, 404, { error: 'not found' });
  } catch (err) {
    console.error('Quai bridge request failed:', err?.message || String(err));
    return send(res, 502, { error: 'Quai request failed' });
  }
});

server.listen(port, '0.0.0.0', () => console.log(`Quai bridge listening on ${port}`));
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
  server.close(() => { provider?.destroy(); process.exit(0); });
});
