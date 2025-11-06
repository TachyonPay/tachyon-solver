// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";

contract MockERC20 is ERC20 {
    uint8 private _decimals;

    constructor(
        string memory name,
        string memory symbol,
        uint8 decimals_,
        uint256 initialSupply
    ) ERC20(name, symbol) {
        _decimals = decimals_;
        _mint(msg.sender, initialSupply);
    }

    function decimals() public view virtual override returns (uint8) {
        return _decimals;
    }

    // Public mint function for testnet
    function mint(address to, uint256 amount) public {
        _mint(to, amount);
    }

    // Mint to msg.sender for convenience
    function mint(uint256 amount) public {
        _mint(msg.sender, amount);
    }
} 