// Copyright (c) 2024 IoTeX Foundation
// This source code is provided 'as is' and no warranties are given as to title or non-infringement, merchantability
// or fitness for purpose and, to the extent permitted by law, all liability for your use of the code is disclaimed.
// This source code is governed by Apache License 2.0 that can be found in the LICENSE file.

package action

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/iotexproject/iotex-proto/golang/iotextypes"
	"github.com/pkg/errors"

	"github.com/iotexproject/iotex-core/pkg/log"
)

type (
	// Envelope defines an envelope wrapped on action with some envelope metadata.
	Envelope interface {
		TxData
		Version() uint32
		ChainID() uint32
		Destination() (string, bool)
		Cost() (*big.Int, error)
		IntrinsicGas() (uint64, error)
		Size() uint32
		Action() Action
		ToEthTx(uint32, iotextypes.Encoding) (*types.Transaction, error)
		Proto() *iotextypes.ActionCore
		LoadProto(*iotextypes.ActionCore) error
		SetNonce(uint64)
		SetGas(uint64)
		SetChainID(uint32)
		SanityCheck() error
	}

	// TxData is the interface required to execute a transaction by EVM
	// It follows the same-name interface in go-ethereum
	TxData interface {
		TxCommon
		Value() *big.Int
		To() *common.Address
		Data() []byte
	}

	TxCommon interface {
		Nonce() uint64
		Gas() uint64
		GasPrice() *big.Int
		EffectiveGasPrice(*big.Int) *big.Int
		TxDynamicGas
		AccessList() types.AccessList
		TxBlob
	}

	TxCommonInternal interface {
		TxCommon
		Version() uint32
		ChainID() uint32
		SanityCheck() error
		toProto() *iotextypes.ActionCore
		setNonce(uint64)
		setGas(uint64)
		setChainID(uint32)
		toEthTx(*common.Address, *big.Int, []byte) *types.Transaction
	}

	TxDynamicGas interface {
		GasTipCap() *big.Int
		GasFeeCap() *big.Int
	}

	TxBlob interface {
		BlobGas() uint64
		BlobGasFeeCap() *big.Int
		BlobHashes() []common.Hash
		BlobTxSidecar() *types.BlobTxSidecar
	}

	envelope struct {
		common  TxCommonInternal
		payload actionPayload
	}
)

func (elp *envelope) Version() uint32 {
	return elp.common.Version()
}

func (elp *envelope) ChainID() uint32 {
	return elp.common.ChainID()
}

func (elp *envelope) Nonce() uint64 {
	return elp.common.Nonce()
}

func (elp *envelope) Gas() uint64 {
	return elp.common.Gas()
}

func (elp *envelope) GasPrice() *big.Int {
	return elp.common.GasPrice()
}

func (elp *envelope) EffectiveGasPrice(baseFee *big.Int) *big.Int {
	return elp.common.EffectiveGasPrice(baseFee)
}

func (elp *envelope) AccessList() types.AccessList {
	return elp.common.AccessList()
}

func (elp *envelope) GasTipCap() *big.Int {
	return elp.common.GasTipCap()
}

func (elp *envelope) GasFeeCap() *big.Int {
	return elp.common.GasFeeCap()
}

func (elp *envelope) BlobGas() uint64 {
	return elp.common.BlobGas()
}

func (elp *envelope) BlobGasFeeCap() *big.Int {
	return elp.common.BlobGasFeeCap()
}

func (elp *envelope) BlobHashes() []common.Hash {
	return elp.common.BlobHashes()
}

func (elp *envelope) BlobTxSidecar() *types.BlobTxSidecar {
	return elp.common.BlobTxSidecar()
}

func (elp *envelope) Value() *big.Int {
	if exec, ok := elp.Action().(*Execution); ok {
		return exec.Value()
	}
	return nil
}

func (elp *envelope) To() *common.Address {
	if exec, ok := elp.Action().(*Execution); ok {
		return exec.To()
	}
	return nil
}

func (elp *envelope) Data() []byte {
	if exec, ok := elp.Action().(*Execution); ok {
		return exec.data
	}
	return nil
}

// Destination returns the destination address
func (elp *envelope) Destination() (string, bool) {
	r, ok := elp.payload.(hasDestination)
	if !ok {
		return "", false
	}

	return r.Destination(), true
}

// Cost returns cost of the action
func (elp *envelope) Cost() (*big.Int, error) {
	gas, err := elp.payload.IntrinsicGas()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get payload's intrinsic gas")
	}
	if _, ok := elp.payload.(gasLimitForCost); ok {
		gas = elp.common.Gas()
	}
	if acl := elp.AccessList(); len(acl) > 0 {
		gas += uint64(len(acl)) * TxAccessListAddressGas
		gas += uint64(acl.StorageKeys()) * TxAccessListStorageKeyGas
	}
	cost := new(big.Int).SetUint64(gas)
	cost.Mul(cost, elp.GasPrice())
	if afc, ok := elp.payload.(amountForCost); ok {
		if amount := afc.Amount(); amount != nil {
			cost.Add(cost, amount)
		}
	}
	// account for blob fee
	if len(elp.BlobHashes()) > 0 {
		cost.Add(cost, new(big.Int).Mul(elp.BlobGasFeeCap(), new(big.Int).SetUint64(elp.BlobGas())))
	}
	return cost, nil
}

