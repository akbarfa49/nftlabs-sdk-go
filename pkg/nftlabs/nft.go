package nftlabs

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"golang.org/x/sync/errgroup"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/nftlabs/nftlabs-sdk-go/internal/abi"
)

type Nft interface {
	defaultModule
	Get(tokenId *big.Int) (NftMetadata, error)
	GetAll() ([]NftMetadata, error)
	GetOwned(address string) ([]NftMetadata, error)
	Balance(tokenId *big.Int) (*big.Int, error)
	BalanceOf(address string) (*big.Int, error)
	Transfer(send bool, to string, tokenId *big.Int) (*types.Transaction, error)
	TotalSupply() (*big.Int, error)
	SetApproval(operator string, approved bool) error
	Mint(send bool, metadata MintNftMetadata) (*types.Transaction, NftMetadata, error)
	MintBatch(send bool, meta []MintNftMetadata) (*types.Transaction, []NftMetadata, error)
	MintBatchTo(send bool, to string, meta []MintNftMetadata) (*types.Transaction, []NftMetadata, error)
	Burn(tokenId *big.Int) error
	TransferFrom(send bool, from string, to string, tokenId *big.Int) (*types.Transaction, error)
	SetRoyaltyBps(amount *big.Int) error
	SetRestrictedTransfer(restricted bool) error

	MintTo(send bool, to string, meta MintNftMetadata) (*types.Transaction, NftMetadata, error)
	Multicall(data ...[]byte) (*types.Transaction, error)

	getModule() *abi.NFT
}

type NftModule struct {
	defaultModuleImpl
	Client  *ethclient.Client
	Address string
	Options *SdkOptions
	module  *abi.NFT

	oldModule *abi.OldNFT

	shouldCheckVersion bool
	isOldModule        bool

	main ISdk
}

func (sdk *NftModule) Multicall(data ...[]byte) (*types.Transaction, error) {
	transact, err := sdk.module.Multicall(sdk.main.getTransactOpts(true), data)
	if err != nil {
		return nil, err
	}
	err = waitForTx(sdk.Client, transact.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	return transact, err
}

func (sdk *NftModule) v1MintBatch(send bool, meta []MintNftMetadata, to string) (*types.Transaction, []NftMetadata, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, nil, &NoSignerError{typeName: "nft"}
	}

	storage, err := sdk.main.GetStorage()
	if err != nil {
		return nil, nil, err
	}

	out := make([]interface{}, len(meta))
	for i, m := range meta {
		out[i] = m
	}
	uris, err := storage.UploadBatch(out, sdk.Address, sdk.main.getSignerAddress().String())
	if err != nil {
		return nil, nil, err
	}
	tx, err := sdk.oldModule.MintNFTBatch(sdk.main.getTransactOpts(send), common.HexToAddress(to), uris)
	if err != nil {
		return tx, nil, err
	}
	if !send {
		return tx, nil, nil
	}
	if err := waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts); err != nil {
		return tx, nil, err
	}

	receipt, err := sdk.Client.TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		return tx, nil, err
	}

	batch, err := sdk.getNewMintedBatch(receipt.Logs)
	if err != nil {
		return tx, nil, err
	}

	wg := new(errgroup.Group)
	results := make([]NftMetadata, len(batch.TokenIds))
	for i, id := range batch.TokenIds {
		func(index int, id *big.Int) {
			wg.Go(func() error {
				uri, err := sdk.Get(id)
				if err != nil {
					return err
				} else {
					results[index] = uri
					return nil
				}
			})
		}(i, id)
	}

	if err := wg.Wait(); err != nil {
		log.Println("Failed to get the newly minted batch")
		//dont skip the tokenId because the data is minted already
		for idx, id := range batch.TokenIds {
			results[idx] = NftMetadata{Id: id}
		}
		return tx, results, err
	}
	return tx, results, nil
}

func (sdk *NftModule) MintBatch(send bool, meta []MintNftMetadata) (*types.Transaction, []NftMetadata, error) {
	return sdk.MintBatchTo(send, sdk.main.getSignerAddress().String(), meta)
}
func (sdk *NftModule) MintBatchTo(send bool, to string, meta []MintNftMetadata) (*types.Transaction, []NftMetadata, error) {
	if sdk.isV1() {
		return sdk.v1MintBatch(send, meta, to)
	}

	return sdk.MintBatchToV2(send, to, meta)
}

