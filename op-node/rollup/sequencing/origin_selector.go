package sequencing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/log"

	"github.com/ethereum-optimism/optimism/op-node/rollup"
	"github.com/ethereum-optimism/optimism/op-node/rollup/derive"
	"github.com/ethereum-optimism/optimism/op-node/rollup/engine"
	"github.com/ethereum-optimism/optimism/op-node/rollup/event"
	"github.com/ethereum-optimism/optimism/op-service/eth"
)

type L1Blocks interface {
	derive.L1BlockRefByHashFetcher
	derive.L1BlockRefByNumberFetcher
}

type L1OriginSelector struct {
	ctx  context.Context
	log  log.Logger
	cfg  *rollup.Config
	spec *rollup.ChainSpec

	l1 L1Blocks

	// Internal cache of L1 origins for faster access.
	currentOrigin eth.L1BlockRef
	nextOrigin    eth.L1BlockRef

	mu sync.Mutex
}

func NewL1OriginSelector(ctx context.Context, log log.Logger, cfg *rollup.Config, l1 L1Blocks) *L1OriginSelector {
	return &L1OriginSelector{
		ctx:  ctx,
		log:  log,
		cfg:  cfg,
		spec: rollup.NewChainSpec(cfg),
		l1:   l1,
	}
}

func (los *L1OriginSelector) OnEvent(ev event.Event) bool {
	switch x := ev.(type) {
	case engine.ForkchoiceUpdateEvent:
		los.onForkchoiceUpdate(x.UnsafeL2Head)
	case rollup.ResetEvent, rollup.ForceResetEvent:
		los.reset()
	default:
		return false
	}
	return true
}

