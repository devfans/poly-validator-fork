package validator

import (
	"encoding/hex"
	"math/big"

	"poly-bridge/basedef"
	"poly-bridge/chainsdk"

	"github.com/beego/beego/v2/core/logs"
)

var neoProxyUnlocks = map[string]bool{
	"Unlock":      true,
	"UnlockEvent": true,
}

const NEO_CCM_UNLOCK = "CrossChainUnlockEvent"

type NeoValidator struct {
	sdk  *chainsdk.NeoSdkPro
	conf *ChainConfig
}

func (v *NeoValidator) LatestHeight() (uint64, error) {
	return v.sdk.GetBlockCount()
}

func (v *NeoValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk = chainsdk.NewNeoSdkPro(cfg.Nodes, 10, cfg.ChainId)
	return
}

func (v *NeoValidator) isProxyContract(contract string) bool {
	for _, addr := range v.conf.ProxyContracts {
		if addr == contract {
			return true
		}
	}
	return false
}

func (v *NeoValidator) Scan(height uint64) (txs []*DstTx, err error) {
	block, err := v.sdk.GetBlockByIndex(height)
	if err != nil {
		return nil, err
	}
	for _, tx := range block.Tx {
		if tx.Type != "InvocationTransaction" {
			continue
		}
		appLog, err := v.sdk.GetApplicationLog(tx.Txid)
		if err != nil || appLog == nil {
			continue
		}
		var ccmUnlock *DstTx
		unlocks := []*DstTx{}
		for _, exeitem := range appLog.Executions {
			for _, notify := range exeitem.Notifications {
				if notify.Contract[2:] == v.conf.CCMContract {
					method, _ := hex.DecodeString(notify.State.Value[0].Value)
					if string(method) == NEO_CCM_UNLOCK {
						var dstChain uint64
						chainId := ParseInt(notify.State.Value[1].Value, notify.State.Value[1].Type)
						if chainId == nil {
							logs.Error("Invalid source chain id %v", notify.State.Value[1].Value)
						} else {
							dstChain = chainId.Uint64()
						}

						evt := &DstTx{
							SrcChainId: dstChain,
							PolyTx:     basedef.HexStringReverse(notify.State.Value[3].Value),
							DstHeight:  height,
						}
						if ccmUnlock == nil {
							ccmUnlock = evt
						} else {
							logs.Error("Found more than one ccm unlock event %v", *evt)
						}
					}
				} else if v.isProxyContract(notify.Contract[2:]) {
					method, _ := hex.DecodeString(notify.State.Value[0].Value)
					_, ok := neoProxyUnlocks[string(method)]
					if ok {
						amount := ParseInt(notify.State.Value[3].Value, notify.State.Value[3].Type)
						if amount == nil {
							logs.Error("Invalid dst unlock amount %v", notify.State.Value[3].Value)
							amount = big.NewInt(0)
						}
						unlocks = append(unlocks, &DstTx{
							Amount:     amount,
							DstTx:      tx.Txid[2:],
							DstAsset:   basedef.HexStringReverse(notify.State.Value[1].Value),
							To:         notify.State.Value[2].Value,
							DstChainId: v.conf.ChainId,
						})
					}
				}
			}
		}

		if len(unlocks) != 1 {
			// If more than one unlock in one tx, alarm it
			ccmUnlock = nil
		}
		for _, evt := range unlocks {
			if ccmUnlock != nil {
				evt.SrcChainId = ccmUnlock.SrcChainId
				evt.PolyTx = ccmUnlock.PolyTx
				evt.DstHeight = height
			}
			txs = append(txs, evt)
		}
	}
	return
}

func (v *NeoValidator) Validate(tx *DstTx) (err error) {
	return
}