func (sdk *NftModule) MintBatchToV2(send bool, to string, meta []MintNftMetadata) (*types.Transaction, []NftMetadata, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, nil, &NoSignerError{typeName: "nft"}
	}

	storage, err := sdk.main.GetStorage()
	if err != nil {
		return nil, nil, err
	}

	out := make([]interface{}, len(meta))
	for i, m := range meta {
		out[i] = m
	}

	data := make([][]byte, 0)
	uris, err := storage.UploadBatch(out, sdk.Address, sdk.main.getSignerAddress().String())
	if err != nil {
		return nil, nil, err
	}

	for _, uri := range uris {
		tx, err := sdk.module.NFTTransactor.MintTo(sdk.main.getTransactOpts(false), common.HexToAddress(to), uri)
		if err != nil {
			return nil, nil, err
		}
		if !send {
			return tx, nil, nil
		}
		data = append(data, tx.Data())
	}

	if tx, err := sdk.module.Multicall(sdk.main.getTransactOpts(true), data); err != nil {
		return nil, nil, err
	} else {
		if err := waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts); err != nil {
			// TODO: return clearer error
			return nil, nil, err
		}
		var receipt *types.Receipt
		for i := 0; i < 30; i++ {
			receipt, err = sdk.Client.TransactionReceipt(context.Background(), tx.Hash())
			if err == nil {
				break
			}
			time.Sleep(4 * time.Second)
		}
		if err != nil {
			log.Printf("Failed to lookup transaction receipt with hash %v\n", tx.Hash().String())

			return tx, nil, err
		}

		tokenIds := make([]*big.Int, 0)

		for _, log := range receipt.Logs {
			if m, err := sdk.module.ParseTokenMinted(*log); err != nil {
				continue
			} else {
				tokenIds = append(tokenIds, m.TokenIdMinted)
			}
		}

		wg := new(errgroup.Group)
		results := make([]NftMetadata, len(tokenIds))
		for i, id := range tokenIds {
			func(index int, id *big.Int) {
				wg.Go(func() error {
					uri, err := sdk.Get(id)
					if err != nil {
						return err
					} else {
						results[index] = uri
						return nil
					}
				})
			}(i, id)
		}

		if err := wg.Wait(); err != nil {
			log.Println("Failed to get the newly minted batch")
			for idx, id := range tokenIds {
				results[idx] = NftMetadata{Id: id}
			}
			return tx, results, err
		}

		return tx, results, nil
	}
}

func (sdk *NftModule) Burn(tokenId *big.Int) error {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return &NoSignerError{
			typeName: "nft",
		}
	}

	_, err := sdk.module.Burn(sdk.main.getTransactOpts(true), tokenId)

	return err
}

func (sdk *NftModule) TransferFrom(send bool, from string, to string, tokenId *big.Int) (*types.Transaction, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, &NoSignerError{typeName: "nft"}
	}
	if tx, err := sdk.module.TransferFrom(sdk.main.getTransactOpts(send), common.HexToAddress(from), common.HexToAddress(to), tokenId); err != nil {
		return tx, err
	} else {
		if !send {
			return tx, nil
		}
		return tx, waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	}
}

func (sdk *NftModule) SetRoyaltyBps(amount *big.Int) error {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return &NoSignerError{typeName: "nft"}
	}
	if tx, err := sdk.module.SetRoyaltyBps(sdk.main.getTransactOpts(true), amount); err != nil {
		return err
	} else {
		return waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	}
}

func (sdk *NftModule) v1MintTo(send bool, to string, metadata MintNftMetadata) (*types.Transaction, NftMetadata, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, NftMetadata{}, &NoSignerError{
			typeName: "Nft",
		}
	}
	uri, err := sdk.main.getGateway().Upload(metadata, "", "")
	if err != nil {
		return nil, NftMetadata{}, err
	}
	log.Printf("Got back uri = %v\n", uri)

	tx, err := sdk.oldModule.MintNFT(sdk.main.getTransactOpts(true), common.HexToAddress(to), uri)
	if err != nil {
		log.Printf("Failed to execute transaction %v\n", tx.Hash().String())
		return nil, NftMetadata{}, err
	}
	if !send {
		return tx, NftMetadata{}, err
	}
	if err := waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts); err != nil {
		// TODO: return clearer error
		return tx, NftMetadata{}, err
	}

	receipt, err := sdk.Client.TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		log.Printf("Failed to lookup transaction receipt with hash %v\n", tx.Hash().String())
		return tx, NftMetadata{}, err
	}

	tokenId, err := sdk.getNewMintedNft(receipt.Logs)
	if err != nil {
		return tx, NftMetadata{}, err
	}

	return tx, NftMetadata{
		Id:          tokenId,
		Image:       metadata.Image,
		Description: metadata.Description,
		Name:        metadata.Name,
		Properties:  metadata.Properties,
	}, err
}

