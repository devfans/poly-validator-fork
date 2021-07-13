package validator

import (
	"context"
	"poly-bridge/chainsdk"
	"poly-bridge/go_abi/eccm_abi"
	"poly-bridge/go_abi/lock_proxy_abi"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

const (
	_eth_crosschainlock   = "CrossChainLockEvent"
	_eth_crosschainunlock = "CrossChainUnlockEvent"
	_eth_lock             = "LockEvent"
	_eth_unlock           = "UnlockEvent"
)

type EthValidator struct {
	sdk   *chainsdk.EthereumSdkPro
	conf  *ChainConfig
	proxy []*lock_proxy_abi.LockProxy
	ccm   *eccm_abi.EthCrossChainManager
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

func (v *EthValidator) Scan(height int64) (txs []*DstTx, err error) {
	h := uint64(height)
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
	tx := []*DstTx{}
	for ccmUnlocks.Next() {
		evt := ccmUnlocks.Event
		// unlocks[evt]
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
		}
	}

	return
}

func (v *EthValidator) Validate(tx *DstTx) (err error) {
	return
}
