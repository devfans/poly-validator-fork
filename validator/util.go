package validator

import (
	"math/big"
	"poly-bridge/basedef"
)

func ParseInt(value, ty string) (v *big.Int) {
	switch ty {
	case "Integer":
		v, _ = new(big.Int).SetString(value, 10)
	default:
		v, _ = new(big.Int).SetString(basedef.HexStringReverse(value), 16)
	}
	return
}
