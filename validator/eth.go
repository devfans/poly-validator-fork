package validator

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"poly-bridge/basedef"
	"poly-bridge/chainsdk"
	"poly-bridge/go_abi/eccm_abi"
	"poly-bridge/go_abi/lock_proxy_abi"

	"github.com/beego/beego/v2/core/logs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

type EthValidator struct {
	sdk   *chainsdk.EthereumSdkPro
	conf  *ChainConfig
	proxy []*lock_proxy_abi.LockProxy
	ccm   *eccm_abi.EthCrossChainManager
}

func (v *EthValidator) LatestHeight() (uint64, error) {
	return v.sdk.GetLatestHeight()
}

func (v *EthValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk = chainsdk.NewEthereumSdkPro(cfg.Nodes, 5, cfg.ChainId)
	for _, address := range v.conf.ProxyContracts {
		contract, err := lock_proxy_abi.NewLockProxy(common.HexToAddress(address), v.sdk.GetClient())
		if err != nil {
			return err
		}
		v.proxy = append(v.proxy, contract)
	}
	v.ccm, err = eccm_abi.NewEthCrossChainManager(common.HexToAddress(v.conf.CCMContract), v.sdk.GetClient())
	return
}

func (v *EthValidator) Scan(height uint64) (txs []*DstTx, err error) {
	h := height
	opt := &bind.FilterOpts{
		Start:   h,
		End:     &h,
		Context: context.Background(),
	}
	ccmUnlocks, err := v.ccm.FilterVerifyHeaderAndExecuteTxEvent(opt)
	if err != nil {
		return nil, err
	}

	unlocks := map[string]DstTx{}
	txs = []*DstTx{}
	for ccmUnlocks.Next() {
		evt := ccmUnlocks.Event
		hash := evt.Raw.TxHash.String()[2:]
		unlocks[hash] = DstTx{
			SrcChainId: evt.FromChainID,
			SrcTx:      basedef.HexStringReverse(hex.EncodeToString(evt.FromChainTxHash)),
			PolyTx:     basedef.HexStringReverse(hex.EncodeToString(evt.CrossChainTxHash)),
			DstHeight:  evt.Raw.BlockNumber,
		}
	}

	for _, p := range v.proxy {
		unlockEvents, err := p.FilterUnlockEvent(opt)
		if err != nil {
			return nil, err
		}
		for unlockEvents.Next() {
			evt := unlockEvents.Event
			tx := &DstTx{
				Amount:   evt.Amount,
				DstTx:    evt.Raw.TxHash.String()[2:],
				DstAsset: strings.ToLower(evt.ToAssetHash.String()[2:]),
				To:       strings.ToLower(evt.ToAddress.String()[2:]),
			}
			ccmTx, ok := unlocks[tx.DstTx]
			if ok {
				tx.SrcChainId = ccmTx.SrcChainId
				tx.SrcTx = ccmTx.SrcTx
				tx.PolyTx = ccmTx.PolyTx
				tx.DstHeight = ccmTx.DstHeight
			}
			txs = append(txs, tx)
		}
	}

	return
}

func (v *EthValidator) Validate(tx *DstTx) (err error) {
	data, err := v.sdk.GetTransactionReceipt(common.HexToHash(tx.SrcTx))
	if err != nil {
		return err
	}
	height := uint64(data.BlockNumber.Int64())
	opt := &bind.FilterOpts{
		Start:   height,
		End:     &height,
		Context: context.Background(),
	}

	for _, p := range v.proxy {
		locks, err := p.FilterLockEvent(opt)
		if err != nil {
			return err
		}
		for locks.Next() {
			evt := locks.Event
			amount := evt.Amount
			address := string(evt.ToAddress)
			chainId := evt.ToChainId
			asset := string(evt.ToAssetHash)

			logs.Info("Comparing %v %v %v %v %v", *tx, amount, address, chainId, asset)
			if amount.Cmp(tx.Amount) == 0 && address == tx.To && chainId == tx.DstChainId && asset == tx.DstAsset {
				logs.Info("Successfully validated tx %s to %s asset %v amount %s", tx.SrcTx, address, asset, amount.String())
				return nil
			}
		}
	}
	err = fmt.Errorf("Failed to validate tx %s to %s asset %v amount %s", tx.SrcTx, tx.To, tx.DstAsset, tx.Amount.String())
	return
}
