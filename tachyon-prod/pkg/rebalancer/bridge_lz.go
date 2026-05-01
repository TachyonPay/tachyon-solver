package rebalancer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	log "github.com/sirupsen/logrus"
)

// arrivalPollInterval is how often we re-check the destination balance.
const arrivalPollInterval = 5 * time.Second

// arrivalTimeoutDefault is the default deadline for cross-chain delivery.
// LayerZero v2 typically delivers in 1–3 minutes; we cushion to 10 min.
const arrivalTimeoutDefault = 10 * time.Minute

// waitForBridgeArrival blocks until holder's balance of dstToken on dstChainID
// rises by at least minIncrease (compared to the pre-bridge snapshot), or the
// timeout fires. Returns the actual amount received.
func waitForBridgeArrival(
	ctx context.Context,
	s Signer,
	dstChainID string,
	dstToken, holder common.Address,
	preBalance *big.Int,
	minIncrease *big.Int,
	timeout time.Duration,
) (*big.Int, error) {
	if timeout <= 0 {
		timeout = arrivalTimeoutDefault
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	target := new(big.Int).Add(preBalance, minIncrease)
	tick := time.NewTicker(arrivalPollInterval)
	defer tick.Stop()

	start := time.Now()
	for {
		select {
		case <-deadline.Done():
			cur, _ := erc20BalanceOf(ctx, s, dstChainID, dstToken, holder)
			received := new(big.Int).Sub(cur, preBalance)
			return received, fmt.Errorf("timeout after %s waiting for bridge arrival on %s (pre=%s current=%s minExpected=%s)",
				time.Since(start).Round(time.Second), dstChainID,
				preBalance.String(), cur.String(), minIncrease.String())
		case <-tick.C:
			cur, err := erc20BalanceOf(ctx, s, dstChainID, dstToken, holder)
			if err != nil {
				log.WithError(err).WithField("chain", dstChainID).Warn("Bridge arrival poll RPC error, retrying")
				continue
			}
			if cur.Cmp(target) >= 0 {
				received := new(big.Int).Sub(cur, preBalance)
				log.WithFields(log.Fields{
					"chain":    dstChainID,
					"holder":   holder.Hex(),
					"token":    dstToken.Hex(),
					"received": received.String(),
					"elapsed":  time.Since(start).Round(time.Second).String(),
				}).Info("Bridge arrival confirmed on destination")
				return received, nil
			}
		}
	}
}

// SendParam mirrors the LayerZero v2 OFT.SendParam struct.
type SendParam struct {
	DstEid       uint32
	To           [32]byte
	AmountLD     *big.Int
	MinAmountLD  *big.Int
	ExtraOptions []byte
	ComposeMsg   []byte
	OftCmd       []byte
}

// MessagingFee mirrors LayerZero v2's fee tuple.
type MessagingFee struct {
	NativeFee  *big.Int
	LzTokenFee *big.Int
}

// lzExtraOptionsLzReceive returns the EXECUTOR_LZRECEIVE option blob requesting
// `gas` units on the destination side. Format: 0x00 03 01 00 11 01 <16-byte LE gas>.
//
// Reference: LayerZero v2 OptionsBuilder.newOptions().addExecutorLzReceiveOption(gas, value=0)
func lzExtraOptionsLzReceive(gas uint64) []byte {
	// Type 3 options header
	hdr := []byte{0x00, 0x03}
	// option: workerId=01, optionLen=0x0011 (17 bytes), optionType=01 (LZ_RECEIVE)
	opt := []byte{0x01, 0x00, 0x11, 0x01}
	// 16-byte big-endian uint128 gas; value field omitted (default 0)
	gasBytes := make([]byte, 16)
	new(big.Int).SetUint64(gas).FillBytes(gasBytes)
	return append(append(hdr, opt...), gasBytes...)
}

// addressTo32 left-pads a 20-byte address into a bytes32 for SendParam.to.
func addressTo32(a common.Address) [32]byte {
	var out [32]byte
	copy(out[12:], a.Bytes())
	return out
}

// lzQuoteSend reads the native fee for a given (oft, sendParam) pair.
func lzQuoteSend(ctx context.Context, s Signer, chainID string, oft common.Address, sp SendParam) (MessagingFee, error) {
	data, err := stargateOFTABI.Pack("quoteSend", sp, false)
	if err != nil {
		return MessagingFee{}, err
	}
	out, err := s.CallReadOnly(ctx, chainID, oft, data)
	if err != nil {
		return MessagingFee{}, fmt.Errorf("quoteSend call: %w", err)
	}
	res, err := stargateOFTABI.Unpack("quoteSend", out)
	if err != nil {
		return MessagingFee{}, err
	}
	if len(res) == 0 {
		return MessagingFee{}, fmt.Errorf("empty quoteSend result")
	}
	// `res[0]` is an anonymous struct{ NativeFee, LzTokenFee *big.Int }.
	feeStruct, ok := res[0].(struct {
		NativeFee  *big.Int `json:"nativeFee"`
		LzTokenFee *big.Int `json:"lzTokenFee"`
	})
	if !ok {
		return MessagingFee{}, fmt.Errorf("unexpected quoteSend return %T", res[0])
	}
	return MessagingFee{NativeFee: feeStruct.NativeFee, LzTokenFee: feeStruct.LzTokenFee}, nil
}

// lzSend signs and submits an OFT/Stargate send AND waits for the funds to
// arrive on the destination chain. Returns the source-chain tx hash.
//
//   - `srcChainID` / `oft`: where we sign and call send().
//   - `tokenForApproval`: underlying ERC20 to approve to `oft` before sending.
//     Pass common.Address{} if the OFT does not require approval (e.g. the
//     Horizen ZEN NativeOFT — burns directly from msg.sender's OFT balance).
//   - `dstChainID` / `dstToken`: destination chain and the token whose balance
//     should grow at `to` after delivery. Used to poll for arrival so chained
//     actions don't fire before the bridged funds land.
//   - `dstEid` / `to`: LZ destination endpoint and recipient.
//   - Slippage cap: minAmountLD = amount * (1 - slippagePct).
//   - LZ fee is overpaid by `gasBufferWei` to absorb base-fee jitter.
//
// Cross-chain delivery typically takes 1–3 min; we wait up to 10 min before
// failing.
func lzSend(
	ctx context.Context,
	s Signer,
	srcChainID string,
	oft common.Address,
	tokenForApproval common.Address,
	dstChainID string,
	dstToken common.Address,
	dstEid uint32,
	to common.Address,
	amount *big.Int,
	slippagePct float64,
	gasBufferWei *big.Int,
) (string, error) {
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("amount must be positive")
	}

	minOut := pctOf(amount, 1.0-slippagePct)
	if minOut.Sign() <= 0 {
		minOut = big.NewInt(1)
	}

	sp := SendParam{
		DstEid:       dstEid,
		To:           addressTo32(to),
		AmountLD:     amount,
		MinAmountLD:  minOut,
		ExtraOptions: lzExtraOptionsLzReceive(200_000),
		ComposeMsg:   []byte{},
		OftCmd:       []byte{},
	}

	fee, err := lzQuoteSend(ctx, s, srcChainID, oft, sp)
	if err != nil {
		return "", err
	}
	// Stargate V2 reverts with Stargate_InvalidAmount() unless msg.value
	// matches fee.NativeFee EXACTLY. Vanilla LayerZero OFTs typically refund
	// overpayment, but to keep behavior consistent we always pass exact.
	// The gasBufferWei param is kept in the signature for forward-compat but
	// is intentionally unused.
	_ = gasBufferWei
	value := new(big.Int).Set(fee.NativeFee)

	// Snapshot the destination balance BEFORE we submit anything, so the
	// arrival poll has an accurate baseline.
	preBalance, err := erc20BalanceOf(ctx, s, dstChainID, dstToken, to)
	if err != nil {
		return "", fmt.Errorf("read destination pre-balance: %w", err)
	}

	// Approve the OFT to pull the underlying ERC20 if required.
	if tokenForApproval != (common.Address{}) {
		if hash, err := ensureAllowance(ctx, s, srcChainID, tokenForApproval, oft, amount); err != nil {
			return "", fmt.Errorf("approve: %w", err)
		} else if hash != "" {
			log.WithFields(log.Fields{
				"chain":   srcChainID,
				"token":   tokenForApproval.Hex(),
				"spender": oft.Hex(),
				"txHash":  hash,
			}).Info("Approval submitted before LZ send")
		}
	}

	// Pack send(SendParam, MessagingFee, refundAddress) and submit. Refund
	// goes to the source-chain address (per-chain Safe in Safe mode, EOA
	// otherwise) so any unused native fee returns home on the chain we paid
	// from.
	refund := s.AddressOnChain(srcChainID)
	data, err := stargateOFTABI.Pack("send", sp, fee, refund)
	if err != nil {
		return "", fmt.Errorf("pack send: %w", err)
	}
	tx, _, err := s.SendAndWait(ctx, srcChainID, oft, value, data, 600_000, 0)
	if err != nil {
		return "", err
	}

	log.WithFields(log.Fields{
		"chain":      srcChainID,
		"oft":        oft.Hex(),
		"dstChainID": dstChainID,
		"dstEid":     dstEid,
		"dstToken":   dstToken.Hex(),
		"to":         to.Hex(),
		"amount":     amount.String(),
		"minOut":     minOut.String(),
		"nativeFee":  fee.NativeFee.String(),
		"srcTxHash":  tx.Hash().Hex(),
	}).Info("LZ send mined on source — waiting for destination arrival")

	// Block until the bridged funds show up on the destination side.
	received, err := waitForBridgeArrival(ctx, s, dstChainID, dstToken, to, preBalance, minOut, arrivalTimeoutDefault)
	if err != nil {
		return tx.Hash().Hex(), fmt.Errorf("source tx %s mined but destination arrival failed: %w", tx.Hash().Hex(), err)
	}
	log.WithFields(log.Fields{
		"srcTxHash": tx.Hash().Hex(),
		"received":  received.String(),
	}).Info("Bridge complete")

	return tx.Hash().Hex(), nil
}

