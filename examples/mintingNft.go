package examples

import (
	"log"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/nftlabs"
)

func main() {
	nftContractAddress := ""
	chainRpcUrl := "https://rpc-mumbai.maticvigil.com" // change this

	client, err := ethclient.Dial(chainRpcUrl)
	if err != nil {
		log.Fatal(err)
	}

	sdk, err := nftlabs.NewSdk(client, &nftlabs.SdkOptions{PrivateKey: "// TODO"})
	if err != nil {
		log.Fatal(err)
	}

	nftModule, err := sdk.GetNftModule(nftContractAddress)
	if err != nil {
		log.Fatal(err)
	}

	if tx, result, err := nftModule.Mint(true, nftlabs.MintNftMetadata{
		Name:        "",
		Description: "",
		Image:       "",
	}); err != nil {
		log.Fatal(err)
	} else {
		log.Println(tx.Hash())
		log.Printf("Minted new nft with ID %d", result.Id)
	}
}
