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
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	ecom "github.com/ethereum/go-ethereum/common"
	"github.com/polynetwork/bridge-common/base"
	"github.com/polynetwork/bridge-common/chains/poly"
	"github.com/polynetwork/bridge-common/metrics"
	"github.com/polynetwork/bridge-common/tools"
	"github.com/polynetwork/poly-go-sdk/common"
	pcom "github.com/polynetwork/poly/common"
	"github.com/polynetwork/poly/core/types"
	ccom "github.com/polynetwork/poly/native/service/cross_chain_manager/common"
	"github.com/polynetwork/poly/native/service/utils"

	"github.com/beego/beego/v2/core/logs"
	"github.com/boltdb/bolt"
)

var (
	BUCKET      = []byte("poly-validator")
	BLOCK_DEFER = uint64(5)

	PolyCCMContract string
	DingUrl         string
)

type ChainConfig struct {
	ChainId        uint64
	StartHeight    uint64
	ProxyContracts []string
	CCMContract    string
	Nodes          []string
}

func (c *ChainConfig) HeightKey() []byte {
	return []byte(fmt.Sprintf("height-%v", c.ChainId))
}

func (c *ChainConfig) ReadHeight(db *bolt.DB) (uint64, error) {
	var height int
	err := db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BUCKET).Get(c.HeightKey())
		height, _ = strconv.Atoi(string(v))
		return nil
	})
	return uint64(height), err
}

func (c ChainConfig) WriteHeight(db *bolt.DB, height uint64) error {
	logs.Info("Commit block chain %v height %v", c.ChainId, height)
	return db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(BUCKET).Put(c.HeightKey(), []byte(strconv.Itoa(int(height))))
	})
}

type Config struct {
	PolyNodes       []string
	DingUrl         string
	PolyCCMContract string
	Chains          []*ChainConfig
	MetricHost      string
	MetricPort      int
}

type DstTx struct {
	From       string
	To         string
	SrcTx      string
	SrcIndex   string
	Method     string
	PolyTx     string
	DstTx      string
	SrcChainId uint64
	DstChainId uint64
	DstAsset   string
	Amount     *big.Int
	DstProxy   string
	DstHeight  uint64
	Mark       bool
	sig        chan struct{}
}

func (tx *DstTx) Finish() {
	tx.sig = make(chan struct{})
	close(tx.sig)
}

func (tx *DstTx) Sink(chans map[uint64]chan *DstTx) {
	tx.sig = make(chan struct{})
	ch, ok := chans[tx.SrcChainId]
	if !ok {
		logs.Error("Missing chain validator for %v %v", tx.SrcChainId, *tx)
	} else {
		ch <- tx
	}
}

func (tx *DstTx) Done() {
	close(tx.sig)
}

func (tx *DstTx) Wait() {
	<-tx.sig
}

type ChainValidator interface {
	Setup(*ChainConfig) error
	Scan(uint64) ([]*DstTx, error)
	ScanEvents(uint64, chan tools.CardEvent) error
	Validate(*DstTx) error
	LatestHeight() (uint64, error)
}

type Runner struct {
	Validator ChainValidator
	poly      *poly.SDK
	In        chan *DstTx
	buf       chan *DstTx
	height    uint64
	conf      *ChainConfig
	db        *bolt.DB
	outputs   chan tools.CardEvent
}

func NewRunner(cfg *ChainConfig, db *bolt.DB, poly *poly.SDK, outputs chan tools.CardEvent) (*Runner, error) {
	buf := make(chan *DstTx, 100)
	in := make(chan *DstTx, 100)

	var v ChainValidator
	switch cfg.ChainId {
	case base.ETH, base.HECO, base.BSC, base.OK:
		v = new(EthValidator)
	case base.NEO:
		v = new(NeoValidator)
	case base.ONT:
		v = new(OntValidator)
	default:
		return nil, fmt.Errorf("No validator found %v", *cfg)
	}

	logs.Info("Setting up validator %v", *cfg)
	err := v.Setup(cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to setup validator %w %v", err, *cfg)
	}

	return &Runner{
		Validator: v,
		poly:      poly,
		In:        in,
		buf:       buf,
		conf:      cfg,
		db:        db,
		outputs:   outputs,
	}, nil
}

