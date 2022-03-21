package nftlabs

import (
	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nftlabs/nftlabs-sdk-go/internal/abi"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/globalClient"
)

type erc1155 interface {
	commonModule
}

type erc1155Module struct {
	Client  globalClient.IClient
	Address string
	Options *SdkOptions
	module  *abi.ERC1155

	privateKey    *ecdsa.PrivateKey
	signerAddress common.Address
}

func newErc1155Module(client globalClient.IClient, address string, opt *SdkOptions) (*erc1155Module, error) {
	module, err := abi.NewERC1155(common.HexToAddress(address), client)
	if err != nil {
		// TODO: return better error
		return nil, err
	}

	return &erc1155Module{
		Client:  client,
		Address: address,
		Options: opt,
		module:  module,
	}, nil
}

func (sdk *erc1155Module) SetPrivateKey(privateKey string) error {
	if pKey, publicAddress, err := processPrivateKey(privateKey); err != nil {
		return &NoSignerError{typeName: "erc1155", Err: err}
	} else {
		sdk.privateKey = pKey
		sdk.signerAddress = publicAddress
	}
	return nil
}
func (sdk *erc1155Module) getSigner() func(address common.Address, transaction *types.Transaction) (*types.Transaction, error) {
	return func(address common.Address, transaction *types.Transaction) (*types.Transaction, error) {
		return types.SignTx(transaction, types.NewEIP155Signer(sdk.Options.ChainID), sdk.privateKey)
	}
}