// IntrinsicGas returns intrinsic gas of action.
func (elp *envelope) IntrinsicGas() (uint64, error) {
	gas, err := elp.payload.IntrinsicGas()
	if err != nil {
		return 0, err
	}
	if acl := elp.AccessList(); len(acl) > 0 {
		gas += uint64(len(acl)) * TxAccessListAddressGas
		gas += uint64(acl.StorageKeys()) * TxAccessListStorageKeyGas
	}
	return gas, nil
}

// Size returns the size of envelope
func (elp *envelope) Size() uint32 {
	// VersionSizeInBytes + NonceSizeInBytes + GasSizeInBytes
	var size uint32 = 4 + 8 + 8
	if gasPrice := elp.common.GasPrice(); gasPrice != nil {
		size += uint32(len(gasPrice.Bytes()))
	}
	if s, ok := elp.payload.(hasSize); ok {
		size += s.Size()
	}
	return size
}

// Action returns the action payload.
func (elp *envelope) Action() Action { return elp.payload }

// ToEthTx converts to Ethereum tx
func (elp *envelope) ToEthTx(evmNetworkID uint32, encoding iotextypes.Encoding) (*types.Transaction, error) {
	tx, ok := elp.Action().(EthCompatibleAction)
	if !ok {
		// action type not supported
		return nil, ErrInvalidAct
	}
	to, err := tx.EthTo()
	if err != nil {
		return nil, err
	}
	data, err := tx.EthData()
	if err != nil {
		return nil, err
	}
	return elp.common.toEthTx(to, tx.Value(), data), nil
}

// Proto convert Envelope to protobuf format.
func (elp *envelope) Proto() *iotextypes.ActionCore {
	actCore := elp.common.toProto()

	// TODO assert each action
	switch act := elp.Action().(type) {
	case *Transfer:
		actCore.Action = &iotextypes.ActionCore_Transfer{Transfer: act.Proto()}
	case *Execution:
		actCore.Action = &iotextypes.ActionCore_Execution{Execution: act.Proto()}
	case *GrantReward:
		actCore.Action = &iotextypes.ActionCore_GrantReward{GrantReward: act.Proto()}
	case *ClaimFromRewardingFund:
		actCore.Action = &iotextypes.ActionCore_ClaimFromRewardingFund{ClaimFromRewardingFund: act.Proto()}
	case *DepositToRewardingFund:
		actCore.Action = &iotextypes.ActionCore_DepositToRewardingFund{DepositToRewardingFund: act.Proto()}
	case *PutPollResult:
		actCore.Action = &iotextypes.ActionCore_PutPollResult{PutPollResult: act.Proto()}
	case *CreateStake:
		actCore.Action = &iotextypes.ActionCore_StakeCreate{StakeCreate: act.Proto()}
	case *Unstake:
		actCore.Action = &iotextypes.ActionCore_StakeUnstake{StakeUnstake: act.Proto()}
	case *WithdrawStake:
		actCore.Action = &iotextypes.ActionCore_StakeWithdraw{StakeWithdraw: act.Proto()}
	case *DepositToStake:
		actCore.Action = &iotextypes.ActionCore_StakeAddDeposit{StakeAddDeposit: act.Proto()}
	case *Restake:
		actCore.Action = &iotextypes.ActionCore_StakeRestake{StakeRestake: act.Proto()}
	case *ChangeCandidate:
		actCore.Action = &iotextypes.ActionCore_StakeChangeCandidate{StakeChangeCandidate: act.Proto()}
	case *TransferStake:
		actCore.Action = &iotextypes.ActionCore_StakeTransferOwnership{StakeTransferOwnership: act.Proto()}
	case *CandidateRegister:
		actCore.Action = &iotextypes.ActionCore_CandidateRegister{CandidateRegister: act.Proto()}
	case *CandidateUpdate:
		actCore.Action = &iotextypes.ActionCore_CandidateUpdate{CandidateUpdate: act.Proto()}
	case *CandidateActivate:
		actCore.Action = &iotextypes.ActionCore_CandidateActivate{CandidateActivate: act.Proto()}
	case *CandidateEndorsement:
		actCore.Action = &iotextypes.ActionCore_CandidateEndorsement{CandidateEndorsement: act.Proto()}
	case *CandidateTransferOwnership:
		actCore.Action = &iotextypes.ActionCore_CandidateTransferOwnership{CandidateTransferOwnership: act.Proto()}
	case *txContainer:
		actCore.Action = &iotextypes.ActionCore_TxContainer{TxContainer: act.proto()}
	case *MigrateStake:
		actCore.Action = &iotextypes.ActionCore_StakeMigrate{StakeMigrate: act.Proto()}
	default:
		log.S().Panicf("Cannot convert type of action %T.\r\n", act)
	}
	return actCore
}

