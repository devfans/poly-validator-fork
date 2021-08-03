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
	"context"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	eccm "github.com/polynetwork/bridge-common/abi/eccm_abi"
	"github.com/polynetwork/bridge-common/abi/swapper_abi"
	"github.com/polynetwork/bridge-common/chains/eth"
	"github.com/polynetwork/bridge-common/tools"
)

type O3Validator struct {
	EthValidator
	proxy []*swapper_abi.SwapProxy
}

func (v *O3Validator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk, err = eth.NewSDK(cfg.ChainId, cfg.Nodes, time.Minute, 1)
	if err != nil {
		return
	}

	for _, address := range v.conf.ProxyContracts {
		contract, err := swapper_abi.NewSwapProxy(common.HexToAddress(address), v.sdk.Node().Client)
		if err != nil {
			return err
		}
		v.proxy = append(v.proxy, contract)
	}
	v.ccm, err = eccm.NewEthCrossChainManager(common.HexToAddress(v.conf.CCMContract), v.sdk.Node().Client)
	return
}

func (v *O3Validator) ScanEvents(height uint64, ch chan tools.CardEvent) (err error) {
	return
}

func (v *O3Validator) Scan(height uint64) (txs []*DstTx, err error) {
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

	c := 0
	for ccmUnlocks.Next() {
		c++
	}
	txs = make([]*DstTx, c)
	return
}
