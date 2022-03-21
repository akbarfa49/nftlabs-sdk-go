package nftlabs

import (
	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/nftlabs/nftlabs-sdk-go/internal/abi"
	"github.com/nftlabs/nftlabs-sdk-go/pkg/globalClient"
)

type erc20 interface {
	commonModule
}

type erc20Module struct {
	Client  globalClient.IClient
	Address string
	Options *SdkOptions
	module  *abi.ERC20

	privateKey    *ecdsa.PrivateKey
	signerAddress common.Address
}

func newErc20SdkModule(client globalClient.IClient, address string, opt *SdkOptions) (*erc20Module, error) {
	module, err := abi.NewERC20(common.HexToAddress(address), client)
	if err != nil {
		// TODO: return better error
		return nil, err
	}

	return &erc20Module{
		Client:  client,
		Address: address,
		Options: opt,
		module:  module,
	}, nil
}

func (sdk *erc20Module) SetPrivateKey(privateKey string) error {
	if pKey, publicAddress, err := processPrivateKey(privateKey); err != nil {
		return &NoSignerError{typeName: "erc20", Err: err}
	} else {
		sdk.privateKey = pKey
		sdk.signerAddress = publicAddress
	}
	return nil
}
func (sdk *erc20Module) getSigner() func(address common.Address, transaction *types.Transaction) (*types.Transaction, error) {
	return func(address common.Address, transaction *types.Transaction) (*types.Transaction, error) {
		return types.SignTx(transaction, types.NewEIP155Signer(sdk.Options.ChainID), sdk.privateKey)
	}
}
