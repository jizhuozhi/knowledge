package models

import (
	"crypto/rand"
	"math/big"
)

const (
	idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	idLength   = 12
)

func GenerateID() string {
	b := make([]byte, idLength)
	alphabetLen := big.NewInt(int64(len(idAlphabet)))
	for i := range b {
		n, _ := rand.Int(rand.Reader, alphabetLen)
		b[i] = idAlphabet[n.Int64()]
	}
	return string(b)
}