func (r *Runner) run() error {
	for tx := range r.In {
		var err error
	check:
		for c := 10; c > 0; c-- {
			err = r.Validator.Validate(tx)
			if err == nil {
				break check
			}
			logs.Error("Validate tx error %+v %v", tx, err)
			time.Sleep(time.Second)
		}

		if err != nil {
			r.outputs <- &Output{DstTx: tx, Error: err}
		}
		// Always mark as done so wont stuck here
		tx.Done()
	}
	return nil
}

func (r *Runner) RunChecks(chans map[uint64]chan *DstTx) error {
	height, err := r.conf.ReadHeight(r.db)
	if err != nil {
		return err
	}
	if r.conf.StartHeight != 0 && height != r.conf.StartHeight {
		height = r.conf.StartHeight
		err = r.conf.WriteHeight(r.db, height)
		if err != nil {
			return err
		}
		h, _ := r.conf.ReadHeight(r.db)
		if h != height {
			panic("Write height failed")
		}
	}
	r.height = height
	go r.commitChecks()   // height rolling
	go r.runChecks(chans) // scan dst txs
	go r.run()            // run src checks for dst txs
	return nil
}

func (r *Runner) commitChecks() {
	update := make(chan uint64, 10)
	update <- r.height

	go func() {
		height := <-update
		write := false
		ticker := time.NewTicker(time.Second * 2)
		for h := range update {
			select {
			case <-ticker.C:
				write = true
			default:
			}
			if h > height && write {
				write = false
				height = h
				err := r.conf.WriteHeight(r.db, r.height)
				if err != nil {
					logs.Error("Failed to write chain %v height %v , err %v", r.conf.ChainId, r.height, err)
				}
			}
		}
	}()

	for tx := range r.buf {
		if !tx.Mark {
			tx.Wait()
		}
		r.height = tx.DstHeight
		update <- tx.DstHeight
	}
}

func (r *Runner) WaitBlockHeight(height uint64) uint64 {
	for {
		h, err := r.Validator.LatestHeight()
		if err != nil {
			logs.Error("Failed to get latest height for chain %v", r.conf.ChainId)
		} else {
			logs.Info("Chain %v latest height %v", r.conf.ChainId, h)
			if h > height+BLOCK_DEFER {
				return h
			}
		}
		time.Sleep(time.Second * 6)
	}
}

func (r *Runner) polyMerkleCheck(tx *DstTx, key string) (err error) {
	k, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("Invalid key hex %s err %v", key, err)
	}

	var raw []byte
	for c := 10; c > 0; c-- {
		raw, err = r.poly.Node().GetStorage(utils.CrossChainManagerContractAddress.ToHexString(), k[20:])
		if err == nil && raw != nil {
			des := pcom.NewZeroCopySource(raw)
			merkleValue := new(ccom.ToMerkleValue)
			err = merkleValue.Deserialization(des)
			if err == nil {
				if merkleValue.FromChainID == tx.SrcChainId && merkleValue.MakeTxParam != nil {
					logs.Info("Found merkle value for poly tx %s", tx.PolyTx)
					tx.SrcTx = hex.EncodeToString(merkleValue.MakeTxParam.TxHash)
					tx.Method = merkleValue.MakeTxParam.Method
					des = pcom.NewZeroCopySource(merkleValue.MakeTxParam.Args)

					var asset, assetReversed, address string
					var value *big.Int
					if tx.SrcChainId == 5 { // Switcheo
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

					if address == strings.ToLower(tx.To) &&
						(asset == strings.ToLower(tx.DstAsset) || assetReversed == strings.ToLower(tx.DstAsset)) &&
						value.Cmp(tx.Amount) == 0 {
						logs.Info("Successfully validated %s | %s | %s \n %s %s %s", tx.SrcTx, tx.PolyTx, tx.DstTx, asset, address, value.String())
						return
					}
					err = fmt.Errorf("Diff poly %s dst %s\n amount %s | %s\n to %s | %s\n asset %s | %s\n",
						tx.PolyTx, tx.DstTx, value.String(), tx.Amount.String(), address, tx.To, asset, tx.DstAsset)
				} else {
					err = fmt.Errorf("Invalid source chain id in merkle value %v %v", merkleValue.FromChainID, tx.SrcChainId)
				}
			}
		} else {
			logs.Error("Failed attempt to get poly tx merkle for %s err %w", tx.PolyTx, err)
			time.Sleep(time.Second)
		}
	}
	err = fmt.Errorf("Failed to fetch poly merkle tx for %s %w", tx.PolyTx, err)
	logs.Error("%v", err)
	return

}

