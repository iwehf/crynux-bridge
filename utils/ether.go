package utils

import (
	"math/big"

	"github.com/ethereum/go-ethereum/params"
)

func WeiToEther(wei *big.Int) *big.Float {
	f := new(big.Float)
	f.SetPrec(236) //  IEEE 754 octuple-precision binary floating-point format: binary256
	f.SetMode(big.ToNearestEven)
	fWei := new(big.Float)
	fWei.SetPrec(236) //  IEEE 754 octuple-precision binary floating-point format: binary256
	fWei.SetMode(big.ToNearestEven)
	return f.Quo(fWei.SetInt(wei), big.NewFloat(params.Ether))
}

func EtherToWei(ether *big.Int) *big.Int {
	return big.NewInt(0).Mul(ether, big.NewInt(params.Ether))
}

func GweiToWei(gwei *big.Int) *big.Int {
	return big.NewInt(0).Mul(gwei, big.NewInt(params.GWei))
}
