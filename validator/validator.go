package validator

import (
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/beego/beego/v2/core/logs"
	"github.com/boltdb/bolt"
)

var BUCKET = []byte("poly-validator")

type ChainConfig struct {
	ChainId        uint64
	StartHeight    int64
	ProxyContracts []string
	CCMContract    string
	Nodes          []string
}

func (c *ChainConfig) HeightKey() []byte {
	return []byte(fmt.Sprintf("height-%v", c.ChainId))
}

func (c *ChainConfig) ReadHeight(db *bolt.DB) (int64, error) {
	var height int
	err := db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(BUCKET).Get(c.HeightKey())
		height, _ = strconv.Atoi(string(v))
		return nil
	})
	return int64(height), err
}

func (c ChainConfig) WriteHeight(db *bolt.DB, height int64) error {
	logs.Info("Commit block chain %v height %v", c.ChainId, height)
	return db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(BUCKET).Put(c.HeightKey(), []byte(strconv.Itoa(int(height))))
	})
}

type Config struct {
	Chains []*ChainConfig
}

type DstTx struct {
	From       string
	To         string
	SrcTx      string
	DstTx      string
	SrcChainId uint64
	DstChainId uint64
	DstAsset   string
	Amount     *big.Int
	DstProxy   string
	DstHeight  int64
	sig        chan struct{}
}

func (tx *DstTx) Sink(chans map[uint64]chan *DstTx) {
	tx.sig = make(chan struct{})
	ch, ok := chans[tx.SrcChainId]
	if !ok {
		logs.Error("Missing chain validator for %v", tx.SrcChainId)
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
	Scan(int64) ([]*DstTx, error)
	Validate(*DstTx) error
	LatestHeight() (int64, error)
}

type Runner struct {
	Validator ChainValidator
	In        chan *DstTx
	buf       chan *DstTx
	height    int64
	conf      *ChainConfig
	db        *bolt.DB
}

func NewRunner(cfg *ChainConfig, db *bolt.DB) *Runner {
	buf := make(chan *DstTx, 100)
	in := make(chan *DstTx, 100)
	return &Runner{
		Validator: nil,
		In:        in,
		buf:       buf,
		conf:      cfg,
		db:        db,
	}
}

func (r *Runner) run() error {
	for tx := range r.In {
	check:
		for c := 3; c > 0; c-- {
			err := r.Validator.Validate(tx)
			if err == nil {
				tx.Done()
				break check
			}
			logs.Error("Validate tx error %+v %v", tx, err)
			time.Sleep(time.Second)
		}
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
	update := make(chan int64, 10)
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

func (r *Runner) WaitBlockHeight(height int64) {
	for {
		h, err := r.Validator.LatestHeight()
		if err != nil {
			logs.Error("Failed to get latest height for chain %v", r.conf.ChainId)
		} else {
			logs.Info("Chain %v latest height %v", r.conf.ChainId, h)
			if h > height {
				return
			}
		}
		time.Sleep(time.Second * 6)
	}
}

func (r *Runner) runChecks(chans map[uint64]chan *DstTx) {
	var latest int64
	height := r.height
	for {
		latest = r.WaitBlockHeight(height)
		logs.Info("Running scan on chain %v height %v", r.conf.ChainId, height)
		txs, err := r.Validator.Scan(height)
		if err == nil {
			logs.Info("Scan found %d txs in block chain %v height %v", len(txs), r.conf.ChainId, height)
			for _, tx := range txs {
				tx.Sink(chans)
				r.buf <- tx
			}
			height++
		} else {
			logs.Error("Failed to scan block chain %v height %v err %v", r.conf.ChainId, height, err)
			time.Sleep(time.Second * 2)
		}
	}
}

type Listener struct {
	validators map[uint64]*Runner
	chans      map[uint64]chan *DstTx
}

func (l *Listener) Start(cfg Config) (err error) {
	db, err := bolt.Open("validator.db", 0600, nil)
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

	for _, c := range cfg.Chains {
		r := NewRunner(c, db)
		l.chans[c.ChainId] = r.In
		l.validators[c.ChainId] = r
	}
	for _, v := range l.validators {
		err = v.RunChecks(l.chans)
		if err != nil {
			return err
		}
	}
	return
}
