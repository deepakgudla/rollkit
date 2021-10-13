package block

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/tendermint/tendermint/proxy"
	tmtypes "github.com/tendermint/tendermint/types"

	"github.com/celestiaorg/optimint/config"
	"github.com/celestiaorg/optimint/da"
	"github.com/celestiaorg/optimint/log"
	"github.com/celestiaorg/optimint/mempool"
	"github.com/celestiaorg/optimint/state"
	"github.com/celestiaorg/optimint/store"
	"github.com/celestiaorg/optimint/types"
)

// Manager is responsible for aggregating transactions into blocks.
type Manager struct {
	lastState state.State

	conf    config.BlockManagerConfig
	genesis *tmtypes.GenesisDoc

	proposerKey crypto.PrivKey

	store    store.Store
	executor *state.BlockExecutor

	dalc      da.DataAvailabilityLayerClient
	retriever da.BlockRetriever

	HeaderOutCh chan *types.Header
	HeaderInCh  chan *types.Header

	syncTarget uint64
	blockInCh  chan *types.Block
	retrieveCh chan uint64
	syncCache  map[uint64]*types.Block

	logger log.Logger
}

// getInitialState tries to load lastState from Store, and if it's not available it reads GenesisDoc.
func getInitialState(store store.Store, genesis *tmtypes.GenesisDoc) (state.State, error) {
	s, err := store.LoadState()
	if err != nil {
		s, err = state.NewFromGenesisDoc(genesis)
	}
	return s, err
}

func NewManager(
	proposerKey crypto.PrivKey,
	conf config.BlockManagerConfig,
	genesis *tmtypes.GenesisDoc,
	store store.Store,
	mempool mempool.Mempool,
	proxyApp proxy.AppConnConsensus,
	dalc da.DataAvailabilityLayerClient,
	logger log.Logger,
) (*Manager, error) {
	s, err := getInitialState(store, genesis)
	if err != nil {
		return nil, err
	}

	proposerAddress, err := proposerKey.GetPublic().Raw()
	if err != nil {
		return nil, err
	}

	exec := state.NewBlockExecutor(proposerAddress, conf.NamespaceID, mempool, proxyApp, logger)

	agg := &Manager{
		proposerKey: proposerKey,
		conf:        conf,
		genesis:     genesis,
		lastState:   s,
		store:       store,
		executor:    exec,
		dalc:        dalc,
		retriever:   dalc.(da.BlockRetriever), // TODO(tzdybal): do it in more gentle way (after MVP)
		HeaderOutCh: make(chan *types.Header),
		HeaderInCh:  make(chan *types.Header),
		blockInCh:   make(chan *types.Block),
		retrieveCh:  make(chan uint64),
		syncCache:   make(map[uint64]*types.Block),
		logger:      logger,
	}

	return agg, nil
}

func (m *Manager) SetDALC(dalc da.DataAvailabilityLayerClient) {
	m.dalc = dalc
	m.retriever = dalc.(da.BlockRetriever)
}

func (m *Manager) AggregationLoop(ctx context.Context) {
	timer := time.NewTimer(m.conf.BlockTime)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			start := time.Now()
			err := m.publishBlock(ctx)
			if err != nil {
				m.logger.Error("error while publishing block", "error", err)
			}
			timer.Reset(m.getRemainingSleep(start))
		}
	}
}