func (r *Runner) polyTxCheck(tx *DstTx) (err error) {
	var ptx *types.Transaction
	for c := 10; c > 0; c-- {
		ptx, err = r.poly.Node().GetTransaction(tx.PolyTx)
		if err == nil && ptx != nil {
			return
		} else {
			logs.Error("Failed attempt to get poly tx for %s err %w", tx.PolyTx, err)
			time.Sleep(time.Second)
		}
	}
	err = fmt.Errorf("Failed to fetch poly tx for %s %w", tx.PolyTx, err)
	logs.Error("%v", err)
	return
}

func (r *Runner) polyCheck(tx *DstTx) (err error) {
	var event *common.SmartContactEvent
	for c := 10; c > 0; c-- {
		event, err = r.poly.Node().GetSmartContractEvent(tx.PolyTx)
		if err == nil && event != nil {
			for _, notify := range event.Notify {
				if notify.ContractAddress == PolyCCMContract {
					states := notify.States.([]interface{})
					contractMethod, _ := states[0].(string)
					logs.Info("poly tx: %s, tx hash: %s", tx.PolyTx, event.TxHash)
					if len(states) > 4 && (contractMethod == "makeProof" || contractMethod == "btcTxToRelay") {
						srcChain := uint64(states[1].(float64))
						switch srcChain {
						case base.ETH, base.BSC, base.O3, base.OK, base.HECO:
							tx.SrcIndex = states[3].(string)
						default:
							tx.SrcIndex = HexStringReverse(states[3].(string))
						}
						key := states[5].(string)
						return r.polyMerkleCheck(tx, key)
					}
				}
			}
		} else {
			logs.Error("Failed attempt to get poly event for tx %s err %v", tx.PolyTx, err)
			time.Sleep(time.Second)
		}
	}
	err = fmt.Errorf("Failed to get poly event for tx %s %w", tx.PolyTx, err)
	logs.Error("%v", err)
	return
}

func (r *Runner) ScanEvents(height uint64) error {
	return r.Validator.ScanEvents(height, r.outputs)
}

func (r *Runner) scan(height uint64) (txs []*DstTx, err error) {
	for c := 8; c > 0; c-- {
		txs, err = r.Validator.Scan(height)
		if err != nil {
			return
		}
		valid := true
		for _, tx := range txs {
			if tx.PolyTx == "" {
				logs.Error("Invalid poly tx(%d) for chain %s dst tx %s", c, tx.DstChainId, tx.DstTx)
				valid = false
				break
			}
		}
		if valid {
			return
		}
		time.Sleep(time.Second * 8)
	}
	return
}

