package relayer

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"fmt"

	// "github.com/ethereum/go-ethereum/accounts/keystore"
	// "github.com/ethereum/go-ethereum/crypto"
	"github.com/ontio/ontology-crypto/keypair"
	// "github.com/polynetwork/bridge-common/base"
	"github.com/polynetwork/bridge-common/log"

	"github.com/polynetwork/bridge-common/util"
	"github.com/polynetwork/poly/native/service/utils"
	"github.com/polynetwork/poly-relayer/config"
	"github.com/polynetwork/poly/common"
	vconfig "github.com/polynetwork/poly/consensus/vbft/config"
	"github.com/polynetwork/poly/native/service/governance/side_chain_manager"
	// "github.com/polynetwork/poly/core/types"
)

func TestSideChain(t *testing.T) {
	conf, err := config.New("/Users/stefanliu/git/relayer/config.json")
	if err != nil { t.Fatal(err) }
	err = conf.Init()
	if err != nil { t.Fatal(err) }
	chainID := uint64(2)
	ps, err := PolySubmitter()
	if err != nil {
		return
	}
	data, err := ps.SDK().Node().GetStorage(utils.SideChainManagerContractAddress.ToHexString(),
		append([]byte(side_chain_manager.SIDE_CHAIN), utils.GetUint64Bytes(chainID)...))
	if err != nil {
		return
	}
	if data == nil {
		log.Info("No such chain", "id", chainID)
	} else {
		chain := new(side_chain_manager.SideChain)
		err = chain.Deserialization(common.NewZeroCopySource(data))
		if err != nil {
			return
		}
		fmt.Println(util.Verbose(chain))
		fmt.Println("extra:", string(chain.ExtraInfo))
		fmt.Printf("ccm: %x\n", chain.CCMCAddress)
	}
}

/*
func TestBin(t *testing.T) {
	p, err := crypto.HexToECDSA("53a5f351c2b6a563392bbadfbe362e661aa7d7274e2ca0932c3b33e6d9faf376")
	if err != nil { t.Fatal(err) }
	ks := keystore.NewKeyStore("test1", keystore.StandardScryptN, keystore.StandardScryptP)
	account, err := ks.ImportECDSA(p, "test")
	if err != nil { t.Fatal(err) }
	t.Log(account.Address.String())
}

func TestOntGenesis(t *testing.T) {
	conf, err := config.New("/Users/stefanliu/git/relayer/config.json")
	if err != nil { t.Fatal(err) }
	err = conf.Init()
	if err != nil { t.Fatal(err) }
	pl, err := PolyListener()
	if err != nil {
		t.Fatal(err)
	}
	lis, err := ChainListener(base.ONT, pl.SDK())
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(14334300)
	/*
	height, err := lis.LastHeaderSync(0, 0)
	if err != nil { t.Fatal(err) }
	/
	fmt.Println(height)
	header, hash, err := lis.Header(height)
	if err !=nil { t.Fatal(err) }
	fmt.Printf("Header %x \n Hash %x \n", header, hash)
}

func TestDecodeTx(t *testing.T) {
	raw := ""
	tx := &types.Transaction{}
	data, err := hex.DecodeString(util.LowerHex(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Deserialization(common.NewZeroCopySource(data)); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%+v", tx.Sigs)
}

func TestCheckPolySigner(t *testing.T) {
	keystr, _ := hex.DecodeString("1205028172918540b2b512eae1872a2a2e3a28d989c60d95dab8829ada7d7dd706d658")
	key, _ := keypair.DeserializePublicKey(keystr)
	log.Info("x1", "key", string(keystr), "hex", hex.EncodeToString(keystr))
	address := common.AddressFromVmCode(keypair.SerializePublicKey(key))
	log.Info("xx1", "address", (&address).ToHexString())
}
*/

func TestSyncContractGenesis(t *testing.T) {
	conf, err := config.New("/Users/stefanliu/git/relayer/config.json")
	if err != nil { t.Fatal(err) }
	err = conf.Init()
	if err != nil { t.Fatal(err) }
	t.Log("Height 0")
	ks, err := getBookKeepers(0)
	if err != nil { t.Fatal(err) }
	keys := []byte{}
	for i, k := range ks {
		t.Log(fmt.Sprintf("%v: %x", i, k))
		keys = append(keys, k...)
	}
	ps, err := PolySubmitter()
	if err != nil {
		t.Fatal(err)
	}
	height, err := ps.SDK().Node().GetLatestHeight()
	if err != nil { t.Fatal(err) }
	t.Log("Height", height)
	ks2, err := getBookKeepers(height)
	if err != nil { t.Fatal(err) }
	keys2 := []byte{}
	for i, k := range ks2 {
		t.Log(fmt.Sprintf("%v: %x", i, k))
		keys2 = append(keys2, k...)
	}
	eq := hex.EncodeToString(keys) == hex.EncodeToString(keys2)
	t.Log(eq)
}


func getBookKeepers(height uint64) (ks [][]byte, err error) {
	ps, err := PolySubmitter()
	if err != nil {
		return
	}
	//NOTE: only block 0 can succeed?!
	/*
		if height == 0 {
			height, err = ps.SDK().Node().GetLatestHeight()
			if err != nil { return err }
		}
	*/
	block, err := ps.SDK().Node().GetBlockByHeight(uint32(height))
	if err != nil { return }
	info := &vconfig.VbftBlockInfo{}
	err = json.Unmarshal(block.Header.ConsensusPayload, info);
	if err != nil {
		panic(err)
	}
	if info.NewChainConfig == nil {
		height = uint64(info.LastConfigBlockNum)
		log.Info("Last config block num", "height", height)
		block, err := ps.SDK().Node().GetBlockByHeight(uint32(height))
		if err != nil { return nil, err }
		info = &vconfig.VbftBlockInfo{}
		err = json.Unmarshal(block.Header.ConsensusPayload, info);
		if err != nil {
			panic(err)
		}
	}
	var bookkeepers []keypair.PublicKey
	for _, peer := range info.NewChainConfig.Peers {
		keystr, _ := hex.DecodeString(peer.ID)
		key, _ := keypair.DeserializePublicKey(keystr)
		log.Info("x", "key", string(keystr), "hex", hex.EncodeToString(keystr))
		address := common.AddressFromVmCode(keypair.SerializePublicKey(key))
		log.Info("xx", "address", (&address).ToHexString())
		bookkeepers = append(bookkeepers, key)
	}
	{
		keystr, _ := hex.DecodeString("120503ef44beba84422bd76a599531c9fe50969a929a0fee35df66690f370ce19fa8c0")
		key, _ := keypair.DeserializePublicKey(keystr)
		log.Info("x1", "key", string(keystr), "hex", hex.EncodeToString(keystr))
		address := common.AddressFromVmCode(keypair.SerializePublicKey(key))
		log.Info("xx1", "address", (&address).ToHexString())
	}
	bookkeepers = keypair.SortPublicKeys(bookkeepers)
	for _, key := range bookkeepers {
		ks = append(ks, GetOntNoCompressKey(key))
		ks = append(ks, keypair.SerializePublicKey(key))
	}
	return
}
