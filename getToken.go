package main

import (
	"github.com/scitokens/scitokens-go"
)

type getTokener interface {
	getToken() (scitokens.SciToken, error)
}

func GetToken(g getTokener) (scitokens.SciToken, error) {
	return g.getToken()
}
