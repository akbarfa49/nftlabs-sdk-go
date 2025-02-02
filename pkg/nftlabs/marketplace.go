package nftlabs

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	ethabi "github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
	"github.com/kpango/fastime"
	"github.com/nftlabs/nftlabs-sdk-go/internal/abi"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/globalClient"
)

type Marketplace interface {
	GetListing(listingId *big.Int) (Listing, error)
	GetAll(filter ListingFilter) ([]Listing, error)
	GetMarketFeeBps() (*big.Int, error)
	SetMarketFeeBps(fee *big.Int) error
	WatchNewListing(sink chan<- interface{}) (ethereum.Subscription, error)
}

type MarketplaceModule struct {
	Client  globalClient.IClient
	Address string
	module  *abi.Marketplace

	main ISdk
}

func newMarketplaceModule(client globalClient.IClient, address string, main ISdk) (*MarketplaceModule, error) {
	module, err := abi.NewMarketplace(common.HexToAddress(address), client)
	if err != nil {
		return nil, err
	}

	return &MarketplaceModule{
		Client:  client,
		Address: address,
		module:  module,
		main:    main,
	}, nil
}

func (sdk *MarketplaceModule) GetMarketFeeBps() (*big.Int, error) {
	if result, err := sdk.module.MarketplaceCaller.MarketFeeBps(&bind.CallOpts{}); err != nil {
		return nil, err
	} else {
		return big.NewInt(0).SetUint64(result), nil
	}
}

func (sdk *MarketplaceModule) SetMarketFeeBps(fee *big.Int) error {
	if tx, err := sdk.module.SetMarketFeeBps(&bind.TransactOpts{
		NoSend: false,
		From:   sdk.main.getSignerAddress(),
		Signer: sdk.main.getSigner(),
	}, fee); err != nil {
		return err
	} else {
		return waitForTx(sdk.Client, tx.Hash(), txWaitTimeBetweenAttempts, txMaxAttempts)
	}
}

func (sdk *MarketplaceModule) GetListing(listingId *big.Int) (Listing, error) {
	if result, err := sdk.module.MarketplaceCaller.Listings(&bind.CallOpts{}, listingId); err != nil {
		return Listing{}, err
	} else {
		return sdk.transformResultToListing(result)
	}
}

func (sdk *MarketplaceModule) GetAll(filter ListingFilter) ([]Listing, error) {
	listings := make([]abi.IMarketplaceListing, 0)
	filteredListings := make([]abi.IMarketplaceListing, 0)
	results := make([]Listing, 0)

	hasFilter := filter.TokenContract != "" || filter.TokenId != nil || filter.Seller != ""

	totalListings, err := sdk.module.MarketplaceCaller.TotalListings(&bind.CallOpts{})
	if err != nil {
		return nil, err
	}

	zero := big.NewInt(0)
	one := big.NewInt(1)
	for i := new(big.Int).Set(zero); i.Cmp(totalListings) < 0; i.Add(i, one) {
		listing, err := sdk.module.MarketplaceCaller.Listings(&bind.CallOpts{}, i)
		if err != nil {
			return nil, err
		}
		listings = append(listings, listing)
	}

	for _, listing := range listings {
		if listing.Quantity.Cmp(zero) == 0 {
			continue
		}
		if !sdk.isStillValidDirectListing(listing) {
			continue
		}
		if hasFilter {
			if filter.TokenContract != "" && common.HexToAddress(filter.TokenContract) != listing.AssetContract {
				continue
			}
			if filter.TokenId != nil && filter.TokenId.Cmp(listing.TokenId) != 0 {
				continue
			}
			if filter.Seller != "" && common.HexToAddress(filter.Seller) != listing.TokenOwner {
				continue
			}
		}
		filteredListings = append(filteredListings, listing)
	}

	for _, listing := range filteredListings {
		transformed, err := sdk.transformResultToListing(listing)
		if err != nil {
			continue
		}
		results = append(results, transformed)
	}

	return results, nil
}

func (sdk *MarketplaceModule) isStillValidDirectListing(listing abi.IMarketplaceListing) bool {
	nftAbi, err := abi.NewERC721(listing.AssetContract, sdk.Client)
	if err != nil {
		return false
	}
	addr, err := nftAbi.OwnerOf(&bind.CallOpts{}, listing.TokenId)
	if err != nil {
		return false
	}
	// TODO: check token owner balance
	// TODO: check token owner approval

	return fastime.UnixNow() < listing.EndTime.Int64() && strings.EqualFold(addr.String(), listing.TokenOwner.String())
}

