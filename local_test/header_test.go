package localtest

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/stretchr/testify/assert"
)

func TestRlpEncodeHeader(t *testing.T) {
	// header := types.Header{
	// 	ParentHash:  common.Hash{},
	// 	UncleHash:   common.Hash{},
	// 	Coinbase:    common.Address{},
	// 	Root:        common.Hash{},
	// 	TxHash:      common.Hash{},
	// 	ReceiptHash: common.Hash{},
	// 	Bloom:       types.Bloom{},
	// 	Difficulty:  big.NewInt(0),
	// 	Number:      big.NewInt(0),
	// 	GasLimit:    0,
	// 	GasUsed:     0,
	// }

	blockStr := `{
        "author": "0x13c9f0ae8a79df8cd3bc18301477ae6376dba8d0",
        "baseFeePerGas": "0x1",
        "difficulty": "0x4",
        "espaceGasLimit": "0x0",
        "extraData": "0x",
        "gasLimit": "0x1c9c380",
        "gasUsed": "0x0",
        "hash": "0x70f4f584669b71767a306d631a3208f9fa87182df262a1ff8e90bcb7dcd65fa3",
        "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
        "miner": "0x13c9f0ae8a79df8cd3bc18301477ae6376dba8d0",
        "mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
        "nonce": "0xf9d9fd8fa8b128b1",
        "number": "0x333e4",
        "parentHash": "0xbb440e6739a2b63eb655fdf0fd777091868d66da4aa7c7f0baf0ac4521c9ad23",
        "receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
        "sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
        "size": "0x0",
        "stateRoot": "0xdc8cb6475f3ebc6a0149c1d426ec67b8f0f3c20df51d49030de73fb8b1426b35",
        "timestamp": "0x67dd28ce",
        "totalDifficulty": "0x0",
        "transactions": [],
        "transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
        "uncles": []
    }`

	// var block types.Block
	// err := json.Unmarshal([]byte(blockStr), &block)
	// assert.NoError(t, err)

	// fmt.Printf("block: %+v\n", block)

	var header types.Header
	err := json.Unmarshal([]byte(blockStr), &header)
	assert.NoError(t, err)

	buf := bytes.NewBuffer(nil)
	err = header.EncodeRLP(buf)
	assert.NoError(t, err)

	fmt.Println(hex.EncodeToString(buf.Bytes()))
	fmt.Println(header.Hash())
}

func TestRlpEncodeBytes(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	w := rlp.NewEncoderBuffer(buf)
	w.WriteBytes([]byte{})
	w.Flush()
	fmt.Println(hex.EncodeToString(buf.Bytes()))
}

func TestRlpEncodeUint64(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	w := rlp.NewEncoderBuffer(buf)
	w.WriteUint64(0)
	w.Flush()
	fmt.Println("bytes length: ", len(buf.Bytes()))
	fmt.Println(hex.EncodeToString(buf.Bytes()))
}