func (r *Runner) runChecks(chans map[uint64]chan *DstTx) {
	height := r.height
	var latest uint64
	for {
		if latest <= height+BLOCK_DEFER {
			latest = r.WaitBlockHeight(height)
		}
		logs.Info("Running scan on chain %v height %v", r.conf.ChainId, height)
		err := r.ScanEvents(height)
		if err == nil {
			txs, err := r.scan(height)
			if err == nil {
				logs.Info("Scan found %d txs in block chain %v height %v", len(txs), r.conf.ChainId, height)
				for _, tx := range txs {
					if tx.PolyTx == "" {
						r.outputs <- &Output{DstTx: tx, Error: fmt.Errorf("Invalid poly tx on tx unlock event on chain %d", tx.DstChainId)}
					} else {
						err := r.polyCheck(tx)
						if err != nil {
							r.outputs <- &Output{DstTx: tx, Error: err}
						} else {
							tx.Finish()
							// tx.Sink(chans)
							r.buf <- tx
						}
					}
				}
				if len(txs) == 0 && height%10 == 0 {
					tx := &DstTx{DstHeight: height, Mark: true}
					r.buf <- tx
				}
				metrics.Record(height, "%d", r.conf.ChainId)
				height++
				time.Sleep(time.Millisecond)
			}
		}

		if err != nil {
			logs.Error("Failed to scan block chain %v height %v err %v", r.conf.ChainId, height, err)
			time.Sleep(time.Second * 2)
		}
	}
}

type Listener struct {
	validators map[uint64]*Runner
	chans      map[uint64]chan *DstTx
	outputs    chan tools.CardEvent
}

func (l *Listener) watch() {
	c := 0
	for o := range l.outputs {
		c++
		logs.Error("!!!!!!! Alarm(%v): %v", c, o)
		err := tools.PostCardEvent(o)
		if err != nil {
			logs.Error("Post dingtalk error %s", err)
		}
		time.Sleep(time.Second)
	}
}

func (l *Listener) Start(cfg Config, ctx context.Context, wg *sync.WaitGroup, chain int) (err error) {
	PolyCCMContract = cfg.PolyCCMContract
	DingUrl = cfg.DingUrl

	db, err := bolt.Open(".validator.db", 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(BUCKET)
		return e
	})
	if err != nil {
		return err
	}

	poly, err := poly.NewSDK(base.POLY, cfg.PolyNodes, time.Minute, 1)
	if err != nil {
		return err
	}

	l.validators = map[uint64]*Runner{}
	l.chans = map[uint64]chan *DstTx{}
	l.outputs = make(chan tools.CardEvent, 1000)

	go l.watch()

	for _, c := range cfg.Chains {
		if chain != 0 && uint64(chain) != c.ChainId {
			continue
		}
		r, err := NewRunner(c, db, poly, l.outputs)
		if err != nil {
			return err
		}
		l.chans[c.ChainId] = r.In
		l.validators[c.ChainId] = r
	}
	for _, v := range l.validators {
		err = v.RunChecks(l.chans)
		if err != nil {
			return err
		}
	}
	// TODO sigs
	<-ctx.Done()
	return
}

func PostDingCard(title, body string, btns interface{}) error {
	payload := map[string]interface{}{}
	payload["msgtype"] = "actionCard"
	card := map[string]interface{}{}
	card["title"] = title
	card["text"] = body
	card["hideAvatar"] = 0
	card["btns"] = btns
	payload["actionCard"] = card
	return postDing(payload)
}

func postDing(payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", DingUrl, bytes.NewBuffer(data))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	logs.Info("PostDing response Body:", string(respBody))
	return nil
}

func ScanPolyProofs(height uint64, poly *poly.SDK, ccmContract string) (err error) {
	PolyCCMContract = ccmContract

	events, err := poly.Node().GetSmartContractEventByBlock(uint32(height))
	if err != nil {
		panic(err)
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
					err = polyMerkleCheck(poly, key)
					if err != nil {
						logs.Error("polyMerkleCheck err %v", err)
					}
				}
			}
		}
	}
	return
}

func polyMerkleCheck(poly *poly.SDK, key string) error {
	k, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("Invalid key hex %s err %v", key, err)
	}

	raw, err := poly.Node().GetStorage(utils.CrossChainManagerContractAddress.ToHexString(), k[20:])
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
