package types

import (
	"math/rand"
	"time"

	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtypes "github.com/cometbft/cometbft/types"
)

// TestChainID is a constant used for testing purposes. It represents a mock chain ID.
const TestChainID = "test"

// GetRandomValidatorSet returns the validator set from
// GetRandomValidatorSetWithPrivKey without the private key.
func GetRandomValidatorSet() *cmtypes.ValidatorSet {
	valSet, _ := GetRandomValidatorSetWithPrivKey()
	return valSet
}

// GetRandomValidatorSetWithPrivKey returns a validator set with a single
// validator
func GetRandomValidatorSetWithPrivKey() (*cmtypes.ValidatorSet, ed25519.PrivKey) {
	privKey := ed25519.GenPrivKey()
	pubKey := privKey.PubKey()
	return &cmtypes.ValidatorSet{
		Proposer: &cmtypes.Validator{PubKey: pubKey, Address: pubKey.Address()},
		Validators: []*cmtypes.Validator{
			{PubKey: pubKey, Address: pubKey.Address()},
		},
	}, privKey
}

// GetRandomBlock returns a block with random data
func GetRandomBlock(height uint64, nTxs int) *Block {
	header := GetRandomHeader()
	header.BaseHeader.Height = height
	block := &Block{
		SignedHeader: SignedHeader{
			Header: header,
		},
		Data: Data{
			Txs: make(Txs, nTxs),
			IntermediateStateRoots: IntermediateStateRoots{
				RawRootsList: make([][]byte, nTxs),
			},
		},
	}

	block.SignedHeader.AppHash = GetRandomBytes(32)

	for i := 0; i < nTxs; i++ {
		block.Data.Txs[i] = GetRandomTx()
		block.Data.IntermediateStateRoots.RawRootsList[i] = GetRandomBytes(32)
	}

	// TODO(tzdybal): see https://github.com/rollkit/rollkit/issues/143
	if nTxs == 0 {
		block.Data.Txs = nil
		block.Data.IntermediateStateRoots.RawRootsList = nil
	}

	return block
}

// GetRandomHeader returns a header with random fields and current time
func GetRandomHeader() Header {
	return Header{
		BaseHeader: BaseHeader{
			Height:  uint64(rand.Int63()), //nolint:gosec,
			Time:    uint64(time.Now().UnixNano()),
			ChainID: TestChainID,
		},
		Version: Version{
			Block: InitStateVersion.Consensus.Block,
			App:   InitStateVersion.Consensus.App,
		},
		LastHeaderHash:  GetRandomBytes(32),
		LastCommitHash:  GetRandomBytes(32),
		DataHash:        GetRandomBytes(32),
		ConsensusHash:   GetRandomBytes(32),
		AppHash:         GetRandomBytes(32),
		LastResultsHash: GetRandomBytes(32),
		ProposerAddress: GetRandomBytes(32),
		AggregatorsHash: GetRandomBytes(32),
	}
}

// GetRandomNextHeader returns a header with random data and height of +1 from
// the provided Header
func GetRandomNextHeader(header Header) Header {
	nextHeader := GetRandomHeader()
	nextHeader.BaseHeader.Height = header.Height() + 1
	nextHeader.BaseHeader.Time = uint64(time.Now().Add(1 * time.Second).UnixNano())
	nextHeader.LastHeaderHash = header.Hash()
	nextHeader.ProposerAddress = header.ProposerAddress
	nextHeader.AggregatorsHash = header.AggregatorsHash
	nextHeader.NextAggregatorsHash = header.NextAggregatorsHash
	return nextHeader
}

// GetRandomSignedHeader returns a signed header with random data
func GetRandomSignedHeader() (*SignedHeader, ed25519.PrivKey, error) {
	valSet, privKey := GetRandomValidatorSetWithPrivKey()
	signedHeader := &SignedHeader{
		Header:     GetRandomHeader(),
		Validators: valSet,
	}
	signedHeader.Header.ProposerAddress = valSet.Proposer.Address
	signedHeader.Header.AggregatorsHash = valSet.Hash()
	signedHeader.Header.NextAggregatorsHash = valSet.Hash()
	commit, err := getCommit(signedHeader.Header, privKey)
	if err != nil {
		return nil, nil, err
	}
	signedHeader.Commit = *commit
	return signedHeader, privKey, nil
}

// GetRandomNextSignedHeader returns a signed header with random data and height of +1 from
// the provided signed header
func GetRandomNextSignedHeader(signedHeader *SignedHeader, privKey ed25519.PrivKey) (*SignedHeader, error) {
	valSet := signedHeader.Validators
	newSignedHeader := &SignedHeader{
		Header:     GetRandomNextHeader(signedHeader.Header),
		Validators: valSet,
	}
	newSignedHeader.LastCommitHash = signedHeader.Commit.GetCommitHash(
		&newSignedHeader.Header, signedHeader.ProposerAddress,
	)
	commit, err := getCommit(newSignedHeader.Header, privKey)
	if err != nil {
		return nil, err
	}
	newSignedHeader.Commit = *commit
	return newSignedHeader, nil
}

// GetRandomTx returns a transaction with a random size between 100 and 200
// bytes.
func GetRandomTx() Tx {
	size := rand.Int()%100 + 100 //nolint:gosec
	return Tx(GetRandomBytes(size))
}

// GetRandomBytes returns a byte slice of random bytes of length n.
func GetRandomBytes(n int) []byte {
	data := make([]byte, n)
	_, _ = rand.Read(data) //nolint:gosec,staticcheck
	return data
}

func getCommit(header Header, privKey ed25519.PrivKey) (*Commit, error) {
	headerBytes, err := header.MarshalBinary()
	if err != nil {
		return nil, err
	}
	sign, err := privKey.Sign(headerBytes)
	if err != nil {
		return nil, err
	}
	return &Commit{
		Signatures: []Signature{sign},
	}, nil
}
