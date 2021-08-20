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
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"os/exec"
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
	BLOCK_DEFER = uint64(0)

	PolyCCMContract string
	DingUrl         string
)

type ChainConfig struct {
	ChainId              uint64
	CounterBlocks        int
	CounterDuration      uint64
	StartHeight          uint64
	ProxyContracts       []string
	CCMContract          string
	Nodes                []string
	TraceAddresses       []string
	HeightReportInterval int64
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
	StartHeight     uint64
	DingUrl         string
	PolyCCMContract string
	Chains          []*ChainConfig
	MetricHost      string
	MetricPort      int
	PauseCCMCommand []string
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
	counter   *tools.BlockCounter
	tps       *tools.TimedCounter
	poly      *poly.SDK
	In        chan *DstTx
	buf       chan *DstTx
	height    uint64
	conf      *ChainConfig
	db        *bolt.DB
	outputs   chan tools.CardEvent
	Name      string
}

func NewRunner(cfg *ChainConfig, db *bolt.DB, poly *poly.SDK, outputs chan tools.CardEvent) (*Runner, error) {
	buf := make(chan *DstTx, 100)
	in := make(chan *DstTx, 100)

	var v ChainValidator
	switch cfg.ChainId {
	case base.ETH, base.HECO, base.BSC, base.OK, base.MATIC:
		v = new(EthValidator)
	case base.NEO:
		v = new(NeoValidator)
	case base.ONT:
		v = new(OntValidator)
	case base.O3:
		v = new(O3Validator)
	case base.SWITCHEO:
		v = new(SwitcheoValidator)
	case base.POLY:
		p := new(PolyValidator)
		p.SetupSDK(poly)
		v = p
	default:
		return nil, fmt.Errorf("No validator found %v", *cfg)
	}

	logs.Info("Setting up validator %v", *cfg)
	err := v.Setup(cfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to setup validator %w %v", err, *cfg)
	}

	if cfg.CounterBlocks == 0 {
		cfg.CounterBlocks = 10
	}
	if cfg.CounterDuration == 0 {
		cfg.CounterDuration = 60
	}

	if cfg.HeightReportInterval == 0 {
		cfg.HeightReportInterval = 60 * 20
	}

	tps, err := tools.NewTimedCounter(time.Duration(cfg.CounterDuration) * time.Second)
	if err != nil {
		return nil, err
	}
	counter, err := tools.NewBlockCounter(cfg.CounterBlocks)
	if err != nil {
		return nil, err
	}

	return &Runner{
		Validator: v,
		counter:   counter,
		tps:       tps,
		poly:      poly,
		In:        in,
		buf:       buf,
		conf:      cfg,
		db:        db,
		outputs:   outputs,
		Name:      base.GetChainName(cfg.ChainId),
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
			r.outputs <- &InvalidUnlockEvent{DstTx: tx, Error: err}
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
	go r.commitChecks() // height rolling

	switch r.conf.ChainId {
	case base.POLY, base.O3, base.SWITCHEO:
		go r.runSimpleChecks(chans) // run simple metrics
	default:
		go r.runChecks(chans) // scan dst txs
		go r.run()            // run src checks for dst txs
	}

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
			if h >= height+BLOCK_DEFER {
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
	for c := 20; c > 0; c-- {
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

func (r *Runner) runSimpleChecks(chans map[uint64]chan *DstTx) {
	height := r.height
	var latest uint64
	lastHeightUpdate := time.Now().Unix()
	reportInterval := r.conf.HeightReportInterval

	for {
		if latest < height+BLOCK_DEFER {
			latest = r.WaitBlockHeight(height)
		}
		// logs.Info("Running scan on chain %v height %v", r.conf.ChainId, height)
		txs, err := r.Validator.Scan(height)
		if err == nil {
			if height%100 == 0 {
				tx := &DstTx{DstHeight: height, Mark: true}
				r.buf <- tx
			}
			metrics.Record(height, "blocks.%s", r.Name)
			r.tps.Tick(len(txs))
			r.counter.Tick(height)
			metrics.Record(r.tps.Tps(), "tps.%s", r.Name)
			metrics.Record(r.counter.BlockTime(), "blocktime.%s", r.Name)
			height++
			lastHeightUpdate = time.Now().Unix()
			reportInterval = r.conf.HeightReportInterval
			time.Sleep(time.Millisecond)
		} else {
			logs.Error("Failed to scan block chain %v height %v err %v", r.conf.ChainId, height, err)
			duration := time.Now().Unix() - lastHeightUpdate
			if duration > reportInterval {
				r.outputs <- &ChainHeightStuckEvent{Chain: r.Name, CurrentHeight: height, Duration: time.Duration(duration) * time.Second, Nodes: r.conf.Nodes}
				reportInterval *= 2
			}

			time.Sleep(time.Second * 2)
		}
	}
}

func (r *Runner) runChecks(chans map[uint64]chan *DstTx) {
	height := r.height
	var latest uint64
	lastHeightUpdate := time.Now().Unix()
	reportInterval := r.conf.HeightReportInterval
	for {
		if latest < height+BLOCK_DEFER {
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
						r.outputs <- &InvalidUnlockEvent{DstTx: tx, Error: fmt.Errorf("Invalid poly tx on tx unlock event on chain %d", tx.DstChainId)}
					} else {
						err := r.polyCheck(tx)
						if err != nil {
							r.outputs <- &InvalidUnlockEvent{DstTx: tx, Error: err}
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
				metrics.Record(height, "blocks.%s", r.Name)
				r.tps.Tick(len(txs))
				r.counter.Tick(height)
				metrics.Record(r.tps.Tps(), "tps.%s", r.Name)
				metrics.Record(r.counter.BlockTime(), "blocktime.%s", r.Name)

				height++
				lastHeightUpdate = time.Now().Unix()
				reportInterval = r.conf.HeightReportInterval
				time.Sleep(time.Millisecond)
			}
		}

		if err != nil {
			logs.Error("Failed to scan block chain %v height %v err %v", r.conf.ChainId, height, err)
			duration := time.Now().Unix() - lastHeightUpdate
			if duration > reportInterval {
				r.outputs <- &ChainHeightStuckEvent{Chain: r.Name, CurrentHeight: height, Duration: time.Duration(duration) * time.Second, Nodes: r.conf.Nodes}
				reportInterval *= 2
			}
			time.Sleep(time.Second * 2)
		}
	}
}

type Listener struct {
	validators map[uint64]*Runner
	chans      map[uint64]chan *DstTx
	outputs    chan tools.CardEvent
	conf       Config
}

func (l *Listener) handleEvent(o tools.CardEvent) {
	ev, ok := o.(*InvalidUnlockEvent)
	if !ok {
		return
	}
	go func() {
		cmd := exec.Command(l.conf.PauseCCMCommand[0], l.conf.PauseCCMCommand[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stdout
		err := cmd.Run()
		if err != nil {
			logs.Error("Run handle event command error %v %v", err, *ev)
		}
	}()
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
		l.handleEvent(o)
		time.Sleep(time.Second)
	}
}

func (l *Listener) Start(cfg Config, ctx context.Context, wg *sync.WaitGroup, chain int) (err error) {
	l.conf = cfg
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

	// Poly
	{
		c := &ChainConfig{
			ChainId:     base.POLY,
			StartHeight: cfg.StartHeight,
		}
		r, err := NewRunner(c, db, poly, l.outputs)
		if err != nil {
			return err
		}
		r.RunChecks(l.chans)
		if err != nil {
			return err
		}
	}

	// TODO sigs
	<-ctx.Done()
	return
}
