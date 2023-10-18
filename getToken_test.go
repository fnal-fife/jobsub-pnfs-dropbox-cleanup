package main

import (
	"testing"

	"github.com/lestrrat-go/jwx/jwt"
	scitokens "github.com/scitokens/scitokens-go"
	"github.com/stretchr/testify/assert"
)

// ORder of ops:
// 1. Get scitoken (GetToken(getTokener getToken)).  Then we have htgettoken type implement getTokener
// 2. validate token
// 3. Check scope validity
// 4.  Maybe some function that wraps all of these?

// TODO
// 1. Test that checks that we can get a token that is valid (use scitokens-go Validate to make sure we're good)
// 2. MAY NOT NEED this since it's already tested in scitokens-go library.  Test that checks our scope checking.  If we give a token with certain scopes, we should check that it can correctly use scitokens.Allowed for a given path
// 3.

// Generated following sample token at https://demo.scitokens.org/:
// {
//   "ver": "scitoken:2.0",
//   "aud": "https://demo.scitokens.org",
//   "iss": "https://demo.scitokens.org",
//   "exp": 1697645926,
//   "iat": 1697645326,
//   "nbf": 1697645326,
//   "jti": "7581d0f1-2faf-40d3-9d88-3454a082070a",
//   "scope": "storage.modify:/foo/bar"
// }
// Using the encoding of that token for tests

var sampleTokenString = `eyJhbGciOiJSUzI1NiIsImtpZCI6ImtleS1yczI1NiIsInR5cCI6IkpXVCJ9.eyJ2ZXIiOiJzY2l0b2tlbjoyLjAiLCJhdWQiOiJodHRwczovL2RlbW8uc2NpdG9rZW5zLm9yZyIsImlzcyI6Imh0dHBzOi8vZGVtby5zY2l0b2tlbnMub3JnIiwiZXhwIjoxNjk3NjQ2MDMwLCJpYXQiOjE2OTc2NDU0MzAsIm5iZiI6MTY5NzY0NTQzMCwianRpIjoiNzU4MWQwZjEtMmZhZi00MGQzLTlkODgtMzQ1NGEwODIwNzBhIiwic2NvcGUiOiJzdG9yYWdlLm1vZGlmeTovZm9vL2JhciJ9.Kd7JonIkzwHKFLrTfG35afxHLF-J0NrftiUHkvcvdDtYOTFCbKUnImuYJV8DONT9uNXjr96Ir8LqnhCC4o2K3pKFXFCrJLAHBC-dGRcT5gjhPybTr4ya9SSUT0miHuZRLx2ODpX7xNh6HU4CfRYv6iUwvoKbhU8wGjM-0jeMO52EMfmjn2a0u_2f9rio1ZugtrHn6mRX7Ix3KiStSUEkVy9yJxsT2DAZ12RyUehpNcfCoyo8HslsyjkMqlNTMq3pv64GF1O5V0i9MJPBFDLcPcVtK4M2va6dTiAcp1CFTQoeNC_DShfksnn9WNajDVA57-SuxzF9am4tnYH_i0CPjQ`

type testGetTokener struct{}

func (t *testGetTokener) getToken() (scitokens.SciToken, error) {
	// From sample token as explained above
	token, err := jwt.ParseString(sampleTokenString)
	if err != nil {
		return nil, err
	}
	scitoken, err := scitokens.NewSciToken(token)
	if err != nil {
		return nil, err
	}
	return scitoken, nil
}

func TestGetToken(t *testing.T) {
	// TODO test cases for errors
	g := new(testGetTokener)
	token, err := GetToken(g)
	if err != nil {
		assert.FailNow(t, "This failed.  Change later")
	}
	enf, _ := scitokens.NewEnforcer("https://demo.scitokens.org")
	err = enf.Validate(token, scitokens.WithScope(scitokens.ParseScope("storage.modify:/foo/bar")))
	if assert.ErrorContains(t, err, "exp not satisfied") {
		return
	}
	assert.NoError(t, err)
}
