package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/divyam234/teldrive/models"
	"github.com/divyam234/teldrive/schemas"
	"github.com/divyam234/teldrive/types"
	"github.com/divyam234/teldrive/utils"
	"github.com/divyam234/teldrive/utils/auth"
	"github.com/divyam234/teldrive/utils/tgc"
	"github.com/gin-gonic/gin"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/gorilla/websocket"
	"github.com/gotd/td/session"
	tgauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"gorm.io/gorm"
)

type AuthService struct {
	Db                *gorm.DB
	SessionMaxAge     int
	SessionCookieName string
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

func IP4toInt(IPv4Address net.IP) int64 {
	IPv4Int := big.NewInt(0)
	IPv4Int.SetBytes(IPv4Address.To4())
	return IPv4Int.Int64()
}

func Pack32BinaryIP4(ip4Address string) []byte {
	ipv4Decimal := IP4toInt(net.ParseIP(ip4Address))

	buf := new(bytes.Buffer)
	binary.Write(buf, binary.BigEndian, uint32(ipv4Decimal))
	return buf.Bytes()
}

func generateTgSession(dcID int, authKey []byte, port int) string {

	dcMaps := map[int]string{
		1: "149.154.175.53",
		2: "149.154.167.51",
		3: "149.154.175.100",
		4: "149.154.167.91",
		5: "91.108.56.130",
	}

	dcIDByte := byte(dcID)
	serverAddressBytes := Pack32BinaryIP4(dcMaps[dcID])
	portByte := make([]byte, 2)
	binary.BigEndian.PutUint16(portByte, uint16(port))

	packet := make([]byte, 0)
	packet = append(packet, dcIDByte)
	packet = append(packet, serverAddressBytes...)
	packet = append(packet, portByte...)
	packet = append(packet, authKey...)

	base64Encoded := base64.URLEncoding.EncodeToString(packet)
	return "1" + base64Encoded
}

func setCookie(c *gin.Context, key string, value string, age int) {

	config := utils.GetConfig()

	if config.CookieSameSite {
		c.SetSameSite(2)
	} else {
		c.SetSameSite(4)
	}
	c.SetCookie(key, value, age, "/", c.Request.Host, config.Https, true)

}

func checkUserIsAllowed(userName string) bool {
	config := utils.GetConfig()
	found := false
	if len(config.AllowedUsers) > 0 {
		for _, user := range config.AllowedUsers {
			if user == userName {
				found = true
				break
			}
		}
	} else {
		found = true
	}
	return found
}

func (as *AuthService) LogIn(c *gin.Context) (*schemas.Message, *types.AppError) {
	var session types.TgSession
	if err := c.ShouldBindJSON(&session); err != nil {
		return nil, &types.AppError{Error: errors.New("invalid request payload"), Code: http.StatusBadRequest}
	}

	if !checkUserIsAllowed(session.UserName) {
		return nil, &types.AppError{Error: errors.New("user not allowed"), Code: http.StatusUnauthorized}
	}

	now := time.Now().UTC()

	jwtClaims := &types.JWTClaims{Claims: jwt.Claims{
		Subject:  strconv.FormatInt(session.UserID, 10),
		IssuedAt: jwt.NewNumericDate(now),
		Expiry:   jwt.NewNumericDate(now.Add(time.Duration(as.SessionMaxAge) * time.Second)),
	}, TgSession: session.Sesssion,
		Name:      session.Name,
		UserName:  session.UserName,
		Bot:       session.Bot,
		IsPremium: session.IsPremium,
	}

	jweToken, err := auth.Encode(jwtClaims)

	if err != nil {
		return nil, &types.AppError{Error: err, Code: http.StatusBadRequest}
	}

	user := models.User{
		UserId:    session.UserID,
		Name:      session.Name,
		UserName:  session.UserName,
		IsPremium: session.IsPremium,
		TgSession: session.Sesssion,
	}

	var result []models.User

	if err := as.Db.Model(&models.User{}).Where("user_id = ?", session.UserID).
		Find(&result).Error; err != nil {
		return nil, &types.AppError{Error: errors.New("failed to create or update user"),
			Code: http.StatusInternalServerError}
	}
	if len(result) == 0 {
		if err := as.Db.Create(&user).Error; err != nil {
			return nil, &types.AppError{Error: errors.New("failed to create or update user"),
				Code: http.StatusInternalServerError}
		}
		//Create root folder on first login

		file := &models.File{
			Name:     "root",
			Type:     "folder",
			MimeType: "drive/folder",
			Path:     "/",
			Depth:    utils.IntPointer(0),
			UserID:   session.UserID,
			Status:   "active",
			ParentID: "root",
		}
		if err := as.Db.Create(file).Error; err != nil {
			return nil, &types.AppError{Error: errors.New("failed to create or update user"),
				Code: http.StatusInternalServerError}
		}
	} else {
		if err := as.Db.Model(&models.User{}).Where("user_id = ?", session.UserID).
			Update("tg_session", session.Sesssion).Error; err != nil {
			return nil, &types.AppError{Error: errors.New("failed to create or update user"),
				Code: http.StatusInternalServerError}
		}
	}
	setCookie(c, as.SessionCookieName, jweToken, as.SessionMaxAge)

	return &schemas.Message{Status: true, Message: "login success"}, nil
}

func (as *AuthService) GetSession(c *gin.Context) *types.Session {

	cookie, err := c.Request.Cookie(as.SessionCookieName)

	if err != nil {
		return nil
	}

	jwePayload, err := auth.Decode(cookie.Value)

	if err != nil {
		return nil
	}

	now := time.Now().UTC()

	newExpires := now.Add(time.Duration(as.SessionMaxAge) * time.Second)

	session := &types.Session{Name: jwePayload.Name, UserName: jwePayload.UserName, Expires: newExpires.Format(time.RFC3339)}

	jwePayload.IssuedAt = jwt.NewNumericDate(now)

	jwePayload.Expiry = jwt.NewNumericDate(newExpires)

	jweToken, err := auth.Encode(jwePayload)

	if err != nil {
		return nil
	}
	setCookie(c, as.SessionCookieName, jweToken, as.SessionMaxAge)
	return session
}

func (as *AuthService) Logout(c *gin.Context) (*schemas.Message, *types.AppError) {
	val, _ := c.Get("jwtUser")
	jwtUser := val.(*types.JWTClaims)
	client, _ := tgc.UserLogin(jwtUser.TgSession)

	tgc.RunWithAuth(c, client, "", func(ctx context.Context) error {
		_, err := client.API().AuthLogOut(c)
		return err
	})

	setCookie(c, as.SessionCookieName, "", -1)
	return &schemas.Message{Status: true, Message: "logout success"}, nil
}

func prepareSession(user *tg.User, data *session.Data) *types.TgSession {
	sessionString := generateTgSession(data.DC, data.AuthKey, 443)
	session := &types.TgSession{
		Sesssion:  sessionString,
		UserID:    user.ID,
		Bot:       user.Bot,
		UserName:  user.Username,
		Name:      fmt.Sprintf("%s %s", user.FirstName, user.LastName),
		IsPremium: user.Premium,
	}
	return session
}

func (as *AuthService) HandleMultipleLogin(c *gin.Context) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	dispatcher := tg.NewUpdateDispatcher()
	loggedIn := qrlogin.OnLoginToken(dispatcher)
	sessionStorage := &session.StorageMemory{}
	tgClient := tgc.NoLogin(dispatcher, sessionStorage)

	err = tgClient.Run(c, func(ctx context.Context) error {
		for {
			message := &SocketMessage{}
			err := conn.ReadJSON(message)

			if err != nil {
				return err
			}
			if message.AuthType == "qr" {
				go func() {
					authorization, err := tgClient.QR().Auth(c, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
						conn.WriteJSON(map[string]interface{}{"type": "auth", "payload": map[string]string{"token": token.URL()}})
						return nil
					})

					if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
						conn.WriteJSON(map[string]interface{}{"type": "auth", "message": "2FA required"})
						return
					}

					if err != nil {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": err.Error()})
						return
					}
					user, ok := authorization.User.AsNotEmpty()
					if !ok {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "auth failed"})
						return
					}
					if !checkUserIsAllowed(user.Username) {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "user not allowed"})
						tgClient.API().AuthLogOut(c)
						return
					}
					res, _ := sessionStorage.LoadSession(c)
					sessionData := &SessionData{}
					json.Unmarshal(res, sessionData)
					session := prepareSession(user, &sessionData.Data)
					conn.WriteJSON(map[string]interface{}{"type": "auth", "payload": session, "message": "success"})
				}()
			}
			if message.AuthType == "phone" && message.Message == "sendcode" {
				go func() {
					res, err := tgClient.Auth().SendCode(c, message.PhoneNo, tgauth.SendCodeOptions{})
					if err != nil {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": err.Error()})
						return
					}
					code := res.(*tg.AuthSentCode)
					conn.WriteJSON(map[string]interface{}{"type": "auth", "payload": map[string]string{"phoneCodeHash": code.PhoneCodeHash}})
				}()
			}
			if message.AuthType == "phone" && message.Message == "signin" {
				go func() {
					auth, err := tgClient.Auth().SignIn(c, message.PhoneNo, message.PhoneCode, message.PhoneCodeHash)

					if errors.Is(err, tgauth.ErrPasswordAuthNeeded) {
						conn.WriteJSON(map[string]interface{}{"type": "auth", "message": "2FA required"})
						return
					}

					if err != nil {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": err.Error()})
						return
					}
					user, ok := auth.User.AsNotEmpty()
					if !ok {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "auth failed"})
						return
					}
					if !checkUserIsAllowed(user.Username) {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "user not allowed"})
						tgClient.API().AuthLogOut(c)
						return
					}
					res, _ := sessionStorage.LoadSession(c)
					sessionData := &SessionData{}
					json.Unmarshal(res, sessionData)
					session := prepareSession(user, &sessionData.Data)
					conn.WriteJSON(map[string]interface{}{"type": "auth", "payload": session, "message": "success"})
				}()
			}

			if message.AuthType == "2fa" && message.Password != "" {
				go func() {
					auth, err := tgClient.Auth().Password(c, message.Password)
					if err != nil {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": err.Error()})
						return
					}
					user, ok := auth.User.AsNotEmpty()
					if !ok {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "auth failed"})
						return
					}
					if !checkUserIsAllowed(user.Username) {
						conn.WriteJSON(map[string]interface{}{"type": "error", "message": "user not allowed"})
						tgClient.API().AuthLogOut(c)
						return
					}
					res, _ := sessionStorage.LoadSession(c)
					sessionData := &SessionData{}
					json.Unmarshal(res, sessionData)
					session := prepareSession(user, &sessionData.Data)
					conn.WriteJSON(map[string]interface{}{"type": "auth", "payload": session, "message": "success"})
				}()
			}
		}
	})

	if err != nil {
		return
	}
}