func (sdk *NftModule) MintTo(send bool, to string, metadata MintNftMetadata) (*types.Transaction, NftMetadata, error) {
	if sdk.isV1() {
		return sdk.v1MintTo(send, to, metadata)
	}

	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, NftMetadata{}, &NoSignerError{
			typeName: "Nft",
		}
	}
	uri, err := sdk.main.getGateway().Upload(metadata, "", "")
	if err != nil {
		return nil, NftMetadata{}, err
	}
	log.Printf("Got back uri = %v\n", uri)

	tx, err := sdk.module.NFTTransactor.MintTo(sdk.main.getTransactOpts(send), common.HexToAddress(to), uri)
	if err != nil {
		log.Printf("Failed to execute transaction %v, tx=%v\n", err, tx)
		return tx, NftMetadata{}, err
	}
	if !send {
		return tx, NftMetadata{}, err
	}
	if err := waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts); err != nil {
		// TODO: return clearer error
		return tx, NftMetadata{}, err
	}
	var receipt *types.Receipt
	for i := 0; i < 30; i++ {
		receipt, err = sdk.Client.TransactionReceipt(context.Background(), tx.Hash())
		if err == nil {
			break
		}
		time.Sleep(4 * time.Second)
	}
	if err != nil {
		log.Printf("Failed to lookup transaction receipt with hash %v\n", tx.Hash().String())
		return tx, NftMetadata{}, err
	}

	tokenId, err := sdk.getNewMintedNft(receipt.Logs)
	if err != nil {
		return tx, NftMetadata{}, err
	}

	return tx, NftMetadata{
		Id:          tokenId,
		Image:       metadata.Image,
		Description: metadata.Description,
		Name:        metadata.Name,
		Properties:  metadata.Properties,
	}, err
}

func (sdk *NftModule) Mint(send bool, metadata MintNftMetadata) (*types.Transaction, NftMetadata, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, NftMetadata{}, &NoSignerError{typeName: "nft"}
	}
	return sdk.MintTo(send, sdk.main.getSignerAddress().String(), metadata)
}

func (sdk *NftModule) SetApproval(operator string, approved bool) error {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return &NoSignerError{typeName: "nft"}
	}
	if tx, err := sdk.module.SetApprovalForAll(sdk.main.getTransactOpts(true), common.HexToAddress(operator), approved); err != nil {
		return err
	} else {
		return waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	}
}

func (sdk *NftModule) TotalSupply() (*big.Int, error) {
	return sdk.module.TotalSupply(&bind.CallOpts{})
}

func (sdk *NftModule) GetOwned(address string) ([]NftMetadata, error) {
	var addressToCheck common.Address
	if address == "" && sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, &NoSignerError{typeName: "nft"}
	} else if address != "" {
		addressToCheck = common.HexToAddress(address)
	} else {
		addressToCheck = sdk.main.getSignerAddress()
	}

	balance, err := sdk.module.BalanceOf(&bind.CallOpts{}, addressToCheck)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	ch := make(chan NftMetadata)
	errCh := make(chan error)

	defer close(errCh)

	for i := big.NewInt(0); i.Cmp(balance) == -1; i.Add(big.NewInt(1), i) {
		wg.Add(1)
		go func(idStr string) {
			defer wg.Done()

			id := big.NewInt(0)
			id.SetString(idStr, 10)

			result, err := sdk.module.TokenOfOwnerByIndex(&bind.CallOpts{}, addressToCheck, id)
			if err != nil {
				log.Printf("Failed to get token of owner by index for user %v, err = %v, id=%d\n", addressToCheck.String(), err, id)
				ch <- NftMetadata{}
				return
			}

			nft, err := sdk.Get(result)
			if err != nil {
				log.Printf("Failed to get token for user %v, err = %v\n", addressToCheck.String(), err)
				ch <- NftMetadata{}
			}

			ch <- nft
		}(i.String())
	}

	results := make([]NftMetadata, balance.Int64())
	for i := range results {
		results[i] = <-ch
	}

	wg.Wait()
	close(ch)

	return results, nil
}

func newNftModule(client *ethclient.Client, address string, main ISdk) (Nft, error) {
	module, err := abi.NewNFT(common.HexToAddress(address), client)
	if err != nil {
		// TODO: return better error
		return nil, err
	}

	oldModule, err := abi.NewOldNFT(common.HexToAddress(address), client)
	if err != nil {
		// TODO: return better error
		return nil, err
	}

	return &NftModule{
		Client:             client,
		Address:            address,
		module:             module,
		main:               main,
		oldModule:          oldModule,
		isOldModule:        false,
		shouldCheckVersion: true,
	}, nil
}

