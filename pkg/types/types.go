package types

import (
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/gotd/td/session"
)

type AppError struct {
	Error error
	Code  int
}

type Part struct {
	DecryptedSize int64
	Size          int64
	Salt          string
	ID            int64
}

type JWTClaims struct {
	jwt.Claims
	TgSession string `json:"tgSession"`
	Name      string `json:"name"`
	UserName  string `json:"userName"`
	Bot       bool   `json:"bot"`
	IsPremium bool   `json:"isPremium"`
	Hash      string `json:"hash"`
}

type SessionData struct {
	Version int
	Data    session.Data
}
type SocketMessage struct {
	AuthType      string `json:"authType"`
	Message       string `json:"message"`
	PhoneNo       string `json:"phoneNo,omitempty"`
	PhoneCodeHash string `json:"phoneCodeHash,omitempty"`
	PhoneCode     string `json:"phoneCode,omitempty"`
	Password      string `json:"password,omitempty"`
}

type BotInfo struct {
	Id         int64
	UserName   string
	AccessHash int64
	Token      string
}

type Range struct {
	Start  int64
	End    int64
	PartNo int64
}
