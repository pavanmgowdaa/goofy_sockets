// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/// @notice Minimal registry of ticket hashes. Personal data must never be stored here.
contract FairPassTickets {
    address public immutable admin;
    struct Ticket { address owner; bool redeemed; bool exists; }
    mapping(bytes32 => Ticket) private tickets;
    event Registered(bytes32 indexed ticketId, address indexed owner);
    event Transferred(bytes32 indexed ticketId, address indexed from, address indexed to);
    event Redeemed(bytes32 indexed ticketId);
    modifier onlyAdmin() { require(msg.sender == admin, "not admin"); _; }
    constructor() { admin = msg.sender; }
    function register(bytes32 ticketId, address owner) external onlyAdmin {
        require(ticketId != bytes32(0) && owner != address(0) && !tickets[ticketId].exists, "invalid ticket");
        tickets[ticketId] = Ticket(owner, false, true); emit Registered(ticketId, owner);
    }
    function transfer(bytes32 ticketId, address to) external {
        Ticket storage t = tickets[ticketId]; require(t.exists && !t.redeemed && t.owner == msg.sender && to != address(0), "invalid transfer");
        t.owner = to; emit Transferred(ticketId, msg.sender, to);
    }
    /// @notice Platform-mediated ownership change after a verified resale payment.
    function adminTransfer(bytes32 ticketId, address to) external onlyAdmin {
        Ticket storage t = tickets[ticketId]; require(t.exists && !t.redeemed && to != address(0), "invalid transfer");
        address from = t.owner; t.owner = to; emit Transferred(ticketId, from, to);
    }
    function redeem(bytes32 ticketId) external onlyAdmin {
        Ticket storage t = tickets[ticketId]; require(t.exists && !t.redeemed, "invalid redemption");
        t.redeemed = true; emit Redeemed(ticketId);
    }
    function verify(bytes32 ticketId) external view returns (address owner, bool redeemed, bool exists) {
        Ticket memory t = tickets[ticketId]; return (t.owner, t.redeemed, t.exists);
    }
}
