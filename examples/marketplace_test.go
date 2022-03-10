package examples

import (
	"fmt"
	"log"
	"testing"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/nftlabs"
)

func TestExampleMarketplace(t *testing.T) {
	marketplacemodule := "0x5d463684a15a40C102F23987989787C2Bb171E61"
	chainRpcUrl := "https://matic-mumbai.chainstacklabs.com" // change this

	client, err := ethclient.Dial(chainRpcUrl)
	if err != nil {
		log.Fatal(err)
	}

	sdk, err := nftlabs.NewSdk(client, &nftlabs.SdkOptions{})
	if err != nil {
		log.Fatal(err)
	}
	m, err := sdk.GetMarketplaceModule(marketplacemodule)
	if err != nil {
		panic(err)
	}
	list, err := m.GetAll(nftlabs.ListingFilter{})
	if err != nil {
		panic(err)
	}
	log.Println(`amount: `, len(list))
	for _, v := range list {
		log.Println(v.SaleStart.Date())
		fmt.Println(v.SaleEnd.Date())
	}
}
