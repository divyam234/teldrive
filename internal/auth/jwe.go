package auth

import (
	"encoding/json"
	"strconv"

	"github.com/divyam234/teldrive/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/go-jose/go-jose/v3"
)

func Encode(secret string, payload *types.JWTClaims) (string, error) {

	rcpt := jose.Recipient{
		Algorithm: jose.PBES2_HS256_A128KW,
		Key:       secret,
	}

	enc, err := jose.NewEncrypter(jose.A128CBC_HS256, rcpt, nil)

	if err != nil {
		return "", err
	}

	jwt, _ := json.Marshal(payload)

	jweObject, err := enc.Encrypt(jwt)

	if err != nil {
		return "", err
	}

	jweToken, err := jweObject.CompactSerialize()

	if err != nil {
		return "", err
	}
	return jweToken, nil
}

func Decode(secret string, token string) (*types.JWTClaims, error) {
	jwe, err := jose.ParseEncrypted(token)
	if err != nil {
		return nil, err
	}

	decryptedData, err := jwe.Decrypt(secret)

	if err != nil {
		return nil, err
	}

	jwtToken := &types.JWTClaims{}

	err = json.Unmarshal(decryptedData, jwtToken)

	if err != nil {
		return nil, err
	}

	return jwtToken, nil

}

func GetUser(c *gin.Context) (int64, string) {
	val, _ := c.Get("jwtUser")
	jwtUser := val.(*types.JWTClaims)
	userId, _ := strconv.ParseInt(jwtUser.Subject, 10, 64)
	return userId, jwtUser.TgSession
}
