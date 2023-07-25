package relayer

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/polynetwork/bridge-common/base"
	ethc "github.com/polynetwork/bridge-common/chains/eth"
	"github.com/polynetwork/bridge-common/tools"
	"github.com/polynetwork/poly-relayer/config"
	"github.com/polynetwork/poly-relayer/msg"
	"github.com/polynetwork/poly-relayer/relayer/eth"
)

func TestBot(t *testing.T) {
	conf, err := config.New("../config.json")
	if err != nil {
		t.Fatal(err)
	}
	err = conf.Init()
	if err != nil {
		t.Fatal(err)
	}

	tx := &msg.Tx{
		SrcChainId: base.BSC,
		DstChainId: base.ETH,
		DstHash: "xxxxx",
	}

	event := &msg.InvalidPolyCommitEvent{Tx: tx, Error: fmt.Errorf("invalid poly commit tx from chain %d, %v", tx.SrcChainId, err)}
	pushTgEvent(config.CONFIG.Validators.TgUrl, event)
}

func TestValidate(t *testing.T) {
	conf, err := config.New("../config.json")
	if err != nil {
		t.Fatal(err)
	}
	err = conf.Init()
	if err != nil {
		t.Fatal(err)
	}
	pl, err := PolyListener()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := ChainListener(base.BSC, pl.SDK())
	if err != nil {
		t.Fatal(err)
	}

	{
		l := lis.(*eth.Listener)
		txs, err := l.ScanDst(29597714)
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range txs {
			err := pl.Validate(tx)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	txs, err := pl.ScanDst(30318897)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(len(txs))
	for _, tx := range txs {
		fmt.Println(tx.PolyHash)
		err = lis.(*eth.Listener).Validate(tx)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("done")
	}

	{
		l := lis.(*eth.Listener)
		txs, err := l.ScanDst(29589899)
		if err != nil {
			t.Fatal(err)
		}
		for _, tx := range txs {
			err := pl.Validate(tx)
			if err != nil {
				event := &msg.InvalidPolyCommitEvent{Tx: tx, Error: fmt.Errorf("invalid poly commit tx from chain %d, %v", tx.SrcChainId, err)}
				pushTgEvent(config.CONFIG.Validators.TgUrl, event)
			}
		}
	}
}

func TestValidateEvent(t *testing.T) {
	var ev tools.CardEvent
	ev = &msg.InvalidPolyCommitEvent{
		Error: fmt.Errorf("no"),
	}
	pause := ShouldPauseForEvent(ev)
	t.Logf("Pause %v %+v", pause, ev)
}

func TestStorage(t *testing.T) {
	c := ethc.New("https://rpc.ankr.com/eth")
	hash, err := c.StorageAt(context.Background(), common.HexToAddress("0xcf2afe102057ba5c16f899271045a0a37fcb10f0"), common.HexToHash("1B833bF1A0094A941A208BF8799F93998625d543"), nil)
	t.Logf("hash %v, err %v\n", hash, err)
}
