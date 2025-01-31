package keeper

import (
	"bytes"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth"
	"github.com/cosmos/cosmos-sdk/x/staking/exported"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// register all staking invariants
func RegisterInvariants(ir sdk.InvariantRegistry, k Keeper, f types.FeeCollectionKeeper,
	d types.DistributionKeeper, am types.AccountKeeper) {

	ir.RegisterRoute(types.ModuleName, "supply",
		SupplyInvariants(k, f, d, am))
	ir.RegisterRoute(types.ModuleName, "nonnegative-power",
		NonNegativePowerInvariant(k))
	ir.RegisterRoute(types.ModuleName, "positive-delegation",
		PositiveDelegationInvariant(k))
	ir.RegisterRoute(types.ModuleName, "delegator-shares",
		DelegatorSharesInvariant(k))
}

// AllInvariants runs all invariants of the staking module.
func AllInvariants(k Keeper, f types.FeeCollectionKeeper,
	d types.DistributionKeeper, am types.AccountKeeper) sdk.Invariant {

	return func(ctx sdk.Context) error {
		err := SupplyInvariants(k, f, d, am)(ctx)
		if err != nil {
			return err
		}

		err = NonNegativePowerInvariant(k)(ctx)
		if err != nil {
			return err
		}

		err = PositiveDelegationInvariant(k)(ctx)
		if err != nil {
			return err
		}

		err = DelegatorSharesInvariant(k)(ctx)
		if err != nil {
			return err
		}

		return nil
	}
}

// SupplyInvariants checks that the total supply reflects all held not-bonded tokens, bonded tokens, and unbonding delegations
// nolint: unparam
func SupplyInvariants(k Keeper, f types.FeeCollectionKeeper,
	d types.DistributionKeeper, am types.AccountKeeper) sdk.Invariant {

	return func(ctx sdk.Context) error {
		pool := k.GetPool(ctx)

		loose := sdk.ZeroDec()
		bonded := sdk.ZeroDec()
		am.IterateAccounts(ctx, func(acc auth.Account) bool {
			loose = loose.Add(acc.GetCoins().AmountOf(k.BondDenom(ctx)).ToDec())
			return false
		})
		k.IterateUnbondingDelegations(ctx, func(_ int64, ubd types.UnbondingDelegation) bool {
			for _, entry := range ubd.Entries {
				loose = loose.Add(entry.Balance.ToDec())
			}
			return false
		})
		k.IterateValidators(ctx, func(_ int64, validator exported.ValidatorI) bool {
			switch validator.GetStatus() {
			case sdk.Bonded:
				bonded = bonded.Add(validator.GetBondedTokens().ToDec())
			case sdk.Unbonding, sdk.Unbonded:
				loose = loose.Add(validator.GetTokens().ToDec())
			}
			// add yet-to-be-withdrawn
			loose = loose.Add(d.GetValidatorOutstandingRewardsCoins(ctx, validator.GetOperator()).AmountOf(k.BondDenom(ctx)))
			return false
		})

		// add outstanding fees
		loose = loose.Add(f.GetCollectedFees(ctx).AmountOf(k.BondDenom(ctx)).ToDec())

		// add community pool
		loose = loose.Add(d.GetFeePoolCommunityCoins(ctx).AmountOf(k.BondDenom(ctx)))

		// Not-bonded tokens should equal coin supply plus unbonding delegations
		// plus tokens on unbonded validators
		if !pool.NotBondedTokens.ToDec().Equal(loose) {
			return fmt.Errorf("loose token invariance:\n"+
				"\tpool.NotBondedTokens: %v\n"+
				"\tsum of account tokens: %v", pool.NotBondedTokens, loose)
		}

		// Bonded tokens should equal sum of tokens with bonded validators
		if !pool.BondedTokens.ToDec().Equal(bonded) {
			return fmt.Errorf("bonded token invariance:\n"+
				"\tpool.BondedTokens: %v\n"+
				"\tsum of account tokens: %v", pool.BondedTokens, bonded)
		}

		return nil
	}
}

// NonNegativePowerInvariant checks that all stored validators have >= 0 power.
func NonNegativePowerInvariant(k Keeper) sdk.Invariant {
	return func(ctx sdk.Context) error {
		iterator := k.ValidatorsPowerStoreIterator(ctx)

		for ; iterator.Valid(); iterator.Next() {
			validator, found := k.GetValidator(ctx, iterator.Value())
			if !found {
				panic(fmt.Sprintf("validator record not found for address: %X\n", iterator.Value()))
			}

			powerKey := types.GetValidatorsByPowerIndexKey(validator)

			if !bytes.Equal(iterator.Key(), powerKey) {
				return fmt.Errorf("power store invariance:\n\tvalidator.Power: %v"+
					"\n\tkey should be: %v\n\tkey in store: %v",
					validator.GetTendermintPower(), powerKey, iterator.Key())
			}

			if validator.Tokens.IsNegative() {
				return fmt.Errorf("negative tokens for validator: %v", validator)
			}
		}
		iterator.Close()
		return nil
	}
}

// PositiveDelegationInvariant checks that all stored delegations have > 0 shares.
func PositiveDelegationInvariant(k Keeper) sdk.Invariant {
	return func(ctx sdk.Context) error {
		delegations := k.GetAllDelegations(ctx)
		for _, delegation := range delegations {
			if delegation.Shares.IsNegative() {
				return fmt.Errorf("delegation with negative shares: %+v", delegation)
			}
			if delegation.Shares.IsZero() {
				return fmt.Errorf("delegation with zero shares: %+v", delegation)
			}
		}

		return nil
	}
}

// DelegatorSharesInvariant checks whether all the delegator shares which persist
// in the delegator object add up to the correct total delegator shares
// amount stored in each validator
func DelegatorSharesInvariant(k Keeper) sdk.Invariant {
	return func(ctx sdk.Context) error {
		validators := k.GetAllValidators(ctx)
		for _, validator := range validators {

			valTotalDelShares := validator.GetDelegatorShares()

			totalDelShares := sdk.ZeroDec()
			delegations := k.GetValidatorDelegations(ctx, validator.GetOperator())
			for _, delegation := range delegations {
				totalDelShares = totalDelShares.Add(delegation.Shares)
			}

			if !valTotalDelShares.Equal(totalDelShares) {
				return fmt.Errorf("broken delegator shares invariance:\n"+
					"\tvalidator.DelegatorShares: %v\n"+
					"\tsum of Delegator.Shares: %v", valTotalDelShares, totalDelShares)
			}
		}
		return nil
	}
}