// FindL1Origin determines what the next L1 Origin should be.
// The L1 Origin is either the L2 Head's Origin, or the following L1 block
// if the next L2 block's time is greater than or equal to the L2 Head's Origin.
func (los *L1OriginSelector) FindL1Origin(ctx context.Context, l2Head eth.L2BlockRef) (eth.L1BlockRef, error) {
	currentOrigin, nextOrigin, err := los.CurrentAndNextOrigin(ctx, l2Head)
	if err != nil {
		los.log.Error("Failed to get current and next L1 origin", "err", err, "l2_head", l2Head)
		return eth.L1BlockRef{}, err
	}

	los.log.Info("Finding L1 origin for L2 block",
		"l2_head", l2Head.ID(),
		"l2_head_number", l2Head.Number,
		"l2_head_time", l2Head.Time,
		"current_origin", currentOrigin.ID(),
		"current_origin_number", currentOrigin.Number,
		"current_origin_time", currentOrigin.Time,
		"next_origin_exists", nextOrigin != eth.L1BlockRef{},
		"next_l2_time", l2Head.Time+los.cfg.BlockTime)

	if nextOrigin != (eth.L1BlockRef{}) {
		los.log.Info("Next origin details",
			"next_origin", nextOrigin.ID(),
			"next_origin_number", nextOrigin.Number,
			"next_origin_time", nextOrigin.Time)
	}

	// If the next L2 block time is greater than the next origin block's time, we can choose to
	// start building on top of the next origin. Sequencer implementation has some leeway here and
	// could decide to continue to build on top of the previous origin until the Sequencer runs out
	// of slack. For simplicity, we implement our Sequencer to always start building on the latest
	// L1 block when we can.
	if nextOrigin != (eth.L1BlockRef{}) && l2Head.Time+los.cfg.BlockTime >= nextOrigin.Time {
		los.log.Info("Using next origin for L2 block",
			"next_origin", nextOrigin.ID(),
			"next_origin_number", nextOrigin.Number,
			"l2_time", l2Head.Time+los.cfg.BlockTime,
			"next_origin_time", nextOrigin.Time)
		return nextOrigin, nil
	}

	msd := los.spec.MaxSequencerDrift(currentOrigin.Time)
	log := los.log.New("current", currentOrigin, "current_time", currentOrigin.Time,
		"l2_head", l2Head, "l2_head_time", l2Head.Time, "max_seq_drift", msd)

	pastSeqDrift := l2Head.Time+los.cfg.BlockTime-currentOrigin.Time > msd

	los.log.Info("Max sequencer drift check",
		"current_origin_time", currentOrigin.Time,
		"next_l2_time", l2Head.Time+los.cfg.BlockTime,
		"max_seq_drift", msd,
		"past_seq_drift", pastSeqDrift,
		"time_diff", l2Head.Time+los.cfg.BlockTime-currentOrigin.Time)

	// If we are not past the max sequencer drift, we can just return the current origin.
	if !pastSeqDrift {
		los.log.Info("Using current origin for L2 block (within max sequencer drift)",
			"current_origin", currentOrigin.ID(),
			"current_origin_number", currentOrigin.Number)
		return currentOrigin, nil
	}

	// Otherwise, we need to find the next L1 origin block in order to continue producing blocks.
	log.Warn("Next L2 block time is past the sequencer drift + current origin time")

	if nextOrigin == (eth.L1BlockRef{}) {
		los.log.Info("Next origin not set, fetching it now", "current_origin_number", currentOrigin.Number)
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		// If the next origin is not set, we need to fetch it now.
		nextOrigin, err = los.fetch(fetchCtx, currentOrigin.Number+1)
		if err != nil {
			los.log.Error("Failed to fetch next L1 origin",
				"current_origin", currentOrigin.ID(),
				"current_origin_number", currentOrigin.Number,
				"err", err)
			return eth.L1BlockRef{}, fmt.Errorf("cannot build next L2 block past current L1 origin %s by more than sequencer time drift, and failed to find next L1 origin: %w", currentOrigin, err)
		}
		los.log.Info("Fetched next origin",
			"next_origin", nextOrigin.ID(),
			"next_origin_number", nextOrigin.Number,
			"next_origin_time", nextOrigin.Time)
	}

	// If the next origin is ahead of the L2 head, we must return the current origin.
	if l2Head.Time+los.cfg.BlockTime < nextOrigin.Time {
		los.log.Info("Next L2 block time is before next origin time, using current origin",
			"current_origin", currentOrigin.ID(),
			"next_l2_time", l2Head.Time+los.cfg.BlockTime,
			"next_origin_time", nextOrigin.Time)
		return currentOrigin, nil
	}

	los.log.Info("Using next origin for L2 block (past max sequencer drift)",
		"next_origin", nextOrigin.ID(),
		"next_origin_number", nextOrigin.Number)
	return nextOrigin, nil
}

// CurrentAndNextOrigin returns the most recent origin known to the sequencer, and origin after
// that, based on the provided l2Head.
func (los *L1OriginSelector) CurrentAndNextOrigin(ctx context.Context, l2Head eth.L2BlockRef) (eth.L1BlockRef, eth.L1BlockRef, error) {
	los.log.Debug("Getting current and next origin", "l2_head", l2Head.ID(), "l2_head_number", l2Head.Number)

	los.mu.Lock()
	defer los.mu.Unlock()

	if l2Head.L1Origin == los.currentOrigin.ID() {
		// Most likely outcome: the L2 head is still on the current origin.
		current := los.currentOrigin
		next := los.nextOrigin
		los.log.Debug("Using cached L1 origins",
			"current_origin", current.ID(),
			"current_origin_number", current.Number,
			"next_origin_exists", next != eth.L1BlockRef{})
	} else if l2Head.L1Origin == los.nextOrigin.ID() {
		// If the L2 head has progressed to the next origin, update the current and next origins.
		los.currentOrigin = los.nextOrigin
		los.nextOrigin = eth.L1BlockRef{}
		current := los.currentOrigin
		los.log.Debug("L2 head progressed to next origin, updating cache",
			"new_current_origin", current.ID(),
			"new_current_origin_number", current.Number)
	} else {
		// If for some reason the L2 head is not on the current or next origin, we need to find the
		// current origin block and reset the next origin.
		// This is most likely to occur on the first block after a restart.
		los.log.Debug("L2 head origin not in cache, fetching",
			"l1_origin_hash", l2Head.L1Origin.Hash,
			"l1_origin_number", l2Head.L1Origin.Number)
		// Grab a reference to the current L1 origin block. This call is by hash and thus easily cached.
		currentOrigin, err := los.l1.L1BlockRefByHash(ctx, l2Head.L1Origin.Hash)
		if err != nil {
			los.log.Error("Failed to fetch current origin",
				"l1_origin_hash", l2Head.L1Origin.Hash,
				"l1_origin_number", l2Head.L1Origin.Number,
				"err", err)
			return eth.L1BlockRef{}, eth.L1BlockRef{}, err
		}
		los.log.Debug("Fetched current origin",
			"current_origin", currentOrigin.ID(),
			"current_origin_number", currentOrigin.Number,
			"current_origin_time", currentOrigin.Time)

		los.currentOrigin = currentOrigin
		los.nextOrigin = eth.L1BlockRef{}
	}

	return los.currentOrigin, los.nextOrigin, nil
}

