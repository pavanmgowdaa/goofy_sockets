import fs from 'node:fs';
import path from 'node:path';
import solc from 'solc';
import { ContractFactory, JsonRpcProvider, Wallet } from 'quais';

const rpcURL=process.env.QUAI_RPC_URL;
const privateKey=process.env.QUAI_PRIVATE_KEY;
if(!rpcURL||!privateKey){throw new Error('QUAI_RPC_URL and QUAI_PRIVATE_KEY are required')}
const sourcePath=path.resolve('contracts/FairPassTickets.sol');
const source=fs.readFileSync(sourcePath,'utf8');
const input={language:'Solidity',sources:{'FairPassTickets.sol':{content:source}},settings:{optimizer:{enabled:true,runs:200},outputSelection:{'*':{'*':['abi','evm.bytecode.object']}}}};
const output=JSON.parse(solc.compile(JSON.stringify(input)));
const errors=(output.errors||[]).filter((item)=>item.severity==='error');
if(errors.length){throw new Error(errors.map((item)=>item.formattedMessage).join('\n'))}
const compiled=output.contracts['FairPassTickets.sol'].FairPassTickets;
fs.writeFileSync('contracts/FairPassTickets.artifact.json',JSON.stringify({contractName:'FairPassTickets',abi:compiled.abi,bytecode:`0x${compiled.evm.bytecode.object}`},null,2));
const provider=new JsonRpcProvider(rpcURL,undefined,{usePathing:true});
const wallet=new Wallet(privateKey,provider);
const factory=new ContractFactory(compiled.abi,`0x${compiled.evm.bytecode.object}`,wallet);
const deployed=await factory.deploy();
await deployed.waitForDeployment();
console.log(JSON.stringify({address:await deployed.getAddress(),deployment_tx:deployed.deploymentTransaction()?.hash,admin:wallet.address}));
await provider.destroy();