func (sdk *NftModule) isV1() (result bool) {
	if !sdk.shouldCheckVersion {
		return sdk.isOldModule
	}

	_, err := sdk.module.NextTokenIdToMint(&bind.CallOpts{})
	if err != nil {
		sdk.isOldModule = true
		result = true
	} else {
		result = false
	}

	sdk.shouldCheckVersion = false
	return result
}

func (sdk *NftModule) Get(tokenId *big.Int) (NftMetadata, error) {
	tokenUri, err := sdk.module.NFTCaller.TokenURI(&bind.CallOpts{}, tokenId)
	if err != nil {
		return NftMetadata{}, err
	}

	body, err := sdk.main.getGateway().Get(tokenUri)
	if err != nil {
		return NftMetadata{}, err
	}
	metadata := NftMetadata{
		Id: tokenId,
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return NftMetadata{}, &UnmarshalError{body: string(body), typeName: "nft", UnderlyingError: err}
	}

	return metadata, nil
}

func (sdk *NftModule) GetAsync(tokenId *big.Int, ch chan<- NftMetadata, errCh chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()

	result, err := sdk.Get(tokenId)
	if err != nil {
		log.Printf("Failed to fetch nft with id %d\n err=%v", tokenId, err)
		errCh <- err
		return
	}
	ch <- result
}

func (sdk *NftModule) GetAll() ([]NftMetadata, error) {
	var maxId *big.Int

	if sdk.isV1() {
		max, err := sdk.oldModule.NextTokenId(&bind.CallOpts{})
		if err != nil {
			return nil, err
		}
		maxId = max
	} else {
		max, err := sdk.module.NFTCaller.NextTokenIdToMint(&bind.CallOpts{})
		if err != nil {
			return nil, err
		}
		maxId = max
	}

	var wg sync.WaitGroup

	// nfts := make([]NftMetadata, 0)
	ch := make(chan NftMetadata)
	errCh := make(chan error)

	defer close(errCh)

	count := maxId.Int64()
	for i := int64(0); i < count; i++ {
		id := new(big.Int)
		id.SetInt64(i)

		wg.Add(1)
		go sdk.GetAsync(id, ch, errCh, &wg)
	}

	results := make([]NftMetadata, count)
	for i := range results {
		results[i] = <-ch
	}

	wg.Wait()
	close(ch)

	return results, nil
}

func (sdk *NftModule) BalanceOf(address string) (*big.Int, error) {
	return sdk.module.NFTCaller.BalanceOf(&bind.CallOpts{}, common.HexToAddress(address))
}

func (sdk *NftModule) Balance(tokenId *big.Int) (*big.Int, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, &NoSignerError{typeName: "nft"}
	}

	return sdk.module.NFTCaller.BalanceOf(&bind.CallOpts{}, sdk.main.getSignerAddress())
}

func (sdk *NftModule) Transfer(send bool, to string, tokenId *big.Int) (*types.Transaction, error) {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return nil, &NoSignerError{typeName: "nft"}
	}

	// TODO: allow you to pass transact opts

	return sdk.module.NFTTransactor.SafeTransferFrom(sdk.main.getTransactOpts(send), sdk.main.getSignerAddress(), common.HexToAddress(to), tokenId)
}

func (sdk *NftModule) getNewMintedNft(logs []*types.Log) (*big.Int, error) {
	var tokenId *big.Int
	for _, l := range logs {
		iterator, err := sdk.module.ParseTokenMinted(*l)
		if err != nil {
			continue
		}

		if iterator.TokenIdMinted != nil {
			tokenId = iterator.TokenIdMinted
			break
		}
	}

	if tokenId == nil {
		return nil, errors.New("Could not find Minted event for transaction")
	}

	return tokenId, nil
}

func (sdk *NftModule) getNewMintedBatch(logs []*types.Log) (*abi.OldNFTMintedBatch, error) {
	var batch *abi.OldNFTMintedBatch
	for _, l := range logs {
		iterator, err := sdk.oldModule.ParseMintedBatch(*l)
		if err != nil {
			continue
		}

		if iterator.TokenIds != nil {
			batch = iterator
			break
		}
	}

	if batch == nil {
		return nil, errors.New("Could not find Minted batch event for transaction")
	}

	return batch, nil
}

func (sdk *NftModule) getModule() *abi.NFT {
	return sdk.module
}

// SetRestrictedTransfer will disable all transfers if set to true
func (sdk *NftModule) SetRestrictedTransfer(restricted bool) error {
	if sdk.main.getSignerAddress() == common.HexToAddress("0") {
		return &NoSignerError{typeName: "nft"}
	}
	if tx, err := sdk.module.SetRestrictedTransfer(sdk.main.getTransactOpts(true), restricted); err != nil {
		return err
	} else {
		return waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	}
}