func (los *L1OriginSelector) maybeSetNextOrigin(nextOrigin eth.L1BlockRef) {
	los.mu.Lock()
	defer los.mu.Unlock()

	// Set the next origin if it is the immediate child of the current origin.
	if nextOrigin.ParentHash == los.currentOrigin.Hash {
		los.nextOrigin = nextOrigin
	}
}

func (los *L1OriginSelector) onForkchoiceUpdate(unsafeL2Head eth.L2BlockRef) {
	// Only allow a relatively small window for fetching the next origin, as this is performed
	// on a best-effort basis.
	ctx, cancel := context.WithTimeout(los.ctx, 500*time.Millisecond)
	defer cancel()

	currentOrigin, nextOrigin, err := los.CurrentAndNextOrigin(ctx, unsafeL2Head)
	if err != nil {
		log.Error("Failed to get current and next L1 origin on forkchoice update", "err", err)
		return
	}

	los.tryFetchNextOrigin(ctx, currentOrigin, nextOrigin)
}

// tryFetchNextOrigin schedules a fetch for the next L1 origin block if it is not already set.
// This method always closes the channel, even if the next origin is already set.
func (los *L1OriginSelector) tryFetchNextOrigin(ctx context.Context, currentOrigin, nextOrigin eth.L1BlockRef) {
	// If the next origin is already set, we don't need to do anything.
	if nextOrigin != (eth.L1BlockRef{}) {
		return
	}

	// If the current origin is not set, we can't schedule the next origin check.
	if currentOrigin == (eth.L1BlockRef{}) {
		return
	}

	if _, err := los.fetch(ctx, currentOrigin.Number+1); err != nil {
		if errors.Is(err, ethereum.NotFound) {
			log.Debug("No next potential L1 origin found")
		} else {
			log.Error("Failed to get next origin", "err", err)
		}
	}
}

func (los *L1OriginSelector) fetch(ctx context.Context, number uint64) (eth.L1BlockRef, error) {
	// Attempt to find the next L1 origin block, where the next origin is the immediate child of
	// the current origin block.
	// The L1 source can be shimmed to hide new L1 blocks and enforce a sequencer confirmation distance.
	nextOrigin, err := los.l1.L1BlockRefByNumber(ctx, number)
	if err != nil {
		return eth.L1BlockRef{}, err
	}

	los.maybeSetNextOrigin(nextOrigin)

	return nextOrigin, nil
}

func (los *L1OriginSelector) reset() {
	los.mu.Lock()
	defer los.mu.Unlock()

	los.currentOrigin = eth.L1BlockRef{}
	los.nextOrigin = eth.L1BlockRef{}
}
