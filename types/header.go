package types

import (
	"bytes"
	"encoding"
	"fmt"
	"time"

	"github.com/celestiaorg/go-header"
)

// Hash is a 32-byte array which is used to represent a hash result.
type Hash = header.Hash

// BaseHeader contains the most basic data of a header
type BaseHeader struct {
	// Height represents the block height (aka block number) of a given header
	Height uint64
	// Time contains Unix nanotime of a block
	Time uint64
	// The Chain ID
	ChainID string
}

// Header defines the structure of Rollkit block header.
type Header struct {
	BaseHeader
	// Block and App version
	Version Version

	// prev block info
	LastHeaderHash Hash

	// hashes of block data
	LastCommitHash Hash // commit from aggregator(s) from the last block
	DataHash       Hash // Block.Data root aka Transactions
	ConsensusHash  Hash // consensus params for current block
	AppHash        Hash // state after applying txs from the current block

	// Root hash of all results from the txs from the previous block.
	// This is ABCI specific but smart-contract chains require some way of committing
	// to transaction receipts/results.
	LastResultsHash Hash

	// Note that the address can be derived from the pubkey which can be derived
	// from the signature when using secp256k.
	// We keep this in case users choose another signature format where the
	// pubkey can't be recovered by the signature (e.g. ed25519).
	ProposerAddress []byte // original proposer of the block

	// Hash of block aggregator set, at a time of block creation
	AggregatorsHash Hash

	// Hash of next block aggregator set, at a time of block creation
	NextAggregatorsHash Hash
}

// New creates a new Header.
func (h *Header) New() *Header {
	return new(Header)
}

// IsZero returns true if the header is nil.
func (h *Header) IsZero() bool {
	return h == nil
}

// ChainID returns chain ID of the header.
func (h *Header) ChainID() string {
	return h.BaseHeader.ChainID
}

// Height returns height of the header.
func (h *Header) Height() uint64 {
	return h.BaseHeader.Height
}

// LastHeader returns last header hash of the header.
func (h *Header) LastHeader() Hash {
	return h.LastHeaderHash[:]
}

// Time returns timestamp as unix time with nanosecond precision
func (h *Header) Time() time.Time {
	return time.Unix(0, int64(h.BaseHeader.Time))
}

// Verify verifies the header.
func (h *Header) Verify(untrstH *Header) error {
	// perform actual verification
	if untrstH.Height() == h.Height()+1 {
		// Check the validator hashes are the same in the case headers are adjacent
		if !bytes.Equal(untrstH.AggregatorsHash[:], h.NextAggregatorsHash[:]) {
			return &header.VerifyError{
				Reason: fmt.Errorf("expected old header validators (%X) to match those from new header (%X)",
					h.NextAggregatorsHash,
					untrstH.AggregatorsHash,
				),
			}
		}
	}

	// TODO: There must be a way to verify non-adjacent headers
	// Ensure that untrusted commit has enough of trusted commit's power.
	// err := h.ValidatorSet.VerifyCommitLightTrusting(eh.ChainID, untrst.Commit, light.DefaultTrustLevel)
	// if err != nil {
	// 	return &VerifyError{err}
	// }

	return nil
}

// Validate performs basic validation of a header.
func (h *Header) Validate() error {
	return h.ValidateBasic()
}

// ValidateBasic performs basic validation of a header.
func (h *Header) ValidateBasic() error {
	if len(h.ProposerAddress) == 0 {
		return ErrNoProposerAddress
	}

	return nil
}

var _ header.Header[*Header] = &Header{}
var _ encoding.BinaryMarshaler = &Header{}
var _ encoding.BinaryUnmarshaler = &Header{}
