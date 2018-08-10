package service

import (
	"math"
	"bytes"
	"fmt"

	"github.com/elastos/Elastos.ELA.SideChain/smartcontract/storage"
	"github.com/elastos/Elastos.ELA.SideChain/vm"
	"github.com/elastos/Elastos.ELA.SideChain/core"
	"github.com/elastos/Elastos.ELA.SideChain/smartcontract/errors"
	"github.com/elastos/Elastos.ELA.SideChain/smartcontract/states"
	"github.com/elastos/Elastos.ELA.SideChain/blockchain"
	"github.com/elastos/Elastos.ELA.SideChain/store"
	"github.com/elastos/Elastos.ELA.SideChain/contract"

	"github.com/elastos/Elastos.ELA.Utility/crypto"
	"github.com/elastos/Elastos.ELA.Utility/common"
)

type StateMachine struct {
	*StateReader
	CloneCache *storage.CloneCache
}

func NewStateMachine(dbCache storage.DBCache, innerCache storage.DBCache) *StateMachine {
	var stateMachine StateMachine
	stateMachine.CloneCache = storage.NewCloneDBCache(innerCache, dbCache)
	stateMachine.StateReader = NewStateReader()

	stateMachine.StateReader.Register("Neo.Asset.Create", stateMachine.CreateAsset)
	stateMachine.StateReader.Register("Neo.Contract.Create", stateMachine.CreateContract)
	stateMachine.StateReader.Register("Neo.Blockchain.GetContract", stateMachine.GetContract)
	stateMachine.StateReader.Register("Neo.Asset.Renew", stateMachine.AssetRenew)
	stateMachine.StateReader.Register("Neo.Storage.Get", stateMachine.StorageGet)
	stateMachine.StateReader.Register("Neo.Contract.Destroy", stateMachine.ContractDestory)
	stateMachine.StateReader.Register("Neo.Storage.Put", stateMachine.StoragePut)
	stateMachine.StateReader.Register("Neo.Storage.Delete", stateMachine.StorageDelete)
	stateMachine.StateReader.Register("Neo.Contract.GetStorageContext", stateMachine.GetStorageContext)

	return &stateMachine
}

func (s *StateMachine) CreateAsset(engine *vm.ExecutionEngine) (bool, error) {
	tx := engine.GetDataContainer().(*core.Transaction)
	assetID := tx.Hash()
	assetType := core.AssetType(vm.PopInt(engine))
	name := vm.PopByteArray(engine)
	if len(name) > 1024 {
		return false, errors.ErrAssetNameInvalid
	}
	amount := vm.PopBigInt(engine)
	if amount.Int64() == 0 {
		return false, errors.ErrAssetAmountInvalid
	}
	precision := vm.PopBigInt(engine)
	if precision.Int64() > 8 {
		return false, errors.ErrAssetPrecisionInvalid
	}
	if amount.Int64()%int64(math.Pow(10, 8-float64(precision.Int64()))) != 0 {
		return false, errors.ErrAssetAmountInvalid
	}
	ownerByte := vm.PopByteArray(engine)
	owner, err := crypto.DecodePoint(ownerByte)
	if err != nil {
		return false, err
	}
	if result, err := s.StateReader.CheckWitnessPublicKey(engine, owner); !result {
		return false, err
	}
	adminByte := vm.PopByteArray(engine)
	admin, err := common.Uint168FromBytes(adminByte)
	if err != nil {
		return false, err
	}
	issueByte := vm.PopByteArray(engine)
	issue, err := common.Uint168FromBytes(issueByte)
	if err != nil {
		return false, err
	}

	assetState := &states.AssetState{
		AssetId:    assetID,
		AssetType:  core.AssetType(assetType),
		Name:       string(name),
		Amount:     common.Fixed64(amount.Int64()),
		Precision:  byte(precision.Int64()),
		Admin:      *admin,
		Issuer:     *issue,
		Owner:      owner,
		Expiration: blockchain.DefaultLedger.Store.GetHeight() + 1 + 2000000,
		IsFrozen:   false,
	}
	s.CloneCache.GetInnerCache().GetWriteSet().Add(store.ST_AssetState, string(assetID.Bytes()), assetState)
	vm.PushData(engine, assetState)
	return true, nil
}

