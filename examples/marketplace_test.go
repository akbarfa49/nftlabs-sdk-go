package examples

import (
	"log"
	"testing"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/nftlabs"
)

func TestExampleMarketplace(t *testing.T) {
	marketplacemodule := "0x7d5aC19e543f0146d79AE273faf55A97E57996c9"
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
		log.Println(v)
	}
}