func (sdk *MarketplaceModule) transformResultToListing(listing abi.IMarketplaceListing) (Listing, error) {
	listingCurrency := listing.Currency

	var currencyMetadata *CurrencyValue
	if listingCurrency.String() == "0x0000000000000000000000000000000000000000" || strings.ToLower(listingCurrency.String()) == "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" {
		currencyMetadata = nil
	} else {
		// TODO: this is bad, don't want to create an instance of the module every time
		// but idk how else to get it in here given that the address is dynamic per listing
		// damages testability
		currency, err := newCurrencyModule(sdk.Client, listingCurrency.Hex(), sdk.main)
		if err != nil {
			// TODO: return better error
			return Listing{}, err
		}

		if currencyValue, err := currency.GetValue(listing.BuyoutPricePerToken); err != nil {
			// TODO: return better error
			return Listing{}, err
		} else {
			currencyMetadata = &currencyValue
		}
	}

	var nftMetadata *NftMetadata
	if listing.AssetContract.String() != "0x0000000000000000000000000000000000000000" {
		// TODO: again, bad, need to create this in the function because we don't know the nft contract when we get here
		// damages testability
		nftModule, err := newNftModule(sdk.Client, listing.AssetContract.Hex(), sdk.main)
		if err != nil {
			// TODO: return better error
			return Listing{}, err
		}

		if meta, err := nftModule.Get(listing.TokenId); err != nil {
			// TODO: return better error
			return Listing{}, err
		} else {
			nftMetadata = &meta
		}
	}

	var saleStart *time.Time
	if listing.StartTime.Int64() > 0 {
		tm := time.Unix(listing.StartTime.Int64(), 0)
		saleStart = &tm
	} else {
		saleStart = nil
	}

	var saleEnd *time.Time
	if listing.EndTime.Int64() > 0 && listing.EndTime.Int64() < math.MaxInt64-1 {
		tm := time.Unix(listing.EndTime.Int64(), 0)
		saleEnd = &tm
	} else {
		saleEnd = nil
	}

	return Listing{
		Id:               listing.ListingId,
		Seller:           listing.TokenOwner,
		TokenContract:    listing.AssetContract,
		TokenId:          listing.TokenId,
		TokenMetadata:    nftMetadata,
		Quantity:         listing.Quantity,
		CurrentContract:  listingCurrency,
		CurrencyMetadata: currencyMetadata,
		Price:            listing.BuyoutPricePerToken,
		SaleStart:        saleStart,
		SaleEnd:          saleEnd,
	}, nil
}
func (sdk *MarketplaceModule) WatchNewListing(sink chan<- interface{}) (ethereum.Subscription, error) {
	// sink := make(chan *abi.MarketplaceNewListing)

	// es, err := sdk.module.WatchNewListing(&bind.WatchOpts{}, sink, nil, []common.Address{common.HexToAddress(sdk.Address)}, nil)
	// log.Println(err)
	// go func() {
	// 	log.Println(`watching`)
	// 	for {
	// 		log.Println(`waiting for sink`)
	// 		v := <-sink
	// 		log.Println(v.Listing.TokenOwner.String())
	// 	}
	// }()
	parsedabi, err := ethabi.JSON(strings.NewReader(abi.MarketplaceABI))
	if err != nil {
		return nil, err
	}

	for k, v := range parsedabi.Events {
		fmt.Println(k)
		fmt.Println(v.ID)
	}

	e := ethereum.FilterQuery{
		Addresses: []common.Address{common.HexToAddress(sdk.Address)},
	}
	logs := make(chan types.Log)
	sub, err := sdk.Client.SubscribeFilterLogs(context.Background(), e, logs)
	// parsedabi, _ := ethabi.JSON(strings.NewReader(abi.MarketplaceABI))
	// parsedabi.Unpack()
	// log.Println(parsedabi.Events)
	// parsedabi
	unpacker := func(abii ethabi.ABI, out interface{}, event string, log types.Log) error {
		if len(log.Data) > 0 {
			if err := abii.UnpackIntoInterface(out, event, log.Data); err != nil {
				return err
			}
		}
		var indexed ethabi.Arguments
		for _, arg := range abii.Events[event].Inputs {
			if arg.Indexed {
				indexed = append(indexed, arg)
			}
		}
		return ethabi.ParseTopics(out, indexed, log.Topics[1:])
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {

			case <-quit:
				return nil

			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				switch log.Topics[0].String() {

				//NewListing
				case `0x9e578277632a71dd17ab11c1f584c51deafef022c94389ecb050eb92713725f6`:
					nebi := new(abi.MarketplaceNewListing)
					if err := unpacker(parsedabi, nebi, `NewListing`, log); err != nil {
						return err
					}
				case `0xa00227275ba75aea329d91406a2884d227dc386f939f1d18e15a7317152432ca`:
					nebi := new(abi.MarketplaceListingUpdate)
					if err := unpacker(parsedabi, nebi, `ListingUpdate`, log); err != nil {
						return err
					}
				}
			case err := <-sub.Err():
				return err
			}

		}

	}), nil
}