// stargateUSDCBridge bridges USDC ↔ USDC.e between Base and Horizen using the
// Stargate USDC pool on the source chain. Waits for the funds to actually
// land on the destination before returning.
func stargateUSDCBridge(ctx context.Context, s Signer, srcChainID, dstChainID string, amount *big.Int, slippagePct float64, gasBufferWei *big.Int) (string, error) {
	var oft, tokenIn, dstToken common.Address
	var dstEid uint32

	switch {
	case srcChainID == ChainIDBase && dstChainID == ChainIDHorizen:
		oft = BaseStargatePoolUSDC
		tokenIn = BaseUSDC
		dstToken = HzUSDCe
		dstEid = LzEidHorizen
	case srcChainID == ChainIDHorizen && dstChainID == ChainIDBase:
		oft = HzStargateOFTUSDC
		tokenIn = HzUSDCe
		dstToken = BaseUSDC
		dstEid = LzEidBase
	default:
		return "", fmt.Errorf("unsupported Stargate USDC route %s → %s", srcChainID, dstChainID)
	}

	return lzSend(ctx, s, srcChainID, oft, tokenIn, dstChainID, dstToken, dstEid, s.AddressOnChain(dstChainID), amount, slippagePct, gasBufferWei)
}

// oftZENBridge bridges ZEN between Base and Horizen using the LayerZero OFT.
// Waits for arrival on the destination chain before returning.
func oftZENBridge(ctx context.Context, s Signer, srcChainID, dstChainID string, amount *big.Int, slippagePct float64, gasBufferWei *big.Int) (string, error) {
	var oft, tokenIn, dstToken common.Address
	var dstEid uint32

	switch {
	case srcChainID == ChainIDBase && dstChainID == ChainIDHorizen:
		oft = BaseZENOFTAdapter
		tokenIn = BaseZEN          // OFTAdapter wraps the ERC20, approval required
		dstToken = HzZENOFT        // NativeOFT IS the ZEN token on Horizen
		dstEid = LzEidHorizen
	case srcChainID == ChainIDHorizen && dstChainID == ChainIDBase:
		oft = HzZENOFT
		tokenIn = common.Address{} // NativeOFT, no approval needed
		dstToken = BaseZEN         // underlying ZEN ERC20 on Base
		dstEid = LzEidBase
	default:
		return "", fmt.Errorf("unsupported ZEN OFT route %s → %s", srcChainID, dstChainID)
	}

	return lzSend(ctx, s, srcChainID, oft, tokenIn, dstChainID, dstToken, dstEid, s.AddressOnChain(dstChainID), amount, slippagePct, gasBufferWei)
}
