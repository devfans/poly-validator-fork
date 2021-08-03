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
	"fmt"
	"time"

	"github.com/polynetwork/bridge-common/chains/switcheo"
	"github.com/polynetwork/bridge-common/tools"
)

type SwitcheoValidator struct {
	sdk  *switcheo.SDK
	conf *ChainConfig
}

func (v *SwitcheoValidator) ScanEvents(height uint64, ch chan tools.CardEvent) (err error) {
	return
}

func (v *SwitcheoValidator) LatestHeight() (uint64, error) {
	return v.sdk.Node().GetLatestHeight()
}

func (v *SwitcheoValidator) Setup(cfg *ChainConfig) (err error) {
	v.conf = cfg
	v.sdk, err = switcheo.NewSDK(cfg.ChainId, cfg.Nodes, time.Minute, 1)
	if err != nil {
		return
	}
	return
}

func (v *SwitcheoValidator) Scan(height uint64) (txs []*DstTx, err error) {
	query := fmt.Sprintf("tx.height=%d", height)
	page := 1
	size := 100
	c := 0
	for {
		res, err := v.sdk.Node().TxSearch(query, false, page, size, "asc")
		if err != nil {
			return nil, err
		}

		for _, tx := range res.Txs {
			for _, e := range tx.TxResult.Events {
				if e.Type == "verify_to_cosmos_proof" {
					c++
				}
			}
		}

		if res.TotalCount <= page*size {
			break
		}
		page++
	}
	txs = make([]*DstTx, c)
	return
}

func (v *SwitcheoValidator) Validate(tx *DstTx) (err error) {
	return
}
