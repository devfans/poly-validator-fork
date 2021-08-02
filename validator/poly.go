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
	"strings"

	"github.com/beego/beego/v2/core/logs"
	ecom "github.com/ethereum/go-ethereum/common"
	"github.com/polynetwork/bridge-common/base"
	"github.com/polynetwork/bridge-common/chains/poly"
	"github.com/polynetwork/bridge-common/tools"
	pcom "github.com/polynetwork/poly/common"
	ccom "github.com/polynetwork/poly/native/service/cross_chain_manager/common"
	"github.com/polynetwork/poly/native/service/utils"
)

type PolyValidator struct {
	sdk *poly.SDK
}

func (v *PolyValidator) ScanEvents(height uint64, ch chan tools.CardEvent) (err error) {
	return
}

func (v *PolyValidator) LatestHeight() (uint64, error) {
	h, err := v.sdk.Node().GetCurrentBlockHeight()
	return uint64(h), err
}

func (v *PolyValidator) SetupSDK(poly *poly.SDK) {
	v.sdk = poly
}

func (v *PolyValidator) Setup(cfg *ChainConfig) (err error) {
	return
}

func (v *PolyValidator) Scan(height uint64) (txs []*DstTx, err error) {
	events, err := v.sdk.Node().GetSmartContractEventByBlock(uint32(height))
	if err != nil {
		return nil, err
	}

	c := 0
	for _, event := range events {
		for _, notify := range event.Notify {
			if notify.ContractAddress == PolyCCMContract {
				states := notify.States.([]interface{})
				contractMethod, _ := states[0].(string)
				if len(states) >= 4 && (contractMethod == "makeProof" || contractMethod == "btcTxToRelay") {
					c++
				}
			}
		}
	}
	txs = make([]*DstTx, c)
	return
}

func (v *PolyValidator) Validate(tx *DstTx) (err error) {
	return
}

func (v *PolyValidator) MerkleCheck(key string) error {
	k, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("Invalid key hex %s err %v", key, err)
	}

	raw, err := v.sdk.Node().GetStorage(utils.CrossChainManagerContractAddress.ToHexString(), k[20:])
	if err == nil && raw != nil {
		des := pcom.NewZeroCopySource(raw)
		merkleValue := new(ccom.ToMerkleValue)
		err = merkleValue.Deserialization(des)
		if err == nil {
			logs.Info("Found Merkle value: \n %+v", *merkleValue)
			if merkleValue.MakeTxParam != nil {
				logs.Info("Parsed merkle value param: \n%+v", *(merkleValue.MakeTxParam))
				srcTx := hex.EncodeToString(merkleValue.MakeTxParam.TxHash)
				method := merkleValue.MakeTxParam.Method
				des = pcom.NewZeroCopySource(merkleValue.MakeTxParam.Args)

				var asset, assetReversed, address string
				var value *big.Int
				if merkleValue.FromChainID == 5 { // Switcheo
					des.NextVarBytes()
					dstAsset, _ := des.NextVarBytes()
					asset = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(dstAsset).Hex()), "0x")
					assetReversed = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(pcom.ToArrayReverse(dstAsset)).Hex()), "0x")
					to, _ := des.NextVarBytes()
					address = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(to).Hex()), "0x")
					amount, _ := des.NextBytes(32)
					value = new(big.Int).SetBytes(pcom.ToArrayReverse(amount))
				} else {
					dstAsset, _ := des.NextVarBytes()
					asset = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(dstAsset).Hex()), "0x")
					assetReversed = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(pcom.ToArrayReverse(dstAsset)).Hex()), "0x")
					to, _ := des.NextVarBytes()
					address = strings.TrimPrefix(strings.ToLower(ecom.BytesToAddress(to).Hex()), "0x")
					amount, _ := des.NextBytes(32)
					value = new(big.Int).SetBytes(pcom.ToArrayReverse(amount))
				}
				data := map[string]interface{}{
					"asset":         asset,
					"assetReversed": assetReversed,
					"to":            address,
					"amount":        value.String(),
					"srcTx":         srcTx,
					"method":        method,
				}
				logs.Info("Value:\n %+v", data)
			}
		}
	}
	return nil
}

func (v *PolyValidator) ScanProofs(height uint64, ccmContract string) (err error) {
	PolyCCMContract = ccmContract

	events, err := v.sdk.Node().GetSmartContractEventByBlock(uint32(height))
	if err != nil {
		return err
	}
	for _, event := range events {
		for _, notify := range event.Notify {
			if notify.ContractAddress == PolyCCMContract {
				states := notify.States.([]interface{})
				contractMethod, _ := states[0].(string)
				if len(states) > 4 && (contractMethod == "makeProof" || contractMethod == "btcTxToRelay") {
					srcChain := uint64(states[1].(float64))
					var srcIndex string
					switch srcChain {
					case base.ETH, base.BSC, base.O3, base.OK, base.HECO:
						srcIndex = states[3].(string)
					default:
						srcIndex = HexStringReverse(states[3].(string))
					}
					key := states[5].(string)
					logs.Info("Tx hash: %s, index %s, chain %v, %s", event.TxHash, srcIndex, srcChain, key)
					err = v.MerkleCheck(key)
					if err != nil {
						logs.Error("polyMerkleCheck err %v", err)
					}
				}
			}
		}
	}
	return
}