// LoadProto loads fields from protobuf format.
func (elp *envelope) LoadProto(pbAct *iotextypes.ActionCore) error {
	if pbAct == nil {
		return ErrNilProto
	}
	if elp == nil {
		return ErrNilAction
	}
	if err := elp.loadProtoTxCommon(pbAct); err != nil {
		return err
	}
	return elp.loadProtoActionPayload(pbAct)
}

func (elp *envelope) loadProtoTxCommon(pbAct *iotextypes.ActionCore) error {
	var err error
	switch pbAct.Version {
	case LegacyTxType:
		elp.common, err = fromProtoLegacyTx(pbAct)
	case AccessListTxType:
		elp.common, err = fromProtoAccessListTx(pbAct)
	case DynamicFeeTxType:
		tx := &DynamicFeeTx{}
		if err = tx.fromProto(pbAct); err == nil {
			elp.common = tx
		}
	case BlobTxType:
		tx := BlobTx{}
		if err = tx.fromProto(pbAct); err == nil {
			elp.common = &tx
		}
	default:
		panic(fmt.Sprintf("unsupported action version = %d", pbAct.Version))
	}
	return err
}

func (elp *envelope) loadProtoActionPayload(pbAct *iotextypes.ActionCore) error {
	switch {
	case pbAct.GetTransfer() != nil:
		act := &Transfer{}
		if err := act.LoadProto(pbAct.GetTransfer()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetExecution() != nil:
		act := &Execution{}
		if err := act.LoadProto(pbAct.GetExecution()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetGrantReward() != nil:
		act := &GrantReward{}
		if err := act.LoadProto(pbAct.GetGrantReward()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetClaimFromRewardingFund() != nil:
		act := &ClaimFromRewardingFund{}
		if err := act.LoadProto(pbAct.GetClaimFromRewardingFund()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetDepositToRewardingFund() != nil:
		act := &DepositToRewardingFund{}
		if err := act.LoadProto(pbAct.GetDepositToRewardingFund()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetPutPollResult() != nil:
		act := &PutPollResult{}
		if err := act.LoadProto(pbAct.GetPutPollResult()); err != nil {
			return err
		}
		elp.payload = act

	case pbAct.GetStakeCreate() != nil:
		act := &CreateStake{}
		if err := act.LoadProto(pbAct.GetStakeCreate()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeUnstake() != nil:
		act := &Unstake{}
		if err := act.LoadProto(pbAct.GetStakeUnstake()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeWithdraw() != nil:
		act := &WithdrawStake{}
		if err := act.LoadProto(pbAct.GetStakeWithdraw()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeAddDeposit() != nil:
		act := &DepositToStake{}
		if err := act.LoadProto(pbAct.GetStakeAddDeposit()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeRestake() != nil:
		act := &Restake{}
		if err := act.LoadProto(pbAct.GetStakeRestake()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeChangeCandidate() != nil:
		act := &ChangeCandidate{}
		if err := act.LoadProto(pbAct.GetStakeChangeCandidate()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeTransferOwnership() != nil:
		act := &TransferStake{}
		if err := act.LoadProto(pbAct.GetStakeTransferOwnership()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetCandidateRegister() != nil:
		act := &CandidateRegister{}
		if err := act.LoadProto(pbAct.GetCandidateRegister()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetCandidateUpdate() != nil:
		act := &CandidateUpdate{}
		if err := act.LoadProto(pbAct.GetCandidateUpdate()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetCandidateActivate() != nil:
		act := &CandidateActivate{}
		if err := act.LoadProto(pbAct.GetCandidateActivate()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetCandidateEndorsement() != nil:
		act := &CandidateEndorsement{}
		if err := act.LoadProto(pbAct.GetCandidateEndorsement()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetCandidateTransferOwnership() != nil:
		act := &CandidateTransferOwnership{}
		if err := act.LoadProto(pbAct.GetCandidateTransferOwnership()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetTxContainer() != nil:
		act := &txContainer{}
		if err := act.loadProto(pbAct.GetTxContainer()); err != nil {
			return err
		}
		elp.payload = act
	case pbAct.GetStakeMigrate() != nil:
		act := &MigrateStake{}
		if err := act.LoadProto(pbAct.GetStakeMigrate()); err != nil {
			return err
		}
		elp.payload = act
	default:
		return errors.Errorf("no applicable action to handle proto type %T", pbAct.Action)
	}
	return nil
}

func (elp *envelope) SetNonce(n uint64) {
	elp.common.setNonce(n)
}

func (elp *envelope) SetGas(gas uint64) {
	elp.common.setGas(gas)
}

// SetChainID sets the chainID value
func (elp *envelope) SetChainID(chainID uint32) {
	elp.common.setChainID(chainID)
}

// SanityCheck does the sanity check
func (elp *envelope) SanityCheck() error {
	if err := elp.payload.SanityCheck(); err != nil {
		return err
	}
	return elp.common.SanityCheck()
}
