package main

// crypto.go: randToken / hashPwd 的实现(拆出来是为了和业务代码分开)

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

func randRead(b []byte) (int, error) { return rand.Read(b) }

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func hexEncode(b []byte) string { return hex.EncodeToString(b) }
