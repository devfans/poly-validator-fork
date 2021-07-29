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
	"math/big"
	"time"

	"github.com/beego/beego/v2/core/logs"
	"github.com/polynetwork/bridge-common/chains/ont"
	"github.com/polynetwork/bridge-common/tools"
)

const (
	ONT_PROXY_UNLOCK = "unlock"
	ONT_CCM_UNLOCK   = "verifyToOntProof"
)

type OntValidator struct {
	sdk   *ont.SDK
	conf  *ChainConfig
	cache []*DstTx // temp block cache
}

func (v *OntValidator) LatestHeight() (uint64, error) {
	return v.sdk.Node().GetLatestHeight()
}

func (v *OntValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk, err = ont.NewSDK(cfg.ChainId, cfg.Nodes, time.Minute, 1)
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
	return v.cache, nil
}

func (v *OntValidator) ScanEvents(height uint64, ch chan tools.CardEvent) (err error) {
	v.cache = nil

	smEvents, err := v.sdk.Node().GetSmartContractEventByBlock(uint32(height))
	if err != nil {
		return err
	}

	txs := []*DstTx{}
	events := []tools.CardEvent{}

	for _, evt := range smEvents {
		var ccmUnlock *DstTx
		unlocks := []*DstTx{}
		for _, notify := range evt.Notify {
			if notify.ContractAddress == v.conf.CCMContract {
				states := notify.States.([]interface{})
				method, _ := states[0].(string)
				if method == ONT_CCM_UNLOCK {
					evt := &DstTx{
						SrcChainId: uint64(states[3].(float64)),
						PolyTx:     HexStringReverse(states[1].(string)),
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
					amount, _ := new(big.Int).SetString(HexStringReverse(states[3].(string)), 16)
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
				} else {
					var ev tools.CardEvent
					switch string(method) {
					case "TransferOwnershipEvent":
						ev = &SetManagerProxyEvent{
							TxHash:   evt.TxHash,
							Contract: notify.ContractAddress,
							ChainId:  v.conf.ChainId,
							/*
								Manager:  notify.State.Value[1].Value,
								Operator: notify.State.Value[0].Value,
							*/
						}
					case "BindProxyHashEvent":
						ev = &BindProxyEvent{
							TxHash:   evt.TxHash,
							Contract: notify.ContractAddress,
							ChainId:  v.conf.ChainId,
							/*
								ToChainId: toChainId,
								ToProxy:   notify.State.Value[1].Value,
							*/
						}
					case "BindAssetHashEvent":
						ev = &BindAssetEvent{
							TxHash:   evt.TxHash,
							Contract: notify.ContractAddress,
							ChainId:  v.conf.ChainId,
							/*
								FromAsset: notify.State.Value[0].Value,
								ToChainId: toChainId,
								Asset:     notify.State.Value[2].Value,
							*/
						}
					}
					if ev != nil {
						events = append(events, ev)
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

func (v *OntValidator) Validate(tx *DstTx) (err error) {
	return
}