func (s *StateMachine) CreateContract(engine *vm.ExecutionEngine) (bool, error) {
	codeByte := vm.PopByteArray(engine)
	if len(codeByte) > 1024*1024 {
		return false, nil
	}
	parameters := vm.PopByteArray(engine)
	if len(parameters) > 252 {
		return false, nil
	}
	parameterList := make([]contract.ContractParameterType, 0)
	for _, v := range parameters {
		parameterList = append(parameterList, contract.ContractParameterType(v))
	}
	returnType := vm.PopInt(engine)
	nameByte := vm.PopByteArray(engine)
	if len(nameByte) > 252 {
		return false, nil
	}
	versionByte := vm.PopByteArray(engine)
	if len(versionByte) > 252 {
		return false, nil
	}
	authorByte := vm.PopByteArray(engine)
	if len(authorByte) > 252 {
		return false, nil
	}
	emailByte := vm.PopByteArray(engine)
	if len(emailByte) > 252 {
		return false, nil
	}
	descByte := vm.PopByteArray(engine)
	if len(descByte) > 65536 {
		return false, nil
	}
	funcCode := &contract.FunctionCode{
		Code:           codeByte,
		ParameterTypes: parameterList,
		ReturnType:     contract.ContractParameterType(returnType),
	}
	contractState := states.ContractState{
		Code:        funcCode,
		Name:        common.BytesToHexString(nameByte),
		Version:     common.BytesToHexString(versionByte),
		Author:      common.BytesToHexString(authorByte),
		Email:       common.BytesToHexString(emailByte),
		Description: common.BytesToHexString(descByte),
	}
	codeHash, err := common.Uint168FromBytes(codeByte)
	if err != nil {
		return false, err
	}
	s.CloneCache.GetInnerCache().GetOrAdd(store.ST_Contract, string(codeHash.Bytes()), &contractState)
	vm.PushData(engine, contractState)
	return true, nil
}

func (s *StateMachine) GetContract(engine *vm.ExecutionEngine) (bool, error) {
	hashByte := vm.PopByteArray(engine)
	hash, err := common.Uint168FromBytes(hashByte)
	if err != nil {
		return false, err
	}
	item, err := s.CloneCache.TryGet(store.ST_Contract, storage.KeyToStr(hash))
	if err != nil {
		return false, err
	}
	vm.PushData(engine, item.(*states.ContractState))
	return true, nil
}

func (s *StateMachine) AssetRenew(engine *vm.ExecutionEngine) (bool, error) {
	data := vm.PopInteropInterface(engine)
	years := vm.PopInt(engine)
	at := data.(*states.AssetState)
	height := blockchain.DefaultLedger.Store.GetHeight() + 1
	b := new(bytes.Buffer)
	at.AssetId.Serialize(b)
	state, err := s.CloneCache.TryGet(store.ST_AssetState, b.String())
	if err != nil {
		return false, err
	}
	assetState := state.(*states.AssetState)
	if assetState.Expiration < height {
		assetState.Expiration = height
	}
	assetState.Expiration += uint32(years) * 2000000
	return true, nil
}

func (s *StateMachine) ContractDestory(engine *vm.ExecutionEngine) (bool, error) {
	data := engine.ExecutingScript()
	if data == nil {
		return false, nil
	}

	hash, err := common.Uint168FromBytes(data)
	if err != nil {
		return false, err
	}
	keyStr := storage.KeyToStr(hash)
	item, err := s.CloneCache.TryGet(store.ST_Contract, keyStr)
	if err != nil || item == nil {
		return false, err
	}
	s.CloneCache.GetInnerCache().GetWriteSet().Delete(keyStr)
	return true, nil
}

func (s *StateMachine) CheckStorageContext(context *StorageContext) (bool, error) {
	item, err := s.CloneCache.TryGet(store.ST_Contract, string(context.codeHash.Bytes()))
	if err != nil {
		return false, err
	}
	if item == nil {
		return false, fmt.Errorf("check storage context fail, codehash=%v", context.codeHash)
	}
	return true, nil
}

func (s *StateMachine) StorageGet(engine *vm.ExecutionEngine) (bool, error) {
	opInterface := vm.PopInteropInterface(engine)
	context := opInterface.(*StorageContext)
	if exist, err := s.CheckStorageContext(context); !exist {
		return false, err
	}
	key := vm.PopByteArray(engine)
	storageKey := states.NewStorageKey(context.codeHash, key)
	item, err := s.CloneCache.TryGet(store.ST_Storage, storage.KeyToStr(storageKey))
	if err != nil {
		return false, err
	}
	if item ==  nil {
		vm.PushData(engine, []byte{})
	} else {
		vm.PushData(engine, item.(*states.StorageItem).Value)
	}
	return true, nil
}

func (s *StateMachine) StoragePut(engine *vm.ExecutionEngine) (bool, error) {
	opInterface := vm.PopInteropInterface(engine)
	context := opInterface.(*StorageContext)
	key := vm.PopByteArray(engine)
	value := vm.PopByteArray(engine)
	storageKey := states.NewStorageKey(context.codeHash, key)
	s.CloneCache.GetInnerCache().GetWriteSet().Add(store.ST_Storage, storage.KeyToStr(storageKey), states.NewStorageItem(value))
	return true, nil
}

func (s *StateMachine) StorageDelete(engine *vm.ExecutionEngine) (bool, error) {
	opInterface := vm.PopInteropInterface(engine)
	context := opInterface.(*StorageContext)
	key := vm.PopByteArray(engine)
	storageKey := states.NewStorageKey(context.codeHash, key)
	s.CloneCache.GetInnerCache().GetWriteSet().Delete(storage.KeyToStr(storageKey))
	return true, nil
}

func (s *StateMachine) GetStorageContext(engine *vm.ExecutionEngine) (bool, error) {
	return true, nil
}

func contains(programHashes []common.Uint168, programHash common.Uint168) bool {
	for _, v := range programHashes {
		if v == programHash {
			return true
		}
	}
	return false
}