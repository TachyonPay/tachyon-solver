import { expect } from "chai";
import { ethers } from "hardhat";
import { Signer } from "ethers";
import { time } from "@nomicfoundation/hardhat-network-helpers";

describe("BridgeIntentV2", function () {
  let bridgeIntent: any;
  let tokenA: any;
  let tokenB: any;
  let admin: Signer;
  let user: Signer;
  let solver1: Signer;
  let solver2: Signer;
  let solver3: Signer;
  let relayer: Signer;
  let pauser: Signer;
  let operator: Signer;
  let recipient1: Signer;
  let recipient2: Signer;

  const CHAIN_ID = 1;
  const DEST_CHAIN_ID = 2;
  const INITIAL_SUPPLY = ethers.parseEther("1000000");
  const AMOUNT_A = ethers.parseUnits("100", 18);
  const EXPECTED_AMOUNT_B = ethers.parseUnits("95", 18);
  const REWARD = ethers.parseUnits("5", 18);
  const AUCTION_DURATION = 3600; // 1 hour

  // Role hashes
  const ADMIN_ROLE = ethers.keccak256(ethers.toUtf8Bytes("ADMIN_ROLE"));
  const RELAYER_ROLE = ethers.keccak256(ethers.toUtf8Bytes("RELAYER_ROLE"));
  const PAUSER_ROLE = ethers.keccak256(ethers.toUtf8Bytes("PAUSER_ROLE"));
  const OPERATOR_ROLE = ethers.keccak256(ethers.toUtf8Bytes("OPERATOR_ROLE"));

  beforeEach(async function () {
    [admin, user, solver1, solver2, solver3, relayer, pauser, operator, recipient1, recipient2] = 
      await ethers.getSigners();

    const MockERC20Factory = await ethers.getContractFactory("MockERC20");
    tokenA = await MockERC20Factory.deploy("Token A", "TKNA", 18, INITIAL_SUPPLY);
    tokenB = await MockERC20Factory.deploy("Token B", "TKNB", 18, INITIAL_SUPPLY);

    const BridgeIntentFactory = await ethers.getContractFactory("BridgeIntentV2");
    bridgeIntent = await BridgeIntentFactory.deploy(CHAIN_ID, await admin.getAddress());

    // Distribute tokenA
    await tokenA.transfer(await user.getAddress(), ethers.parseEther("10000"));
    await tokenA.transfer(await solver1.getAddress(), ethers.parseEther("10000"));
    await tokenA.transfer(await solver2.getAddress(), ethers.parseEther("10000"));
    await tokenA.transfer(await solver3.getAddress(), ethers.parseEther("10000"));

    // Distribute tokenB for chain2 solving
    await tokenB.transfer(await solver1.getAddress(), ethers.parseEther("10000"));

    // Setup roles
    await bridgeIntent.connect(admin).grantRole(RELAYER_ROLE, await relayer.getAddress());
    await bridgeIntent.connect(admin).grantRole(PAUSER_ROLE, await pauser.getAddress());
    await bridgeIntent.connect(admin).grantRole(OPERATOR_ROLE, await operator.getAddress());
  });

  describe("Deployment", function () {
    it("Should set the correct chain ID", async function () {
      expect(await bridgeIntent.chainId()).to.equal(CHAIN_ID);
    });

    it("Should grant all roles to admin", async function () {
      const adminAddr = await admin.getAddress();
      expect(await bridgeIntent.isAdmin(adminAddr)).to.be.true;
      expect(await bridgeIntent.isRelayer(adminAddr)).to.be.true;
      expect(await bridgeIntent.isPauser(adminAddr)).to.be.true;
      expect(await bridgeIntent.isOperator(adminAddr)).to.be.true;
    });

    it("Should revert with zero admin address", async function () {
      const BridgeIntentFactory = await ethers.getContractFactory("BridgeIntentV2");
      await expect(
        BridgeIntentFactory.deploy(CHAIN_ID, ethers.ZeroAddress)
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAddress");
    });

    it("Should start with nextIntentId = 1", async function () {
      expect(await bridgeIntent.nextIntentId()).to.equal(1);
    });
  });

  describe("Role Management", function () {
    it("Should allow admin to grant roles", async function () {
      const newRelayer = await solver3.getAddress();
      await bridgeIntent.connect(admin).grantRole(RELAYER_ROLE, newRelayer);
      expect(await bridgeIntent.isRelayer(newRelayer)).to.be.true;
    });

    it("Should allow admin to revoke roles", async function () {
      const relayerAddr = await relayer.getAddress();
      await bridgeIntent.connect(admin).revokeRole(RELAYER_ROLE, relayerAddr);
      expect(await bridgeIntent.isRelayer(relayerAddr)).to.be.false;
    });

    it("Should not allow non-admin to grant roles", async function () {
      await expect(
        bridgeIntent.connect(user).grantRole(RELAYER_ROLE, await solver1.getAddress())
      ).to.be.reverted;
    });
  });

  describe("Intent Creation", function () {
    it("Should create an intent successfully", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);

      const tx = await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      await expect(tx).to.emit(bridgeIntent, "IntentCreated");
      expect(await bridgeIntent.nextIntentId()).to.equal(2);
    });

    it("Should store intent data correctly", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      const intent = await bridgeIntent.getIntent(1);
      expect(intent.user).to.equal(await user.getAddress());
      expect(intent.amountA).to.equal(AMOUNT_A);
      expect(intent.tokenA).to.equal(await tokenA.getAddress());
      expect(intent.expectedAmountB).to.equal(EXPECTED_AMOUNT_B);
      expect(intent.tokenB).to.equal(await tokenB.getAddress());
      expect(intent.reward).to.equal(REWARD);
      expect(intent.destChainId).to.equal(DEST_CHAIN_ID);
      expect(intent.status).to.equal(0); // Active
    });

    it("Should add intent to active list", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      expect(await bridgeIntent.getActiveIntentCount()).to.equal(1);
      const activeIntents = await bridgeIntent.getActiveIntents(0, 10);
      expect(activeIntents[0]).to.equal(1);
    });

    it("Should fail with zero tokenA address", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          ethers.ZeroAddress,
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAddress");
    });

    it("Should fail with zero tokenB address", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          ethers.ZeroAddress,
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAddress");
    });

    it("Should fail with zero amountA", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          0,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail with zero expectedAmountB", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          0,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail with zero reward", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          0,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail with zero destChainId", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          0,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail with auction duration too short", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          10 // Less than 30 seconds
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidDuration");
    });

    it("Should fail with auction duration too long", async function () {
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          86401 // More than 24 hours
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidDuration");
    });

    it("Should fail with reward below minimum", async function () {
      // Set minimum reward
      await bridgeIntent.connect(operator).setMinReward(await tokenA.getAddress(), REWARD + 1n);
      
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "RewardBelowMinimum");
    });

    it("Should fail when paused", async function () {
      await bridgeIntent.connect(pauser).pause();
      
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await expect(
        bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "EnforcedPause");
    });
  });

  describe("Bidding", function () {
    let intentId: bigint;

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;
    });

    it("Should allow placing a bid with full deposit", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);

      const balanceBefore = await tokenA.balanceOf(await solver1.getAddress());
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);
      const balanceAfter = await tokenA.balanceOf(await solver1.getAddress());

      expect(balanceBefore - balanceAfter).to.equal(bidAmount);
      expect(await bridgeIntent.getSolverBid(intentId, await solver1.getAddress())).to.equal(bidAmount);
    });

    it("Should emit BidPlaced event", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);

      await expect(bridgeIntent.connect(solver1).placeBid(intentId, bidAmount))
        .to.emit(bridgeIntent, "BidPlaced");
    });

    it("Should update bid state correctly", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);

      const bidState = await bridgeIntent.getBidState(intentId);
      expect(bidState.highestBidder).to.equal(await solver1.getAddress());
      expect(bidState.highestBid).to.equal(bidAmount);
      expect(bidState.bidCount).to.equal(1);
    });

    it("Should allow increasing existing bid", async function () {
      const bid1 = ethers.parseUnits("96", 18);
      const bid2 = ethers.parseUnits("100", 18);
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid1);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid2);

      expect(await bridgeIntent.getSolverBid(intentId, await solver1.getAddress())).to.equal(bid2);
      
      const bidState = await bridgeIntent.getBidState(intentId);
      expect(bidState.bidCount).to.equal(1); // Still 1 bidder
    });

    it("Should require minimum 1% increment over highest bid", async function () {
      const bid1 = ethers.parseUnits("100", 18);
      const lowBid = ethers.parseUnits("100.5", 18); // Less than 1% increase
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), lowBid);
      await expect(
        bridgeIntent.connect(solver2).placeBid(intentId, lowBid)
      ).to.be.revertedWithCustomError(bridgeIntent, "BidTooLow");
    });

    it("Should accept bid with exactly 1% increment", async function () {
      const bid1 = ethers.parseUnits("100", 18);
      const minValidBid = ethers.parseUnits("101", 18); // Exactly 1% increase
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), minValidBid);
      await expect(bridgeIntent.connect(solver2).placeBid(intentId, minValidBid)).to.not.be.reverted;
    });

    it("Should extend auction on late bid (anti-snipe)", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      
      // Fast forward to 1 minute before end (within 2 min threshold)
      await time.increase(AUCTION_DURATION - 60);
      
      const intentBefore = await bridgeIntent.getIntent(intentId);
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);
      
      const intentAfter = await bridgeIntent.getIntent(intentId);
      expect(intentAfter.endTime).to.be.gt(intentBefore.endTime);
    });

    it("Should fail on invalid intent ID", async function () {
      await expect(
        bridgeIntent.connect(solver1).placeBid(0, ethers.parseUnits("96", 18))
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidIntentId");

      await expect(
        bridgeIntent.connect(solver1).placeBid(999, ethers.parseUnits("96", 18))
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidIntentId");
    });

    it("Should fail after auction ends", async function () {
      await time.increase(AUCTION_DURATION + 1);
      
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      
      await expect(
        bridgeIntent.connect(solver1).placeBid(intentId, bidAmount)
      ).to.be.revertedWithCustomError(bridgeIntent, "AuctionEnded");
    });

    it("Should fail with zero bid amount", async function () {
      await expect(
        bridgeIntent.connect(solver1).placeBid(intentId, 0)
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail when paused", async function () {
      await bridgeIntent.connect(pauser).pause();
      
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      
      await expect(
        bridgeIntent.connect(solver1).placeBid(intentId, bidAmount)
      ).to.be.revertedWithCustomError(bridgeIntent, "EnforcedPause");
    });
  });

  describe("Bid Withdrawal", function () {
    let intentId: bigint;
    const bid1 = ethers.parseUnits("96", 18);
    const bid2 = ethers.parseUnits("98", 18);

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;

      // Solver1 bids, then solver2 outbids
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver2).placeBid(intentId, bid2);
    });

    it("Should allow non-winner to withdraw after auction ends", async function () {
      await time.increase(AUCTION_DURATION + 1);

      const balanceBefore = await tokenA.balanceOf(await solver1.getAddress());
      await bridgeIntent.connect(solver1).withdrawBid(intentId);
      const balanceAfter = await tokenA.balanceOf(await solver1.getAddress());

      expect(balanceAfter - balanceBefore).to.equal(bid1);
    });

    it("Should emit BidWithdrawn event", async function () {
      await time.increase(AUCTION_DURATION + 1);

      await expect(bridgeIntent.connect(solver1).withdrawBid(intentId))
        .to.emit(bridgeIntent, "BidWithdrawn")
        .withArgs(intentId, await solver1.getAddress(), bid1);
    });

    it("Should not allow withdrawal during active auction", async function () {
      await expect(
        bridgeIntent.connect(solver1).withdrawBid(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "AuctionNotEnded");
    });

    it("Should not allow winner to withdraw after finalization", async function () {
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(intentId);

      await expect(
        bridgeIntent.connect(solver2).withdrawBid(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "NotAuthorized");
    });

    it("Should not allow double withdrawal", async function () {
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.connect(solver1).withdrawBid(intentId);

      // After withdrawal, solverBids is set to 0, so NoBidToWithdraw fires first
      await expect(
        bridgeIntent.connect(solver1).withdrawBid(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "NoBidToWithdraw");
    });

    it("Should fail if no bid to withdraw", async function () {
      await time.increase(AUCTION_DURATION + 1);

      await expect(
        bridgeIntent.connect(solver3).withdrawBid(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "NoBidToWithdraw");
    });
  });

  describe("Auction Finalization", function () {
    let intentId: bigint;

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;
    });

    it("Should finalize with correct winner", async function () {
      const bid1 = ethers.parseUnits("96", 18);
      const bid2 = ethers.parseUnits("98", 18);

      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(intentId, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver2).placeBid(intentId, bid2);

      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(intentId);

      const intent = await bridgeIntent.getIntent(intentId);
      expect(intent.winningSolver).to.equal(await solver2.getAddress());
      expect(intent.winningBid).to.equal(bid2);
      expect(intent.status).to.equal(1); // Finalized
    });

    it("Should emit AuctionFinalized event", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);

      await time.increase(AUCTION_DURATION + 1);

      await expect(bridgeIntent.finalizeAuction(intentId))
        .to.emit(bridgeIntent, "AuctionFinalized");
    });

    it("Should auto-cancel if no bids", async function () {
      await time.increase(AUCTION_DURATION + 1);

      const userBalanceBefore = await tokenA.balanceOf(await user.getAddress());
      await bridgeIntent.finalizeAuction(intentId);
      const userBalanceAfter = await tokenA.balanceOf(await user.getAddress());

      // User should get refund
      expect(userBalanceAfter - userBalanceBefore).to.equal(AMOUNT_A + REWARD);
      
      const intent = await bridgeIntent.getIntent(intentId);
      expect(intent.status).to.equal(3); // Cancelled
    });

    it("Should fail before auction ends", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);

      await expect(
        bridgeIntent.finalizeAuction(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "AuctionNotEnded");
    });

    it("Should fail if already finalized", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);

      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(intentId);

      await expect(
        bridgeIntent.finalizeAuction(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "IntentNotActive");
    });
  });

  describe("Solve Intent on Chain2", function () {
    it("Should solve intent on destination chain", async function () {
      const sourceChainId = 1;
      const intentId = 1;
      const userAddr = await user.getAddress();
      const amountB = ethers.parseUnits("95", 18);
      const recipients = [await recipient1.getAddress(), await recipient2.getAddress()];
      const amounts = [ethers.parseUnits("50", 18), ethers.parseUnits("45", 18)];

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB);

      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          sourceChainId, intentId, userAddr,
          await tokenB.getAddress(), amountB,
          recipients, amounts
        )
      ).to.emit(bridgeIntent, "IntentSolvedOnChain2");
    });

    it("Should distribute tokens to recipients", async function () {
      const sourceChainId = 1;
      const intentId = 1;
      const amountB = ethers.parseUnits("95", 18);
      const recipients = [await recipient1.getAddress(), await recipient2.getAddress()];
      const amounts = [ethers.parseUnits("50", 18), ethers.parseUnits("45", 18)];

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB);
      await bridgeIntent.connect(solver1).solveIntentOnChain2(
        sourceChainId, intentId, await user.getAddress(),
        await tokenB.getAddress(), amountB,
        recipients, amounts
      );

      expect(await tokenB.balanceOf(await recipient1.getAddress())).to.equal(amounts[0]);
      expect(await tokenB.balanceOf(await recipient2.getAddress())).to.equal(amounts[1]);
    });

    it("Should mark intent as solved", async function () {
      const sourceChainId = 1;
      const intentId = 1;
      const amountB = ethers.parseUnits("95", 18);

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB);
      await bridgeIntent.connect(solver1).solveIntentOnChain2(
        sourceChainId, intentId, await user.getAddress(),
        await tokenB.getAddress(), amountB,
        [await recipient1.getAddress()], [amountB]
      );

      expect(await bridgeIntent.isIntentSolvedOnChain2(sourceChainId, intentId)).to.be.true;
      expect(await bridgeIntent.getIntentSolver(sourceChainId, intentId)).to.equal(await solver1.getAddress());
    });

    it("Should fail if already solved", async function () {
      const sourceChainId = 1;
      const intentId = 1;
      const amountB = ethers.parseUnits("95", 18);

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB * 2n);
      await bridgeIntent.connect(solver1).solveIntentOnChain2(
        sourceChainId, intentId, await user.getAddress(),
        await tokenB.getAddress(), amountB,
        [await recipient1.getAddress()], [amountB]
      );

      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          sourceChainId, intentId, await user.getAddress(),
          await tokenB.getAddress(), amountB,
          [await recipient1.getAddress()], [amountB]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "IntentAlreadySolved");
    });

    it("Should fail with array length mismatch", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, await user.getAddress(),
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [await recipient1.getAddress(), await recipient2.getAddress()],
          [ethers.parseUnits("95", 18)] // Only one amount
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "ArrayLengthMismatch");
    });

    it("Should fail with total exceeding expected amount", async function () {
      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), ethers.parseUnits("200", 18));
      
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, await user.getAddress(),
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [await recipient1.getAddress()],
          [ethers.parseUnits("100", 18)] // Exceeds expected
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should fail with empty recipients", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, await user.getAddress(),
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [], []
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });
  });

  describe("Settlement", function () {
    let intentId: bigint;
    const winningBid = ethers.parseUnits("98", 18);

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;

      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), winningBid);
      await bridgeIntent.connect(solver1).placeBid(intentId, winningBid);
      
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(intentId);
    });

    it("Should settle intent and pay solver", async function () {
      const solverBalanceBefore = await tokenA.balanceOf(await solver1.getAddress());
      
      await bridgeIntent.connect(relayer).settleIntent(intentId);
      
      const solverBalanceAfter = await tokenA.balanceOf(await solver1.getAddress());
      // Solver receives amountA + reward (their deposit stays locked)
      expect(solverBalanceAfter - solverBalanceBefore).to.equal(AMOUNT_A + REWARD);
    });

    it("Should emit IntentSettled event", async function () {
      await expect(bridgeIntent.connect(relayer).settleIntent(intentId))
        .to.emit(bridgeIntent, "IntentSettled");
    });

    it("Should update intent status to Completed", async function () {
      await bridgeIntent.connect(relayer).settleIntent(intentId);
      
      const intent = await bridgeIntent.getIntent(intentId);
      expect(intent.status).to.equal(2); // Completed
    });

    it("Should remove from active intents", async function () {
      expect(await bridgeIntent.getActiveIntentCount()).to.equal(1);
      
      await bridgeIntent.connect(relayer).settleIntent(intentId);
      
      expect(await bridgeIntent.getActiveIntentCount()).to.equal(0);
    });

    it("Should fail if not finalized", async function () {
      // Create another intent without finalizing
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      await expect(
        bridgeIntent.connect(relayer).settleIntent(2)
      ).to.be.revertedWithCustomError(bridgeIntent, "IntentNotFinalized");
    });

    it("Should fail if caller is not relayer", async function () {
      await expect(
        bridgeIntent.connect(user).settleIntent(intentId)
      ).to.be.reverted; // AccessControl revert
    });
  });

  describe("Intent Cancellation", function () {
    let intentId: bigint;

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;
    });

    it("Should allow user to cancel after timeout", async function () {
      await time.increase(AUCTION_DURATION + 3601); // 1 hour after auction ends

      const userBalanceBefore = await tokenA.balanceOf(await user.getAddress());
      await bridgeIntent.connect(user).cancelIntent(intentId);
      const userBalanceAfter = await tokenA.balanceOf(await user.getAddress());

      expect(userBalanceAfter - userBalanceBefore).to.equal(AMOUNT_A + REWARD);
    });

    it("Should emit IntentCancelled event", async function () {
      await time.increase(AUCTION_DURATION + 3601);

      await expect(bridgeIntent.connect(user).cancelIntent(intentId))
        .to.emit(bridgeIntent, "IntentCancelled");
    });

    it("Should allow admin to cancel immediately", async function () {
      await bridgeIntent.connect(admin).cancelIntent(intentId);
      
      const intent = await bridgeIntent.getIntent(intentId);
      expect(intent.status).to.equal(3); // Cancelled
    });

    it("Should fail if user cancels too early", async function () {
      await time.increase(AUCTION_DURATION + 100); // Only 100s after auction ends

      await expect(
        bridgeIntent.connect(user).cancelIntent(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "TooEarlyToCancel");
    });

    it("Should fail if not user or admin", async function () {
      await time.increase(AUCTION_DURATION + 3601);

      await expect(
        bridgeIntent.connect(solver1).cancelIntent(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "NotAuthorized");
    });

    it("Should fail if already completed", async function () {
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(intentId, bidAmount);
      
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(intentId);
      await bridgeIntent.connect(relayer).settleIntent(intentId);

      await expect(
        bridgeIntent.connect(admin).cancelIntent(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "IntentAlreadyCompleted");
    });

    it("Should fail if already cancelled", async function () {
      await bridgeIntent.connect(admin).cancelIntent(intentId);

      await expect(
        bridgeIntent.connect(admin).cancelIntent(intentId)
      ).to.be.revertedWithCustomError(bridgeIntent, "IntentAlreadyCompleted");
    });
  });

  describe("Pause/Unpause", function () {
    it("Should allow pauser to pause", async function () {
      await bridgeIntent.connect(pauser).pause();
      expect(await bridgeIntent.paused()).to.be.true;
    });

    it("Should allow pauser to unpause", async function () {
      await bridgeIntent.connect(pauser).pause();
      await bridgeIntent.connect(pauser).unpause();
      expect(await bridgeIntent.paused()).to.be.false;
    });

    it("Should not allow non-pauser to pause", async function () {
      await expect(bridgeIntent.connect(user).pause()).to.be.reverted;
    });
  });

  describe("Operator Functions", function () {
    it("Should allow operator to set minimum reward", async function () {
      const minReward = ethers.parseUnits("1", 18);
      
      await expect(
        bridgeIntent.connect(operator).setMinReward(await tokenA.getAddress(), minReward)
      ).to.emit(bridgeIntent, "MinRewardUpdated");

      expect(await bridgeIntent.minRewardPerToken(await tokenA.getAddress())).to.equal(minReward);
    });

    it("Should not allow non-operator to set minimum reward", async function () {
      await expect(
        bridgeIntent.connect(user).setMinReward(await tokenA.getAddress(), ethers.parseUnits("1", 18))
      ).to.be.reverted;
    });
  });

  describe("Emergency Withdraw", function () {
    it("Should allow admin to emergency withdraw", async function () {
      // First put some tokens in the contract
      await tokenA.transfer(await bridgeIntent.getAddress(), ethers.parseEther("100"));
      
      const adminBalanceBefore = await tokenA.balanceOf(await admin.getAddress());
      await bridgeIntent.connect(admin).emergencyWithdraw(
        await tokenA.getAddress(),
        await admin.getAddress(),
        ethers.parseEther("100")
      );
      const adminBalanceAfter = await tokenA.balanceOf(await admin.getAddress());

      expect(adminBalanceAfter - adminBalanceBefore).to.equal(ethers.parseEther("100"));
    });

    it("Should not allow non-admin to emergency withdraw", async function () {
      await expect(
        bridgeIntent.connect(user).emergencyWithdraw(
          await tokenA.getAddress(),
          await user.getAddress(),
          ethers.parseEther("1")
        )
      ).to.be.reverted;
    });
  });

  describe("View Functions", function () {
    let intentId: bigint;

    beforeEach(async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      intentId = 1n;
    });

    it("Should return correct isIntentActive", async function () {
      expect(await bridgeIntent.isIntentActive(intentId)).to.be.true;
      
      await time.increase(AUCTION_DURATION + 1);
      expect(await bridgeIntent.isIntentActive(intentId)).to.be.false;
    });

    it("Should return correct time remaining", async function () {
      const remaining = await bridgeIntent.getTimeRemaining(intentId);
      expect(remaining).to.be.gt(0);
      expect(remaining).to.be.lte(AUCTION_DURATION);
      
      await time.increase(AUCTION_DURATION + 1);
      expect(await bridgeIntent.getTimeRemaining(intentId)).to.equal(0);
    });

    it("Should return latest intent ID", async function () {
      expect(await bridgeIntent.getLatestIntentId()).to.equal(1);
      
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );
      
      expect(await bridgeIntent.getLatestIntentId()).to.equal(2);
    });

    it("Should return chain ID", async function () {
      expect(await bridgeIntent.getChainId()).to.equal(CHAIN_ID);
    });

    it("Should handle getActiveIntents pagination", async function () {
      // Create more intents
      for (let i = 0; i < 4; i++) {
        await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
        await bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        );
      }

      expect(await bridgeIntent.getActiveIntentCount()).to.equal(5);
      
      const page1 = await bridgeIntent.getActiveIntents(0, 2);
      expect(page1.length).to.equal(2);
      
      const page2 = await bridgeIntent.getActiveIntents(2, 2);
      expect(page2.length).to.equal(2);
      
      const page3 = await bridgeIntent.getActiveIntents(4, 2);
      expect(page3.length).to.equal(1);
      
      const outOfRange = await bridgeIntent.getActiveIntents(10, 2);
      expect(outOfRange.length).to.equal(0);
    });

    it("Should return 0 for invalid intent time remaining", async function () {
      expect(await bridgeIntent.getTimeRemaining(0)).to.equal(0);
      expect(await bridgeIntent.getTimeRemaining(999)).to.equal(0);
    });

    it("Should return false for invalid intent isActive", async function () {
      expect(await bridgeIntent.isIntentActive(0)).to.be.false;
      expect(await bridgeIntent.isIntentActive(999)).to.be.false;
    });
  });

  describe("Additional Branch Coverage", function () {
    it("Should allow first bid without minimum increment check", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      // First bid can be any amount (no minimum increment check since currentHighest is 0)
      const bidAmount = ethers.parseUnits("1", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await expect(bridgeIntent.connect(solver1).placeBid(1, bidAmount)).to.not.be.reverted;
    });

    it("Should handle withdrawal when intent is Finalized but caller is not winner", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      const bid1 = ethers.parseUnits("96", 18);
      const bid2 = ethers.parseUnits("100", 18);
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(1, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver2).placeBid(1, bid2);
      
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(1);

      // solver1 is not the winner, can withdraw even after finalization
      await expect(bridgeIntent.connect(solver1).withdrawBid(1)).to.not.be.reverted;
    });

    it("Should handle withdrawal when intent is Completed", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      const bid1 = ethers.parseUnits("96", 18);
      const bid2 = ethers.parseUnits("100", 18);
      
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(1, bid1);
      
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver2).placeBid(1, bid2);
      
      await time.increase(AUCTION_DURATION + 1);
      await bridgeIntent.finalizeAuction(1);
      await bridgeIntent.connect(relayer).settleIntent(1);

      // solver1 can still withdraw their bid after settlement
      await expect(bridgeIntent.connect(solver1).withdrawBid(1)).to.not.be.reverted;
    });

    it("Should not extend auction if bid is not in extension threshold", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      const intentBefore = await bridgeIntent.getIntent(1);
      
      // Bid early (not within 2 min threshold)
      const bidAmount = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bidAmount);
      await bridgeIntent.connect(solver1).placeBid(1, bidAmount);
      
      const intentAfter = await bridgeIntent.getIntent(1);
      // End time should NOT have changed
      expect(intentAfter.endTime).to.equal(intentBefore.endTime);
    });

    it("Should allow bid increase without additional deposit if no extra needed", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      // This edge case would require additionalDeposit to be 0
      // but that's impossible with the current logic since _bidAmount > currentBid always
      // This test verifies the >= check on additionalDeposit
      const bid1 = ethers.parseUnits("96", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid1);
      await bridgeIntent.connect(solver1).placeBid(1, bid1);
      
      // Increase approval and bid
      const bid2 = ethers.parseUnits("100", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid2);
      await bridgeIntent.connect(solver1).placeBid(1, bid2);
      
      expect(await bridgeIntent.getSolverBid(1, await solver1.getAddress())).to.equal(bid2);
    });

    it("Should handle solveIntentOnChain2 with zero address recipient (skipped)", async function () {
      const amountB = ethers.parseUnits("50", 18);

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB);
      
      // Zero address recipient should be skipped
      await bridgeIntent.connect(solver1).solveIntentOnChain2(
        1, 1, await user.getAddress(),
        await tokenB.getAddress(), amountB,
        [ethers.ZeroAddress, await recipient1.getAddress()],
        [ethers.parseUnits("25", 18), ethers.parseUnits("25", 18)]
      );

      // Only recipient1 should receive tokens
      expect(await tokenB.balanceOf(await recipient1.getAddress())).to.equal(ethers.parseUnits("25", 18));
    });

    it("Should handle _removeFromActive when intent is not active", async function () {
      // This tests the early return in _removeFromActive
      // It's already covered implicitly but let's be explicit
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      // Cancel removes from active
      await bridgeIntent.connect(admin).cancelIntent(1);
      expect(await bridgeIntent.getActiveIntentCount()).to.equal(0);
      
      // Second cancel should not fail (but intent is already cancelled so it will revert for other reasons)
      await expect(bridgeIntent.connect(admin).cancelIntent(1))
        .to.be.revertedWithCustomError(bridgeIntent, "IntentAlreadyCompleted");
    });

    it("Should validate solveIntentOnChain2 with invalid sourceChainId", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          0, 1, await user.getAddress(),
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [await recipient1.getAddress()], [ethers.parseUnits("95", 18)]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should validate solveIntentOnChain2 with invalid intentId", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 0, await user.getAddress(),
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [await recipient1.getAddress()], [ethers.parseUnits("95", 18)]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidIntentId");
    });

    it("Should validate solveIntentOnChain2 with invalid user address", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, ethers.ZeroAddress,
          await tokenB.getAddress(), ethers.parseUnits("95", 18),
          [await recipient1.getAddress()], [ethers.parseUnits("95", 18)]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAddress");
    });

    it("Should validate solveIntentOnChain2 with invalid token address", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, await user.getAddress(),
          ethers.ZeroAddress, ethers.parseUnits("95", 18),
          [await recipient1.getAddress()], [ethers.parseUnits("95", 18)]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAddress");
    });

    it("Should validate solveIntentOnChain2 with zero expectedAmountB", async function () {
      await expect(
        bridgeIntent.connect(solver1).solveIntentOnChain2(
          1, 1, await user.getAddress(),
          await tokenB.getAddress(), 0,
          [await recipient1.getAddress()], [ethers.parseUnits("95", 18)]
        )
      ).to.be.revertedWithCustomError(bridgeIntent, "InvalidAmount");
    });

    it("Should reject bid that equals current highest", async function () {
      await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
      await bridgeIntent.connect(user).createIntent(
        await tokenA.getAddress(),
        await tokenB.getAddress(),
        AMOUNT_A,
        EXPECTED_AMOUNT_B,
        REWARD,
        DEST_CHAIN_ID,
        AUCTION_DURATION
      );

      const bid = ethers.parseUnits("100", 18);
      await tokenA.connect(solver1).approve(await bridgeIntent.getAddress(), bid);
      await bridgeIntent.connect(solver1).placeBid(1, bid);

      // Same bid should fail
      await tokenA.connect(solver2).approve(await bridgeIntent.getAddress(), bid);
      await expect(
        bridgeIntent.connect(solver2).placeBid(1, bid)
      ).to.be.revertedWithCustomError(bridgeIntent, "BidTooLow");
    });
  });

  describe("Edge Cases", function () {
    it("Should handle multiple intents correctly", async function () {
      // Create 3 intents
      for (let i = 0; i < 3; i++) {
        await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
        await bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        );
      }

      expect(await bridgeIntent.getActiveIntentCount()).to.equal(3);
      expect(await bridgeIntent.nextIntentId()).to.equal(4);
    });

    it("Should handle active intent removal correctly (swap and pop)", async function () {
      // Create 3 intents
      for (let i = 0; i < 3; i++) {
        await tokenA.connect(user).approve(await bridgeIntent.getAddress(), AMOUNT_A + REWARD);
        await bridgeIntent.connect(user).createIntent(
          await tokenA.getAddress(),
          await tokenB.getAddress(),
          AMOUNT_A,
          EXPECTED_AMOUNT_B,
          REWARD,
          DEST_CHAIN_ID,
          AUCTION_DURATION
        );
      }

      // Cancel the middle one (intent 2)
      await bridgeIntent.connect(admin).cancelIntent(2);

      expect(await bridgeIntent.getActiveIntentCount()).to.equal(2);
      
      // Verify remaining intents are accessible
      const activeIntents = await bridgeIntent.getActiveIntents(0, 10);
      expect(activeIntents.length).to.equal(2);
    });

    it("Should skip zero amounts in solveIntentOnChain2", async function () {
      const sourceChainId = 1;
      const intentId = 1;
      const amountB = ethers.parseUnits("50", 18);

      await tokenB.connect(solver1).approve(await bridgeIntent.getAddress(), amountB);
      
      // Include a zero amount - should be skipped
      await bridgeIntent.connect(solver1).solveIntentOnChain2(
        sourceChainId, intentId, await user.getAddress(),
        await tokenB.getAddress(), amountB,
        [await recipient1.getAddress(), await recipient2.getAddress()],
        [amountB, 0] // Second amount is 0
      );

      expect(await tokenB.balanceOf(await recipient1.getAddress())).to.equal(amountB);
      expect(await tokenB.balanceOf(await recipient2.getAddress())).to.equal(0);
    });
  });
});
