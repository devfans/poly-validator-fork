package validator

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"sync"
	"time"

	"poly-bridge/basedef"
	"poly-bridge/chainsdk"

	ecom "github.com/ethereum/go-ethereum/common"
	"github.com/polynetwork/poly-go-sdk/common"
	pcom "github.com/polynetwork/poly/common"
	"github.com/polynetwork/poly/core/types"
	ccom "github.com/polynetwork/poly/native/service/cross_chain_manager/common"
	"github.com/polynetwork/poly/native/service/utils"

	"github.com/beego/beego/v2/core/logs"
	"github.com/boltdb/bolt"
)

var BUCKET = []byte("poly-validator")
var PolyCCMContract string

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
	PolyCCMContract string
	Chains          []*ChainConfig
}

type Output struct {
	*DstTx
	Error error
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
	sig        chan struct{}
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
	Validate(*DstTx) error
	LatestHeight() (uint64, error)
}

type Runner struct {
	Validator ChainValidator
	poly      *chainsdk.PolySDKPro
	In        chan *DstTx
	buf       chan *DstTx
	height    uint64
	conf      *ChainConfig
	db        *bolt.DB
	outputs   chan *Output
}

func NewRunner(cfg *ChainConfig, db *bolt.DB, poly *chainsdk.PolySDKPro, outputs chan *Output) (*Runner, error) {
	buf := make(chan *DstTx, 100)
	in := make(chan *DstTx, 100)

	var v ChainValidator
	switch cfg.ChainId {
	case basedef.ETHEREUM_CROSSCHAIN_ID, basedef.HECO_CROSSCHAIN_ID, basedef.BSC_CROSSCHAIN_ID:
		v = new(EthValidator)
		logs.Info("Setting up validator %v", *cfg)
		err := v.Setup(cfg)
		if err != nil {
			return nil, fmt.Errorf("Failed to setup validator %w %v", err, *cfg)
		}
	default:
		return nil, fmt.Errorf("No validator found %v", *cfg)
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
		tx.Wait()
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
			if h > height {
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
		raw, err = r.poly.GetStorage(utils.CrossChainManagerContractAddress.ToHexString(), k[20:])
		if err == nil && raw != nil {
			des := pcom.NewZeroCopySource(raw)
			merkleValue := new(ccom.ToMerkleValue)
			err = merkleValue.Deserialization(des)
			if err == nil {
				if merkleValue.FromChainID == tx.SrcChainId && merkleValue.MakeTxParam != nil {
					logs.Info("Found merkle value for poly tx %s %v %v", tx.PolyTx, *merkleValue, *merkleValue.MakeTxParam)
					tx.SrcTx = hex.EncodeToString(merkleValue.MakeTxParam.TxHash)
					tx.Method = merkleValue.MakeTxParam.Method
					des = pcom.NewZeroCopySource(merkleValue.MakeTxParam.Args)
					dstAsset, ok := des.NextVarBytes()
					logs.Info("dstAsset %v %v", ecom.BytesToAddress(dstAsset).Hex(), ok)
					to, ok := des.NextVarBytes()
					logs.Info("to  %v %v", ecom.BytesToAddress(to).Hex(), ok)
					amount, ok := des.NextBytes(32)
					logs.Info("amount %x %v", amount, ok)
				}
				err = fmt.Errorf("Invalid source chain id in merkle value %v %v", merkleValue.FromChainID, tx.SrcChainId)
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
		ptx, err = r.poly.GetTransaction(tx.PolyTx)
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
		event, err = r.poly.GetSmartContractEvent(tx.PolyTx)
		if err == nil && event != nil {
			for _, notify := range event.Notify {
				if notify.ContractAddress == PolyCCMContract {
					states := notify.States.([]interface{})
					contractMethod, _ := states[0].(string)
					logs.Info("poly tx: %s, tx hash: %s", tx.PolyTx, event.TxHash)
					if len(states) > 4 && (contractMethod == "makeProof" || contractMethod == "btcTxToRelay") {
						srcChain := uint64(states[1].(float64))
						switch srcChain {
						case basedef.ETHEREUM_CROSSCHAIN_ID, basedef.BSC_CROSSCHAIN_ID, basedef.O3_CROSSCHAIN_ID, basedef.OK_CROSSCHAIN_ID, basedef.HECO_CROSSCHAIN_ID:
							tx.SrcIndex = states[3].(string)
						default:
							tx.SrcIndex = basedef.HexStringReverse(states[3].(string))
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

func (r *Runner) runChecks(chans map[uint64]chan *DstTx) {
	height := r.height
	var latest uint64
	for {
		if latest <= height {
			latest = r.WaitBlockHeight(height)
		}
		logs.Info("Running scan on chain %v height %v", r.conf.ChainId, height)
		txs, err := r.Validator.Scan(height)
		if err == nil {
			logs.Info("Scan found %d txs in block chain %v height %v", len(txs), r.conf.ChainId, height)
			for _, tx := range txs {
				err := r.polyCheck(tx)
				if err != nil {
					r.outputs <- &Output{DstTx: tx, Error: err}
				} else {
					tx.Sink(chans)
					r.buf <- tx
				}
			}
			height++
			time.Sleep(time.Second)
		} else {
			logs.Error("Failed to scan block chain %v height %v err %v", r.conf.ChainId, height, err)
			time.Sleep(time.Second * 2)
		}
	}
}

type Listener struct {
	validators map[uint64]*Runner
	chans      map[uint64]chan *DstTx
	outputs    chan *Output
}

func (l *Listener) watch() {
	for o := range l.outputs {
		logs.Error("!!!!!!! Alarm(%v): %v", o.Error, *o.DstTx)
	}
}

func (l *Listener) Start(cfg Config, ctx context.Context, wg *sync.WaitGroup) (err error) {
	PolyCCMContract = cfg.PolyCCMContract

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

	poly := chainsdk.NewPolySDKPro(cfg.PolyNodes, 10, 0)
	l.validators = map[uint64]*Runner{}
	l.chans = map[uint64]chan *DstTx{}
	l.outputs = make(chan *Output, 1000)

	go l.watch()

	for _, c := range cfg.Chains {
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