func (m *Manager) SyncLoop(ctx context.Context) {
	for {
		select {
		case header := <-m.HeaderInCh:
			m.logger.Debug("block header received", "height", header.Height, "hash", header.Hash())
			newHeight := header.Height
			currentHeight := m.store.Height()
			// in case of client reconnecting after being offline
			// newHeight may be significantly larger than currentHeight
			// it's handled gently in RetrieveLoop
			if newHeight > currentHeight {
				atomic.StoreUint64(&m.syncTarget, newHeight)
				m.retrieveCh <- newHeight
			}
		case block := <-m.blockInCh:
			m.logger.Debug("block body retrieved from DALC",
				"height", block.Header.Height,
				"hash", block.Hash(),
			)
			m.syncCache[block.Header.Height] = block
			currentHeight := m.store.Height() // TODO(tzdybal): maybe store a copy in memory
			b1, ok1 := m.syncCache[currentHeight+1]
			b2, ok2 := m.syncCache[currentHeight+2]
			if ok1 && ok2 {
				newState, _, err := m.executor.ApplyBlock(ctx, m.lastState, b1)
				if err != nil {
					m.logger.Error("failed to ApplyBlock", "error", err)
					continue
				}
				err = m.store.SaveBlock(b1, &b2.LastCommit)
				if err != nil {
					m.logger.Error("failed to save block", "error", err)
					continue
				}

				m.lastState = newState
				err = m.store.UpdateState(m.lastState)
				if err != nil {
					m.logger.Error("failed to save updated state", "error", err)
					continue
				}
				delete(m.syncCache, currentHeight+1)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) RetrieveLoop(ctx context.Context) {
	for {
		select {
		case <-m.retrieveCh:
			target := atomic.LoadUint64(&m.syncTarget)
			for h := m.store.Height() + 1; h <= target; h++ {
				m.logger.Debug("trying to retrieve block from DALC", "height", h)
				m.mustRetrieveBlock(ctx, h)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) mustRetrieveBlock(ctx context.Context, height uint64) {
	// TOOD(tzdybal): extract configuration option
	maxRetries := 10

	for r := 0; r < maxRetries; r++ {
		err := m.fetchBlock(ctx, height)
		if err == nil {
			return
		}
		// TODO(tzdybal): configuration option
		// TODO(tzdybal): exponential backoff
		time.Sleep(100 * time.Millisecond)
	}
	// TODO(tzdybal): this is only temporary solution, for MVP
	panic("failed to retrieve block with DALC")
}

func (m *Manager) fetchBlock(ctx context.Context, height uint64) error {
	var err error
	blockRes := m.retriever.RetrieveBlock(height)
	switch blockRes.Code {
	case da.StatusSuccess:
		m.blockInCh <- blockRes.Block
	case da.StatusError:
		err = fmt.Errorf("failed to retrieve block: %s", blockRes.Message)
	case da.StatusTimeout:
		err = fmt.Errorf("timeout during retrieve block: %s", blockRes.Message)
	}
	return err
}

func (m *Manager) getRemainingSleep(start time.Time) time.Duration {
	publishingDuration := time.Since(start)
	sleepDuration := m.conf.BlockTime - publishingDuration
	if sleepDuration < 0 {
		sleepDuration = 0
	}
	return sleepDuration
}

func (m *Manager) publishBlock(ctx context.Context) error {
	var lastCommit *types.Commit
	var err error
	newHeight := m.store.Height() + 1

	// this is a special case, when first block is produced - there is no previous commit
	if newHeight == uint64(m.genesis.InitialHeight) {
		lastCommit = &types.Commit{Height: m.store.Height(), HeaderHash: [32]byte{}}
	} else {
		lastCommit, err = m.store.LoadCommit(m.store.Height())
		if err != nil {
			return fmt.Errorf("error while loading last commit: %w", err)
		}
	}

	m.logger.Info("Creating and publishing block", "height", newHeight)

	block := m.executor.CreateBlock(newHeight, lastCommit, m.lastState)
	m.logger.Debug("block info", "num_tx", len(block.Data.Txs))
	newState, _, err := m.executor.ApplyBlock(ctx, m.lastState, block)
	if err != nil {
		return err
	}

	headerBytes, err := block.Header.MarshalBinary()
	if err != nil {
		return err
	}
	sign, err := m.proposerKey.Sign(headerBytes)
	if err != nil {
		return err
	}

	commit := &types.Commit{
		Height:     block.Header.Height,
		HeaderHash: block.Header.Hash(),
		Signatures: []types.Signature{sign},
	}
	err = m.store.SaveBlock(block, commit)
	if err != nil {
		return err
	}

	m.lastState = newState
	err = m.store.UpdateState(m.lastState)
	if err != nil {
		return err
	}

	return m.broadcastBlock(ctx, block)
}

func (m *Manager) broadcastBlock(ctx context.Context, block *types.Block) error {
	res := m.dalc.SubmitBlock(block)
	if res.Code != da.StatusSuccess {
		return fmt.Errorf("DA layer submission failed: %s", res.Message)
	}

	m.HeaderOutCh <- &block.Header

	return nil
}
