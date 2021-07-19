package validator

import (
	"encoding/hex"
	"math/big"

	"poly-bridge/basedef"
	"poly-bridge/chainsdk"

	"github.com/beego/beego/v2/core/logs"
)

const (
	ONT_PROXY_UNLOCK = "unlock"
	ONT_CCM_UNLOCK   = "verifyToOntProof"
)

type OntValidator struct {
	sdk  *chainsdk.OntologySdkPro
	conf *ChainConfig
}

func (v *OntValidator) LatestHeight() (uint64, error) {
	return v.sdk.GetCurrentBlockHeight()
}

func (v *OntValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk = chainsdk.NewOntologySdkPro(cfg.Nodes, 10, cfg.ChainId)
	return
}

func (v *OntValidator) isProxyContract(contract string) bool {
	for _, addr := range v.conf.ProxyContracts {
		if addr == contract {
			return true
		}
	}
	return false
}

func (v *OntValidator) Scan(height uint64) (txs []*DstTx, err error) {
	events, err := v.sdk.GetSmartContractEventByBlock(uint32(height))
	if err != nil {
		return nil, err
	}
	for _, evt := range events {
		var ccmUnlock *DstTx
		unlocks := []*DstTx{}
		for _, notify := range evt.Notify {
			if notify.ContractAddress == v.conf.CCMContract {
				states := notify.States.([]interface{})
				method, _ := states[0].(string)
				if method == ONT_CCM_UNLOCK {
					evt := &DstTx{
						SrcChainId: uint64(states[3].(float64)),
						PolyTx:     basedef.HexStringReverse(states[1].(string)),
						DstHeight:  height,
					}
					if ccmUnlock == nil {
						ccmUnlock = evt
					} else {
						logs.Error("Found more than one ccm unlock event %v", *evt)
					}
				}
			} else if v.isProxyContract(notify.ContractAddress) {
				states := notify.States.([]interface{})
				m, _ := states[0].(string)
				method, _ := hex.DecodeString(m)
				if string(method) == ONT_PROXY_UNLOCK {
					amount, _ := new(big.Int).SetString(basedef.HexStringReverse(states[3].(string)), 16)
					if amount == nil {
						logs.Error("Invalid dst unlock amount %v", states[3])
						amount = big.NewInt(0)
					}

					unlocks = append(unlocks, &DstTx{
						Amount:     amount,
						DstTx:      evt.TxHash,
						DstAsset:   states[1].(string),
						To:         states[2].(string),
						DstChainId: v.conf.ChainId,
					})
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

func (v *OntValidator) Validate(tx *DstTx) (err error) {
	return
}
