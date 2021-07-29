/*
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The  poly network  is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The  poly network  is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 * You should have received a copy of the GNU Lesser General Public License
 * along with The poly network .  If not, see <http://www.gnu.org/licenses/>.
 */

package validator

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/beego/beego/v2/core/logs"
	"github.com/joeqian10/neo-gogogo/rpc/models"
	"github.com/polynetwork/bridge-common/chains/neo"
	"github.com/polynetwork/bridge-common/tools"
)

var neoProxyUnlocks = map[string]bool{
	"Unlock":      true,
	"UnlockEvent": true,
}

const NEO_CCM_UNLOCK = "CrossChainUnlockEvent"

type NeoValidator struct {
	sdk   *neo.SDK
	conf  *ChainConfig
	cache []*DstTx // temp block cache
}

func (v *NeoValidator) LatestHeight() (uint64, error) {
	return v.sdk.Node().GetLatestHeight()
}

func (v *NeoValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk, err = neo.NewSDK(cfg.ChainId, cfg.Nodes, time.Minute, 1)
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

func (v *NeoValidator) GetBlockByIndex(height uint64) (*models.RpcBlock, error) {
	res := v.sdk.Node().GetBlockByIndex(uint32(height))
	if res.ErrorResponse.Error.Message != "" {
		return nil, fmt.Errorf("%s", res.ErrorResponse.Error.Message)
	}
	return &res.Result, nil
}

func (v *NeoValidator) GetApplicationLog(txId string) (*models.RpcApplicationLog, error) {
	res := v.sdk.Node().GetApplicationLog(txId)
	if res.ErrorResponse.Error.Message != "" {
		return nil, fmt.Errorf("%s", res.ErrorResponse.Error.Message)
	}
	return &res.Result, nil
}

func (v *NeoValidator) Scan(height uint64) (txs []*DstTx, err error) {
	return v.cache, nil
}

func (v *NeoValidator) ScanEvents(height uint64, ch chan tools.CardEvent) (err error) {
	v.cache = nil

	block, err := v.GetBlockByIndex(height)
	if err != nil {
		return err
	}

	txs := []*DstTx{}
	events := []tools.CardEvent{}
	for _, tx := range block.Tx {
		if tx.Type != "InvocationTransaction" {
			continue
		}
		appLog, err := v.GetApplicationLog(tx.Txid)
		if err != nil {
			return err
		}

		if appLog == nil {
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
							PolyTx:     HexStringReverse(notify.State.Value[3].Value),
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
							DstAsset:   HexStringReverse(notify.State.Value[1].Value),
							To:         notify.State.Value[2].Value,
							DstChainId: v.conf.ChainId,
						})
					} else {
						var ev tools.CardEvent
						switch string(method) {
						case "TransferOwnershipEvent":
							ev = &SetManagerProxyEvent{
								TxHash:   tx.Txid[2:],
								Contract: notify.Contract[2:],
								ChainId:  v.conf.ChainId,
								Manager:  notify.State.Value[1].Value,
								Operator: notify.State.Value[0].Value,
							}
						case "BindProxyHashEvent":
							var toChainId uint64
							chainId := ParseInt(notify.State.Value[0].Value, notify.State.Value[0].Type)
							if chainId == nil {
								logs.Error("Invalid to chain id %v", notify.State.Value[0].Value)
							} else {
								toChainId = chainId.Uint64()
							}

							ev = &BindProxyEvent{
								TxHash:    tx.Txid[2:],
								Contract:  notify.Contract[2:],
								ChainId:   v.conf.ChainId,
								ToChainId: toChainId,
								ToProxy:   notify.State.Value[1].Value,
							}
						case "BindAssetHashEvent":
							var toChainId uint64
							chainId := ParseInt(notify.State.Value[1].Value, notify.State.Value[1].Type)
							if chainId == nil {
								logs.Error("Invalid to chain id %v", notify.State.Value[1].Value)
							} else {
								toChainId = chainId.Uint64()
							}

							ev = &BindAssetEvent{
								TxHash:    tx.Txid[2:],
								Contract:  notify.Contract[2:],
								ChainId:   v.conf.ChainId,
								FromAsset: notify.State.Value[0].Value,
								ToChainId: toChainId,
								Asset:     notify.State.Value[2].Value,
							}
						}
						if ev != nil {
							events = append(events, ev)
						}
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
	// Cache for scan call
	v.cache = txs
	for _, ev := range events {
		ch <- ev
	}
	return
}

func (v *NeoValidator) Validate(tx *DstTx) (err error) {
	return
}
